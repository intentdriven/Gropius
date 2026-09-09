package config

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// ChatRule decides which of the models this Mac serves are published as able to
// hold a conversation, from the words HuggingFace itself puts on a repository.
//
// Gropius invents no vocabulary for this. A model carries the Hub's pipeline
// tag and the Hub's tags, recorded when it was downloaded, and the rule is two
// lists of those same words: which pipeline tags count, and which tags a model
// must carry. That is the whole of it, so a vocabulary the Hub changes without
// asking anyone is a rule an operator can follow without waiting for a release.
//
// The verdict is advisory. It reaches a client on the models list as the `chat`
// field and decides nothing about what is served: a model outside the rule is
// still loaded and still answers a request that names it.
type ChatRule struct {
	// PipelineTags are the Hub pipeline tags that count as conversational. An
	// empty list does not test the pipeline tag at all, which is how an
	// operator says "look only at the tags".
	PipelineTags []string `json:"pipeline_tags"`
	// RequiredTags are the Hub tags a model must carry, all of them. An empty
	// list does not test the tags.
	RequiredTags []string `json:"required_tags"`
}

// IsZero reports whether the rule was never set, which is not the same as a
// rule that tests nothing.
//
// The difference is the whole of what the file has to carry. A config.json with
// no chat_rule key means "the default", so a fresh install and a build that
// predates the setting both get the rule Gropius ships. A chat_rule whose two
// lists are present and empty means "test neither half" — every model chats —
// which is what an operator who cleared both fields in Settings asked for.
// Folding the second into the first would hand them back the rule they just
// cleared.
//
// encoding/json calls this for the `omitzero` on Config.ChatRule, and the two
// lists inside are written without `omitempty`, so an empty list survives the
// round trip as an empty list.
func (r ChatRule) IsZero() bool { return r.PipelineTags == nil && r.RequiredTags == nil }

// Equal reports whether two rules say the same thing.
//
// A half that was never set is not equal to a half that was set to nothing:
// the first means the shipped default and the second means "test neither
// half", and a comparison that could not tell them apart would report no
// change across exactly the loss Clone exists to prevent.
func (r ChatRule) Equal(other ChatRule) bool {
	return equalTagLists(r.PipelineTags, other.PipelineTags) &&
		equalTagLists(r.RequiredTags, other.RequiredTags)
}

func equalTagLists(a, b []string) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return slices.Equal(a, b)
}

// Clone returns a rule that shares no backing array with this one, keeping each
// half exactly as it is: nil stays nil, and an empty list stays an empty list.
//
// The second half of that is the point. append onto a nil slice returns nil for
// an empty source, so a clone built that way silently promoted "cleared" to
// "never set" — and every unrelated settings save decodes into a clone.
func (r ChatRule) Clone() ChatRule {
	return ChatRule{PipelineTags: cloneTagList(r.PipelineTags), RequiredTags: cloneTagList(r.RequiredTags)}
}

