package ui

import (
	"encoding/json"
	"strings"
	"testing"
)

// The four historical tables are the dashboard, so what each row of each of
// them says is a pure function of one aggregate row — testable without a DOM,
// the way the request table's row already is.

// The tokens-per-day row shows the model's own figures for the day and its
// share of the whole range beside them, which is the figure that answers
// "which model does the work".
func TestTheTokensPerDayRowShowsTheDaysFiguresAndTheRangesShare(t *testing.T) {
	got := evalPanel(t,
		`dayRowHtml({"day":"2026-09-01","model":"org/alpha","requests":2,`+
			`"prompt_tokens":150,"completion_tokens":225}, 0.2013)`,
		"dayRowHtml", "sharePercent", "figure")
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
			`"prompt_tokens":1,"completion_tokens":2,"from_summary":true}, 0.5)`,
		"dayRowHtml", "sharePercent", "figure")
	if !strings.Contains(coarse, "from daily totals") {
		t.Errorf("a summary-only day is drawn as an exact one:\n%s", coarse)
	}
}

// The latency row is the middle request, the slow one and the slowest but one,
// for the first token and for the generation rate.
func TestTheLatencyRowShowsThePercentiles(t *testing.T) {
	got := evalPanel(t,
		`latencyRowHtml({"model":"org/alpha","requests":10,"rate_requests":7,`+
			`"first_token_ms":{"p50":500,"p90":900,"p99":1000},`+
			`"rate":{"p50":100,"p90":42.25,"p99":0}})`,
		"latencyRowHtml", "msFigure", "rateFigure", "millis", "figure")
	// The rate figures rest on seven of the ten answers, and the table says so
	// rather than putting one count over two populations.
	for _, want := range []string{"esc(org/alpha)", ">10<", ">7<", ">500 ms<", ">900 ms<", ">1.0 s<", ">100.0 tok/s<", ">42.3 tok/s<"} {
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
		"spreadRowHtml", "figure")
	if n := strings.Count(row, "<td"); n != len(want)+1 {
		t.Errorf("the spread row has %d cells for %d headings plus the model:\n%s", n, len(want), row)
	}
}

// The eviction table is the local day, hour by hour.
func TestTheHourRowShowsTheLocalHourAndItsCounts(t *testing.T) {
	got := evalPanel(t, `hourRowHtml({"hour":3,"evictions":7,"loads":2})`, "hourRowHtml", "figure")
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
			[]string{"dayRowHtml", "sharePercent", "figure"}},
		{"latency", `latencyRowHtml({"model":"` + nasty + `","requests":1,"rate_requests":1,"first_token_ms":{"p50":1},"rate":{"p50":1}})`,
			[]string{"latencyRowHtml", "msFigure", "rateFigure", "millis", "figure"}},
		{"spread", `spreadRowHtml({"model":"` + nasty + `","first_token_buckets":[1]})`,
			[]string{"spreadRowHtml", "figure"}},
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

// Emptiness is judged on every table, not on the requests alone. A range that
// holds only loads and evictions has an hourly table with figures in it, and
// "nothing was recorded" printed over the top of that would deny the records
// it was drawn from.
func TestTheEmptyStateIsJudgedOnEveryTable(t *testing.T) {
	body := extractFunction(t, readPanelSource(t), "renderHistory")
	for _, source := range []string{"latency", "hours", "evictions", "loads"} {
		if !strings.Contains(body, source) {
			t.Errorf("the empty state does not consider %q, so a range holding only those reads as empty", source)
		}
	}
	// The tell-tale of the version this replaced: emptiness read off the day
	// rows alone.
	for _, wrong := range []string{"hidden = days.length === 0", "hidden = days.length !== 0"} {
		if strings.Contains(body, wrong) {
			t.Errorf("the empty state is keyed on the day rows alone (%q)", wrong)
		}
	}
}

