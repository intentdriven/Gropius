package ui

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// Every search result says what kind of model it is, in HuggingFace's own
// words, or says there is no tag — which is a fact about the repository and not
// a blank to be read as an oversight.
func TestSearchResultShowsThePipelineTag(t *testing.T) {
	cases := []struct {
		name   string
		result string
		want   string
	}{
		{"a tagged repo", `{"pipeline_tag":"text-generation"}`, "esc(text-generation)"},
		{"a speech model", `{"pipeline_tag":"automatic-speech-recognition"}`, "esc(automatic-speech-recognition)"},
		{"the Hub says nothing", `{}`, "no tag"},
		{"the Hub says nothing, at length", `{"pipeline_tag":""}`, "no tag"},
		{"and a tag is never trusted as markup", `{"pipeline_tag":"<b>x</b>"}`, "esc(<b>x</b>)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evalPanel(t, "pipelineLabel("+c.result+")", "pipelineLabel")
			if got != c.want {
				t.Errorf("pipelineLabel(%s) = %q, want %q", c.result, got, c.want)
			}
		})
	}
}

// pipelineLabel is only worth testing while the search card is drawn from it.
func TestSearchResultIsBuiltFromThePipelineLabel(t *testing.T) {
	body := extractFunction(t, readPanelSource(t), "renderSearch")
	if !strings.Contains(body, "pipelineLabel(m)") {
		t.Error("the search card no longer calls pipelineLabel, so what a result says about its kind is asserted by nothing")
	}
}

// The rule the panel posts is the rule the server declares: the field names are
// read off the Go type here, so renaming one in Go fails this test rather than
// silently dropping half the rule at the next save.
func TestTheSettingsFormPostsTheChatRuleUnderTheServersFieldNames(t *testing.T) {
	for _, name := range chatRuleFieldNames(t) {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `:\s`).MatchString(readPanelSource(t)) {
			t.Errorf("the settings body names no %q, which is half the rule the server reads", name)
		}
	}
	src := readPanelSource(t)
	if !regexp.MustCompile(`chat_rule:\s*chatRule\(`).MatchString(src) {
		t.Error("the settings body no longer builds chat_rule from chatRule(), so the fields reach the server unasserted")
	}
	if !regexp.MustCompile(`\$\('setChatPipelines'\)\.value\s*=`).MatchString(src) ||
		!regexp.MustCompile(`\$\('setChatTags'\)\.value\s*=`).MatchString(src) {
		t.Error("the settings form no longer fills both rule fields from the served settings")
	}
}

// What the two fields mean, typed as a person types them: a comma-separated
// list, trimmed, with the blanks dropped — and a cleared field meaning "do not
// test this half", which is the state that must survive a save.
func TestTheChatRuleFieldsReadAsTypedLists(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want map[string]any
	}{
		{
			name: "the shipped rule, as the fields hold it",
			expr: `chatRule("text-generation, image-text-to-text", "conversational")`,
			want: map[string]any{
				"pipeline_tags": []any{"text-generation", "image-text-to-text"},
				"required_tags": []any{"conversational"},
			},
		},
		{
			name: "spacing and trailing commas are how people type",
			expr: `chatRule("  text-generation ,, image-text-to-text ,", " conversational ")`,
			want: map[string]any{
				"pipeline_tags": []any{"text-generation", "image-text-to-text"},
				"required_tags": []any{"conversational"},
			},
		},
		{
			name: "a cleared field tests nothing, and says so with a list rather than nothing at all",
			expr: `chatRule("", "")`,
			want: map[string]any{
				"pipeline_tags": []any{},
				"required_tags": []any{},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evalPanelValue(t, c.expr, "splitTagField", "chatRule")
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s = %#v, want %#v", c.expr, got, c.want)
			}
		})
	}
}

// The panel is the accessible layer over what Go can do, and the words it
// prints for the shipped rule are Go's. A default changed in one place and not
// the other would leave the panel telling an operator a rule the server does
// not run.
func TestThePanelNamesTheShippedRule(t *testing.T) {
	page, err := assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(page)
	for _, id := range []string{"setChatPipelines", "setChatTags"} {
		if !strings.Contains(markup, `id="`+id+`"`) {
			t.Errorf("the settings pane has no %q field, so the chat rule is a Go setting with no panel equivalent", id)
		}
	}
	rule := config.DefaultChatRule()
	for _, word := range append(append([]string{}, rule.PipelineTags...), rule.RequiredTags...) {
		if !strings.Contains(markup, word) {
			t.Errorf("the settings pane does not name %q, which the shipped rule is made of", word)
		}
	}
}

// chatRuleFieldNames reads the rule's wire names off the Go type, so this file
// holds the panel to the server rather than to a copy of the server.
func chatRuleFieldNames(t *testing.T) []string {
	t.Helper()
	typ := reflect.TypeOf(config.ChatRule{})
	names := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if tag == "" {
			t.Fatalf("%s carries no json tag", typ.Field(i).Name)
		}
		names = append(names, tag)
	}
	if len(names) == 0 {
		t.Fatal("the chat rule has no fields, so this test proves nothing")
	}
	return names
}
