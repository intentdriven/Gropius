package main

import (
	"sort"
	"strings"

	"github.com/intentdriven/Gropius/internal/lifecycle"
)

// This file is the whole of the mapping from a command line to "run the server,
// run a verb, or refuse", and it is a pure function so that every case is a
// table rather than a process. main acts on what it returns.

// verbKind says what this build does with a word it recognises.
type verbKind int

const (
	// verbServer: the explicit form of the bare invocation. The flags that
	// follow it are the server's own.
	verbServer verbKind = iota
	// verbVersion: print which build this is and stop. It reads the binary and
	// contacts nothing.
	verbVersion
	// verbLifecycle: a verb internal/lifecycle carries.
	verbLifecycle
	// verbNotYet: a verb this product has decided on and this build does not
	// carry. It is refused by name, which is a different thing to say than
	// "no such verb": the person who read the documentation and typed it
	// learns that they typed it correctly and that it is not here yet.
	//
	// No verb carries this kind today — install, uninstall and update have all
	// landed. It is kept because it is the shape the NEXT planned verb takes
	// the day its record is written and before its code is, and the refusal it
	// produces is held by a test that registers a word under it rather than by
	// a verb that happens to be unbuilt.
	verbNotYet
)

// verbs is every word this build answers to as a first argument.
//
// A word that is not here reaches the server through nothing: it is refused.
// That rule is older than the verbs and is why this file exists — `gropius
// install` against a build with no verbs used to bind the configured address,
// generate an API key, advertise over Bonjour and never return.
var verbs = map[string]verbKind{
	"serve":     verbServer,
	"version":   verbVersion,
	"status":    verbLifecycle,
	"doctor":    verbLifecycle,
	"config":    verbLifecycle,
	"install":   verbLifecycle,
	"uninstall": verbLifecycle,
	"update":    verbLifecycle,
}

// knownVerbs is the verb set in a stable order, for the refusal to print.
func knownVerbs() []string {
	names := make([]string, 0, len(verbs))
	for name := range verbs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// commandKind is what a command line asks for.
type commandKind int

const (
	// kindServer: parse Args as the server's flags and start it.
	kindServer commandKind = iota
	// kindVerb: run Verb with Args.
	kindVerb
	// kindRefusal: print Message and exit 2, having started nothing.
	kindRefusal
)

// commandLine is the classification of one command line.
type commandLine struct {
	Kind commandKind
	// Verb is the word to dispatch on, for kindVerb.
	Verb string
	// Args is what follows: the server's flags for kindServer, the verb's own
	// arguments for kindVerb.
	Args []string
	// Message is what to print, for kindRefusal.
	Message string
}

// classifyCommandLine maps a command line — os.Args without the program name —
// to what this build should do with it.
//
// Three rules, in this order:
//
//   - Nothing at all is the server, because macOS launches the bundle with no
//     arguments and that has to keep working permanently.
//   - A first argument beginning with "-" is a flag, so the whole command line
//     is the server's. -headless and -root keep working, permanently, for the
//     same reason.
//   - Anything else is a verb, and a verb this build has no meaning for is
//     refused rather than passed over.
func classifyCommandLine(args []string) commandLine {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return commandLine{Kind: kindServer, Args: args}
	}
	if msg := refuseUnknownArgs(args); msg != "" {
		return commandLine{Kind: kindRefusal, Message: msg}
	}
	if verbs[args[0]] == verbServer {
		// serve is the bare invocation written out, so what follows it is the
		// server's command line.
		return commandLine{Kind: kindServer, Args: args[1:]}
	}
	return commandLine{Kind: kindVerb, Verb: args[0], Args: args[1:]}
}

// refuseUnknownArgs returns the message to print for a first argument this
// build has no meaning for, and an empty string for one it does.
//
// Only the first argument is looked at, and only it is named. A command line
// like `gropius doctr --json` leaves both words here because the first word is
// what went wrong, and naming the flag as well would suggest it was the
// problem.
func refuseUnknownArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	if _, known := verbs[args[0]]; known {
		return ""
	}
	return "gropius: unknown argument " + lifecycle.Quote(args[0]) + "\n" +
		"The verbs this build knows are: " + strings.Join(knownVerbs(), ", ") + ".\n" +
		"Run gropius with no arguments to start the server."
}

// refuseTrailingArgs returns the message to print for a word left over after
// the server's flags were parsed.
//
// Go's flag package stops at the first non-flag argument, so `gropius -headless
// status` parses the flag and leaves the verb behind — past the classification
// above, which only ever looked at the first word. Left alone it would start a
// server for somebody who asked for a status, so it is refused here, and the
// refusal says where the verb goes rather than calling it unknown.
func refuseTrailingArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	if _, known := verbs[args[0]]; known {
		return "gropius: " + lifecycle.Quote(args[0]) + " is a verb, and a verb comes first: gropius " + args[0] + "\n" +
			"Flags before it belong to the server, which is not what this command line asked for."
	}
	return refuseUnknownArgs(args)
}