// historyFunctions are everything renderHistory reaches for, so a test can run
// the renderer itself rather than grep its source for the name of an element.
var historyFunctions = []string{
	"renderHistory", "historyBoundsLine", "dayRowHtml", "latencyRowHtml",
	"spreadRowHtml", "hourRowHtml", "bucketLabels", "sharePercent",
	"msFigure", "rateFigure", "millis", "figure", "showHistoryBusy", "busyLine",
}

// evalPanelDOM runs one statement against the named functions and a stand-in
// for the document, and returns what the panel put into each element it
// touched. The panel's own $ is a one-line getElementById, so a map of stubs is
// the whole of the DOM these renderers need — and it is the difference between
// asserting that a renderer names an element and asserting what a reader sees
// in it.
func evalPanelDOM(t *testing.T, statement string, functions ...string) map[string]map[string]any {
	t.Helper()
	src := readPanelSource(t)
	var b strings.Builder
	b.WriteString("const doc = {};\n")
	b.WriteString("function $(id) { if (!doc[id]) doc[id] = {hidden: null, textContent: null, innerHTML: null}; return doc[id]; }\n")
	b.WriteString("const escapeHtml = (s) => `esc(${s})`;\n")
	for _, name := range functions {
		b.WriteString(extractFunction(t, src, name))
		b.WriteString("\n")
	}
	b.WriteString(statement)
	b.WriteString("\nprocess.stdout.write(JSON.stringify(doc));")

	out := evalJS(t, b.String())
	var doc map[string]map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the panel returned %q, which is not a document: %v", out, err)
	}
	return doc
}

// With recording off the endpoint answers an empty view, and this is what the
// reader is shown for it: no tables, no figures, and no claim about a range.
// The whole section is inside a block the switch already hides, so this is
// about what would be there if it were not — an empty payload rendered as a
// day in 1970 is one hidden ancestor away from being on the screen.
func TestTheOffStateDrawsNoFigureAndNoRange(t *testing.T) {
	doc := evalPanelDOM(t, "renderHistory({});", historyFunctions...)

	if hidden, _ := doc["statsHistoryBody"]["hidden"].(bool); !hidden {
		t.Error("the tables are shown for an empty view")
	}
	if hidden, _ := doc["statsHistoryEmpty"]["hidden"].(bool); hidden {
		t.Error("an empty view draws no empty state")
	}
	bounds, _ := doc["statsHistoryBounds"]["textContent"].(string)
	if !strings.Contains(bounds, "Nothing is recorded") {
		t.Errorf("the line above the tables reads %q, want it to say nothing is recorded", bounds)
	}
	// The 1970 an empty payload's zero timestamps render as.
	if strings.Contains(bounds, "1970") {
		t.Errorf("an empty view is drawn as a range in 1970: %q", bounds)
	}
	for _, rows := range []string{"statsDaysRows", "statsLatencyRows", "statsSpreadRows"} {
		if html, _ := doc[rows]["innerHTML"].(string); html != "" {
			t.Errorf("%s holds %q for an empty view", rows, html)
		}
	}
}

// A refusal is not a disconnection: two readings of the records run at once and
// the third is told so, rather than being left with the previous range's tables
// under the new selector value.
func TestARefusedReadingSaysSoRatherThanLeavingStaleTables(t *testing.T) {
	doc := evalPanelDOM(t, `showHistoryBusy("2");`, "showHistoryBusy", "busyLine")
	if hidden, _ := doc["statsHistoryBody"]["hidden"].(bool); !hidden {
		t.Error("a refused reading leaves the previous range's tables on the screen")
	}
	if hidden, _ := doc["statsHistoryBusy"]["hidden"].(bool); hidden {
		t.Error("a refused reading says nothing")
	}
	said, _ := doc["statsHistoryBusy"]["textContent"].(string)
	for _, want := range []string{"already being read", "2 seconds"} {
		if !strings.Contains(said, want) {
			t.Errorf("the refusal reads %q, want it to say %q", said, want)
		}
	}
	// The wait comes from the server's own header, so a server that sends none
	// is not made to have sent one.
	none := evalPanel(t, `busyLine(null)`, "busyLine")
	if strings.Contains(none, "Try again in") {
		t.Errorf("a refusal with no Retry-After invents a wait: %q", none)
	}
}

