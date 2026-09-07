package ui

import (
	"strings"
	"testing"
)

const gb = float64(1 << 30)

// The operator types gigabytes and the file stores bytes: one unit to reason
// in, one unit to store. A blank field means the default, which is how the
// stored settings stay free of a figure nobody chose.
func TestSettingsFormPostsTheBudgetInBytes(t *testing.T) {
	cases := []struct {
		expr string
		want float64
	}{
		{`budgetBytes("96")`, 96 * gb},
		{`budgetBytes("7.5")`, 7.5 * gb},
		{`budgetBytes("")`, 0},
		{`budgetBytes("   ")`, 0},
		{`budgetBytes("0")`, 0},
		{`budgetBytes("-4")`, 0},
		{`budgetBytes("nonsense")`, 0},
	}
	for _, c := range cases {
		if got := evalPanelNumber(t, c.expr, "budgetBytes"); got != c.want {
			t.Errorf("%s = %v, want %v", c.expr, got, c.want)
		}
	}
}

// The field shows what is in force: blank while the budget is the default, so
// that saving an unrelated setting does not turn the default into a figure of
// the operator's own.
func TestSettingsFormLeavesADefaultBudgetFieldBlank(t *testing.T) {
	blank := evalPanel(t, `budgetFieldValue({"budget":82463372083,"budget_is_default":true})`, "budgetFieldValue")
	if blank != "" {
		t.Errorf("budgetFieldValue = %q for the default, want a blank field", blank)
	}
	set := evalPanel(t, `budgetFieldValue({"budget":103079215104,"budget_is_default":false})`, "budgetFieldValue")
	if set != "96" {
		t.Errorf("budgetFieldValue = %q, want the saved budget in gigabytes", set)
	}
}

// Beside the field: what the budget is, what share of this Mac that is, and
// what the models in memory are using of it.
func TestSettingsFormShowsTheBudgetAsAShareOfThisMac(t *testing.T) {
	line := evalPanel(t, `budgetHint({"total_ram":137438953472,"budget":82463372083,`+
		`"budget_is_default":true,"warn_above":116824368742,"resident_bytes":0,"over_budget":false})`,
		"bytes", "size", "budgetHint")
	if !strings.Contains(line, "60%") {
		t.Errorf("budgetHint = %q, want the share of this Mac's memory", line)
	}
	if !strings.Contains(line, "default") {
		t.Errorf("budgetHint = %q, want it to say the figure is the default", line)
	}
}

// A Mac whose memory cannot be read has no share to state, so the line states
// the budget alone rather than a percentage of nothing.
func TestSettingsFormOmitsTheShareOfAMachineItCannotMeasure(t *testing.T) {
	line := evalPanel(t, `budgetHint({"total_ram":0,"budget":8589934592,`+
		`"budget_is_default":true,"warn_above":0,"resident_bytes":0,"over_budget":false})`,
		"bytes", "size", "budgetHint")
	if strings.Contains(line, "%") {
		t.Errorf("budgetHint = %q, want no percentage when this Mac cannot be measured", line)
	}
	if !strings.Contains(line, "8.0 GB") {
		t.Errorf("budgetHint = %q, want the budget itself", line)
	}
}

// Lowering the budget under what is loaded unloads nothing, so the panel has
// to say the machine is over its budget until those models go.
func TestSettingsFormSaysWhenTheMachineIsOverItsBudget(t *testing.T) {
	line := evalPanel(t, `budgetHint({"total_ram":137438953472,"budget":4294967296,`+
		`"budget_is_default":false,"warn_above":116824368742,"resident_bytes":10307921510,"over_budget":true})`,
		"bytes", "size", "budgetHint")
	if !strings.Contains(line, "over") {
		t.Errorf("budgetHint = %q, want it to say the machine is over its budget", line)
	}
	if !strings.Contains(line, "unload") {
		t.Errorf("budgetHint = %q, want it to say nothing is unloaded on the operator's behalf", line)
	}
}

// A budget claiming most of the Mac is advice, and the panel gives it while
// the figure is being chosen rather than only after it is saved.
func TestSettingsFormWarnsAboutABudgetThatClaimsMostOfTheMac(t *testing.T) {
	line := evalPanel(t, `budgetHint({"total_ram":137438953472,"budget":133143986176,`+
		`"budget_is_default":false,"warn_above":116824368742,"resident_bytes":0,"over_budget":false})`,
		"bytes", "size", "budgetHint")
	if !strings.Contains(line, "share") && !strings.Contains(line, "everything else") {
		t.Errorf("budgetHint = %q, want it to say macOS and everything else share this memory", line)
	}
}

// The panel reads the budget the pool is enforcing, through the machine object
// on the snapshot, rather than the stored setting — which is blank while the
// default is in force and says nothing about what is actually being enforced.
func TestThePanelReadsTheBudgetTheMachineObjectCarries(t *testing.T) {
	src := readPanelSource(t)
	if !strings.Contains(src, "state.machine") {
		t.Error("the panel never reads state.machine, so it cannot say what budget is in force")
	}
	if strings.Contains(src, "state.config.max_resident_bytes") {
		t.Error("the panel reads the stored budget; what is enforced is what the machine object carries")
	}
}

// The line is written from the figure being typed, and a cleared field means
// the default — so it has to show the default, not the figure it replaces.
func TestSettingsFormShowsTheDefaultWhenTheFieldIsCleared(t *testing.T) {
	machine := `{"total_ram":137438953472,"budget":103079215104,"default_budget":82463372083,` +
		`"budget_is_default":false,"warn_above":116824368742,"resident_bytes":0,"over_budget":false}`

	cleared := evalPanelValue(t, `budgetShown(`+machine+`, 0)`, "budgetShown")
	if got := cleared["budget"]; got != float64(82463372083) {
		t.Errorf("budgetShown with a cleared field = %v, want the default budget", got)
	}
	if cleared["budget_is_default"] != true {
		t.Error("budgetShown with a cleared field does not say the figure is the default")
	}

	typed := evalPanelValue(t, `budgetShown(`+machine+`, 68719476736)`, "budgetShown")
	if got := typed["budget"]; got != float64(68719476736) {
		t.Errorf("budgetShown = %v, want the figure being typed", got)
	}
	if typed["budget_is_default"] != false {
		t.Error("a typed figure is reported as the default")
	}
}

// Typing a figure under what is already loaded says so before the save, not
// after it.
func TestSettingsFormSaysAFigureBeingTypedIsUnderWhatIsLoaded(t *testing.T) {
	machine := `{"total_ram":137438953472,"budget":103079215104,"default_budget":82463372083,` +
		`"budget_is_default":false,"warn_above":116824368742,"resident_bytes":10307921510,"over_budget":false}`
	shown := evalPanelValue(t, `budgetShown(`+machine+`, 4294967296)`, "budgetShown")
	if shown["over_budget"] != true {
		t.Error("a figure typed under what is already in memory is not reported as over budget")
	}
}

// The field and the line it writes have to exist in the page, or the panel
// throws on load and every tab goes with it.
func TestSettingsFormHasTheBudgetControl(t *testing.T) {
	page, err := assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"setBudget", "budgetHint"} {
		if !strings.Contains(string(page), `id="`+id+`"`) {
			t.Errorf("the settings form has no element with id %q, which app.js addresses on load", id)
		}
	}
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "max_resident_bytes") {
		t.Error("app.js never posts max_resident_bytes, so the budget cannot be saved")
	}
}
