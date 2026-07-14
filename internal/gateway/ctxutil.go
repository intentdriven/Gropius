package gateway

import (
	"context"
	"time"
)

// contextWithTimeout is a small helper so handlers can spawn background work
// that outlives the HTTP request that triggered it.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
