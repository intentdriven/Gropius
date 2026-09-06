package gateway

import (
	"context"
	"net/http"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
)

// contextWithTimeout is a small helper so handlers can spawn background work
// that outlives the HTTP request that triggered it.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// admittedConfigKey is the context key under which withAuth records the
// configuration it admitted a request under.
type admittedConfigKey struct{}

// withAdmittedConfig returns r carrying cfg, so a handler decides on exactly
// the configuration the request was admitted under.
//
// The configuration is live: the control panel writes a new one the instant the
// user clicks Save, which is what ConfigFunc exists for. A handler that read it
// again would be reading a second, possibly different value, and for the API
// key that difference is a request admitted under one rule and served under
// another. One read per request, taken where the admission decision is made.
func withAdmittedConfig(r *http.Request, cfg config.Config) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), admittedConfigKey{}, cfg))
}

// admittedConfig returns the configuration r was admitted under, falling back
// to the live one for a handler reached without the middleware.
func (g *Gateway) admittedConfig(r *http.Request) config.Config {
	if cfg, ok := r.Context().Value(admittedConfigKey{}).(config.Config); ok {
		return cfg
	}
	return g.cfg()
}
