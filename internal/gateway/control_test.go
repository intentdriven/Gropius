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
	"github.com/intentdriven/Gropius/internal/registry"
)

func newTestControl(t *testing.T, cfg config.Config) *httptest.Server {
	t.Helper()
	srv, _ := newTestControlApp(t, cfg)
	return srv
}

// newTestControlApp is newTestControl plus the App behind it, for tests that
// have to see what a request did to the running configuration.
func newTestControlApp(t *testing.T, cfg config.Config) (*httptest.Server, *app.App) {
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
	return srv, a
}

// postJSON posts a raw JSON body to a control-plane endpoint.
func postJSON(t *testing.T, srv *httptest.Server, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// /api/instance serves this run's identity token, which the singleton
// coordinator uses to tell a genuine Gropius apart from a port squatter.
func TestInstanceEndpointServesToken(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	a, err := app.New(app.Options{Paths: paths, Config: config.Default()})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	ctrl := &Control{App: a, InstanceToken: "tok-12345"}
	mux := http.NewServeMux()
	ctrl.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/api/instance")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Token string `json:"token"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Token != "tok-12345" {
		t.Errorf("token = %q, want %q", body.Token, "tok-12345")
	}
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

// The settings form posts only the fields it owns. Saving it must not wipe the
// config fields it doesn't send — Preload (no UI control) and Advertise.
func TestSavingSettingsPreservesUnsentFields(t *testing.T) {
	cfg := config.Default()
	cfg.Advertise = true // non-zero, so the old zero-value decode would visibly flip it
	cfg.Preload = []string{"mlx-community/Qwen3-8B-4bit"}
	srv := newTestControl(t, cfg)

	// A realistic form body: host/port/etc, but NOT advertise or preload.
	body := `{"host":"127.0.0.1","port":11535,"api_key":"","decode_concurrency":4,"idle_timeout_sec":300}`
	resp, err := srv.Client().Post(srv.URL+"/api/settings", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	stResp, err := srv.Client().Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer stResp.Body.Close()
	var st State
	if err := json.NewDecoder(stResp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if !st.Config.Advertise {
		t.Error("Advertise was wiped to false by a save that never mentioned it")
	}
	if len(st.Config.Preload) != 1 || st.Config.Preload[0] != "mlx-community/Qwen3-8B-4bit" {
		t.Errorf("Preload was wiped by an unrelated save: got %v", st.Config.Preload)
	}
	// The field the form DID send must still apply.
	if st.Config.IdleTimeoutSec != 300 {
		t.Errorf("IdleTimeoutSec = %d, want the posted 300", st.Config.IdleTimeoutSec)
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

// Decode concurrency and idle timeout are only read when the pool is built at
// startup; saving a change to them must tell the user a restart is needed.
func TestSavingRestartOnlyFieldsReportsRestart(t *testing.T) {
	srv := newTestControl(t, config.Default())

	// Same host/port as the defaults; decode_concurrency changed from 4 to 8.
	body := `{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":8,"idle_timeout_sec":0}`
	resp, err := srv.Client().Post(srv.URL+"/api/settings", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Restart bool `json:"restart"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.Restart {
		t.Error("changing decode_concurrency reported restart=false; the pool only reads it at startup")
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
		if strings.Contains(e, ".local:") {
			t.Errorf("a loopback-bound server advertised its .local name, which resolves to LAN addresses it will not answer on: %s", e)
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

// A malformed model id is the caller's mistake (400), not a conflict (409).
func TestDownloadInvalidIDIs400(t *testing.T) {
	srv := newTestControl(t, config.Default())
	resp, err := srv.Client().Post(srv.URL+"/api/models/download", "application/json",
		strings.NewReader(`{"model":"not-a-valid-id"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a malformed model id", resp.StatusCode)
	}
}

// Deleting a model that isn't in the registry is 404, not 409.
func TestDeleteUnknownModelIs404(t *testing.T) {
	srv := newTestControl(t, config.Default())
	resp, err := srv.Client().Post(srv.URL+"/api/models/delete", "application/json",
		strings.NewReader(`{"model":"org/never-downloaded"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for deleting a model that isn't present", resp.StatusCode)
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

// /api/search's "limit" must be bounded: it is reachable even from a blind,
// Origin-less cross-origin GET (loopbackOnly's Origin check never sees a
// header on a request like <img src>), and an unbounded value turns one
// request into an unbounded fan-out of outbound Hub lookups.
func TestSearchLimitIsBounded(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", 40},
		{"0", 40},
		{"-5", 40},
		{"10", 10},
		{"100", 100},
		{"100000", 100},
	}
	for _, tc := range cases {
		if got := searchLimit(tc.raw); got != tc.want {
			t.Errorf("searchLimit(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

// A bare "author:" prefix with nothing after the colon must not clear the
// default mlx-community org filter — that would silently turn "override the
// org" into "search the entire Hub," which is reachable by simply typing
// "author:" into the search box with nothing after it.
func TestSearchAuthorEmptyOverrideKeepsDefault(t *testing.T) {
	cases := []struct {
		q, wantAuthor, wantRest string
	}{
		{"", "mlx-community", ""},
		{"qwen", "mlx-community", "qwen"},
		{"author:", "mlx-community", ""},
		{"author: qwen", "mlx-community", "qwen"},
		{"author:alice", "alice", ""},
		{"author:alice qwen", "alice", "qwen"},
	}
	for _, tc := range cases {
		author, rest := searchAuthor(tc.q)
		if author != tc.wantAuthor || rest != tc.wantRest {
			t.Errorf("searchAuthor(%q) = (%q, %q), want (%q, %q)", tc.q, author, rest, tc.wantAuthor, tc.wantRest)
		}
	}
}

// Pins apply the moment they are saved, so the answer must not tell the
// operator to restart for a change that has already taken effect.
func TestSavingPinnedModelsNeedsNoRestart(t *testing.T) {
	srv, a := newTestControlApp(t, config.Default())

	body := `{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,` +
		`"idle_timeout_sec":0,"pinned":["org/keeper"]}`
	resp := postJSON(t, srv, "/api/settings", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Restart bool `json:"restart"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Restart {
		t.Error("pinning reported restart=true, but the pool is told at once")
	}
	if got := a.Pool.Pinned(); len(got) != 1 || got[0] != "org/keeper" {
		t.Errorf("the pool holds %v, want the model just pinned", got)
	}
}

// The settings form does not own the pinned list on every save, and a save
// that names it replaces it — leaving a model out is the only way a form can
// unpin one.
func TestSavingSettingsWithoutNamingPinnedKeepsThePins(t *testing.T) {
	cfg := config.Default()
	cfg.Pinned = []string{"org/keeper"}
	srv, a := newTestControlApp(t, cfg)

	resp := postJSON(t, srv, "/api/settings",
		`{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,"idle_timeout_sec":0}`)
	resp.Body.Close()
	if got := a.Config().Pinned; len(got) != 1 || got[0] != "org/keeper" {
		t.Errorf("Pinned = %v after an unrelated save, want the pin kept", got)
	}
}

// A settings save whose pinned models cannot all be in memory at once is
// refused, and the panel shows the operator why.
func TestSettingsRefusesAPinnedSetLargerThanTheBudget(t *testing.T) {
	srv, a := newTestControlApp(t, config.Default())
	if err := a.Registry.Put(registry.Model{
		RepoID: "org/enormous", Path: a.Paths.ModelDir("org/enormous"),
		Bytes: 1 << 50, State: registry.StateReady, Progress: 100,
	}); err != nil {
		t.Fatal(err)
	}

	resp := postJSON(t, srv, "/api/settings",
		`{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,`+
			`"idle_timeout_sec":0,"pinned":["org/enormous"]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a pinned set that cannot fit", resp.StatusCode)
	}
	if got := a.Config().Pinned; len(got) != 0 {
		t.Errorf("the refused pins reached the running configuration: %v", got)
	}
}
