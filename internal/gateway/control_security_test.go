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
	return (&Control{App: controlApp(t)}).Handler()
}

// controlApp is a control plane's App, built for a test's own data root.
func controlApp(t *testing.T) *app.App {
	t.Helper()
	paths := config.NewPaths(t.TempDir())
	a, err := app.New(app.Options{Paths: paths, Config: config.Default()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
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

// A blind cross-origin GET carries no Origin at all — a browser omits it on a
// no-cors subresource fetch such as <img src="http://127.0.0.1:PORT/api/...">
// — so the Origin allow-list above never sees the request the page made. What
// the browser does send is Sec-Fetch-Site, its own statement of where the
// request came from, and the control plane is gated on that too: a request
// that says it came from anywhere but this document's own origin is refused,
// whatever its method.
func TestControlAPIRefusesACrossSiteFetch(t *testing.T) {
	h := controlHandler(t)

	cases := []struct {
		site      string // the Sec-Fetch-Site header, "" for a request without one
		wantAdmit bool
	}{
		{"cross-site", false},
		{"same-site", false}, // another port on this host is not the panel
		{"same-origin", true},
		{"none", true}, // the operator typed the URL, or opened a bookmark
		{"", true},     // curl, an older browser: the header is not required
	}
	for _, c := range cases {
		t.Run("Sec-Fetch-Site: "+c.site, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/state", nil)
			req.RemoteAddr = "127.0.0.1:5555"
			req.Host = "localhost:11535"
			if c.site != "" {
				req.Header.Set("Sec-Fetch-Site", c.site)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			admitted := w.Code != http.StatusForbidden
			if admitted != c.wantAdmit {
				t.Fatalf("GET /api/state with Sec-Fetch-Site %q returned %d, want admitted = %v",
					c.site, w.Code, c.wantAdmit)
			}
			if !admitted && !strings.Contains(w.Body.String(), "refused") {
				t.Errorf("the refusal does not say what happened: %s", w.Body.String())
			}
		})
	}
}

// The state-changing routes are unchanged: a POST the panel itself makes is
// still served, and a cross-site one is still refused on its Origin — which is
// the header a browser always sends on a cross-site submit, and which is still
// what the refusal names.
func TestControlAPIPOSTsAreUnchangedByTheFetchCheck(t *testing.T) {
	h := controlHandler(t)

	panelPOST := httptest.NewRequest("POST", "/api/stats/clear", nil)
	panelPOST.RemoteAddr = "127.0.0.1:5555"
	panelPOST.Host = "localhost:11535"
	panelPOST.Header.Set("Origin", "http://localhost:11535")
	panelPOST.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, panelPOST)
	if w.Code != http.StatusOK {
		t.Errorf("the panel's own POST returned %d, want 200: %s", w.Code, w.Body.String())
	}

	crossSite := httptest.NewRequest("POST", "/api/models/delete",
		strings.NewReader(`{"model":"org/m"}`))
	crossSite.RemoteAddr = "127.0.0.1:5555"
	crossSite.Host = "localhost:11535"
	crossSite.Header.Set("Origin", "https://evil.example")
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, crossSite)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST returned %d, want 403", w.Code)
	}
	if got := w.Body.String(); !strings.Contains(got, "cross-origin") {
		t.Errorf("the refusal = %s, want the Origin check to be the one that names it", got)
	}
}

// The guard above must not answer a click with a 403. This project's own
// documentation autolinks the panel's address, and a link followed from a
// rendered page of it is cross-site to the browser — but a navigation is not
// the blind read the guard exists for: it puts the answer in a window the
// person who clicked is looking at, and the page that started it can read
// none of it back. The panel is served; a navigation to an API route is not,
// because there the answer is JSON nobody navigated to read.
func TestControlAPIServesACrossSiteNavigationToThePanel(t *testing.T) {
	c := &Control{App: controlApp(t), UI: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>the panel</html>"))
	})}
	h := c.Handler()

	cases := []struct {
		name             string
		method, path     string
		mode, dest, site string
		wantAdmit        bool
	}{
		{"a link to the panel from another site", "GET", "/",
			"navigate", "document", "cross-site", true},
		{"the operator's own bookmark", "GET", "/",
			"navigate", "document", "none", true},
		{"a page fetching the panel's HTML", "GET", "/",
			"cors", "empty", "cross-site", false},
		{"a site framing the panel", "GET", "/",
			"navigate", "iframe", "cross-site", false},
		{"a navigation to an API route", "GET", "/api/state",
			"navigate", "document", "cross-site", false},
		{"a page fetching an API route", "GET", "/api/state",
			"cors", "empty", "cross-site", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			req.RemoteAddr = "127.0.0.1:5555"
			req.Host = "localhost:11535"
			req.Header.Set("Sec-Fetch-Mode", c.mode)
			req.Header.Set("Sec-Fetch-Dest", c.dest)
			req.Header.Set("Sec-Fetch-Site", c.site)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if admitted := w.Code != http.StatusForbidden; admitted != c.wantAdmit {
				t.Fatalf("%s %s (mode %s, dest %s, site %s) returned %d, want admitted = %v",
					c.method, c.path, c.mode, c.dest, c.site, w.Code, c.wantAdmit)
			}
		})
	}
}

// The navigation exception is for reading a page, never for changing
// something: a form on another site posting to the panel is a navigation too.
func TestControlAPIRefusesACrossSiteNavigatingPOST(t *testing.T) {
	h := controlHandler(t)
	req := httptest.NewRequest("POST", "/api/models/delete",
		strings.NewReader(`{"model":"org/m"}`))
	req.RemoteAddr = "127.0.0.1:5555"
	req.Host = "localhost:11535"
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("a cross-site form POST returned %d, want 403", w.Code)
	}
}
