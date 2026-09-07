package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/app"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/stats"
)

// historyPath is the endpoint under a range given in whole UTC seconds, which
// is the only thing it takes.
func historyPath(from, to time.Time) string {
	return fmt.Sprintf("/api/stats/history?from=%d&to=%d", from.Unix(), to.Unix())
}

// The dashboard's figures come from the store over the control plane, as
// aggregates: the panel is never handed a record to add up itself.
func TestTheHistoryEndpointServesTheStoresOwnSums(t *testing.T) {
	a, srv, _ := recordingServer(t)
	// Two models at noon on one local day, so the sums and the shares are
	// readable by eye — alpha 300 tokens, beta 100, of 400 altogether — and
	// both rows land on the same day whatever hour the test is run at.
	base := time.Now().Local().AddDate(0, 0, -2)
	day := time.Date(base.Year(), base.Month(), base.Day(), 12, 0, 0, 0, time.Local)
	for _, r := range []stats.Record{
		{Model: "org/alpha", At: day.Unix(), Class: stats.ClassOK, PromptTokens: 100, CompletionTokens: 200},
		{Model: "org/beta", At: day.Add(time.Hour).Unix(), Class: stats.ClassOK, PromptTokens: 60, CompletionTokens: 40},
	} {
		a.Stats.Add(r)
	}
	a.Stats.LoadStarted("org/alpha")
	a.Stats.LoadFinished("org/alpha", 4*time.Second, nil)
	if err := a.StatsStore.Flush(); err != nil {
		t.Fatal(err)
	}

	// The range reaches from before those records to now, so the load event —
	// which the recorder stamps with the time it happened — is inside it too.
	view, raw := getJSON(t, srv, historyPath(day.Add(-time.Hour), time.Now().Add(time.Hour)))
	if on, _ := view["enabled"].(bool); !on {
		t.Fatalf("the history endpoint says recording is off while it is on:\n%s", raw)
	}
	days, _ := view["days"].([]any)
	if len(days) != 2 {
		t.Fatalf("the range covers %d day rows, want one per model:\n%s", len(days), raw)
	}
	first, _ := days[0].(map[string]any)
	if first["model"] != "org/alpha" || first["day"] != day.Format("2006-01-02") {
		t.Errorf("the first day row is %#v, want alpha's on %s", first, day.Format("2006-01-02"))
	}
	if first["prompt_tokens"] != float64(100) || first["completion_tokens"] != float64(200) {
		t.Errorf("alpha's day row is %#v, want the fixture's 100 in and 200 out", first)
	}

	models, _ := view["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("the range covers %d models, want 2:\n%s", len(models), raw)
	}
	top, _ := models[0].(map[string]any)
	if top["model"] != "org/alpha" || top["tokens"] != float64(300) {
		t.Errorf("the largest model is %#v, want alpha with 300 tokens", top)
	}
	if share, _ := top["share"].(float64); share < 0.7499 || share > 0.7501 {
		t.Errorf("alpha's share is %v, want 300 of 400", share)
	}

	// The load the fixture made falls in the hour it was made in, and the
	// table has the whole day whether or not anything happened in each hour.
	hours, _ := view["hours"].([]any)
	if len(hours) != 24 {
		t.Fatalf("the hourly table has %d rows, want 24:\n%s", len(hours), raw)
	}
	loads := 0
	for _, h := range hours {
		row, _ := h.(map[string]any)
		n, _ := row["loads"].(float64)
		loads += int(n)
	}
	if loads != 1 {
		t.Errorf("the hourly table counts %d loads, want the fixture's one", loads)
	}
}

