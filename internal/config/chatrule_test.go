package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The rule the server ships with, spelled out here rather than read from the
// code: the default is a promise made on three surfaces — this file, the
// control panel and the chat client — and a test that read it from the same
// place the code does would hold none of them to anything.
func TestTheDefaultChatRuleIsTheOneShipped(t *testing.T) {
	r := DefaultChatRule()
	wantPipelines := []string{"text-generation", "image-text-to-text"}
	wantRequired := []string{"conversational"}
	if strings.Join(r.PipelineTags, ",") != strings.Join(wantPipelines, ",") {
		t.Errorf("default pipeline tags = %v, want %v", r.PipelineTags, wantPipelines)
	}
	if strings.Join(r.RequiredTags, ",") != strings.Join(wantRequired, ",") {
		t.Errorf("default required tags = %v, want %v", r.RequiredTags, wantRequired)
	}
}

// What the rule says about a model, at its default and away from it. The words
// are HuggingFace's, and Hub casing is not a promise, so the comparison folds
// case and trims.
func TestChatRuleMatches(t *testing.T) {
	cases := []struct {
		name     string
		rule     ChatRule
		pipeline string
		tags     []string
		want     bool
	}{
		{
			name:     "a conversational text-generation model chats",
			rule:     DefaultChatRule(),
			pipeline: "text-generation",
			tags:     []string{"mlx", "conversational", "safetensors"},
			want:     true,
		},
		{
			name:     "so does a conversational vision-language model",
			rule:     DefaultChatRule(),
			pipeline: "image-text-to-text",
			tags:     []string{"conversational"},
			want:     true,
		},
		{
			name:     "a speech model does not",
			rule:     DefaultChatRule(),
			pipeline: "automatic-speech-recognition",
			tags:     []string{"conversational"},
			want:     false,
		},
		{
			name:     "nor does a text-generation model that is not conversational",
			rule:     DefaultChatRule(),
			pipeline: "text-generation",
			tags:     []string{"mlx"},
			want:     false,
		},
		{
			name: "nor does a model the Hub did not tag at all",
			rule: DefaultChatRule(),
			want: false,
		},
		{
			name:     "casing and surrounding space are not a promise",
			rule:     DefaultChatRule(),
			pipeline: "  Text-Generation ",
			tags:     []string{"CONVERSATIONAL"},
			want:     true,
		},
		{
			name:     "an empty pipeline list does not test the pipeline tag",
			rule:     ChatRule{PipelineTags: []string{}, RequiredTags: []string{"conversational"}},
			pipeline: "automatic-speech-recognition",
			tags:     []string{"conversational"},
			want:     true,
		},
		{
			name:     "an empty required list does not test the tags",
			rule:     ChatRule{PipelineTags: []string{"text-generation"}, RequiredTags: []string{}},
			pipeline: "text-generation",
			want:     true,
		},
		{
			name:     "a rule that tests neither half offers everything",
			rule:     ChatRule{PipelineTags: []string{}, RequiredTags: []string{}},
			pipeline: "",
			want:     true,
		},
		{
			name:     "every required tag has to be there, not just one",
			rule:     ChatRule{RequiredTags: []string{"conversational", "mlx"}},
			pipeline: "text-generation",
			tags:     []string{"conversational"},
			want:     false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.rule.Matches(c.pipeline, c.tags); got != c.want {
				t.Errorf("Matches(%q, %v) = %v, want %v", c.pipeline, c.tags, got, c.want)
			}
		})
	}
}

// A config that says nothing about the rule is served the default; one that
// says something is served what it says. The two are not the same state, and
// the file has to keep them apart.
func TestEffectiveChatRuleResolvesTheDefault(t *testing.T) {
	if got := Default().EffectiveChatRule(); !got.Equal(DefaultChatRule()) {
		t.Errorf("a fresh config resolves to %+v, want the default %+v", got, DefaultChatRule())
	}
	c := Default()
	c.ChatRule = ChatRule{PipelineTags: []string{"text-generation"}, RequiredTags: []string{}}
	if got := c.EffectiveChatRule(); !got.Equal(c.ChatRule) {
		t.Errorf("a stored rule resolves to %+v, want %+v", got, c.ChatRule)
	}
}

