package ui

import (
	"strings"
	"testing"
)

// The three historical tables are the dashboard, so what each row of each of
// them says is a pure function of one aggregate row — testable without a DOM,
// the way the request table's row already is.

// The tokens-per-day row shows the model's own figures for the day and its
// share of the whole range beside them, which is the figure that answers
// "which model does the work".
func TestTheTokensPerDayRowShowsTheDaysFiguresAndTheRangesShare(t *testing.T) {
	got := evalPanel(t,
		`dayRowHtml({"day":"2026-09-01","model":"org/alpha","requests":2,`+
			`"prompt_tokens":150,"completion_tokens":225}, 0.2013)`,
		"dayRowHtml", "sharePercent")
	for _, want := range []string{"2026-09-01", "esc(org/alpha)", ">2<", ">150<", ">225<", ">375<", ">20.1%<"} {
		if !strings.Contains(got, want) {
			t.Errorf("the day row does not show %q:\n%s", want, got)
		}
	}

	// A day the store's retention has reduced to a coarse daily total must say
	// so: a table that mixed exact and coarse rows without a word would be
	// read as exact throughout.
	coarse := evalPanel(t,
		`dayRowHtml({"day":"2026-06-01","model":"org/alpha","requests":9,`+
			`"prompt_tokens":1,"completion_tokens":2,"summary_only":true}, 0.5)`,
		"dayRowHtml", "sharePercent")
	if !strings.Contains(coarse, "daily total only") {
		t.Errorf("a summary-only day is drawn as an exact one:\n%s", coarse)
	}
}

// The latency row is the middle request, the slow one and the slowest but one,
// for the first token and for the generation rate.
func TestTheLatencyRowShowsThePercentiles(t *testing.T) {
	got := evalPanel(t,
		`latencyRowHtml({"model":"org/alpha","requests":10,`+
			`"first_token_ms":{"p50":500,"p90":900,"p99":1000},`+
			`"rate":{"p50":100,"p90":42.25,"p99":0}})`,
		"latencyRowHtml", "msFigure", "rateFigure", "millis")
	for _, want := range []string{"esc(org/alpha)", ">10<", ">500 ms<", ">900 ms<", ">1.0 s<", ">100.0 tok/s<", ">42.3 tok/s<"} {
		if !strings.Contains(got, want) {
			t.Errorf("the latency row does not show %q:\n%s", want, got)
		}
	}
	// A model with nothing to distribute shows no figure rather than a zero,
	// which a reader would take for "instant".
	if !strings.Contains(got, ">—<") {
		t.Errorf("a percentile with no requests behind it is drawn as a figure:\n%s", got)
	}
}

// The spread is the histogram as a row of counts, and its column headings come
// from the edges the server sends with them, so the two cannot drift apart.
func TestTheSpreadRowAndItsHeadingsCoverTheSameBuckets(t *testing.T) {
	labels := evalPanelArray(t, `bucketLabels([100,250,500,1000,2500,5000,10000])`, "bucketLabels")
	want := []string{
		"under 100 ms", "100 ms–250 ms", "250 ms–500 ms", "500 ms–1 s",
		"1 s–2.5 s", "2.5 s–5 s", "5 s–10 s", "10 s and over",
	}
	if len(labels) != len(want) {
		t.Fatalf("seven edges make %d headings, want %d", len(labels), len(want))
	}
	for i, w := range want {
		if labels[i] != w {
			t.Errorf("heading %d reads %q, want %q", i, labels[i], w)
		}
	}

	row := evalPanel(t,
		`spreadRowHtml({"model":"org/alpha","first_token_buckets":[0,2,2,5,1,0,0,0]})`,
		"spreadRowHtml")
	if n := strings.Count(row, "<td"); n != len(want)+1 {
		t.Errorf("the spread row has %d cells for %d headings plus the model:\n%s", n, len(want), row)
	}
}

// The eviction table is the local day, hour by hour.
func TestTheHourRowShowsTheLocalHourAndItsCounts(t *testing.T) {
	got := evalPanel(t, `hourRowHtml({"hour":3,"evictions":7,"loads":2})`, "hourRowHtml")
	for _, want := range []string{">03:00<", ">7<", ">2<"} {
		if !strings.Contains(got, want) {
			t.Errorf("the hour row does not show %q:\n%s", want, got)
		}
	}
}

