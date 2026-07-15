package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/app"
	"github.com/intentdriven/Gropius/internal/config"
)

func controlHandler(t *testing.T) http.Handler {
	t.Helper()
	paths := config.NewPaths(t.TempDir())
	a, err := app.New(app.Options{Paths: paths, Config: config.Default()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return (&Control{App: a}).Handler()
}

// The control plane can delete models and blank the API key. It must never be
// reachable from the LAN, even though it shares a listener with /v1.
func TestControlAPIRejectsNonLoopback(t *testing.T) {
	h := controlHandler(t)

	dangerous := []struct {
		method, path, body string
	}{
		{"GET", "/api/state", ""},
		{"GET", "/api/settings", ""},
		{"POST", "/api/settings", `{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4}`},
		{"POST", "/api/models/delete", `{"model":"org/m"}`},
		{"POST", "/api/models/download", `{"model":"org/m"}`},
	}
	for _, d := range dangerous {
		req := httptest.NewRequest(d.method, d.path, strings.NewReader(d.body))
		req.RemoteAddr = "192.168.1.77:5555" // a LAN host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s from the LAN returned %d, want 403 — the control plane must be loopback-only",
				d.method, d.path, w.Code)
		}
	}
}

func TestControlAPIAllowsLoopback(t *testing.T) {
	h := controlHandler(t)
	for _, addr := range []string{"127.0.0.1:5555", "[::1]:5555"} {
		req := httptest.NewRequest("GET", "/api/state", nil)
		req.RemoteAddr = addr
		req.Host = "localhost:11535" // the Host the real UI uses
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("loopback %s got %d, want 200", addr, w.Code)
		}
	}
}

// DNS rebinding turns an attacker's origin into a loopback socket: RemoteAddr is
// 127.0.0.1 but the Host header is still the attacker's name. The control plane
// must reject it on the Host header even though the peer address looks local.
func TestControlAPIRejectsRebindingHost(t *testing.T) {
	h := controlHandler(t)
	req := httptest.NewRequest("POST", "/api/models/delete",
		strings.NewReader(`{"model":"org/m"}`))
	req.RemoteAddr = "127.0.0.1:5555" // rebound to loopback
	req.Host = "evil.example:11535"   // ...but the attacker's Host survives
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("rebinding Host returned %d, want 403", w.Code)
	}
}

// A classic cross-site POST from a malicious page carries its Origin. Even with
// a loopback peer and Host, a non-loopback Origin must be refused.
func TestControlAPIRejectsCrossOrigin(t *testing.T) {
	h := controlHandler(t)
	req := httptest.NewRequest("POST", "/api/models/delete",
		strings.NewReader(`{"model":"org/m"}`))
	req.RemoteAddr = "127.0.0.1:5555"
	req.Host = "localhost:11535"
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-origin POST returned %d, want 403", w.Code)
	}
}
