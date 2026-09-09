package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/runtime"
)

// modelsRequest builds a GET /v1/models as a client at remoteAddr would send
// it. host is the Host header; a genuine local client names loopback there,
// and httptest's own default ("example.com") is what a DNS-rebound page sends.
func modelsRequest(remoteAddr, host, key string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = remoteAddr
	if host != "" {
		req.Host = host
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req
}

// listModelsFor drives the listing with a prepared request and returns the
// entries as raw maps beside the exact bytes served, so a test can assert both
// what is on the wire and what is not. listModelsEntries is this with an
// off-machine address.
func listModelsFor(t *testing.T, h http.Handler, req *http.Request) ([]map[string]any, string) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/models from %s (Host %s) = %d, want 200: %s",
			req.RemoteAddr, req.Host, w.Code, w.Body.String())
	}
	var out struct {
		Data []map[string]any `json:"data"`
	}
	body := w.Body.String()
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out.Data, body
}

// listModelsEntriesFrom drives the listing as a client at remoteAddr would,
// naming loopback in its Host as a genuine local client does.
func listModelsEntriesFrom(t *testing.T, h http.Handler, key, remoteAddr string) ([]map[string]any, string) {
	t.Helper()
	return listModelsFor(t, h, modelsRequest(remoteAddr, "127.0.0.1:11535", key))
}

// residencyFields is the set this listing withholds from the network.
var residencyFields = []string{"state", "in_flight", "last_used", "pinned"}

// assertNoResidency fails if any residency field reached the wire.
func assertNoResidency(t *testing.T, what, body string) {
	t.Helper()
	for _, name := range residencyFields {
		if strings.Contains(body, name) {
			t.Errorf("%s was served %q: %s", what, name, body)
		}
	}
}

// warmGateway is a keyless install holding one warm model, for the tests below.
func warmGateway(t *testing.T, key string) http.Handler {
	t.Helper()
	return residencyGateway(t, key, runtime.Resident{
		RepoID:   "org/warm",
		State:    runtime.ResidencyLoaded,
		Port:     51234,
		LastUsed: time.Unix(1757145600, 0),
		InFlight: 3,
	})
}

// A keyless install publishes residency to a client on this machine.
//
// The common first-run setup is a server and a chat client on the same Mac
// with no key set, and the picker there had nothing to show: residency was
// conditioned on the install having a key, so a keyless one served none of it
// to anybody. A client connecting over loopback is the same trust class as the
// control panel, which already shows that Mac's operator exactly these facts,
// so withholding them from a program the same person is running on the same
// machine protected nothing and cost the client its picker.
func TestListModelsReportsResidencyToALoopbackClientOnAKeylessInstall(t *testing.T) {
	lastUsed := time.Unix(1757145600, 0)
	h := warmGateway(t, "")

	for _, c := range []struct{ addr, host string }{
		{"127.0.0.1:52001", "127.0.0.1:11535"},
		{"[::1]:52001", "[::1]:11535"},
		{"127.0.0.1:52001", "localhost:11535"},
	} {
		t.Run(c.addr+" "+c.host, func(t *testing.T) {
			entries, _ := listModelsFor(t, h, modelsRequest(c.addr, c.host, ""))

			warm := entryByID(t, entries, "org/warm")
			if warm["state"] != "loaded" {
				t.Errorf("state = %v for a warm model, want %q", warm["state"], "loaded")
			}
			if n, ok := warm["in_flight"].(float64); !ok || int(n) != 3 {
				t.Errorf("in_flight = %v, want 3", warm["in_flight"])
			}
			if n, ok := warm["last_used"].(float64); !ok || int64(n) != lastUsed.Unix() {
				t.Errorf("last_used = %v, want %d (Unix seconds)", warm["last_used"], lastUsed.Unix())
			}
			if _, ok := warm["pinned"]; !ok {
				t.Error("pinned is absent; the four residency fields travel together")
			}

			cold := entryByID(t, entries, "org/cold")
			if cold["state"] != "not_loaded" {
				t.Errorf("state = %v for a model the pool is not holding, want %q", cold["state"], "not_loaded")
			}
		})
	}
}

