package ui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// Every sampling parameter the configuration holds must be reachable from the
// panel, machine-wide and per model, or a default exists that only someone
// editing the file by hand can set.
func TestSettingsFormOffersEverySamplingParameter(t *testing.T) {
	page, err := assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}

	for _, field := range samplingJSONFields(t) {
		if !strings.Contains(string(script), "'"+field+"'") {
			t.Errorf("app.js never names the sampling parameter %q, so the panel cannot send it", field)
		}
	}

	// Both editors, and the field that carries the overrides.
	for _, id := range []string{"setTemp", "setTopP", "setTopK", "setMinP", "setMaxTokens",
		"ovTemp", "ovTopP", "ovTopK", "ovMinP", "ovMaxTokens", "ovModel"} {
		if !strings.Contains(string(page), `id="`+id+`"`) {
			t.Errorf("the settings form has no control with id %q", id)
		}
	}
	// The override editor holds the whole sampling set while the form is open
	// and hands it to the one per-model map the save posts.
	if !strings.Contains(string(script), "sampling: overrides[id]") {
		t.Error("app.js never posts a per-model sampling override, so one cannot be saved")
	}
	// A blank field must reach the server as null. parseInt(x) || 0 turns a
	// blank field into a real zero, which is a temperature, not "unset".
	if !strings.Contains(string(script), "numberOrNull") {
		t.Error("app.js does not send a blank sampling field as null")
	}
}

// samplingJSONFields returns the JSON names of every field on config.Sampling.
func samplingJSONFields(t *testing.T) []string {
	t.Helper()
	rt := reflect.TypeOf(config.Sampling{})
	out := make([]string, 0, rt.NumField())
	for i := range rt.NumField() {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name == "" {
			t.Fatalf("config.Sampling field %s has no json tag", rt.Field(i).Name)
		}
		out = append(out, name)
	}
	return out
}

// panelInputFor maps a sampling parameter to the machine-wide field that edits
// it. The per-model fields deliberately show a different hint (the machine's
// value, not the model server's), so they are not in here.
var panelInputFor = map[string]string{
	"temperature": "setTemp",
	"top_p":       "setTopP",
	"top_k":       "setTopK",
	"min_p":       "setMinP",
	"max_tokens":  "setMaxTokens",
}

// The placeholders tell the user what a blank field means, so they must be the
// model server's own defaults — read off the recorded bounds rather than
// written out here, or the panel can drift from the figure the reference page
// gives while both tests stay green.
func TestBlankSamplingFieldsShowTheModelServerDefaults(t *testing.T) {
	page, err := assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range config.SamplingBounds() {
		id, ok := panelInputFor[b.Field]
		if !ok {
			t.Errorf("no machine-wide input is mapped for %q", b.Field)
			continue
		}
		ph := placeholderOf(inputTag(t, string(page), id))
		if !strings.HasPrefix(ph, b.DefaultText()) {
			t.Errorf("%s placeholder is %q, want it to open with the model server's own default %q",
				id, ph, b.DefaultText())
		}
	}
}