// The body is aggregates and nothing else. A record carries no prompt, no key
// and no address to leak, but it is still one request's own row, and the
// dashboard's promise is that the browser is handed sums.
func TestTheHistoryBodyCarriesNoRecords(t *testing.T) {
	a, srv, paths := recordingServer(t)
	now := time.Now()
	a.Stats.Add(stats.Record{
		Model: "org/alpha", At: now.Unix(), Class: stats.ClassOK, Streamed: true,
		PromptTokens: 10, CompletionTokens: 20, FirstTokenMS: 200, DurationMS: 1000,
		QueueWaitMS: 5, LoadWaitMS: 7,
	})
	if err := a.StatsStore.Flush(); err != nil {
		t.Fatal(err)
	}

	_, raw := getJSON(t, srv, historyPath(now.Add(-time.Hour), now.Add(time.Hour)))
	// A record's own fields, which only a record carries: an aggregate has no
	// use for whether one request streamed or which queue it waited in.
	for _, field := range []string{`"streamed"`, `"class"`, `"queue_wait_ms"`, `"load_wait_ms"`, `"duration_ms"`} {
		if strings.Contains(raw, field) {
			t.Errorf("the history body carries %s, which is a record's field:\n%s", field, raw)
		}
	}
	// And never where the records are: the path carries the serving account's
	// name, and the control plane answers every account on this Mac.
	if strings.Contains(raw, paths.Stats) {
		t.Errorf("the history body carries the store's location:\n%s", raw)
	}
}

// With the switch off there is nothing recorded and so nothing to aggregate,
// and the endpoint says so rather than answering with an empty table that
// reads as "you have served nothing".
func TestTheHistoryEndpointSaysNothingIsRecordedWithTheSwitchOff(t *testing.T) {
	a, err := app.New(app.Options{Paths: config.NewPaths(t.TempDir()), Config: config.Default()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	mux := http.NewServeMux()
	(&Control{App: a}).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	now := time.Now()
	view, raw := getJSON(t, srv, historyPath(now.AddDate(0, 0, -30), now))
	if on, _ := view["enabled"].(bool); on {
		t.Errorf("the history endpoint reports recording as on while it is off:\n%s", raw)
	}
	for _, table := range []string{"days", "models", "latency"} {
		if rows, _ := view[table].([]any); len(rows) != 0 {
			t.Errorf("the %s table has %d rows with recording off:\n%s", table, len(rows), raw)
		}
	}
}

// The figures are the operator's and the machine is the boundary, so the
// endpoint sits behind the guard the rest of the control plane sits behind:
// a request from the LAN, a rebound Host, or a foreign Origin is refused.
func TestTheHistoryEndpointIsRefusedFromAnywhereButThisMac(t *testing.T) {
	cfg := config.Default()
	cfg.Statistics = true
	a, err := app.New(app.Options{Paths: config.NewPaths(t.TempDir()), Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	h := (&Control{App: a}).Handler()

	now := time.Now()
	for _, c := range []struct {
		name   string
		remote string
		host   string
		origin string
	}{
		{"from the LAN", "192.0.2.44:5555", "127.0.0.1:11535", ""},
		{"a rebound host", "127.0.0.1:5555", "gropius.example:11535", ""},
		{"a foreign origin", "127.0.0.1:5555", "127.0.0.1:11535", "https://example.invalid"},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, historyPath(now.AddDate(0, 0, -30), now), nil)
			req.RemoteAddr = c.remote
			req.Host = c.host
			if c.origin != "" {
				req.Header.Set("Origin", c.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Errorf("the history endpoint answered %s with %d, want 403", c.name, w.Code)
			}
		})
	}

	// And the same request from this Mac is answered, so the refusals above
	// are the guard doing its work rather than the endpoint being absent.
	req := httptest.NewRequest(http.MethodGet, historyPath(now.AddDate(0, 0, -30), now), nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Host = "127.0.0.1:11535"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("the history endpoint answered this Mac with %d, want 200", w.Code)
	}
}

// A range is two timestamps and nothing else. One that is missing falls back
// to the last thirty days, and one that is the wrong way round is refused
// rather than quietly turned around into a range nobody asked for.
func TestTheHistoryEndpointTakesARangeAndNothingElse(t *testing.T) {
	_, srv, _ := recordingServer(t)

	view, raw := getJSON(t, srv, "/api/stats/history")
	from, _ := view["from"].(float64)
	to, _ := view["to"].(float64)
	if span := time.Duration(to-from) * time.Second; span < 29*24*time.Hour || span > 31*24*time.Hour {
		t.Errorf("the default range is %v, want about thirty days:\n%s", span, raw)
	}
	if view["max_records"] == nil || view["max_days"] == nil {
		t.Errorf("the body does not say what bounds the figures were drawn under:\n%s", raw)
	}

	now := time.Now()
	for _, path := range []string{
		historyPath(now, now.AddDate(0, 0, -30)),
		"/api/stats/history?from=yesterday&to=today",
	} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, resp.StatusCode)
		}
	}
}
