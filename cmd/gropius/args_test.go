package main

import (
	"slices"
	"strings"
	"testing"
)

// A positional argument used to fall straight through to the server: Go's flag
// package stops parsing at the first non-flag and main never looked at what was
// left over. `gropius install` therefore bound the configured address,
// generated an API key, advertised over Bonjour and never returned — a typo, a
// stray shell word, or a subcommand name run against a build that predates it
// all started a LAN-exposed server instead of reporting a usage error.
//
// The refusal has narrowed to unknown verbs now that this build has verbs, and
// what must not change is the property that produced it: no word this build
// does not understand ever reaches the server.
func TestTheCommandLineIsClassified(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		kind commandKind
		verb string
	}{
		{
			// macOS launches the bundle with no arguments at all, so this has
			// to keep meaning "run the server", permanently.
			name: "no arguments is the server",
			args: nil,
			kind: kindServer,
		},
		{
			name: "an empty slice is the server",
			args: []string{},
			kind: kindServer,
		},
		{
			name: "a leading flag is the server's own command line",
			args: []string{"-headless"},
			kind: kindServer,
		},
		{
			name: "serve is the explicit form of the same thing",
			args: []string{"serve", "-headless"},
			kind: kindServer,
		},
		{
			name: "a known verb is dispatched",
			args: []string{"status"},
			kind: kindVerb,
			verb: "status",
		},
		{
			name: "a verb keeps its own flags",
			args: []string{"doctor", "--json"},
			kind: kindVerb,
			verb: "doctor",
		},
		{
			name: "a verb this build does not carry yet is still a verb",
			args: []string{"install"},
			kind: kindVerb,
			verb: "install",
		},
		{
			name: "a word this build has no meaning for is refused",
			args: []string{"instal"},
			kind: kindRefusal,
		},
		{
			name: "the first word is the one that decides",
			args: []string{"wibble", "--json"},
			kind: kindRefusal,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyCommandLine(tc.args)
			if got.Kind != tc.kind {
				t.Fatalf("classifyCommandLine(%q).Kind = %v, want %v (%s)", tc.args, got.Kind, tc.kind, got.Message)
			}
			if got.Verb != tc.verb {
				t.Errorf("Verb = %q, want %q", got.Verb, tc.verb)
			}
			if tc.kind == kindRefusal && got.Message == "" {
				t.Error("a refusal with no message leaves the person who typed it nothing to read")
			}
		})
	}
}

// The server's own flags travel on, whether the command line named serve or
// nothing at all — otherwise `gropius serve -headless` would start a menu bar
// on a machine that was asked for a daemon.
func TestServeKeepsTheServersFlags(t *testing.T) {
	got := classifyCommandLine([]string{"serve", "-headless", "-root", "somewhere"})
	if !slices.Equal(got.Args, []string{"-headless", "-root", "somewhere"}) {
		t.Errorf("Args = %q, want the flags that followed serve", got.Args)
	}
	bare := classifyCommandLine([]string{"-headless"})
	if !slices.Equal(bare.Args, []string{"-headless"}) {
		t.Errorf("Args = %q, want the whole command line", bare.Args)
	}
}

// The refusal has to say what to do instead, because the people who hit it are
// the ones who guessed a verb this build does not have.
func TestTheRefusalNamesTheOffendingWordAndTheVerbsThatExist(t *testing.T) {
	got := refuseUnknownArgs([]string{"instal"})
	if got == "" {
		t.Fatal("refuseUnknownArgs returned no refusal for a word this build does not know")
	}
	if !strings.Contains(got, "instal") {
		t.Errorf("refusal %q does not name the offending argument", got)
	}
	for _, verb := range knownVerbs() {
		if !strings.Contains(got, verb) {
			t.Errorf("refusal %q does not name the verb %q, so the reader cannot see what this build does answer to", got, verb)
		}
	}
}

// A verb this build understands is not refused by the same function: it is
// dispatched, and what happens to it is the verb's own business.
func TestAKnownVerbIsNotRefused(t *testing.T) {
	for _, verb := range knownVerbs() {
		if msg := refuseUnknownArgs([]string{verb}); msg != "" {
			t.Errorf("refuseUnknownArgs(%q) = %q, want no refusal for a verb this build knows", verb, msg)
		}
	}
}

// Flags are parsed before the words left over are looked at, so a verb typed
// after a flag never reaches the dispatch. It must not reach the server
// either: the refusal says where the verb goes.
func TestATrailingWordIsRefusedAfterTheFlags(t *testing.T) {
	msg := refuseTrailingArgs([]string{"status"})
	if msg == "" {
		t.Fatal("a word left over after the flags was allowed to fall through to the server")
	}
	if !strings.Contains(msg, "status") {
		t.Errorf("refusal %q does not name the word that was left over", msg)
	}
	if !strings.Contains(msg, "gropius status") {
		t.Errorf("refusal %q does not say where the verb goes", msg)
	}
	if refuseTrailingArgs(nil) != "" {
		t.Error("a command line with nothing left over is not a refusal")
	}
	if refuseTrailingArgs([]string{"wibble"}) == "" {
		t.Error("a word this build does not know is still refused after the flags")
	}
}

// Every verb the table says this build carries is wired to something. A verb
// listed as known and reaching no implementation would be refused by the
// dispatch's default case, which reads to the person who typed it as this
// build not having it — the one thing the table is there to say.
func TestEveryWiredVerbIsWired(t *testing.T) {
	for name, kind := range verbs {
		_, wired := lifecycleVerbs[name]
		if kind == verbLifecycle && !wired {
			t.Errorf("%q is listed as a verb this build carries, but nothing runs it", name)
		}
		if kind != verbLifecycle && wired {
			t.Errorf("%q is wired to a lifecycle verb but is not listed as one", name)
		}
	}
}

// The verbs the spec names are all known, whether or not this build carries
// them yet: a verb that is merely unbuilt must say so, and a person who reads
// the documentation and types one of these should not be told it does not
// exist.
func TestTheVerbSetIsTheOneTheRecordNames(t *testing.T) {
	for _, verb := range []string{"serve", "version", "status", "doctor", "install", "uninstall", "update"} {
		if _, ok := verbs[verb]; !ok {
			t.Errorf("%q is not a verb this build knows", verb)
		}
	}
}
