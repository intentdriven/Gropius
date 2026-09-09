package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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

	name := strings.Repeat("ab12", 8)
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

	// Built rather than written out: see the note in the singleton tests — a
	// hex literal beside a path reads as a credential to a secret scanner.
	wrong := strings.Repeat("ab12", 7) + "ab1"
	for _, probe := range []string{
		"", "../config.json", "..%2Fconfig.json", "../../../../etc/passwd",
		wrong, ".gropius-challenge-x",
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
		if strings.Contains(e.URL, "127.0.0.1:11535/v1") {
			hasLoopback = true
		}
		if strings.Contains(e.URL, ".local:11535/v1") {
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
		if strings.Contains(e.URL, "192.168.") || strings.Contains(e.URL, "10.") {
			t.Errorf("a loopback-bound server advertised a LAN address: %s", e.URL)
		}
		if strings.Contains(e.URL, ".local:") {
			t.Errorf("a loopback-bound server advertised its .local name, which resolves to LAN addresses it will not answer on: %s", e.URL)
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

// appendEndpoint's comment says every entry goes through the URL check,
// "including the machine's own name, which is read from scutil and is no more
// trusted for this than a hand-edited Host is". Nothing tested that claim:
// deleting the config.URLHost guard from endpointURL left the whole suite
// green, because the bind path is already filtered by boundAddr — which calls
// URLHost itself — and the two hosts that reach endpointURL without passing
// through boundAddr, this Mac's scutil name and each enumerated address, have
// no seam a test can drive.
//
// So the guard is tested where it is: on the function. A host that cannot be
// carried in a URL yields no URL and no entry, rather than a base URL the
// panel, the menu bar and the clipboard hand out.
func TestEndpointURLRefusesAHostNoURLCanCarry(t *testing.T) {
	refused := []struct {
		host string
		why  string
	}{
		{"", "scutil returns an empty name on a Mac that will not say it"},
		{"alices mac.local", "a space is in neither an address nor a name, and scutil will hand one over"},
		{"alices\r\nmac.local", "CR and LF in a base URL is a header-injection primitive in whichever client pastes it"},
		{"fe80::1%en0", "a zone's % introduces an escape in a URL rather than standing for itself"},
		{"[fe80::1%en0]", "and bracketed"},
		{"[::1", "an unclosed bracket"},
		{"::1]", "an unopened bracket"},
		{"[]", "brackets around nothing"},
		{"[[::1]]", "already doubled"},
		{"-nope.local", "not a legal label"},
		{"192.168.1.5:8080", "a host and a port is not a host"},
	}
	for _, c := range refused {
		if got := endpointURL(c.host, 11535); got != "" {
			t.Errorf("endpointURL(%q) = %q, want \"\" — %s", c.host, got, c.why)
		}
		if got := appendEndpoint(nil, c.host, 11535, ""); len(got) != 0 {
			t.Errorf("appendEndpoint(%q) listed %#v, want nothing — %s", c.host, got, c.why)
		}
	}

	accepted := map[string]string{
		"alices-mac.local": "http://alices-mac.local:11535/v1",
		"192.168.1.5":      "http://192.168.1.5:11535/v1",
		"[::1]":            "http://[::1]:11535/v1",
		"::1":              "http://[::1]:11535/v1",
	}
	for host, want := range accepted {
		if got := endpointURL(host, 11535); got != want {
			t.Errorf("endpointURL(%q) = %q, want %q — refusing this one would drop an address the server answers on", host, got, want)
		}
	}
}

// A setting the file could not carry as written is repaired on load and is in
// force in a changed form — an API key trimmed to the ceiling is the key
// clients must send from then on. A log line at startup is not where the
// operator finds that out: the panel shows the key as asterisks either way, so
// without a warning on this channel the trim is invisible on the surface the
// operator is actually looking at.
func TestStateWarnsAboutASettingRepairedOnLoad(t *testing.T) {
	srv, _ := newTestControlAppRepaired(t, config.Default(),
		[]string{"api_key (trimmed to the 512-byte ceiling)"})

	var found string
	for _, w := range stateOf(t, srv).Warnings {
		if strings.Contains(w, "api_key") {
			found = w
		}
	}
	if found == "" {
		t.Fatalf("no warning named the repaired setting: %v", stateOf(t, srv).Warnings)
	}
	if strings.Contains(found, "ignor") {
		t.Errorf("the warning %q says the setting was ignored; it is in force", found)
	}
}

// Saving rewrites config.json from the values in force, so the file no longer
// carries anything that needed repairing — and a warning that outlives the fix
// is the same class of untruth as the wording it replaced.
func TestASuccessfulSaveClearsTheRepairWarning(t *testing.T) {
	srv, _ := newTestControlAppRepaired(t, config.Default(),
		[]string{"api_key (trimmed to the 512-byte ceiling)"})

	resp := postJSON(t, srv, "/api/settings", `{"idle_timeout_sec":120}`)
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	for _, w := range stateOf(t, srv).Warnings {
		if strings.Contains(w, "api_key") {
			t.Errorf("the repair warning survived the save that fixed it: %q", w)
		}
	}
}

// newTestControlAppRepaired is newTestControlApp with the settings the load
// had to repair, which is what the panel warns about.
func newTestControlAppRepaired(t *testing.T, cfg config.Config, repaired []string) (*httptest.Server, *app.App) {
	t.Helper()

	paths := config.NewPaths(t.TempDir())
	a, err := app.New(app.Options{Paths: paths, Config: cfg})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	ctrl := &Control{App: a, Repaired: repaired}
	mux := http.NewServeMux()
	ctrl.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, a
}

// A save reads the current settings, decodes the posted body into a copy of
// them, and writes the result back — so two saves that overlap each write a
// configuration that never saw the other's change, and whichever calls
// SetConfig last silently reverts a field it was never asked about. Two
// browser tabs, or the panel and a script, are enough.
func TestConcurrentSavesEachKeepTheirOwnField(t *testing.T) {
	srv, a := newTestControlApp(t, config.Default())

	// A pair of saves is repeated rather than sent once: the window between
	// reading the settings and writing them is short, so one pair can
	// interleave harmlessly and prove nothing.
	for i := range 40 {
		idle, decode := 60+i, 1+i%8
		bodies := []string{
			fmt.Sprintf(`{"idle_timeout_sec":%d}`, idle),
			fmt.Sprintf(`{"decode_concurrency":%d}`, decode),
		}
		results := make(chan error, len(bodies))
		var wg sync.WaitGroup
		for _, body := range bodies {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/settings",
					strings.NewReader(body))
				if err != nil {
					results <- err
					return
				}
				req.Header.Set("Content-Type", "application/json")
				resp, err := srv.Client().Do(req)
				if err != nil {
					results <- err
					return
				}
				defer resp.Body.Close()
				io.Copy(io.Discard, resp.Body)
				if resp.StatusCode != http.StatusOK {
					results <- fmt.Errorf("POST %s: status %d", body, resp.StatusCode)
					return
				}
				results <- nil
			}()
		}
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatalf("round %d: %v", i, err)
			}
		}

		got := a.Config()
		if got.IdleTimeoutSec != idle {
			t.Fatalf("round %d: idle_timeout_sec = %d, want %d — the save that set it was overwritten by one that never saw it",
				i, got.IdleTimeoutSec, idle)
		}
		if got.DecodeConcurrency != decode {
			t.Fatalf("round %d: decode_concurrency = %d, want %d — the save that set it was overwritten by one that never saw it",
				i, got.DecodeConcurrency, decode)
		}
	}
}