// A model id is a name the store took from a directory, so it reaches the
// panel as text and leaves it as text. Every table that draws one puts it
// through escapeHtml, which the harness marks.
func TestEveryHistoricalTableEscapesTheModelId(t *testing.T) {
	const nasty = `<img src=x onerror=alert(1)>`
	for _, c := range []struct {
		name      string
		expr      string
		functions []string
	}{
		{"tokens per day", `dayRowHtml({"day":"2026-09-01","model":"` + nasty + `","requests":1,"prompt_tokens":1,"completion_tokens":1}, 1)`,
			[]string{"dayRowHtml", "sharePercent"}},
		{"latency", `latencyRowHtml({"model":"` + nasty + `","requests":1,"first_token_ms":{"p50":1},"rate":{"p50":1}})`,
			[]string{"latencyRowHtml", "msFigure", "rateFigure", "millis"}},
		{"spread", `spreadRowHtml({"model":"` + nasty + `","first_token_buckets":[1]})`,
			[]string{"spreadRowHtml"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := evalPanel(t, c.expr, c.functions...)
			if !strings.Contains(got, "esc("+nasty+")") {
				t.Errorf("the model id does not go through escapeHtml:\n%s", got)
			}
			// And nowhere else: a second placement of the id that skipped
			// the helper would be the one that mattered. (The harness stubs
			// escapeHtml with a marker rather than the real one, which needs
			// a DOM, so what is asserted is that every occurrence went
			// through it.)
			if strings.Count(got, nasty) != strings.Count(got, "esc("+nasty+")") {
				t.Errorf("the model id appears in the row without going through escapeHtml:\n%s", got)
			}
		})
	}
}

// The line above the tables says what they cover and what they were held to,
// because a table that quietly stopped short would be read as a quiet month.
func TestTheBoundsLineSaysWhatTheFiguresCover(t *testing.T) {
	got := evalPanel(t,
		`historyBoundsLine({"from":1788696030,"to":1788796030,"narrowed":true,"truncated":true,`+
			`"skipped":4,"max_days":366,"max_records":1000000})`,
		"historyBoundsLine")
	for _, want := range []string{"366 days", "1000000 records", "4 lines could not be read", "switch was off"} {
		if !strings.Contains(got, want) {
			t.Errorf("the bounds line does not say %q:\n%s", want, got)
		}
	}
	// And says none of it when none of it happened.
	plain := evalPanel(t,
		`historyBoundsLine({"from":1788696030,"to":1788796030,"max_days":366,"max_records":1000000})`,
		"historyBoundsLine")
	for _, unwanted := range []string{"narrowed", "newest", "could not be read"} {
		if strings.Contains(plain, unwanted) {
			t.Errorf("the bounds line claims %q on a whole, untruncated range:\n%s", unwanted, plain)
		}
	}
}

// The tables are in the Statistics view the recorder named, not on a second
// surface, and they are fetched from the aggregates endpoint — never from the
// tick that redraws the live rows every two seconds.
func TestTheHistoricalTablesLiveInTheStatisticsViewAndAreFetchedOnce(t *testing.T) {
	markup := readPanelMarkup(t)
	for _, id := range []string{
		`id="statsRange"`, `id="statsHistoryBounds"`, `id="statsHistoryEmpty"`,
		`id="statsDaysRows"`, `id="statsLatencyRows"`, `id="statsSpreadRows"`, `id="statsHoursRows"`,
	} {
		if !strings.Contains(markup, id) {
			t.Errorf("the Statistics view has no %s", id)
		}
	}
	// One tab, one story: the historical tables are inside the view the
	// recorder already added rather than in a tab of their own.
	if strings.Contains(markup, `data-tab="insights"`) {
		t.Error("the panel adds a second tab for the historical views")
	}

	src := readPanelSource(t)
	if !strings.Contains(src, "/api/stats/history") {
		t.Error("the panel never asks for the aggregates")
	}
	watch := extractFunction(t, src, "watchStats")
	if !strings.Contains(watch, "refreshHistory()") {
		t.Error("opening the Statistics view does not fetch the historical tables")
	}
	if strings.Contains(watch, "setInterval(refreshHistory") {
		t.Error("the historical tables are re-fetched on the two-second tick")
	}
	if !strings.Contains(extractFunction(t, src, "renderHistory"), "statsHistoryEmpty") {
		t.Error("a range with nothing in it draws no empty state")
	}
}
