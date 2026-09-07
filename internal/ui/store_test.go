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
	for _, field := range []string{"stats_store", "oldest", "dropped", "bytes"} {
		if !strings.Contains(src, field) {
			t.Errorf("the panel never reads %q, which the store reports — a figure nobody can see", field)
		}
	}
	if !strings.Contains(src, "statsStoreLine") {
		t.Error("the panel never fills in the line that says what the store holds")
	}
}