func placeholderOf(tag string) string {
	idx := strings.Index(tag, `placeholder="`)
	if idx < 0 {
		return ""
	}
	rest := tag[idx+len(`placeholder="`):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// The panel's own record of the parameters must not drift from the wire
// format: what it posts is decoded straight into config.Sampling.
func TestPostedSamplingShapeDecodesIntoTheConfigType(t *testing.T) {
	fields := samplingJSONFields(t)
	body := map[string]any{}
	for _, f := range fields {
		body[f] = nil
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var s config.Sampling
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("a form post of every field as null does not decode: %v", err)
	}
	if !s.IsZero() {
		t.Errorf("sampling = %+v, want every field unset", s)
	}
}

// Two ways the override editor could lie to the user, both pinned here
// because the panel has no runtime test harness in this repository.
func TestOverrideEditorDoesNotSilentlyDiscardInput(t *testing.T) {
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(script)

	// The per-model fields sit inside the settings form, above its submit
	// button, so pressing Save (or Enter in a number field) must not throw
	// away what is typed in them.
	if !strings.Contains(js, "foldPendingOverride()") {
		t.Error("the settings submit handler does not fold the per-model fields in, so values typed there are silently dropped on Save")
	}
	if strings.Count(js, "foldPendingOverride()") < 2 {
		t.Error("foldPendingOverride is defined but not called from both the button and the submit handler")
	}

	// Setting an override replaces it wholesale, so the fields have to show
	// what is stored — otherwise editing a temperature quietly deletes the
	// token budget saved beside it.
	if !strings.Contains(js, "prefillOverride") {
		t.Error("selecting a model does not load its saved override into the fields, so editing one parameter discards the others")
	}
	if !strings.Contains(js, `$('ovModel').addEventListener('change'`) {
		t.Error("no change listener on the model select, so the fields never follow the selection")
	}
}

// The panel's own numeric constraints have to admit everything the server
// accepts. An explicit fractional step makes the browser refuse to submit the
// whole form for a value that is not a multiple of it — so temperature 0.62 or
// top-p 0.995, both perfectly good, would take every other setting on the form
// down with them, without the handler ever running.
func TestSamplingInputsDoNotBlockValuesTheServerAccepts(t *testing.T) {
	page, err := assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"setTemp", "setTopP", "setMinP", "ovTemp", "ovTopP", "ovMinP"} {
		tag := inputTag(t, string(page), id)
		if !strings.Contains(tag, `step="any"`) {
			t.Errorf("%s carries %q — a fractional step blocks the form for values the model server serves", id, tag)
		}
	}
	// The two integer fields are whole numbers, which is the accepted set.
	for _, id := range []string{"setTopK", "setMaxTokens", "ovTopK", "ovMaxTokens"} {
		if tag := inputTag(t, string(page), id); !strings.Contains(tag, `step="1"`) {
			t.Errorf("%s is a whole-number field but carries %q", id, tag)
		}
	}
}

// The top-k ceiling is Gropius' own, so the panel has to carry the same figure
// the configuration enforces rather than a copy that can drift from it.
func TestTopKInputsCarryTheConfiguredCeiling(t *testing.T) {
	page, err := assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(`max="%d"`, config.MaxTopK)
	for _, id := range []string{"setTopK", "ovTopK"} {
		if tag := inputTag(t, string(page), id); !strings.Contains(tag, want) {
			t.Errorf("%s carries %q, want %s", id, tag, want)
		}
	}
}

// The model select is rebuilt from the live state, so a model that finishes
// downloading while the form is open can be given an override without a reload.
func TestOverrideModelListFollowsNewModels(t *testing.T) {
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(script)
	if !strings.Contains(js, "refreshOverrideModels") {
		t.Fatal("the model select is not refreshed independently of the form's values")
	}
	// It has to be called from render(), which runs on every state frame, not
	// only from renderSettings(), which returns early while the form is dirty.
	renderBody := between(js, "function render() {", "}")
	if !strings.Contains(renderBody, "refreshOverrideModels()") {
		t.Errorf("render() does not refresh the model select: %q", renderBody)
	}
}

// inputTag returns the opening tag of the input with the given id.
func inputTag(t *testing.T, page, id string) string {
	t.Helper()
	idx := strings.Index(page, `id="`+id+`"`)
	if idx < 0 {
		t.Fatalf("no control with id %q", id)
	}
	tag := page[idx:]
	if end := strings.Index(tag, ">"); end >= 0 {
		tag = tag[:end]
	}
	return tag
}

func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return rest
	}
	return rest[:j]
}

// The completion-token ceiling is Gropius' own too, so the panel carries the
// configured figure rather than a copy of it.
func TestMaxTokensInputsCarryTheConfiguredCeiling(t *testing.T) {
	page, err := assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(`max="%d"`, config.MaxCompletionTokens)
	for _, id := range []string{"setMaxTokens", "ovMaxTokens"} {
		if tag := inputTag(t, string(page), id); !strings.Contains(tag, want) {
			t.Errorf("%s carries %q, want %s", id, tag, want)
		}
	}
}
