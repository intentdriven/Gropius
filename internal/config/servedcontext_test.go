package config

import (
	"strings"
	"testing"
)

// The window Gropius serves for a model is the operator's figure when they
// have set one and the model's own declared cap when they have not. One
// function answers it, because the charge, the gateway's refusal, the models
// list and the panel must all mean the same window by it.
func TestServedContextFallsBackToTheDeclaredWindow(t *testing.T) {
	c := Config{Models: map[string]ModelSettings{
		"org/set": {ServedContext: 32768},
		"org/off": {Pinned: true},
	}}
	cases := []struct {
		name     string
		repoID   string
		declared int64
		want     int64
	}{
		{"a figure of the operator's own", "org/set", 262144, 32768},
		{"a model with other settings and no window", "org/off", 262144, 262144},
		{"a model with no settings at all", "org/none", 262144, 262144},
		{"the id as the model list spells it", "ORG/SET", 262144, 32768},
		{"no declared cap either", "org/none", 0, 0},
		// A setting larger than the model can address is the operator asking
		// for a window the model does not have; the model's own cap wins.
		{"a figure above the declared cap", "org/set", 8192, 8192},
	}
	for _, tc := range cases {
		if got := c.ServedContext(tc.repoID, tc.declared); got != tc.want {
			t.Errorf("%s: ServedContext(%q, %d) = %d, want %d", tc.name, tc.repoID, tc.declared, got, tc.want)
		}
	}
}

// The window is a token count, bounded by the same ceiling a declared window
// is bounded by: it is written to config.json, which another local account can
// write in shared-cache mode, and it decides how much memory a model is
// charged.
func TestServedContextIsValidated(t *testing.T) {
	c := Default()
	c.Models = map[string]ModelSettings{"org/a": {ServedContext: MaxContextLength + 1}}
	err := c.Validate()
	if err == nil {
		t.Fatal("a served context past the ceiling was accepted")
	}
	if !strings.Contains(err.Error(), "served_context") || !strings.Contains(err.Error(), "org/a") {
		t.Errorf("error = %q, want it to name the field and the model", err)
	}
	c.Models = map[string]ModelSettings{"org/a": {ServedContext: -1}}
	if err := c.Validate(); err == nil {
		t.Error("a negative served context was accepted")
	}
	c.Models = map[string]ModelSettings{"org/a": {ServedContext: 32768}}
	if err := c.Validate(); err != nil {
		t.Errorf("a plausible served context was refused: %v", err)
	}
}

// The file path drops what it cannot use rather than refusing the file: one
// unusable figure must not wedge every other setting in it.
func TestAnUnusableServedContextIsDroppedFromTheFile(t *testing.T) {
	c := Default()
	c.Models = map[string]ModelSettings{"org/a": {ServedContext: MaxContextLength + 1, Pinned: true}}
	dropped := c.sanitizeModels()
	if got := c.Models["org/a"].ServedContext; got != 0 {
		t.Errorf("ServedContext = %d, want it dropped", got)
	}
	if !c.Models["org/a"].Pinned {
		t.Error("the model's pin went with it; only the unusable field is dropped")
	}
	if len(dropped) != 1 || !strings.Contains(dropped[0], "served_context") {
		t.Errorf("dropped = %v, want it to name the field", dropped)
	}
}

// Two spellings of one model id are two keys in the map and one model, so the
// window they resolve to must not depend on which the map iteration reached
// first. The file path drops the duplicate and the settings path refuses it,
// but a map built by neither — a config assembled in Go, a file read by an
// older build — can still hold both, and this must not be the place that
// answers differently on different runs.
func TestServedContextWithTwoCaseVariantKeysIsDeterministic(t *testing.T) {
	c := Config{Models: map[string]ModelSettings{
		"org/Model": {ServedContext: 8192},
		"org/model": {ServedContext: 65536},
	}}
	// Asked a hundred times, because one map iteration proves nothing.
	for i := 0; i < 100; i++ {
		if got := c.ServedContext("ORG/MODEL", 262144); got != 65536 {
			t.Fatalf("ServedContext = %d on iteration %d, want the largest of the variants (65536)", got, i)
		}
	}
	// An exact key is the operator's own spelling and wins outright, whatever
	// the variants say.
	if got := c.ServedContext("org/Model", 262144); got != 8192 {
		t.Errorf("ServedContext = %d for the exact key, want its own figure 8192", got)
	}
}
