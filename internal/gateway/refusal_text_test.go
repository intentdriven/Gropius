package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/mlxtest"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// refusingGateway is an install whose pool refuses every acquisition with err,
// so a test can read the refusal two clients are served for the same error.
func refusingGateway(t *testing.T, key string, err error) http.Handler {
	t.Helper()
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	t.Cleanup(fake.Close)

	cfg := config.Default()
	cfg.APIKey = key
	models := &stubModels{models: []registry.Model{
		{RepoID: "org/warm", State: registry.StateReady, Path: "/models/org/warm"},
	}}
	return New(Options{
		Config: cfg,
		Pool:   &stubPool{srv: fake, acquireErr: err},
		Models: models,
	}).Handler()
}

// completionAs sends one completion request as a client at remoteAddr would,
// naming loopback in its Host as a genuine local client does, and returns the
// whole answer so a test can compare status, headers and body.
func completionAs(t *testing.T, h http.Handler, remoteAddr, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"org/warm","messages":[{"role":"user","content":"hi"}]}`))
	req.RemoteAddr = remoteAddr
	req.Host = "127.0.0.1:11535"
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// poolRefusals are the three refusals that describe this Mac rather than the
// request, each in the exact shape the pool produces it.
func poolRefusals() []struct {
	name    string
	err     error
	private string // the fact the refusal discloses
} {
	return []struct {
		name    string
		err     error
		private string
	}{
		{
			name:    "the overload refusal names the in-flight count",
			err:     fmt.Errorf("org/warm is overloaded (%d requests already in flight): %w", 7, runtime.ErrBusy),
			private: "7 requests already in flight",
		},
		{
			name:    "the nothing-can-be-freed refusal names the memory budget",
			err:     &runtime.NoRoomError{Limit: 41 << 30, Waited: 300 * time.Second},
			private: runtime.HumanBytes(41 << 30),
		},
		{
			name: "the too-large-to-load refusal names the memory budget",
			err: fmt.Errorf("org/warm needs about %s of memory but the limit is %s — raise the memory budget or choose a smaller quantization",
				runtime.HumanBytes(60<<30), runtime.HumanBytes(41<<30)),
			private: runtime.HumanBytes(41 << 30),
		},
	}
}

// The three pool refusals describe the machine, not the request: how many
// requests a model is already handling, and the resident memory budget in
// bytes — which is a fraction of this Mac's physical RAM, and so says roughly
// how much memory it has. A client can induce all three itself. That is the
// same class of fact the models list withholds from an open server's network
// clients, so the refusal withholds it on the same rule.
func TestAPoolRefusalTellsAnUnentitledClientNothingAboutThisMachine(t *testing.T) {
	for _, c := range poolRefusals() {
		t.Run(c.name, func(t *testing.T) {
			h := refusingGateway(t, "", c.err)
			w := completionAs(t, h, "203.0.113.50:9999", "")

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", w.Code)
			}
			body := w.Body.String()
			if strings.Contains(body, c.private) {
				t.Errorf("a LAN client on an open server was told %q: %s", c.private, body)
			}
			if !strings.Contains(body, genericRefusal) {
				t.Errorf("body = %s, want the generic refusal %q", body, genericRefusal)
			}
		})
	}
}

// The same error, two clients, two bodies — and one status line and one set of
// headers. A client backing off honestly reads the status and the headers, and
// neither moves: what changes is only what the body says about this Mac.
func TestAPoolRefusalKeepsItsStatusAndHeadersForEveryClient(t *testing.T) {
	for _, c := range poolRefusals() {
		t.Run(c.name, func(t *testing.T) {
			h := refusingGateway(t, "", c.err)
			local := completionAs(t, h, "127.0.0.1:52001", "")
			lan := completionAs(t, h, "203.0.113.50:9999", "")

			if local.Code != lan.Code {
				t.Errorf("status = %d for a client on this machine and %d for a LAN client; "+
					"back-off reads the status", local.Code, lan.Code)
			}
			for _, header := range []string{"Retry-After", "X-Gropius-State", "X-Gropius-Queue-Time", "Content-Type"} {
				if got, want := lan.Header().Get(header), local.Header().Get(header); got != want {
					t.Errorf("%s = %q for a LAN client and %q for a client on this machine; "+
						"only the body text is gated", header, got, want)
				}
			}
			if local.Body.String() == lan.Body.String() {
				t.Errorf("both clients were served the same body: %s", local.Body.String())
			}
			if !strings.Contains(local.Body.String(), c.private) {
				t.Errorf("a client on this machine lost the informative refusal: %s", local.Body.String())
			}
		})
	}
}

// The two entitled classes are the models list's, not a third rule of this
// path's own: a client on this machine, and any client a keyed install
// admitted. Both are served the refusal that says what is actually wrong,
// because both are already served exactly these facts by the listing.
func TestAPoolRefusalStaysInformativeForAnEntitledClient(t *testing.T) {
	err := &runtime.NoRoomError{Limit: 41 << 30}
	budget := runtime.HumanBytes(41 << 30)

	t.Run("a client on this machine, on a keyless install", func(t *testing.T) {
		w := completionAs(t, refusingGateway(t, "", err), "127.0.0.1:52001", "")
		if !strings.Contains(w.Body.String(), budget) {
			t.Errorf("body = %s, want the budget figure %s", w.Body.String(), budget)
		}
	})

	t.Run("a LAN client presenting the key", func(t *testing.T) {
		w := completionAs(t, refusingGateway(t, "bh_secret", err), "203.0.113.50:9999", "bh_secret")
		if !strings.Contains(w.Body.String(), budget) {
			t.Errorf("body = %s, want the budget figure %s", w.Body.String(), budget)
		}
	})
}

// A blind cross-origin fetch is not a client on this machine, here either: a
// page the operator's browser visits connects from 127.0.0.1 and, on a no-cors
// subresource, carries no Origin at all. The refusal is gated on the one
// predicate the whole server uses for "came from this machine", so the page is
// served what the LAN is.
func TestAPoolRefusalTellsACrossSiteFetchNothing(t *testing.T) {
	h := refusingGateway(t, "", &runtime.NoRoomError{Limit: 41 << 30})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"org/warm","messages":[{"role":"user","content":"hi"}]}`))
	req.RemoteAddr = "127.0.0.1:52001"
	req.Host = "127.0.0.1:11535"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if got := w.Body.String(); strings.Contains(got, runtime.HumanBytes(41<<30)) {
		t.Errorf("a cross-site fetch was told the memory budget: %s", got)
	}
}

// A launch failure is redacted for everybody, as it always was: its text can
// carry absolute local paths from the serving account's home directory, which
// is not a residency fact and is nobody's business, entitled or not.
func TestALaunchFailureStaysGenericForEveryClient(t *testing.T) {
	err := fmt.Errorf("start model server: %w",
		&runtime.LaunchError{Err: errors.New("exec: no such file or directory")})
	h := refusingGateway(t, "", err)

	for _, addr := range []string{"127.0.0.1:52001", "203.0.113.50:9999"} {
		w := completionAs(t, h, addr, "")
		if got := w.Body.String(); !strings.Contains(got, "the model could not be started") {
			t.Errorf("%s was served %s, want the redacted launch refusal", addr, got)
		}
	}
}

// The rule is a reference-page promise, not only a behaviour: the page that
// tells a client what an open server withholds must say that the refusals
// follow the listing.
func TestTheModelsListReferenceDescribesTheRefusalRule(t *testing.T) {
	page, err := os.ReadFile("../../docs/models-list.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), genericRefusal) {
		t.Errorf("the reference does not give the generic refusal %q that an open "+
			"server serves its network clients", genericRefusal)
	}
}
