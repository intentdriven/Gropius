package ui

import (
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// A Go capability with no panel equivalent is a gap, not a feature tier. The
// level lives in config.json and it lives in Settings, and this is the test
// that keeps the two together rather than the habit of remembering.
func TestSettingsOffersTheLogLevel(t *testing.T) {
	markup := readPanelMarkup(t)
	if !strings.Contains(markup, `id="setLogLevel"`) {
		t.Error(`the settings form has no id="setLogLevel"`)
	}
	// Both levels, spelled the way the settings file spells them — a control
	// offering a word the server does not accept would refuse every save made
	// through it.
	for _, level := range []string{config.LogLevelSparse, config.LogLevelDetailed} {
		if !strings.Contains(markup, `value="`+level+`"`) {
			t.Errorf("the level control does not offer %q", level)
		}
	}

	src := readPanelSource(t)
	// Read into the form, and posted back under the name the settings file
	// uses. A field the form draws but never sends is a control that does
	// nothing.
	for _, wiring := range []string{"setLogLevel", "log_level:", "c.log_level"} {
		if !strings.Contains(src, wiring) {
			t.Errorf("the panel never names %q, so the control is not wired to anything", wiring)
		}
	}
}

// The pane has to say what the two words mean before an operator can choose
// between them, and it has to say the thing that is easy to assume and wrong:
// that this is Gropius's own log level and never the model servers'.
func TestThePaneSaysWhatTheTwoLevelsMean(t *testing.T) {
	markup := readPanelMarkup(t)
	for _, phrase := range []string{
		"model server",
		"never",
	} {
		if !strings.Contains(strings.ToLower(markup), phrase) {
			t.Errorf("the settings pane never says %q", phrase)
		}
	}
	// It must also point at the page that says the rest of it.
	if !strings.Contains(markup, "docs/logging.md") {
		t.Error("the settings pane does not link the logging page")
	}
}