func cloneTagList(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// validate refuses a rule this build cannot hold, naming the half at fault.
//
// Refused rather than repaired, unlike the same rule arriving in config.json:
// this runs on the settings write path, where the rule is a field the caller
// touched and a person is waiting to be told which one was wrong. Cutting it
// down silently would leave an oversized rule in the file looking effective
// until the next start repaired it — a setting in force that nobody agreed to.
// Load sanitizes before it validates, so a hand-edited or planted file still
// loads rather than taking the install down to loopback.
func (r ChatRule) validate() error {
	for _, half := range []struct {
		field string
		words []string
	}{
		{"chat_rule.pipeline_tags", r.PipelineTags},
		{"chat_rule.required_tags", r.RequiredTags},
	} {
		if len(half.words) > MaxChatRuleTags {
			return fmt.Errorf("%s names %d tags, more than the %d this holds",
				half.field, len(half.words), MaxChatRuleTags)
		}
		for _, word := range half.words {
			if len(word) > MaxChatRuleTagBytes {
				return fmt.Errorf("%s: %q is longer than the %d bytes a tag may be",
					half.field, word, MaxChatRuleTagBytes)
			}
			if strings.TrimSpace(word) == "" || !printableTag(strings.TrimSpace(word)) {
				return fmt.Errorf("%s: %q is not a tag", half.field, word)
			}
		}
	}
	return nil
}

// DefaultChatRule is the rule Gropius ships: a model that generates text, or
// text from images, and that the Hub tags as conversational.
//
// The same two lists are the chat client's shipped default, held to this one by
// a test in internal/archtest, and the figures the control panel shows, held to
// it by a test in internal/ui. Change it here and both fail until they follow.
func DefaultChatRule() ChatRule {
	return ChatRule{
		PipelineTags: []string{"text-generation", "image-text-to-text"},
		RequiredTags: []string{"conversational"},
	}
}

// EffectiveChatRule is the rule in force: the stored one, or the shipped
// default when nothing is stored.
func (c Config) EffectiveChatRule() ChatRule {
	if c.ChatRule.IsZero() {
		return DefaultChatRule()
	}
	return c.ChatRule
}

// Matches reports whether a model with this pipeline tag and these tags counts
// as able to hold a conversation under the rule.
//
// Case is folded and surrounding space trimmed on both sides. Hub casing is not
// a promise — a repository's tags are typed by its owner — and a rule that
// missed "Conversational" would look broken for a reason nobody could see.
func (r ChatRule) Matches(pipelineTag string, tags []string) bool {
	if len(r.PipelineTags) > 0 {
		if !containsTag(r.PipelineTags, pipelineTag) {
			return false
		}
	}
	for _, required := range r.RequiredTags {
		if !containsTag(tags, required) {
			return false
		}
	}
	return true
}

func containsTag(list []string, want string) bool {
	want = foldTag(want)
	if want == "" {
		return false
	}
	for _, have := range list {
		if foldTag(have) == want {
			return true
		}
	}
	return false
}

func foldTag(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// MaxChatRuleTags bounds how many words each half of the rule may name, and
// MaxChatRuleTagBytes how long one of them may be.
//
// Everything saved is written to config.json, which Load refuses above
// MaxConfigBytes, and a config.json that cannot be read sends the next start
// into its fail-closed loopback-only branch. A tag list is one more lever for
// that, so it is bounded — generously: the Hub's whole pipeline vocabulary is
// well under fifty entries and its longest tag is a couple of dozen bytes.
const (
	MaxChatRuleTags     = 64
	MaxChatRuleTagBytes = 128
)

// sanitizeChatRule repairs a rule this build cannot use and returns what it
// repaired, so a hand-edited file, a backup or another build's settings still
// load.
//
// Repaired rather than refused, for the reason sanitizeStats gives: a refused
// config.json is a machine-wide outage, and this decides one boolean in a
// listing. What goes is what could not have come from the Hub or could not be
// served safely — a word longer than the bound, one carrying a control
// character, a blank, a repeat — and an over-full list is cut to the bound. A
// dropped word is dropped rather than trimmed to fit: a truncated tag is a word
// the Hub never said, and it would match nothing while looking as though it
// should.
func (c *Config) sanitizeChatRule() []string {
	if c.ChatRule.IsZero() {
		return nil
	}
	var repaired []string
	if list, changed := sanitizeTagList(c.ChatRule.PipelineTags); changed {
		c.ChatRule.PipelineTags = list
		repaired = append(repaired, fmt.Sprintf("chat_rule.pipeline_tags=%d usable", len(list)))
	}
	if list, changed := sanitizeTagList(c.ChatRule.RequiredTags); changed {
		c.ChatRule.RequiredTags = list
		repaired = append(repaired, fmt.Sprintf("chat_rule.required_tags=%d usable", len(list)))
	}
	return repaired
}

// sanitizeTagList bounds one half of the rule, reporting whether anything had
// to change. A nil list stays nil: that half was never set.
func sanitizeTagList(in []string) ([]string, bool) {
	if in == nil {
		return nil, false
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, tag := range in {
		if len(out) >= MaxChatRuleTags {
			break
		}
		tag = strings.TrimSpace(tag)
		if tag == "" || len(tag) > MaxChatRuleTagBytes || !printableTag(tag) {
			continue
		}
		folded := foldTag(tag)
		if seen[folded] {
			continue
		}
		seen[folded] = true
		out = append(out, tag)
	}
	return out, len(out) != len(in)
}

// printableTag reports whether a word is safe to keep. A tag reaches the
// control panel, the chat client's Settings and the LAN, so a control
// character in one is either corruption or someone's idea of a payload; either
// way it is not a word the Hub uses.
func printableTag(s string) bool {
	for _, r := range s {
		if r == unicode.ReplacementChar || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
