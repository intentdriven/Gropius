package gateway

import (
	"net/http"
	"testing"

	"github.com/intentdriven/Gropius/internal/app"
	"github.com/intentdriven/Gropius/internal/registry"
)

// A model action refused because the server is going away is not a conflict
// about that model. 409 tells a caller that the state of this model is the
// problem and that a different one would work; neither is true of a server that
// has begun shutting down, and a client that retries on 503 and gives up on 409
// would give up for the wrong reason. The other refusals are pinned beside it so
// that naming this one cannot quietly move them.
func TestAModelActionRefusedByAShutdownIsUnavailableRatherThanAConflict(t *testing.T) {
	cases := map[string]struct {
		err  error
		want int
	}{
		"shutting down":       {app.ErrShuttingDown, http.StatusServiceUnavailable},
		"being deleted":       {app.ErrDeleting, http.StatusConflict},
		"already downloading": {app.ErrAlreadyDownloading, http.StatusConflict},
		"a malformed id":      {app.ErrInvalidRepoID, http.StatusBadRequest},
		"no such model":       {registry.ErrNotFound, http.StatusNotFound},
	}
	for what, c := range cases {
		if got := modelErrorStatus(c.err); got != c.want {
			t.Errorf("%s maps to %d, want %d", what, got, c.want)
		}
	}
}
