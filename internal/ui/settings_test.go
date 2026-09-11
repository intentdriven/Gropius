package ui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/capability"
	"github.com/intentdriven/Gropius/internal/config"
)

// evalPanelValue evaluates one expression against the named functions lifted
// out of app.js and decodes what it returns, so a function that builds an
// object rather than a string can be asserted on its value.
func evalPanelValue(t *testing.T, expr string, functions ...string) map[string]any {
	t.Helper()
	src := readPanelSource(t)
	var b strings.Builder
	for _, name := range functions {
		b.WriteString(extractFunction(t, src, name))
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "process.stdout.write(JSON.stringify(%s));", expr)

	out := evalJS(t, b.String())
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("the panel returned %q, which is not an object: %v", out, err)
	}
	return v
}

// The per-model settings a save posts are the whole map, not a patch: the
// server replaces what it holds with what the form sends, which is how a model
// left out of the form has its merging switched off and its pin dropped. A
// model with a setting this form does not own keeps it either way, so a later
// per-model setting is not wiped by someone ticking a box.
//
// One map, so one function: merging, pinning and the sampling override are
// three rows of the same form and three fields of the same entry.
func TestSettingsFormPostsThePerModelMapWhole(t *testing.T) {
	const qwen = "mlx-community/Qwen3-8B-4bit"
	cases := []struct {
		name string
		expr string
		want map[string]any
	}{
		{
			name: "a box that is ticked switches merging on",
			expr: `modelSettings({}, {}, ["` + qwen + `"], ["` + qwen + `"], [], [])`,
			want: map[string]any{qwen: map[string]any{"merge_system_messages": true}},
		},
		{
			name: "a box that is clear leaves the model out",
			expr: `modelSettings({"` + qwen + `":{"merge_system_messages":true}}, {}, ["` + qwen + `"], [], [], [])`,
			want: map[string]any{},
		},
		{
			name: "a ticked pin box pins the model",
			expr: `modelSettings({}, {}, [], [], ["org/a"], ["org/a"])`,
			want: map[string]any{"org/a": map[string]any{"pinned": true}},
		},
		{
			name: "an unticked pin box unpins it and leaves its other settings",
			expr: `modelSettings({"org/a":{"pinned":true,"merge_system_messages":true}}, {}, [], [], ["org/a"], [])`,
			want: map[string]any{"org/a": map[string]any{"merge_system_messages": true}},
		},
		{
			name: "the override editor holds the whole sampling set",
			expr: `modelSettings({"org/a":{"sampling":{"temperature":0.7}}}, {}, [], [], [], [])`,
			want: map[string]any{},
		},
		{
			name: "a sampling override sits beside the switches on one model",
			expr: `modelSettings({"org/a":{"pinned":true}}, {"org/a":{"temperature":0.7}}, ["org/a"], ["org/a"], ["org/a"], ["org/a"])`,
			want: map[string]any{"org/a": map[string]any{
				"sampling":              map[string]any{"temperature": 0.7},
				"merge_system_messages": true,
				"pinned":                true,
			}},
		},
		{
			// The settings are keyed by model id, and a model can be given
			// settings before it is downloaded — the app keeps a key for a
			// model this machine does not have. The form draws a box only for
			// the models it lists, so a model it does not list has no box to
			// read, and its settings are carried through rather than deleted
			// by an unrelated save.
			name: "a model the form does not list keeps its settings",
			expr: `modelSettings({"org/not-downloaded":{"merge_system_messages":true}}, {}, ["org/a"], [], [], [])`,
			want: map[string]any{
				"org/not-downloaded": map[string]any{"merge_system_messages": true},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evalPanelValue(t, c.expr, "modelSettings", "applyModelSwitch", "applyModelNumber")
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s = %#v, want %#v", c.expr, got, c.want)
			}
		})
	}
}

// modelSettings is only worth testing while the form both builds its body from
// it and renders the boxes it reads. These are the lines the value tests above
// cannot reach without a DOM.
//
// Matched on the identifiers rather than the line, so re-aligning the object
// literal the body is built from — a whitespace-only edit — does not report
// the switches as unasserted.
func TestSettingsFormIsWiredToThePerModelSwitches(t *testing.T) {
	src := readPanelSource(t)
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`models:\s*modelSettings\(\s*state\.config\.models,\s*overrides,\s*` +
			`listedMergeModels\(\),\s*checkedMergeModels\(\),\s*` +
			`listedPinModels\(\),\s*checkedPinModels\(\),\s*` +
			`listedContextModels\(\),\s*typedContextModels\(\),?\s*\)`),
		regexp.MustCompile(`\brenderMergeSwitches\(\)`),
		regexp.MustCompile(`\brenderPinSwitches\(\)`),
		regexp.MustCompile(`\brenderContextFields\(\)`),
	} {
		if !want.MatchString(src) {
			t.Errorf("the control panel no longer matches %s — the per-model switches are then asserted by nothing", want)
		}
	}
}