// The line above the tables reports what actually happened. A reading that met
// a bound after it had already read past the start of the range covered the
// range; only a reading that stopped short of it is a figure drawn short.
func TestTheBoundsLineTellsACoveredRangeFromOneDrawnShort(t *testing.T) {
	covered := evalPanel(t,
		`historyBoundsLine({"from":1788696030,"to":1788796030,"truncated":true,"reached_start":true,`+
			`"max_days":366,"max_records":1000000})`, "historyBoundsLine")
	if strings.Contains(covered, "short of the start") || strings.Contains(covered, "do not reach") {
		t.Errorf("a range the reading covered is reported as drawn short: %q", covered)
	}

	short := evalPanel(t,
		`historyBoundsLine({"from":1788696030,"to":1788796030,"truncated":true,"reached_start":false,`+
			`"max_days":366,"max_records":1000000})`, "historyBoundsLine")
	if !strings.Contains(short, "short of the start of this range") {
		t.Errorf("a reading that stopped before the range's start does not say so: %q", short)
	}

	// And a store that simply does not go back that far is a different thing
	// from a reading a bound stopped.
	young := evalPanel(t,
		`historyBoundsLine({"from":1788696030,"to":1788796030,"truncated":false,"reached_start":false,`+
			`"max_days":366,"max_records":1000000})`, "historyBoundsLine")
	if !strings.Contains(young, "records do not reach the start of this range") {
		t.Errorf("a store younger than the range does not say so: %q", young)
	}
	if strings.Contains(young, "short of the start") {
		t.Errorf("a young store is reported as a reading stopped by a bound: %q", young)
	}
}

// The oldest row of a range that starts part-way through a day says so, because
// beside whole days it reads as a quiet one.
func TestTheClippedDayRowSaysItIsPartOfTheDay(t *testing.T) {
	got := evalPanel(t,
		`dayRowHtml({"day":"2026-09-01","model":"org/alpha","requests":2,`+
			`"prompt_tokens":10,"completion_tokens":20,"partial":true}, 0.5)`,
		"dayRowHtml", "sharePercent", "figure")
	if !strings.Contains(got, "part of the day") {
		t.Errorf("a clipped day is drawn as a whole one:\n%s", got)
	}
	whole := evalPanel(t,
		`dayRowHtml({"day":"2026-09-01","model":"org/alpha","requests":2,`+
			`"prompt_tokens":10,"completion_tokens":20}, 0.5)`,
		"dayRowHtml", "sharePercent", "figure")
	if strings.Contains(whole, "part of the day") {
		t.Errorf("a whole day is marked as clipped:\n%s", whole)
	}
}

// "Everything kept" asks for the widest range one reading covers, not for the
// epoch: asking for the epoch made the answer report that it had narrowed a
// range nobody meant to be wider, on the one option a reader picks when they
// want the lot.
func TestEverythingKeptAsksForTheWidestRangeRatherThanTheEpoch(t *testing.T) {
	src := readPanelSource(t)
	body := extractFunction(t, src, "refreshHistory")
	if strings.Contains(body, "from=0") || strings.Contains(body, "? to -") {
		t.Errorf("the range is still asked for from the epoch:\n%s", body)
	}
	if !strings.Contains(body, "maxHistoryDays") {
		t.Error("Everything kept does not ask for the widest range one reading covers")
	}
	// And the figure it sends is the server's own bound rather than a
	// different number that would drift from it.
	if !strings.Contains(src, "const maxHistoryDays = 366") {
		t.Error("the panel's widest range is not the 366 days the server bounds a view to")
	}
}
