package ui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
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
// left out of the form has its merging switched off. A model with settings this
// form does not own keeps them either way, so a later per-model setting is not
// wiped by someone ticking a merging box.
func TestSettingsFormPostsThePerModelMapWhole(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want map[string]any
	}{
		{
			name: "a box that is ticked switches merging on",
			expr: `perModelSettings({}, ["mlx-community/Qwen3-8B-4bit"])`,
			want: map[string]any{
				"mlx-community/Qwen3-8B-4bit": map[string]any{"merge_system_messages": true},
			},
		},
		{
			name: "a box that is clear leaves the model out",
			expr: `perModelSettings({"mlx-community/Qwen3-8B-4bit":{"merge_system_messages":true}}, [])`,
			want: map[string]any{},
		},
		{
			name: "a model's other settings survive an unticked box",
			expr: `perModelSettings({"org/a":{"merge_system_messages":true,"temperature":0.7}}, [])`,
			want: map[string]any{"org/a": map[string]any{"temperature": 0.7}},
		},
		{
			name: "a model's other settings survive a ticked box",
			expr: `perModelSettings({"org/a":{"temperature":0.7}}, ["org/a"])`,
			want: map[string]any{
				"org/a": map[string]any{"temperature": 0.7, "merge_system_messages": true},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evalPanelValue(t, c.expr, "perModelSettings")
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s = %#v, want %#v", c.expr, got, c.want)
			}
		})
	}
}

// perModelSettings is only worth testing while the form both builds its body
// from it and renders the boxes it reads. These are the two lines the value
// tests above cannot reach without a DOM.
func TestSettingsFormIsWiredToThePerModelSwitches(t *testing.T) {
	src := readPanelSource(t)
	for _, fragment := range []string{
		"per_model:         perModelSettings(state.config.per_model, checkedMergeModels()),",
		"renderMergeSwitches();",
	} {
		if !strings.Contains(src, fragment) {
			t.Errorf("the control panel no longer contains %q — the per-model switches are then asserted by nothing", fragment)
		}
	}
}