// The figure beside the field is what makes an impossible pinned set visible
// at pin time rather than at the first refused request, so it has to charge
// each model what the pool charges it. A model whose configuration says
// nothing about its cache is charged its size on disk plus a fifth, as it
// always was.
func TestSettingsFormChargesPinnedModelsWhatThePoolCharges(t *testing.T) {
	const models = `[{"repo_id":"org/a","bytes":1000},{"repo_id":"org/b","bytes":500}]`
	cases := []struct {
		expr string
		want float64
	}{
		{fmt.Sprintf(`pinnedCharge(%s, [], {}, 0)`, models), 0},
		{fmt.Sprintf(`pinnedCharge(%s, ["org/a"], {}, 0)`, models), float64(capability.LoadCost(1000))},
		{fmt.Sprintf(`pinnedCharge(%s, ["org/a","org/b"], {}, 0)`, models),
			float64(capability.LoadCost(1000) + capability.LoadCost(500))},
		// A pinned model this Mac has not downloaded has no size to charge.
		{fmt.Sprintf(`pinnedCharge(%s, ["org/not-downloaded"], {}, 0)`, models), 0},
	}
	for _, tc := range cases {
		got := evalPanelNumber(t, tc.expr, "foldRepoID", "servedContext", "modelCharge", "pinnedCharge")
		if got != tc.want {
			t.Errorf("%s = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

// And a model that does say — every model downloaded by a build that records
// the figure — is charged what the window it is served at and its cache cost
// make it, once per sequence its server may decode. The panel is the surface
// an operator picks a pinned set on, so a panel charging anything else would
// show a set fitting that the pool then refuses to hold.
func TestSettingsFormChargesTheCacheTheModelsConfigurationImplies(t *testing.T) {
	const (
		size      = 1000
		kv        = 3
		declared  = 200
		sequences = 4
	)
	models := fmt.Sprintf(
		`[{"repo_id":"org/a","bytes":%d,"context_length":%d,"kv_charge_per_token":%d}]`,
		size, declared, kv)
	want := float64(capability.LoadCostOf(capability.Load{
		DiskBytes:        size,
		KVChargePerToken: kv,
		Window:           declared,
		Sequences:        sequences,
	}))
	expr := fmt.Sprintf(`pinnedCharge(%s, ["org/a"], {}, %d)`, models, sequences)
	if got := evalPanelNumber(t, expr, "foldRepoID", "servedContext", "modelCharge", "pinnedCharge"); got != want {
		t.Errorf("%s = %v, want %v — the panel and the pool charge the same model differently", expr, got, want)
	}
}

// A model served at a window of the operator's own is charged that window,
// which is the whole point of the setting: the figure beside the boxes falls
// when they lower it, and the pinned set they could not save becomes one they
// can.
func TestSettingsFormChargesTheServedContext(t *testing.T) {
	const (
		size      = 1000
		kv        = 3
		declared  = 200000
		served    = 2000
		sequences = 1
	)
	models := fmt.Sprintf(
		`[{"repo_id":"org/a","bytes":%d,"context_length":%d,"kv_charge_per_token":%d}]`,
		size, declared, kv)
	cfg := fmt.Sprintf(`{"models":{"org/a":{"served_context":%d}}}`, served)
	want := float64(capability.LoadCostOf(capability.Load{
		DiskBytes:        size,
		KVChargePerToken: kv,
		Window:           served,
		Sequences:        sequences,
	}))
	expr := fmt.Sprintf(`pinnedCharge(%s, ["org/a"], %s, %d)`, models, cfg, sequences)
	if got := evalPanelNumber(t, expr, "foldRepoID", "servedContext", "modelCharge", "pinnedCharge"); got != want {
		t.Errorf("%s = %v, want %v", expr, got, want)
	}
}

// The panel and Go must agree about which window that is, including the two
// cases where the setting is not honoured: none set, and one larger than the
// model can address.
func TestThePanelResolvesTheServedWindowAsGoDoes(t *testing.T) {
	cfg := config.Config{Models: map[string]config.ModelSettings{
		"org/set":  {ServedContext: 32768},
		"org/over": {ServedContext: 300000},
	}}
	cases := []struct{ id string }{{"org/set"}, {"org/over"}, {"org/none"}}
	const declared = 262144
	js := `{"models":{"org/set":{"served_context":32768},"org/over":{"served_context":300000}}}`
	for _, c := range cases {
		expr := fmt.Sprintf(`servedContext(%s, %q, %d)`, js, c.id, declared)
		want := float64(cfg.ServedContext(c.id, declared))
		if got := evalPanelNumber(t, expr, "foldRepoID", "servedContext"); got != want {
			t.Errorf("%s = %v, want %v", expr, got, want)
		}
	}
}

// The window is a per-model setting like the pin and the merging switch, and
// it posts on the same map: a figure typed into a model's field reaches the
// save, a blank field means the model's own window and leaves nothing behind,
// and a model the form does not list keeps what it has.
func TestSettingsFormPostsTheServedContext(t *testing.T) {
	got := evalPanelValue(t,
		`{"out": modelSettings({"org/kept":{"served_context":1000},"org/cleared":{"served_context":2000}}, {},
			[], [], [], [], ["org/typed","org/cleared"], {"org/typed":4096,"org/cleared":0})}`,
		"foldRepoID", "modelSettings", "applyModelSwitch", "applyModelNumber")
	want := map[string]any{"out": map[string]any{
		"org/kept":  map[string]any{"served_context": float64(1000)},
		"org/typed": map[string]any{"served_context": float64(4096)},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("modelSettings = %v, want %v", got, want)
	}
}

// Every pin has to be removable from the form that made it. A pin whose model
// is not in the list — one deleted since it was pinned, or one pinned before
// it was downloaded — gets a row of its own, or it can never be unticked: the
// form carries through what it does not list, so a pin with no row is a pin
// for good.
func TestSettingsFormDrawsARowForEveryPin(t *testing.T) {
	const models = `[{"repo_id":"org/here","bytes":10}]`
	got := evalPanelValue(t,
		fmt.Sprintf(`{"rows": pinRows(%s, ["org/here", "org/gone"])}`, models),
		"foldRepoID", "pinRows")
	want := map[string]any{"rows": []any{
		map[string]any{"id": "org/here", "checked": true, "absent": false},
		map[string]any{"id": "org/gone", "checked": true, "absent": true},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pinRows = %v, want %v", got, want)
	}
}

// A model that is still downloading declares its size from the first byte.
// Charging it nothing is how a pinned pair that cannot fit gets ticked: the
// figure reads 0 while both boxes are ticked, and the machine is over budget
// the moment the downloads land.
func TestSettingsFormChargesADownloadItsDeclaredSize(t *testing.T) {
	const models = `[{"repo_id":"org/incoming","bytes":0,"size_bytes":1000}]`
	got := evalPanelNumber(t, fmt.Sprintf(`pinnedCharge(%s, ["org/incoming"], {}, 0)`, models), "foldRepoID", "servedContext", "modelCharge", "pinnedCharge")
	if want := float64(capability.LoadCost(1000)); got != want {
		t.Errorf("pinnedCharge = %v, want %v", got, want)
	}
}

// The panel's pinned set comes from the pool, through the state snapshot, and
// not from the stored settings: the two agree except in the moment a pin is
// reconciled with a model that has just arrived, and the panel is the surface
// an operator acts on. Asserted against the source because the alternative is
// a whole DOM: the reverting edit is a one-word change, and this is what makes
// it fail.
func TestThePanelReadsThePinnedSetThePoolIsEnforcing(t *testing.T) {
	src := readPanelSource(t)
	if !strings.Contains(src, "state.pinned") {
		t.Error("the panel never reads state.pinned, so it cannot show what the pool is protecting")
	}
	if !regexp.MustCompile(`pinRows\(\s*state\.models \|\| \[\],\s*state\.pinned \|\| \[\]\s*\)`).MatchString(src) {
		t.Error("the pin boxes are no longer drawn from state.pinned; the stored settings are not what is being enforced")
	}
	if !strings.Contains(src, "state.machine") {
		t.Error("the panel never reads state.machine, so it cannot say what a pinned set leaves of the budget")
	}
}
