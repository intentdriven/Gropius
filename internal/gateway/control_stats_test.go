package gateway

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/app"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/stats"
)

// getJSON fetches a control-plane endpoint and returns its decoded body along
// with the raw bytes, because some of what is asserted below is the absence of
// a field rather than its value.
func getJSON(t *testing.T, srv *httptest.Server, path string) (map[string]any, string) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", path, resp.StatusCode)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded, string(raw)
}

// A fresh install: the switch is off, and neither the snapshot the panel
// redraws from nor the statistics endpoint carries a single figure worked out
// from a request.
func TestAFreshInstallShowsNothingWorkedOutFromARequest(t *testing.T) {
	srv, a := newTestControlApp(t, config.Default())

	state, rawState := getJSON(t, srv, "/api/state")
	cfg, _ := state["config"].(map[string]any)
	if on, _ := cfg["statistics"].(bool); on {
		t.Error("a fresh install reports the statistics switch as on")
	}
	if _, present := state["stats"]; present {
		t.Errorf("the state snapshot carries request-derived figures with the switch off:\n%s", rawState)
	}

	view, _ := getJSON(t, srv, "/api/stats")
	if on, _ := view["enabled"].(bool); on {
		t.Error("the statistics endpoint reports itself as recording with the switch off")
	}
	for _, field := range []string{"models", "requests", "rollups"} {
		if got, ok := view[field].([]any); ok && len(got) != 0 {
			t.Errorf("the statistics endpoint holds %d %s with the switch off", len(got), field)
		}
	}

	// And nothing has been written anywhere: the recorder holds no store, and
	// the data root holds only what a fresh install already holds.
	if a.Stats.Enabled() {
		t.Error("the recorder is recording on a fresh install")
	}
}

// With the switch on, the per-model counters ride the snapshot the panel
// already receives and the request rows come from the statistics endpoint.
// They are split that way on purpose: the snapshot is re-encoded and redrawn
// every couple of seconds, and a thousand rows on that path is a panel that
// costs more than the figures are worth.
func TestTheCountersRideTheSnapshotAndTheRowsDoNot(t *testing.T) {
	cfg := config.Default()
	cfg.Statistics = true
	srv, a := newTestControlApp(t, cfg)

	a.Stats.Add(stats.Record{Model: "org/a", Class: stats.ClassOK, PromptTokens: 5, CompletionTokens: 9})

	state, rawState := getJSON(t, srv, "/api/state")
	summary, ok := state["stats"].([]any)
	if !ok || len(summary) != 1 {
		t.Fatalf("the snapshot carries %v, want one model's counters:\n%s", state["stats"], rawState)
	}
	counters, _ := summary[0].(map[string]any)
	if counters["model"] != "org/a" || counters["requests"].(float64) != 1 {
		t.Errorf("the snapshot's counters are %v, want one request for org/a", counters)
	}
	if strings.Contains(rawState, `"requests":[`) {
		t.Errorf("the snapshot carries the request rows; they belong on /api/stats:\n%s", rawState)
	}

	view, _ := getJSON(t, srv, "/api/stats")
	rows, _ := view["requests"].([]any)
	if len(rows) != 1 {
		t.Fatalf("the statistics endpoint holds %d request rows, want 1", len(rows))
	}
	row, _ := rows[0].(map[string]any)
	if row["model"] != "org/a" || row["completion_tokens"].(float64) != 9 {
		t.Errorf("the row is %v, want org/a with 9 completion tokens", row)
	}
}

// Turning the switch off in Settings applies at once and takes what was
// recorded with it, so the panel goes back to showing nothing worked out from
// a request.
func TestTurningTheSwitchOffEmptiesThePanelAtOnce(t *testing.T) {
	cfg := config.Default()
	cfg.Statistics = true
	srv, a := newTestControlApp(t, cfg)
	a.Stats.Add(stats.Record{Model: "org/a", Class: stats.ClassOK})

	resp := postJSON(t, srv, "/api/settings",
		`{"host":"0.0.0.0","port":11535,"decode_concurrency":4,"statistics":false}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("saving settings returned %d, want 200", resp.StatusCode)
	}

	if a.Stats.Enabled() {
		t.Error("the recorder is still recording after the switch went off")
	}
	state, rawState := getJSON(t, srv, "/api/state")
	if _, present := state["stats"]; present {
		t.Errorf("the snapshot still carries counters after the switch went off:\n%s", rawState)
	}
	view, _ := getJSON(t, srv, "/api/stats")
	if rows, _ := view["requests"].([]any); len(rows) != 0 {
		t.Errorf("the statistics endpoint still holds %d rows after the switch went off", len(rows))
	}
}

// And turning it on applies at once too — the operator does not restart a
// server to start counting.
func TestTurningTheSwitchOnAppliesAtOnce(t *testing.T) {
	srv, a := newTestControlApp(t, config.Default())

	resp := postJSON(t, srv, "/api/settings",
		`{"host":"0.0.0.0","port":11535,"decode_concurrency":4,"statistics":true}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("saving settings returned %d, want 200", resp.StatusCode)
	}
	var saved struct {
		Restart bool `json:"restart"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.Restart {
		t.Error("saving the statistics switch asked for a restart; it applies live")
	}
	if !a.Stats.Enabled() {
		t.Error("the recorder is not recording after the switch went on")
	}
	if !a.Config().Statistics {
		t.Error("the saved configuration does not carry the switch")
	}
}

// The figures are the operator's, and the machine is the boundary. The
// endpoint that serves them sits on the control plane, which answers nobody
// but this Mac.
func TestTheStatisticsEndpointIsLoopbackOnly(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	cfg := config.Default()
	cfg.Statistics = true
	a, err := app.New(app.Options{Paths: paths, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	h := (&Control{App: a}).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	req.RemoteAddr = "192.0.2.44:5555" // a LAN host (RFC 5737 documentation range)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("/api/stats from the LAN returned %d, want 403", w.Code)
	}
}

// Recording lives in memory and writes nothing. Keeping records across a
// restart is the next intent's (itd-2609061521102742), under its own decision
// record, and until it exists a reader of this build should be able to see for
// themselves that turning the switch on puts nothing on their disk.
func TestRecordingWritesNoFile(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
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

	// Everything the settings save itself writes is already on disk before the
	// tree is read, so what is compared is only what recording adds.
	resp := postJSON(t, srv, "/api/settings",
		`{"host":"0.0.0.0","port":11535,"decode_concurrency":4,"statistics":true}`)
	resp.Body.Close()
	before := treeOf(t, root)

	for i := range 50 {
		a.Stats.Add(stats.Record{Model: "org/a", Class: stats.ClassOK, CompletionTokens: i})
	}
	a.Stats.LoadStarted("org/a")
	a.Stats.LoadFinished("org/a", 0, nil)
	a.Stats.Removed("org/a", stats.ReasonEvicted)
	getJSON(t, srv, "/api/stats")

	if after := treeOf(t, root); !slices.Equal(before, after) {
		t.Errorf("recording wrote to the data root.\nbefore: %v\nafter:  %v", before, after)
	}
}

// treeOf lists every path under root, relative to it, with each file's size,
// so both a new file and a file that grew show up as a difference.
func treeOf(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			out = append(out, rel+"/")
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("%s %d", rel, info.Size()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}
