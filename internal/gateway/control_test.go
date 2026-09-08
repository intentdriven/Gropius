package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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

// /api/instance answers a challenge the caller wrote into the data root, which
// is how the singleton coordinator tells a genuine Gropius apart from a port
// squatter without either side keeping a secret.
func TestInstanceEndpointAnswersAChallenge(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	a, err := app.New(app.Options{Paths: paths, Config: config.Default()})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	name := "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(config.ChallengePath(paths.Root, name), []byte("the-answer"), 0o640); err != nil {
		t.Fatal(err)
	}

	ctrl := &Control{App: a, Root: paths.Root}
	mux := http.NewServeMux()
	ctrl.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/api/instance?challenge=" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Answer string `json:"answer"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Answer != "the-answer" {
		t.Errorf("answer = %q, want %q", body.Answer, "the-answer")
	}
}

// The endpoint must never become an arbitrary-file-read oracle: the challenge
// name is caller-supplied and is turned into a path the server reads. A
// traversal attempt and an unknown name must be indistinguishable, so the
// endpoint reports nothing about what exists.
func TestInstanceEndpointRefusesTraversalAndLeaksNothing(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	a, err := app.New(app.Options{Paths: paths, Config: config.Default()})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	secret := filepath.Join(paths.Root, "config.json")
	if err := os.WriteFile(secret, []byte("{\"api_key\":\"s3cret\"}"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctrl := &Control{App: a, Root: paths.Root}
	mux := http.NewServeMux()
	ctrl.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	for _, probe := range []string{
		"", "../config.json", "..%2Fconfig.json", "../../../../etc/passwd",
		"0123456789abcdef0123456789abcdee", ".gropius-challenge-x",
	} {
		resp, err := srv.Client().Get(srv.URL + "/api/instance?challenge=" + probe)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("challenge %q: status = %d, want 404", probe, resp.StatusCode)
		}
		if strings.Contains(string(b), "s3cret") {
			t.Fatalf("challenge %q read a file outside the challenge set", probe)
		}
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

// A save that names the pinned list replaces it. Unlike the two per-model maps
// beside it, no guard is needed for that — encoding/json resets a slice's
// length rather than merging into it — but the difference is subtle enough
// that removing a pin deserves a test of its own.
func TestSavingAShorterPinnedListRemovesTheRest(t *testing.T) {
	cfg := config.Default()
	cfg.Pinned = []string{"org/one", "org/two"}
	srv, a := newTestControlApp(t, cfg)

	resp := postJSON(t, srv, "/api/settings",
		`{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,`+
			`"idle_timeout_sec":0,"pinned":["org/one"]}`)
	resp.Body.Close()
	if got := a.Config().Pinned; len(got) != 1 || got[0] != "org/one" {
		t.Errorf("Pinned = %v, want only the model the save named", got)
	}
	if got := a.Pool.Pinned(); len(got) != 1 || got[0] != "org/one" {
		t.Errorf("the pool holds %v, want only the model the save named", got)
	}

	// And an explicit null clears it, the way naming a collection means "these
	// are its members" everywhere else in this handler.
	resp = postJSON(t, srv, "/api/settings",
		`{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,`+
			`"idle_timeout_sec":0,"pinned":null}`)
	resp.Body.Close()
	if got := a.Config().Pinned; len(got) != 0 {
		t.Errorf("Pinned = %v after an explicit null, want none", got)
	}
}

// A pinned set can stop fitting without a settings save: a pinned model that
// was deleted is charged nothing until it is downloaded back. Nothing refuses
// that, so the panel is where the operator finds out — beside the warning about
// an open LAN endpoint, on the same surface, not only in the log.
func TestStateWarnsWhenThePinnedSetNoLongerFits(t *testing.T) {
	srv, a := newTestControlApp(t, config.Default())
	if err := a.Registry.Put(registry.Model{
		RepoID: "org/enormous", Path: a.Paths.ModelDir("org/enormous"),
		Bytes: 1 << 50, State: registry.StateReady, Progress: 100,
	}); err != nil {
		t.Fatal(err)
	}

	if warned(t, srv) {
		t.Fatal("the panel warns about the pinned set before anything is pinned")
	}

	// Pinned while the model was not there to be charged, as a delete and a
	// re-download leave it.
	cfg := a.Config()
	cfg.Pinned = []string{"org/gone"}
	if err := a.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := a.Registry.Put(registry.Model{
		RepoID: "org/gone", Path: a.Paths.ModelDir("org/gone"),
		Bytes: 1 << 50, State: registry.StateReady, Progress: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if !warned(t, srv) {
		t.Error("the panel says nothing although the pinned set no longer fits")
	}
}

// warned reports whether /api/state carries a warning about the pinned models.
func warned(t *testing.T, srv *httptest.Server) bool {
	t.Helper()
	for _, w := range fetchState(t, srv).Warnings {
		if strings.Contains(w, "pinned") {
			return true
		}
	}
	return false
}

// The panel's whole view of pinning — which boxes are ticked, which cards carry
// the pill, and what the pinned set leaves of the budget — is drawn from these
// two fields. Without them the operator sees no pin anywhere and an empty
// budget line, which is silently the opposite of the promise, so the snapshot
// has to be held to carrying them.
func TestStateCarriesThePinnedSetAndTheMemoryBudget(t *testing.T) {
	srv, a := newTestControlApp(t, config.Default())
	if err := a.Registry.Put(registry.Model{
		RepoID: "org/keeper", Path: a.Paths.ModelDir("org/keeper"),
		Bytes: 1 << 20, State: registry.StateReady, Progress: 100,
	}); err != nil {
		t.Fatal(err)
	}
	cfg := a.Config()
	cfg.Pinned = []string{"org/keeper"}
	if err := a.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}

	st := fetchState(t, srv)
	if !reflect.DeepEqual(st.Pinned, []string{"org/keeper"}) {
		t.Errorf("state.pinned = %v, want the pinned model — the panel draws every pin from this", st.Pinned)
	}
	if st.Machine.Budget <= 0 {
		t.Errorf("state.machine.budget = %d, want the pool's budget — the panel cannot say what a pin leaves without it",
			st.Machine.Budget)
	}
	// Read from the pool, not the stored settings: the two agree except in the
	// moment a pin is reconciled with a model that has just arrived, and this
	// is the surface an operator acts on.
	if got := a.Pool.Pinned(); !reflect.DeepEqual(st.Pinned, got) {
		t.Errorf("state.pinned = %v but the pool is enforcing %v", st.Pinned, got)
	}
}

// fetchState decodes the control plane's whole snapshot.
func fetchState(t *testing.T, srv *httptest.Server) State {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st State
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	return st
}
