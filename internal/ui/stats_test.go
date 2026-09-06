package ui

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The request table's one piece of real logic is what each cell says, so it
// lives in a pure function a test can lift out of app.js and evaluate. What is
// asserted here is the row a reader actually sees.
func TestTheRequestRowShowsFiguresAndNothingElse(t *testing.T) {
	cases := []struct {
		name string
		rec  string
		want map[string]any
	}{
		{
			name: "an answer",
			rec: `{"model":"org/a","at":1788696030,"class":"ok","streamed":true,` +
				`"prompt_tokens":120,"completion_tokens":201,"first_token_ms":200,"duration_ms":4200,` +
				`"queue_wait_ms":40,"load_wait_ms":1200}`,
			want: map[string]any{
				"model": "org/a", "mode": "stream", "outcome": "answered", "tokens": "120 / 201",
				"first": "200 ms", "rate": "50.0 tok/s", "waited": "1.2 s", "total": "4.2 s",
				"failed": false,
			},
		},
		{
			// Only an answer carries counts, so a refusal shows none rather
			// than showing zeroes that read as "it used no tokens".
			name: "a refusal",
			rec: `{"model":"org/a","at":1788696030,"class":"busy","streamed":true,` +
				`"prompt_tokens":0,"completion_tokens":0,"first_token_ms":-1,"duration_ms":3}`,
			want: map[string]any{
				"model": "org/a", "mode": "stream", "outcome": "too busy", "tokens": "—",
				"first": "—", "rate": "—", "waited": "—", "total": "3 ms", "failed": true,
			},
		},
		{
			// A request refused before it resolved to a model has no model to
			// name, and the name the client asked for is never recorded.
			name: "a request that named no model this Mac has",
			rec: `{"model":"","at":1788696030,"class":"client_error","streamed":false,` +
				`"prompt_tokens":0,"completion_tokens":0,"first_token_ms":-1,"duration_ms":1}`,
			want: map[string]any{
				"model": "—", "mode": "once", "outcome": "rejected", "tokens": "—",
				"first": "—", "rate": "—", "waited": "—", "total": "1 ms", "failed": true,
			},
		},
		{
			// Nothing streamed, so there was no first token to time — but the
			// answer still carries its counts.
			name: "an answer that did not stream",
			rec: `{"model":"org/a","at":1788696030,"class":"ok","streamed":false,` +
				`"prompt_tokens":10,"completion_tokens":20,"first_token_ms":-1,"duration_ms":1000,` +
				`"queue_wait_ms":5,"load_wait_ms":0}`,
			want: map[string]any{
				"model": "org/a", "mode": "once", "outcome": "answered", "tokens": "10 / 20",
				"first": "—", "rate": "—", "waited": "5 ms", "total": "1.0 s", "failed": false,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evalPanelValue(t, "requestRow("+c.rec+")",
				"requestRow", "outcomeLabel", "generationRate", "millis")
			clock, _ := got["time"].(string)
			if !regexp.MustCompile(`^\d\d:\d\d:\d\d$`).MatchString(clock) {
				t.Errorf("the row's time reads %q, want a clock time", clock)
			}
			delete(got, "time")
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("requestRow(%s) =\n%#v\nwant\n%#v", c.rec, got, c.want)
			}
		})
	}
}

// The rate is the one figure the panel works out rather than reads, and every
// tool that shows it agrees on the definition: the tokens after the first, over
// the time spent generating them. A request with nothing to divide by has no
// rate rather than a misleading one.
func TestTheGenerationRateIsWhatEveryoneElseMeansByIt(t *testing.T) {
	cases := []struct {
		rec  string
		want string
	}{
		{`{"class":"ok","completion_tokens":201,"first_token_ms":200,"duration_ms":4200}`, "50.0 tok/s"},
		{`{"class":"ok","completion_tokens":1,"first_token_ms":200,"duration_ms":4200}`, "—"},
		{`{"class":"ok","completion_tokens":201,"first_token_ms":-1,"duration_ms":4200}`, "—"},
		{`{"class":"ok","completion_tokens":201,"first_token_ms":4200,"duration_ms":4200}`, "—"},
		{`{"class":"busy","completion_tokens":0,"first_token_ms":-1,"duration_ms":3}`, "—"},
	}
	for _, c := range cases {
		got := evalPanel(t, "generationRate("+c.rec+")", "generationRate")
		if got != c.want {
			t.Errorf("generationRate(%s) = %q, want %q", c.rec, got, c.want)
		}
	}
}