// The distinction the file has to carry: no key at all means the default, and
// a rule with both lists cleared means "offer everything". A round trip that
// folded the second into the first would hand an operator who cleared the
// fields the very rule they cleared.
func TestChatRuleRoundTripsThroughTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// No key at all.
	if err := os.WriteFile(path, []byte(`{"port":11535}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.ChatRule.IsZero() {
		t.Errorf("a file with no chat_rule loaded %+v, want the unset rule", c.ChatRule)
	}
	if !c.EffectiveChatRule().Equal(DefaultChatRule()) {
		t.Errorf("a file with no chat_rule is served %+v, want the default", c.EffectiveChatRule())
	}

	// Both lists cleared, which is "test neither half".
	cleared := Default()
	cleared.ChatRule = ChatRule{PipelineTags: []string{}, RequiredTags: []string{}}
	b, err := json.Marshal(cleared)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"chat_rule"`) {
		t.Fatalf("a cleared rule was not written to the file at all: %s", b)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	c, _, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.ChatRule.IsZero() {
		t.Error("a cleared rule read back as the unset rule, so clearing the fields restores the default")
	}
	if !c.EffectiveChatRule().Matches("automatic-speech-recognition", nil) {
		t.Error("a cleared rule does not offer everything, which is what clearing both fields means")
	}

	// An unset rule is not written at all, so a file this build wrote is a
	// file an operator can still read.
	b, err = json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"chat_rule"`) {
		t.Errorf("an unset rule was written to the file: %s", b)
	}
}

// The rule is read from a file another local account can write in shared-cache
// mode, and its words are republished to the LAN. A hand-edited or planted rule
// is repaired rather than refused: a refused config.json takes the whole
// install down to loopback, which is far too much to pay for a tag list.
func TestLoadRepairsAnUnusableChatRule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	long := strings.Repeat("x", MaxChatRuleTagBytes+1)
	many := make([]string, MaxChatRuleTags+5)
	for i := range many {
		many[i] = fmt.Sprintf("tag-%d", i)
	}
	rule := ChatRule{
		PipelineTags: append([]string{"text-generation", long, "with\x00nul", "  ", "text-generation"}, "image-text-to-text"),
		RequiredTags: many,
	}
	c := Default()
	c.ChatRule = rule
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	got, notices, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(notices.Repaired) == 0 {
		t.Error("an unusable chat rule was repaired silently; the operator is told nothing")
	}
	want := []string{"text-generation", "image-text-to-text"}
	if strings.Join(got.ChatRule.PipelineTags, ",") != strings.Join(want, ",") {
		t.Errorf("pipeline tags = %v, want %v — the over-long, the unprintable, the blank and the repeat all go", got.ChatRule.PipelineTags, want)
	}
	if len(got.ChatRule.RequiredTags) != MaxChatRuleTags {
		t.Errorf("required tags = %d entries, want the %d the bound allows", len(got.ChatRule.RequiredTags), MaxChatRuleTags)
	}
}

// Clone is what stands between a posted rule and the configuration in force:
// the settings write path decodes into a clone, and a shared backing array
// would put a caller's words into the live rule before Validate had seen them.
func TestCloneCopiesTheChatRule(t *testing.T) {
	c := Default()
	c.ChatRule = ChatRule{PipelineTags: []string{"text-generation"}, RequiredTags: []string{"conversational"}}
	clone := c.Clone()
	clone.ChatRule.PipelineTags[0] = "changed"
	clone.ChatRule.RequiredTags[0] = "changed"
	if c.ChatRule.PipelineTags[0] != "text-generation" || c.ChatRule.RequiredTags[0] != "conversational" {
		t.Errorf("writing to the clone's rule changed the original: %+v", c.ChatRule)
	}
}

// A rule an operator cleared is a rule they set, and every path that copies a
// configuration has to keep it. Clone is on the settings write path — a POST is
// decoded into a clone of what is in force — so a Clone that turned an empty
// half back into an unset one would reinstate the shipped default at the next
// save of any other setting, which is the wedge this repository has built three
// times.
func TestCloneKeepsAClearedHalfCleared(t *testing.T) {
	c := Default()
	c.ChatRule = ChatRule{PipelineTags: []string{}, RequiredTags: []string{}}
	clone := c.Clone()
	if clone.ChatRule.IsZero() {
		t.Fatal("a cleared rule cloned as an unset one; the shipped default is back")
	}
	if clone.ChatRule.PipelineTags == nil || clone.ChatRule.RequiredTags == nil {
		t.Errorf("clone = %#v, want both halves present and empty", clone.ChatRule)
	}
	if !clone.EffectiveChatRule().Matches("automatic-speech-recognition", nil) {
		t.Error("the cloned rule no longer offers everything, which is what clearing both fields means")
	}
}

// Equal is what a caller asks whether a rule moved. "Never set" and "set to
// nothing" are different settings — the first means the shipped default — so a
// comparison that could not tell them apart would report no change across
// exactly the loss above.
func TestEqualTellsAnUnsetRuleFromAClearedOne(t *testing.T) {
	unset := ChatRule{}
	cleared := ChatRule{PipelineTags: []string{}, RequiredTags: []string{}}
	if unset.Equal(cleared) || cleared.Equal(unset) {
		t.Error("an unset rule compares equal to a cleared one; the two are different settings")
	}
	if !cleared.Equal(ChatRule{PipelineTags: []string{}, RequiredTags: []string{}}) {
		t.Error("two cleared rules do not compare equal")
	}
	if !unset.Equal(ChatRule{}) {
		t.Error("two unset rules do not compare equal")
	}
}

// The bounds are enforced on the way in from a person as well as on the way in
// from the file. A save is a field the caller touched, so an unusable rule is
// refused and named rather than quietly cut down — and until it is, an
// oversized rule sits in config.json looking effective until the next restart
// repairs it.
func TestValidateRefusesAnUnusableChatRule(t *testing.T) {
	many := make([]string, MaxChatRuleTags+1)
	for i := range many {
		many[i] = fmt.Sprintf("tag-%d", i)
	}
	cases := []struct {
		name string
		rule ChatRule
		want string
	}{
		{
			name: "too many pipeline tags",
			rule: ChatRule{PipelineTags: many},
			want: "chat_rule.pipeline_tags",
		},
		{
			name: "too many required tags",
			rule: ChatRule{RequiredTags: many},
			want: "chat_rule.required_tags",
		},
		{
			name: "a word longer than the bound",
			rule: ChatRule{PipelineTags: []string{strings.Repeat("x", MaxChatRuleTagBytes+1)}},
			want: "chat_rule.pipeline_tags",
		},
		{
			name: "a word that is not printable",
			rule: ChatRule{RequiredTags: []string{"with\x00nul"}},
			want: "chat_rule.required_tags",
		},
		{
			name: "a word that is nothing but space",
			rule: ChatRule{RequiredTags: []string{"   "}},
			want: "chat_rule.required_tags",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Default()
			cfg.ChatRule = c.rule
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil for %+v, want a refusal", c.rule)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("Validate() = %v, want it to name %s", err, c.want)
			}
		})
	}

	// And the two rules that must always pass: the shipped one, and a cleared
	// one. A save refused over the rule an operator just cleared would be the
	// same wedge from the other side.
	for _, r := range []ChatRule{{}, DefaultChatRule(), {PipelineTags: []string{}, RequiredTags: []string{}}} {
		cfg := Default()
		cfg.ChatRule = r
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() = %v for %#v, want nil", err, r)
		}
	}

	// A file is repaired rather than refused, so a hand-edited or planted rule
	// cannot lock the install down to loopback on the way past this check.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := Default()
	cfg.ChatRule = ChatRule{PipelineTags: many}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path); err != nil {
		t.Errorf("Load() = %v for an oversized rule, want it repaired rather than refused", err)
	}
}
