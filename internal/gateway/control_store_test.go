package gateway

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/app"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/mlxtest"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/stats"
)

// recordingServer is an app with recording on and its store under a temporary
// root, behind the control plane.
func recordingServer(t *testing.T) (*app.App, *httptest.Server, config.Paths) {
	t.Helper()
	paths := config.NewPaths(t.TempDir())
	cfg := config.Default()
	cfg.Statistics = true
	a, err := app.New(app.Options{Paths: paths, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	ctrl := &Control{App: a}
	mux := http.NewServeMux()
	ctrl.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return a, srv, paths
}

// The panel is told how far back the store reaches, so the two retention
// figures beside it mean something: a cap in megabytes says nothing about
// whether it holds a week or a year.
func TestTheStatisticsEndpointSaysHowFarBackTheStoreReaches(t *testing.T) {
	a, srv, paths := recordingServer(t)
	a.Stats.Add(stats.Record{Model: "org/a", At: 1788696030, Class: stats.ClassOK})
	a.Stats.Add(stats.Record{Model: "org/a", At: 1788700000, Class: stats.ClassOK})
	if err := a.StatsStore.Flush(); err != nil {
		t.Fatal(err)
	}

	// The oldest record the store actually holds, read back out of it: the
	// figure the panel shows has to be that one, not zero and not the date of
	// something that was pruned.
	var held []stats.Line
	if err := a.StatsStore.Latest(0, func(l stats.Line) bool { held = append(held, l); return true }); err != nil {
		t.Fatal(err)
	}
	if len(held) == 0 {
		t.Fatal("the store holds nothing, so this test proves nothing")
	}
	oldest := held[len(held)-1].At

	view, raw := getJSON(t, srv, "/api/stats")
	store, ok := view["store"].(map[string]any)
	if !ok {
		t.Fatalf("the statistics endpoint says nothing about the store:\n%s", raw)
	}
	if got := store["oldest"]; got != float64(oldest) {
		t.Errorf("the store's oldest record is reported as %v, want %d — the oldest it holds", got, oldest)
	}
	if got, _ := store["bytes"].(float64); got <= 0 {
		t.Errorf("the store is reported as using %v bytes", got)
	}
	// The panel is told what the store holds, never where it is: the path
	// carries the serving account's name, and the control plane answers every
	// account on this Mac.
	if _, present := store["dir"]; present {
		t.Errorf("the statistics endpoint publishes the store's path:\n%s", raw)
	}
	if strings.Contains(raw, paths.Stats) {
		t.Errorf("the statistics endpoint carries the store's location:\n%s", raw)
	}

	// And the same figures reach the snapshot the Settings page draws from,
	// because that is where the retention fields are.
	state, rawState := getJSON(t, srv, "/api/state")
	if _, ok := state["stats_store"].(map[string]any); !ok {
		t.Errorf("the snapshot says nothing about the store, so Settings cannot show it:\n%s", rawState)
	}
}

// With recording off there is nothing to say about a store, and saying it
// would be a figure worked out from a request on a panel that promises none.
func TestTheStoreIsNotReportedWhileRecordingIsOff(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	a, err := app.New(app.Options{Paths: paths, Config: config.Default()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	ctrl := &Control{App: a}
	mux := http.NewServeMux()
	ctrl.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	view, raw := getJSON(t, srv, "/api/stats")
	if _, present := view["store"]; present {
		t.Errorf("the statistics endpoint describes a store with recording off:\n%s", raw)
	}
	state, rawState := getJSON(t, srv, "/api/state")
	if _, present := state["stats_store"]; present {
		t.Errorf("the snapshot describes a store with recording off:\n%s", rawState)
	}
}

// Clear is the one thing that removes a record. It removes both halves — the
// files and the live view — and leaves recording on.
func TestClearRemovesTheStoreAndTheView(t *testing.T) {
	a, srv, paths := recordingServer(t)
	for i := range 5 {
		a.Stats.Add(stats.Record{Model: "org/a", At: int64(i + 1), Class: stats.ClassOK})
	}
	if err := a.StatsStore.Flush(); err != nil {
		t.Fatal(err)
	}
	bystander := filepath.Join(paths.Stats, "notes.txt")
	if err := os.WriteFile(bystander, []byte("not the store's"), 0o600); err != nil {
		t.Fatal(err)
	}

	resp := postJSON(t, srv, "/api/stats/clear", `{}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/stats/clear = %d, want 200", resp.StatusCode)
	}

	if files, err := a.StatsStore.Files(); err != nil {
		t.Fatal(err)
	} else if len(files) != 0 {
		t.Errorf("the store still holds %v", files)
	}
	if _, err := os.Stat(bystander); err != nil {
		t.Errorf("Clear removed a file that is not the store's: %v", err)
	}
	view, _ := getJSON(t, srv, "/api/stats")
	if rows, _ := view["requests"].([]any); len(rows) != 0 {
		t.Errorf("the live view still holds %d requests after Clear", len(rows))
	}
	if on, _ := view["enabled"].(bool); !on {
		t.Error("Clear switched recording off; it empties what is held, it does not decide anything")
	}
}

// Clear is administrative, so it is on the loopback-only control plane like
// everything else that can throw something away.
func TestClearIsLoopbackOnly(t *testing.T) {
	a, _, _ := recordingServer(t)
	ctrl := &Control{App: a}
	req := httptest.NewRequest(http.MethodPost, "/api/stats/clear", strings.NewReader(`{}`))
	req.RemoteAddr = "192.0.2.44:51820"
	w := httptest.NewRecorder()
	ctrl.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("POST /api/stats/clear from the LAN returned %d, want 403", w.Code)
	}
}

// Nothing of a request reaches the files, any more than it reaches the live
// view: not a prompt, not a key, not the address it came from. The store is
// handed the recorder's own records, which cannot carry any of the three, and
// this is the byte scan that says so.
func TestNothingFromTheRequestReachesTheStore(t *testing.T) {
	const (
		sentinel = "SENTINEL-PROMPT-fb0c1d"
		token    = "bh_secrettokenvalue0123"
		lanAddr  = "192.0.2.44:51820"
	)

	const modelPath = "/models/" + testModelID
	fake := mlxtest.Start(mlxtest.Options{ModelArg: modelPath, Reply: "GROPIUS OK"})
	t.Cleanup(fake.Close)
	models := &stubModels{models: []registry.Model{{RepoID: testModelID, Path: modelPath, State: registry.StateReady}}}

	dir := filepath.Join(t.TempDir(), "stats")
	store := stats.NewStore(dir, stats.StoreOptions{})
	t.Cleanup(func() { store.Close() })
	if err := store.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	rec := stats.New(stats.Options{Store: store})
	rec.SetEnabled(true)

	cfg := config.Default()
	cfg.Statistics = true
	cfg.APIKey = token
	g := New(Options{Config: cfg, Pool: &stubPool{srv: fake}, Models: models, Stats: rec})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+testModelID+`","stream":true,"messages":[{"role":"user","content":"`+sentinel+`"}]}`))
	req.RemoteAddr = lanAddr
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	rec.LoadFinished(testModelID, 0, nil)
	rec.Removed(testModelID, stats.ReasonEvicted)
	rec.RecordSettings(stats.Settings{At: 1, BudgetBytes: 1, DecodeConcurrency: 1})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) == 0 {
		t.Fatal("the store holds no files, so this scan proves nothing")
	}
	for _, e := range ents {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) == 0 {
			t.Fatalf("%s is empty, so this scan proves nothing", e.Name())
		}
		for _, secret := range []string{sentinel, token, "192.0.2.44"} {
			if strings.Contains(string(raw), secret) {
				t.Errorf("%s carries %q", e.Name(), secret)
			}
		}
	}
}

// Clearing is destructive and anonymous — the control plane asks nobody who
// they are — so it leaves a line in the log saying it happened.
func TestClearingTheRecordsIsNoted(t *testing.T) {
	var logged bytes.Buffer
	paths := config.NewPaths(t.TempDir())
	cfg := config.Default()
	cfg.Statistics = true
	a, err := app.New(app.Options{Paths: paths, Config: cfg,
		Log: slog.New(slog.NewTextHandler(&logged, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	ctrl := &Control{App: a}
	mux := http.NewServeMux()
	ctrl.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	postJSON(t, srv, "/api/stats/clear", `{}`).Body.Close()
	if !strings.Contains(logged.String(), "clearing the request statistics") {
		t.Errorf("clearing the records left no line in the log:\n%s", logged.String())
	}
}

// Clear works with the switch off. Someone who stops recording and then
// decides the history should go too must not have to turn recording back on to
// remove it — and while it is off the panel shows nothing about the store, so
// this is the one control pressed without a figure beside it.
func TestClearWorksWhileRecordingIsOff(t *testing.T) {
	a, srv, paths := recordingServer(t)
	a.Stats.Add(stats.Record{Model: "org/a", At: 1788696030, Class: stats.ClassOK})
	if err := a.StatsStore.Flush(); err != nil {
		t.Fatal(err)
	}
	if files, err := a.StatsStore.Files(); err != nil || len(files) == 0 {
		t.Fatalf("nothing was recorded (%v, %v), so this test proves nothing", files, err)
	}

	off := config.Default()
	if err := a.SetConfig(off); err != nil {
		t.Fatal(err)
	}
	state, raw := getJSON(t, srv, "/api/state")
	if _, present := state["stats_store"]; present {
		t.Errorf("the panel describes the store with recording off:\n%s", raw)
	}

	resp := postJSON(t, srv, "/api/stats/clear", `{}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/stats/clear with recording off = %d, want 200", resp.StatusCode)
	}
	if files, err := a.StatsStore.Files(); err != nil {
		t.Fatal(err)
	} else if len(files) != 0 {
		t.Errorf("the store still holds %v", files)
	}
	if a.StatsStore.Enabled() || a.Stats.Enabled() {
		t.Error("clearing with the switch off turned recording back on")
	}
	if _, err := os.Stat(paths.Stats); err != nil {
		t.Errorf("the store directory went with the records: %v", err)
	}
}

// The same scan, of the summary rather than of the records. The summary
// outlives the detail it is folded from by years, so it is the file that would
// carry a leak furthest — and it is written by a different path from the one
// the records take.
func TestNothingFromTheRequestReachesTheSummary(t *testing.T) {
	const (
		sentinel = "SENTINEL-PROMPT-9d41ae"
		token    = "bh_secrettokenvalue4567"
		lanAddr  = "192.0.2.51:51999"
	)

	const modelPath = "/models/" + testModelID
	fake := mlxtest.Start(mlxtest.Options{ModelArg: modelPath, Reply: "GROPIUS OK"})
	t.Cleanup(fake.Close)
	models := &stubModels{models: []registry.Model{{RepoID: testModelID, Path: modelPath, State: registry.StateReady}}}

	dir := filepath.Join(t.TempDir(), "stats")
	// A cap the first handful of records passes, so the drop that writes the
	// summary happens inside this test rather than in a year's time.
	store := stats.NewStore(dir, stats.StoreOptions{MaxBytes: 4 << 10, RotateBytes: 1 << 10})
	t.Cleanup(func() { store.Close() })
	if err := store.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	rec := stats.New(stats.Options{Store: store})
	rec.SetEnabled(true)

	cfg := config.Default()
	cfg.Statistics = true
	cfg.APIKey = token
	g := New(Options{Config: cfg, Pool: &stubPool{srv: fake}, Models: models, Stats: rec})

	for range 40 {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"`+testModelID+`","stream":true,"messages":[{"role":"user","content":"`+sentinel+`"}]}`))
		req.RemoteAddr = lanAddr
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
	}
	rec.LoadFinished(testModelID, 0, nil)
	rec.Removed(testModelID, stats.ReasonEvicted)
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}

	summary := filepath.Join(dir, "summary.jsonl")
	raw, err := os.ReadFile(summary)
	if err != nil {
		t.Fatalf("no summary was written, so this scan proves nothing: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("the summary is empty, so this scan proves nothing")
	}
	if !strings.Contains(string(raw), testModelID) {
		t.Fatalf("the summary does not name the model whose requests it counts, so this scan proves nothing:\n%s", raw)
	}
	for _, secret := range []string{sentinel, token, "192.0.2.51", "GROPIUS OK"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the summary carries %q", secret)
		}
	}
	// And the closed field set, so a field added without a word about it fails
	// here rather than shipping.
	allowed := map[string]bool{"v": true, "kind": true}
	for _, f := range stats.StoreFields() {
		allowed[f] = true
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("a summary line is not one JSON object: %q", line)
		}
		for k := range obj {
			if !allowed[k] {
				t.Errorf("the summary carries %q, which is no field the store documents", k)
			}
		}
	}
}

// The endpoint the Statistics tab reads carries the days whose detail the
// store no longer holds, or nothing a person can see would say a period's
// records are gone rather than that nothing happened in it.
func TestTheStatisticsEndpointCarriesTheDaysWhoseDetailIsGone(t *testing.T) {
	a, srv, _ := recordingServer(t)
	// Two years back, against a one-month horizon: the file goes, and the fold
	// keeps its totals.
	old := time.Now().AddDate(-2, 0, 0).Unix()
	for range 3 {
		a.Stats.Add(stats.Record{
			Model: testModelID, At: old, Class: stats.ClassOK,
			PromptTokens: 10, CompletionTokens: 20, FirstTokenMS: 30, DurationMS: 100,
		})
	}
	if err := a.StatsStore.Flush(); err != nil {
		t.Fatal(err)
	}
	a.StatsStore.SetRetention(1, 200<<20)
	if err := a.StatsStore.Flush(); err != nil {
		t.Fatal(err)
	}

	res, err := http.Get(srv.URL + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var view struct {
		Summaries []stats.SummaryDay `json:"summaries"`
	}
	if err := json.NewDecoder(res.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if len(view.Summaries) != 1 {
		t.Fatalf("the endpoint carries %d summarized days, want the one the horizon dropped", len(view.Summaries))
	}
	got := view.Summaries[0]
	if _, err := time.Parse(time.DateOnly, got.Day); err != nil {
		t.Errorf("the summarized day came back as %q, which is no date: %v", got.Day, err)
	}
	if got.Requests != 3 || got.CompletionTokens != 60 {
		t.Errorf("the day came back as %d requests and %d tokens out, want 3 and 60", got.Requests, got.CompletionTokens)
	}
	if got.DetailHeld {
		t.Error("a day the store holds no records from is reported as one whose detail is still held")
	}
}