// Every outcome the recorder can name has words a reader understands. A class
// with no label would show as its own identifier, which is a leak of the
// implementation into the panel and reads as a bug.
func TestEveryOutcomeHasWordsForIt(t *testing.T) {
	classes := []string{
		"ok", "client_error", "upstream_status", "busy", "refused",
		"launch_failed", "not_ready", "unreachable", "cancelled", "gateway_error",
	}
	for _, class := range classes {
		got := evalPanel(t, `outcomeLabel("`+class+`")`, "outcomeLabel")
		if got == "" || got == class {
			t.Errorf("the outcome %q shows as %q, which is the name the code uses rather than words a reader understands", class, got)
		}
	}
}

// The pure functions above are only worth testing while the panel is built
// from them and posts the switch they exist to serve. These are the lines the
// value tests cannot reach without a DOM.
func TestThePanelIsWiredToTheStatisticsSwitchAndTheView(t *testing.T) {
	src := readPanelSource(t)
	for _, want := range []*regexp.Regexp{
		// The form posts the switch, and fills it from the running configuration.
		regexp.MustCompile(`statistics:\s*\$\('setStats'\)\.checked`),
		regexp.MustCompile(`\$\('setStats'\)\.checked\s*=\s*!!c\.statistics`),
		// The view is drawn from the endpoint that serves the rows, not from
		// the state snapshot, which deliberately does not carry them.
		regexp.MustCompile(`api\('/api/stats'\)`),
		// The tab handler is what starts the polling; the fetch alone would
		// still match if nothing ever called it.
		regexp.MustCompile(`watchStats\(name === 'stats'\)`),
		regexp.MustCompile(`\brequestRow\(`),
		regexp.MustCompile(`\brecentTotals\(`),
	} {
		if !want.MatchString(src) {
			t.Errorf("the control panel no longer matches %s — that behaviour is then asserted by nothing", want)
		}
	}
}

// The minute buckets are what the view says about the shape of a day, and a
// figure that is kept but never shown is a figure nobody can check. These are
// the totals drawn from them.
func TestTheRecentTotalsAreDrawnFromTheMinuteBuckets(t *testing.T) {
	// The first bucket is two hours before "now": inside the day, outside the
	// hour. The other two are inside both.
	rollups := `[{"minute":1788696000,"requests":5,"prompt_tokens":50,"completion_tokens":100},` +
		`{"minute":1788699600,"requests":2,"prompt_tokens":20,"completion_tokens":40},` +
		`{"minute":1788703140,"requests":3,"prompt_tokens":30,"completion_tokens":60}]`
	const now = 1788703200 // one minute after the last bucket

	got := evalPanelValue(t, "recentTotals("+rollups+", "+fmt.Sprint(now)+")", "recentTotals")
	want := map[string]any{
		"hourRequests": float64(5), "hourCompletion": float64(100),
		"dayRequests": float64(10), "dayCompletion": float64(200),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("recentTotals = %#v, want %#v", got, want)
	}
}

// Nothing recorded is kept out of sight: every field of a record and every
// per-model counter reaches the panel somewhere, or it is a figure the
// operator was told about and cannot see.
func TestEveryRecordedFigureReachesThePanel(t *testing.T) {
	src := readPanelSource(t)
	for _, field := range []string{
		"prompt_tokens", "completion_tokens", "first_token_ms", "duration_ms",
		"queue_wait_ms", "load_wait_ms", "streamed", "model", "class", "at",
		"loads", "failed_loads", "evictions", "last_load_ms",
		"last_first_token_ms", "last_duration_ms", "last_completion_tokens",
		"by_class", "requests", "rollups",
	} {
		if !strings.Contains(src, field) {
			t.Errorf("the panel never reads %q, which the recorder keeps — a figure nobody can see", field)
		}
	}
}
