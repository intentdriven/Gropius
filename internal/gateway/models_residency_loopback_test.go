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

// listModelsEntriesFrom drives the models listing as a client at remoteAddr
// would, and returns the entries as raw maps beside the exact bytes served.
// listModelsEntries is this with an off-machine address.
func listModelsEntriesFrom(t *testing.T, h http.Handler, key, remoteAddr string) ([]map[string]any, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = remoteAddr
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/models from %s = %d, want 200: %s", remoteAddr, w.Code, w.Body.String())
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

// A keyless install publishes residency to a client on this machine.
//
// The common first-run setup is a server and a chat client on the same Mac
// with no key set, and the picker there had nothing to show: residency was
// conditioned on the install having a key, so a keyless one served none of it
// to anybody. A loopback client is the same trust class as the control panel,
// which already shows that Mac's operator exactly these facts, so withholding
// them from a program the same person is running on the same machine protected
// nothing and cost the client its picker.
func TestListModelsReportsResidencyToALoopbackClientOnAKeylessInstall(t *testing.T) {
	lastUsed := time.Unix(1757145600, 0)
	h := residencyGateway(t, "", runtime.Resident{
		RepoID:   "org/warm",
		State:    runtime.ResidencyLoaded,
		Port:     51234,
		LastUsed: lastUsed,
		InFlight: 3,
	})

	for _, addr := range []string{"127.0.0.1:52001", "[::1]:52001"} {
		t.Run(addr, func(t *testing.T) {
			entries, _ := listModelsEntriesFrom(t, h, "", addr)

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
	h := residencyGateway(t, "", runtime.Resident{
		RepoID:   "org/warm",
		State:    runtime.ResidencyLoaded,
		Port:     51234,
		LastUsed: time.Unix(1757145600, 0),
		InFlight: 3,
	})

	_, body := listModelsEntriesFrom(t, h, "", "203.0.113.50:9999")
	for _, name := range []string{"state", "in_flight", "last_used", "pinned"} {
		if strings.Contains(body, name) {
			t.Errorf("a LAN client on a keyless install was served %q: %s", name, body)
		}
	}
}

// A keyed install is unchanged: the key admits a LAN client to residency, as it
// always has. TestListModelsReportsResidencyOnAKeyedInstall asserts the values;
// this one exists so the keyed path is named in the same place as the two
// keyless ones, and would fail if loopback had become the only way in.
func TestListModelsStillReportsResidencyToAKeyedLANClient(t *testing.T) {
	h := residencyGateway(t, "bh_secret", runtime.Resident{
		RepoID:   "org/warm",
		State:    runtime.ResidencyLoaded,
		LastUsed: time.Unix(1757145600, 0),
		InFlight: 3,
	})

	entries, _ := listModelsEntriesFrom(t, h, "bh_secret", "203.0.113.50:9999")
	if got := entryByID(t, entries, "org/warm")["state"]; got != "loaded" {
		t.Errorf("state = %v for a keyed LAN client, want %q", got, "loaded")
	}
}
