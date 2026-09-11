package ui

import (
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// No control in the Settings pane is narrower than the server.
//
// A browser enforces a number field's own min, max and step BEFORE the submit
// listener runs, so a bound here that is tighter than config.Validate's makes a
// value the server would have accepted unsavable from the panel — and unsavable
// in a way the operator cannot act on, because the form simply does not
// submit. The budget field's step="0.1" was exactly that, and so was a select
// whose stored value was not one of its options: two of the three wedge
// incidents AGENTS.md records were client-side.
//
// This is the half that runs everywhere. The round-trip tests beside it need
// node and skip without it, which on a machine with no node leaves the
// majority of the wedge history unguarded; this reads the attributes out of the
// markup and asks config.Validate directly, so it needs nothing but Go.
//
// THE RULE. For each numeric control, the value just outside its own bound is
// taken to the server: if Validate ACCEPTS that value, the control is narrower
// than the server and a stored configuration exists that the panel cannot
// save. The probe is built from the markup rather than from a list of bounds
// kept here, so widening a control without widening the server, or narrowing
// the control at all, is what fails.
//
// WHAT IT CANNOT DO. It says nothing about a control that is WIDER than the
// server: that direction is a save the operator is told about, which is a
// disappointment rather than a wedge. And it reaches only the settings that
// have a numeric control; the enumeration in internal/archtest is what holds
// the panel to having one at all.
func TestNoSettingsControlIsNarrowerThanValidate(t *testing.T) {
	base := settingsProbeBase()
	if err := base.Validate(); err != nil {
		t.Fatalf("the configuration every probe starts from is itself refused (%v), so every probe below "+
			"would read as a control that is safely narrow", err)
	}

	pane := settingsPane(t, readPanelMarkup(t))
	for _, c := range numericSettingsControls() {
		t.Run(c.id, func(t *testing.T) {
			tag := fieldTag(t, pane, c.id)
			step := attr(tag, "step")
			// A field whose setting can hold a fraction must not be stepped to
			// whole numbers, or every fractional value the server accepts is
			// one the panel refuses. This is the budget wedge in general form.
			if !c.integer && step != "" && step != "any" {
				t.Errorf(`%s has step=%q on a setting that holds a fraction; only step="any" leaves the `+
					`values between the steps savable`, c.id, step)
			}
			outside := 1.0
			if !c.integer && (step == "" || step == "any") {
				outside = 0.5
			}

			if raw := attr(tag, "min"); raw != "" {
				min, err := strconv.ParseFloat(raw, 64)
				if err != nil {
					t.Fatalf("%s has min=%q, which is not a number", c.id, raw)
				}
				assertServerRefuses(t, c, min-outside, fmt.Sprintf("min=%s", raw))
			}
			if raw := attr(tag, "max"); raw != "" {
				max, err := strconv.ParseFloat(raw, 64)
				if err != nil {
					t.Fatalf("%s has max=%q, which is not a number", c.id, raw)
				}
				assertServerRefuses(t, c, max+outside, fmt.Sprintf("max=%s", raw))
			}
		})
	}
}

// assertServerRefuses takes one value the control will not let through to the
// server and fails when the server would have taken it.
func assertServerRefuses(t *testing.T, c settingsControl, value float64, bound string) {
	t.Helper()
	cfg := settingsProbeBase()
	c.set(&cfg, value)
	if err := cfg.Validate(); err == nil {
		t.Errorf("%s refuses %v on its own %s, and the server accepts it — a config.json holding that "+
			"value loads, and the panel then cannot save at all: the browser refuses the form before the "+
			"save is ever posted", c.id, value, bound)
	}
}

// settingsControl is one numeric control in the Settings pane, and the setting
// it writes.
type settingsControl struct {
	// id is the control's id in the markup, which is where its bounds are read
	// from.
	id string
	// integer reports whether the setting behind it holds whole numbers only.
	integer bool
	// set puts a value from this control into a configuration, in the units the
	// setting is stored in — the budget is typed in gigabytes and the
	// statistics limit in megabytes, and both are stored in bytes.
	set func(*config.Config, float64)
}

const probeModel = "mlx-community/Qwen3-8B-4bit"

// settingsProbeBase is a configuration the load path accepts, which every
// probe starts from so that a refusal can only come from the one value the
// probe changed.
func settingsProbeBase() config.Config {
	c := config.Default()
	c.Models = map[string]config.ModelSettings{probeModel: {}}
	return c
}

func numericSettingsControls() []settingsControl {
	perModel := func(put func(*config.ModelSettings, float64)) func(*config.Config, float64) {
		return func(c *config.Config, v float64) {
			ms := c.Models[probeModel]
			put(&ms, v)
			c.Models[probeModel] = ms
		}
	}
	return []settingsControl{
		{"setPort", true, func(c *config.Config, v float64) { c.Port = int(v) }},
		{"setIdle", true, func(c *config.Config, v float64) { c.IdleTimeoutSec = int(v) }},
		{"setBudget", false, func(c *config.Config, v float64) { c.MaxResidentBytes = int64(v * (1 << 30)) }},
		{"setGraceSec", true, func(c *config.Config, v float64) { c.EvictionGraceSec = int(v) }},
		{"setGraceWait", true, func(c *config.Config, v float64) { c.EvictionMaxWaitSec = int(v) }},
		{"setConc", true, func(c *config.Config, v float64) { c.DecodeConcurrency = int(v) }},
		{"setStatsMonths", true, func(c *config.Config, v float64) { c.StatsMonths = int(v) }},
		{"setStatsMB", true, func(c *config.Config, v float64) { c.StatsMaxBytes = int64(v) * (1 << 20) }},

		{"setTemp", false, func(c *config.Config, v float64) { c.Sampling.Temperature = &v }},
		{"setTopP", false, func(c *config.Config, v float64) { c.Sampling.TopP = &v }},
		{"setTopK", true, func(c *config.Config, v float64) { n := int(v); c.Sampling.TopK = &n }},
		{"setMinP", false, func(c *config.Config, v float64) { c.Sampling.MinP = &v }},
		{"setMaxTokens", true, func(c *config.Config, v float64) { n := int(v); c.Sampling.MaxTokens = &n }},

		{"ovTemp", false, perModel(func(m *config.ModelSettings, v float64) { m.Sampling.Temperature = &v })},
		{"ovTopP", false, perModel(func(m *config.ModelSettings, v float64) { m.Sampling.TopP = &v })},
		{"ovTopK", true, perModel(func(m *config.ModelSettings, v float64) { n := int(v); m.Sampling.TopK = &n })},
		{"ovMinP", false, perModel(func(m *config.ModelSettings, v float64) { m.Sampling.MinP = &v })},
		{"ovMaxTokens", true, perModel(func(m *config.ModelSettings, v float64) { n := int(v); m.Sampling.MaxTokens = &n })},
	}
}

// Every numeric control in the Settings pane is covered by the table above. A
// control added without an entry would be tested by nothing, and the rule this
// file exists for would quietly stop applying to the newest field — which is
// the field it is most likely to be wrong about.
func TestEveryNumericSettingsControlIsProbed(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range numericSettingsControls() {
		covered[c.id] = true
	}
	for _, id := range numericControlIDs(t, settingsPane(t, readPanelMarkup(t))) {
		if !covered[id] {
			t.Errorf("the Settings pane carries a number field %q that no entry in "+
				"numericSettingsControls names, so nothing checks that its bounds are not narrower than "+
				"the server's", id)
		}
	}
}

// numericControlIDs is every `type="number"` input in a stretch of markup.
func numericControlIDs(t *testing.T, pane string) []string {
	t.Helper()
	var out []string
	for _, m := range numberInputRE.FindAllStringSubmatch(pane, -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatal("the Settings pane carries no number fields, so this scan is reading the wrong markup")
	}
	return out
}

var numberInputRE = regexp.MustCompile(`<input id="([A-Za-z0-9_]+)"[^>]*type="number"`)
