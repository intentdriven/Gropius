package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/app"
	"github.com/intentdriven/Gropius/internal/config"
)

func newTestControl(t *testing.T, cfg config.Config) *httptest.Server {
	t.Helper()

	paths := config.NewPaths(t.TempDir())
	a, err := app.New(app.Options{Paths: paths, Config: cfg})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	ctrl := &Control{App: a}
	mux := http.NewServeMux()
	ctrl.Routes(mux)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// The UI is driven entirely by /api/events. A warning that only appears in
// /api/state is a warning the user never sees — which is exactly what happened
// with the "open to the whole network" notice.
func TestEventStreamCarriesTheSameWarningsAsState(t *testing.T) {
	cfg := config.Default() // LAN-exposed, no API key
	srv := newTestControl(t, cfg)

	// /api/state
	resp, err := srv.Client().Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	var fromState State
	json.NewDecoder(resp.Body).Decode(&fromState)
	resp.Body.Close()

	if len(fromState.Warnings) == 0 {
		t.Fatal("an unauthenticated LAN-exposed server must warn the user")
	}

	// First frame of /api/events
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/events", nil)
	streamResp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer streamResp.Body.Close()

	buf := make([]byte, 8192)
	n, _ := streamResp.Body.Read(buf)
	frame := string(buf[:n])
	payload, ok := strings.CutPrefix(strings.TrimSpace(frame), "data: ")
	if !ok {
		t.Fatalf("not an SSE frame: %q", frame)
	}
	var fromStream State
	if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &fromStream); err != nil {
		t.Fatalf("decode stream frame: %v", err)
	}

	if len(fromStream.Warnings) != len(fromState.Warnings) {
		t.Errorf("stream carries %d warnings but /api/state carries %d — the UI reads the stream, "+
			"so warnings missing there are invisible to the user",
			len(fromStream.Warnings), len(fromState.Warnings))
	}
}

func TestNoLANWarningWhenAuthIsSet(t *testing.T) {
	cfg := config.Default()
	cfg.APIKey = "bh_secret"
	srv := newTestControl(t, cfg)

	resp, err := srv.Client().Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var st State
	json.NewDecoder(resp.Body).Decode(&st)
	for _, w := range st.Warnings {
		if strings.Contains(w, "requires no API key") {
			t.Error("the open-network warning should disappear once a key is set")
		}
	}
}

// The control panel is reachable over the LAN, so it must never echo secrets.
func TestSecretsAreRedactedInState(t *testing.T) {
	cfg := config.Default()
	cfg.APIKey = "bh_supersecret"
	cfg.HFToken = "hf_supersecret"
	srv := newTestControl(t, cfg)

	resp, err := srv.Client().Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	raw := string(body[:n])

	if strings.Contains(raw, "bh_supersecret") {
		t.Error("the API key was sent to the control panel in plaintext")
	}
	if strings.Contains(raw, "hf_supersecret") {
		t.Error("the HuggingFace token was sent to the control panel in plaintext")
	}
}

// The UI receives "********" for a secret. Saving the form must not then
// overwrite the real key with literal asterisks.
func TestSavingRedactedPlaceholderKeepsTheRealSecret(t *testing.T) {
	cfg := config.Default()
	cfg.APIKey = "bh_real_key"
	srv := newTestControl(t, cfg)

	body := `{"host":"0.0.0.0","port":11535,"api_key":"********","decode_concurrency":4,"idle_timeout_sec":0}`
	resp, err := srv.Client().Post(srv.URL+"/api/settings", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	// Fetch state: the warning must still be absent, which is only true if the
	// real key survived.
	stResp, err := srv.Client().Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer stResp.Body.Close()
	var st State
	json.NewDecoder(stResp.Body).Decode(&st)

	for _, w := range st.Warnings {
		if strings.Contains(w, "requires no API key") {
			t.Error("saving the redacted placeholder wiped the real API key")
		}
	}
}

func TestSettingsRejectsInvalidPort(t *testing.T) {
	srv := newTestControl(t, config.Default())

	body := `{"host":"0.0.0.0","port":99999,"decode_concurrency":4}`
	resp, err := srv.Client().Post(srv.URL+"/api/settings", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an out-of-range port", resp.StatusCode)
	}
}

func TestEndpointsIncludeLoopbackAndHostname(t *testing.T) {
	cfg := config.Default()
	cfg.Port = 11535

	eps := Endpoints(cfg)
	var hasLoopback, hasLocal bool
	for _, e := range eps {
		if strings.Contains(e, "127.0.0.1:11535/v1") {
			hasLoopback = true
		}
		if strings.Contains(e, ".local:11535/v1") {
			hasLocal = true
		}
	}
	if !hasLoopback {
		t.Error("endpoints must include the loopback URL (other user accounts use it)")
	}
	if !hasLocal {
		t.Error("endpoints must include the .local name (other machines use it)")
	}
}

// A loopback-only server must not advertise LAN URLs it will not answer on.
func TestLoopbackOnlyConfigAdvertisesNoLANAddress(t *testing.T) {
	cfg := config.Default()
	cfg.Host = "127.0.0.1"

	for _, e := range Endpoints(cfg) {
		if strings.Contains(e, "192.168.") || strings.Contains(e, "10.") {
			t.Errorf("a loopback-bound server advertised a LAN address: %s", e)
		}
	}
}

func TestDownloadRequiresModelField(t *testing.T) {
	srv := newTestControl(t, config.Default())
	resp, err := srv.Client().Post(srv.URL+"/api/models/download", "application/json",
		strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCancelUnknownDownloadIs404(t *testing.T) {
	srv := newTestControl(t, config.Default())
	resp, err := srv.Client().Post(srv.URL+"/api/models/cancel", "application/json",
		strings.NewReader(`{"model":"org/nothing"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestEventStreamIsSSE(t *testing.T) {
	srv := newTestControl(t, config.Default())

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/events", nil)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}