// The other half of the same rule, stated as its own test because it is the
// part that must not move: a keyless install is open to the whole LAN, and an
// off-machine client on one still learns nothing from the listing about what
// this Mac is running or when. What loopback got is a client on the machine,
// not a relaxation of the open-server rule.
func TestListModelsWithholdsResidencyFromALANClientOnAKeylessInstall(t *testing.T) {
	_, body := listModelsEntriesFrom(t, warmGateway(t, ""), "", "203.0.113.50:9999")
	assertNoResidency(t, "a LAN client on a keyless install", body)
}

// A loopback source address is not on its own a client on this machine: a page
// the operator's browser visits connects from 127.0.0.1 too. On a keyless
// install withAuth returns before its own Host and Origin guards — there is no
// key for them to protect — so nothing upstream of this handler has looked at
// either header, and a DNS-rebound page (which is same-origin from the
// browser's view, so carries no Origin at all, only a Host naming the
// attacker's domain) would otherwise read every model's in-flight count and
// last-used time. That is precisely the activity the test above denies the LAN,
// reached from the LAN by another route.
func TestListModelsWithholdsResidencyFromARebornOriginOnAKeylessInstall(t *testing.T) {
	h := warmGateway(t, "")

	t.Run("a foreign Host from loopback", func(t *testing.T) {
		// What httptest sends by default, and what a rebound page sends.
		_, body := listModelsFor(t, h, modelsRequest("127.0.0.1:52001", "attacker.example:11535", ""))
		assertNoResidency(t, "a DNS-rebound page", body)
	})

	t.Run("a foreign Origin from loopback", func(t *testing.T) {
		req := modelsRequest("127.0.0.1:52001", "127.0.0.1:11535", "")
		req.Header.Set("Origin", "https://attacker.example")
		_, body := listModelsFor(t, h, req)
		assertNoResidency(t, "a cross-origin page", body)
	})

	// The genuine local UI is same-origin on loopback, and an ordinary local
	// client (curl, a chat application) sends no Origin at all. Neither may be
	// caught by the guard above.
	t.Run("the local panel's own origin", func(t *testing.T) {
		req := modelsRequest("127.0.0.1:52001", "127.0.0.1:11535", "")
		req.Header.Set("Origin", "http://127.0.0.1:11535")
		entries, _ := listModelsFor(t, h, req)
		if got := entryByID(t, entries, "org/warm")["state"]; got != "loaded" {
			t.Errorf("state = %v for the panel's own origin, want %q", got, "loaded")
		}
	})
}

// A keyed install is unchanged: the key admits a LAN client to residency, as it
// always has. TestListModelsReportsResidencyOnAKeyedInstall asserts the values;
// this one exists so the keyed path is named in the same place as the keyless
// ones, and would fail if loopback had become the only way in — including for
// the foreign Host that withAuth deliberately falls through to the bearer
// check, so a same-machine proxy that presents the key keeps working.
func TestListModelsStillReportsResidencyToAKeyedClient(t *testing.T) {
	h := warmGateway(t, "bh_secret")

	for _, c := range []struct{ name, addr, host string }{
		{"a LAN client with the key", "203.0.113.50:9999", "127.0.0.1:11535"},
		{"a proxied client with a foreign Host and the key", "127.0.0.1:52001", "attacker.example:11535"},
	} {
		t.Run(c.name, func(t *testing.T) {
			entries, _ := listModelsFor(t, h, modelsRequest(c.addr, c.host, "bh_secret"))
			if got := entryByID(t, entries, "org/warm")["state"]; got != "loaded" {
				t.Errorf("state = %v, want %q", got, "loaded")
			}
		})
	}
}
