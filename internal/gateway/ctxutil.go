package gateway

import (
	"context"
	"net/http"
	"time"
)

// contextWithTimeout is a small helper so handlers can spawn background work
// that outlives the HTTP request that triggered it.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// admittedKeyedKey is the context key under which withAuth records whether the
// request was admitted on an install that has an API key configured.
type admittedKeyedKey struct{}

// withAdmittedKeyed returns r carrying that one bit, so a handler decides on
// exactly the admission withAuth made.
//
// The configuration is live: the control panel writes a new one the instant the
// user clicks Save, which is what ConfigFunc exists for. A handler that read it
// again would be reading a second, possibly different value, and for the API
// key that difference is a request admitted under one rule and served under
// another. One read per request, taken where the admission decision is made.
//
// The bit travels rather than the configuration it came from: r.Context() is
// handed to the pool and to the outbound relay request, and the API key has no
// business being reachable from a RoundTripper for the length of a generation.
func withAdmittedKeyed(r *http.Request, keyed bool) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), admittedKeyedKey{}, keyed))
}

// admittedKeyed reports whether r was admitted on a keyed install.
//
// It fails closed. A handler reached without withAuth in front of it has made
// no admission decision at all, and answering "keyed" there would hand back
// the live-configuration read this exists to remove.
func (g *Gateway) admittedKeyed(r *http.Request) bool {
	keyed, _ := r.Context().Value(admittedKeyedKey{}).(bool)
	return keyed
}
