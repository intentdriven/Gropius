package ui

import (
	"strings"
	"testing"
)

// The two retention figures are the operator's, so they are in Settings beside
// the switch, and Clear is beside them: how long records are kept, how much
// room they may take, and the button that throws them away.
func TestSettingsOffersTheRetentionFiguresAndClear(t *testing.T) {
	markup := readPanelMarkup(t)
	for _, id := range []string{`id="setStatsMonths"`, `id="setStatsMB"`, `id="statsClear"`, `id="statsStoreLine"`} {
		if !strings.Contains(markup, id) {
			t.Errorf("the settings form has no %s", id)
		}
	}

	src := readPanelSource(t)
	// Read into the form, and posted back under the names the settings file
	// uses. A field the form draws but never sends is a control that does
	// nothing.
	for _, wiring := range []string{
		"setStatsMonths", "setStatsMB",
		"stats_months:", "stats_max_bytes:",
		"/api/stats/clear",
	} {
		if !strings.Contains(src, wiring) {
			t.Errorf("the panel never names %q, so the control is not wired to anything", wiring)
		}
	}
}

// A cap in megabytes says nothing about whether the store holds a week or a
// year, so the panel shows what is actually held beside the two figures: the
// date it reaches back to, the room it is using, and anything the writer could
// not keep up with.
func TestThePanelSaysHowFarBackTheStoreReaches(t *testing.T) {
	src := readPanelSource(t)
	for _, field := range []string{"stats_store", "oldest", "dropped", "bytes", "skipped", "refused"} {
		if !strings.Contains(src, field) {
			t.Errorf("the panel never reads %q, which the store reports — a figure nobody can see", field)
		}
	}
	if !strings.Contains(src, "statsStoreLine") {
		t.Error("the panel never fills in the line that says what the store holds")
	}
	// A store that could not be opened must not look like an empty one: the
	// operator would believe records were accumulating.
	if !strings.Contains(src, "could not open the store") {
		t.Error("the panel reports a refused store as an empty one")
	}
	// Clear works whether recording is on or off, so what it asks before
	// removing months of records must not claim anything about the switch.
	if strings.Contains(src, "Recording stays on") {
		t.Error("the Clear dialog says recording stays on; Clear leaves the switch as it found it, which may be off")
	}
	if !strings.Contains(src, "cannot be undone") {
		t.Error("the Clear dialog does not say the records cannot be got back")
	}
}

// Retention that cannot run is a store quietly over the size the operator set,
// so the panel says it out loud beside the two figures rather than leaving
// them looking honoured.
func TestThePanelSaysWhenRetentionIsStuck(t *testing.T) {
	src := readPanelSource(t)
	if !strings.Contains(src, "retention_wedged") {
		t.Error("the panel never reads retention_wedged, so a store past its own limits looks like one within them")
	}
	if !strings.Contains(src, "the limits are not being applied") {
		t.Error("the panel does not say what a stuck retention means for the figures beside it")
	}
}

// The table of recent requests reaches back only as far as retention allowed.
// A month whose records have been dropped must not look like a month with no
// traffic in it: the totals that were kept are shown, and the panel says the
// detail for those days is gone.
func TestThePanelShowsTheDaysWhoseDetailIsGone(t *testing.T) {
	markup := readPanelMarkup(t)
	for _, id := range []string{`id="statsSummaryBlock"`, `id="statsSummaryRows"`, `id="statsSummaryNote"`} {
		if !strings.Contains(markup, id) {
			t.Errorf("the statistics tab has no %s", id)
		}
	}
	src := readPanelSource(t)
	// Every figure a summary line carries that the panel can draw with.
	for _, field := range []string{
		"summaries", "detail_held", "day", "requests", "prompt_tokens",
		"completion_tokens", "first_token_ms_total", "first_token_requests",
		"duration_ms_total",
	} {
		if !strings.Contains(src, field) {
			t.Errorf("the panel never reads %q, which a summary line carries — a figure nobody can see", field)
		}
	}
	if !strings.Contains(src, "no longer held") {
		t.Error("the panel never says the detailed records for those days are no longer held")
	}
}
