package main

import (
	"strings"
	"testing"
)

// A positional argument used to fall straight through to the server: Go's flag
// package stops parsing at the first non-flag and main never looked at what was
// left over. `gropius install` therefore bound the configured address,
// generated an API key, advertised over Bonjour and never returned — a typo, a
// stray shell word, or a subcommand name run against a build that predates it
// all started a LAN-exposed server instead of reporting a usage error.
func TestUnknownArgumentIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "no arguments is the server", args: nil, want: ""},
		{name: "empty slice is the server", args: []string{}, want: ""},
		{name: "a bare word is refused", args: []string{"install"}, want: "install"},
		{
			name: "the first word is the one named",
			args: []string{"update", "--yes"},
			want: "update",
		},
		{
			name: "a flag placed after a positional is still a refusal",
			args: []string{"doctor", "-root", "/tmp/x"},
			want: "doctor",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := refuseUnknownArgs(tc.args)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("refuseUnknownArgs(%q) = %q, want no refusal", tc.args, got)
				}
				return
			}
			if got == "" {
				t.Fatalf("refuseUnknownArgs(%q) returned no refusal, want one naming %q", tc.args, tc.want)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("refuseUnknownArgs(%q) = %q, which does not name the offending argument %q",
					tc.args, got, tc.want)
			}
		})
	}
}

// The refusal has to say what to do instead, because the people who hit it are
// the ones who guessed a subcommand this build does not have.
func TestRefusalNamesTheServerInvocation(t *testing.T) {
	got := refuseUnknownArgs([]string{"install"})
	if got == "" {
		t.Fatal("refuseUnknownArgs returned no refusal for an unknown argument")
	}
	if !strings.Contains(got, "gropius -version") {
		t.Errorf("refusal %q does not point at a command that works", got)
	}
}