// The panel is served the rule in force, not the empty record of a rule nobody
// has saved yet: the form shows what it is given and posts it back, so a blank
// pair of fields would read as "test nothing" and hand every model to the
// picker at the next save.
func TestTheSettingsAnswerCarriesTheRuleInForce(t *testing.T) {
	srv := newTestControl(t, config.Default())

	resp, err := srv.Client().Get(srv.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		ChatRule config.ChatRule `json:"chat_rule"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.ChatRule.Equal(config.DefaultChatRule()) {
		t.Errorf("the panel is served %+v, want the rule in force %+v", out.ChatRule, config.DefaultChatRule())
	}
}

// The rule is one more setting, and the file is a surface an operator edits by
// hand: a save that names it must not disturb anything else, and a save that
// does not name it must not disturb the rule.
func TestSavingTheChatRuleTouchesNothingElse(t *testing.T) {
	cfg := config.Default()
	cfg.Advertise = true
	cfg.Preload = []string{"mlx-community/Qwen3-8B-4bit"}
	cfg.Pinned = []string{"org/keeper"}
	srv, a := newTestControlApp(t, cfg)

	resp := postJSON(t, srv, "/api/settings",
		`{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,"idle_timeout_sec":0,`+
			`"chat_rule":{"pipeline_tags":["text-generation"],"required_tags":[]}}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := a.Config()
	if len(got.ChatRule.PipelineTags) != 1 || got.ChatRule.PipelineTags[0] != "text-generation" {
		t.Errorf("chat rule = %+v, want the one just saved", got.ChatRule)
	}
	if len(got.ChatRule.RequiredTags) != 0 || got.ChatRule.RequiredTags == nil {
		t.Errorf("required tags = %v, want the empty list the operator asked for", got.ChatRule.RequiredTags)
	}
	if !got.Advertise || len(got.Preload) != 1 || len(got.Pinned) != 1 {
		t.Errorf("an unrelated setting moved: advertise=%v preload=%v pinned=%v", got.Advertise, got.Preload, got.Pinned)
	}
}

// And the other direction: a save that says nothing about the rule keeps it —
// including the rule of an operator who cleared both fields, which is a rule
// they set and not a rule they never had. Restoring the shipped default there
// would mark half their models as unable to chat on the next unrelated save.
func TestSavingSettingsWithoutNamingTheChatRuleKeepsIt(t *testing.T) {
	cases := []struct {
		name string
		rule config.ChatRule
	}{
		{
			name: "a rule the operator narrowed",
			rule: config.ChatRule{PipelineTags: []string{"text-generation"}, RequiredTags: []string{}},
		},
		{
			name: "a rule the operator cleared entirely",
			rule: config.ChatRule{PipelineTags: []string{}, RequiredTags: []string{}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.ChatRule = c.rule
			srv, a := newTestControlApp(t, cfg)

			resp := postJSON(t, srv, "/api/settings",
				`{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,"idle_timeout_sec":0}`)
			resp.Body.Close()
			got := a.Config().ChatRule
			if !got.Equal(c.rule) {
				t.Errorf("chat rule = %+v after an unrelated save, want %+v", got, c.rule)
			}
			if got.IsZero() {
				t.Error("the rule read back as unset, so the shipped default is in force again")
			}
		})
	}
}

// The bounds are the settings path's too. An oversized rule posted to the panel
// is refused and named, rather than accepted, written to config.json and cut
// down at the next restart — which would leave a rule in force that nobody
// agreed to, in a file that says otherwise.
func TestSavingAnOversizedChatRuleIsRefused(t *testing.T) {
	srv, a := newTestControlApp(t, config.Default())

	many := make([]string, config.MaxChatRuleTags+1)
	for i := range many {
		many[i] = fmt.Sprintf("tag-%d", i)
	}
	body, err := json.Marshal(map[string]any{
		"host": "0.0.0.0", "port": 11535, "api_key": "", "decode_concurrency": 4,
		"idle_timeout_sec": 0,
		"chat_rule":        map[string]any{"pipeline_tags": many, "required_tags": []string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := postJSON(t, srv, "/api/settings", string(body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Error.Message, "chat_rule.pipeline_tags") {
		t.Errorf("the refusal reads %q; it must name the field the operator has to fix", out.Error.Message)
	}
	if !a.Config().ChatRule.IsZero() {
		t.Errorf("the refused rule reached the configuration anyway: %+v", a.Config().ChatRule)
	}
}
