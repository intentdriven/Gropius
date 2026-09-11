package main

import (
	"fmt"
	"io"
	"os"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/lifecycle"
)

// lifecycleVerbs is what this build actually runs for each verb it carries.
// The table beside it in args.go says which words are verbs at all; this one
// says which of them have something behind them, and a test holds the two
// together.
var lifecycleVerbs = map[string]func(lifecycle.Env, []string) int{
	"status":    lifecycle.RunStatus,
	"doctor":    lifecycle.RunDoctor,
	"config":    lifecycle.RunConfig,
	"install":   lifecycle.RunInstall,
	"uninstall": lifecycle.RunUninstall,
}

// verbEnvFor is how the runner gets the world a verb runs in.
//
// A variable, not a call, and that is the whole of the repair for
// iss-2609111240578491: the runner used to build its environment from the live
// process — this account's root, its home, its /Applications, its osascript —
// with nothing a test could put in the way. A test that reached a writing verb
// therefore acted on the machine it was running on, and one did: it quit the
// running copy and raised the administrator panel twice.
//
// cmd/gropius's TestMain replaces this, and the writing entries of
// lifecycleVerbs, before any test runs; a guard beside them asserts that the
// defaults in a test binary are the fakes, and internal/lifecycle refuses to
// build a live install or uninstall environment inside a test binary at all.
var verbEnvFor = verbEnv

// runCommandVerb runs one verb and returns the code the process exits with. It
// starts no server, opens no listener and advertises nothing: a verb is a
// question asked in a terminal, and it answers and stops.
//
// The two streams are arguments rather than os.Stdout and os.Stderr reached for
// in here, so a test can run the whole path — command line, environment, verb —
// and read what a person would have seen.
func runCommandVerb(cmd commandLine, out, errOut io.Writer) int {
	switch verbs[cmd.Verb] {
	case verbVersion:
		// Refused like any other verb given a word it has no meaning for.
		// Quietly printing the version anyway would tell somebody who typed
		// `gropius version --json` that they had got what they asked for.
		if len(cmd.Args) > 0 {
			fmt.Fprintln(errOut, "gropius version: unexpected argument "+lifecycle.Quote(cmd.Args[0]))
			return lifecycle.ExitUsage
		}
		fmt.Fprintln(out, "gropius "+version)
		return 0
	case verbNotYet:
		// Named rather than dismissed: the person who read the documentation
		// and typed this learns that they typed it correctly.
		fmt.Fprintln(errOut, "gropius "+cmd.Verb+": not in this build yet")
		return lifecycle.ExitUsage
	}

	run, ok := lifecycleVerbs[cmd.Verb]
	if !ok {
		// Unreachable while the table test passes, and a refusal rather than a
		// silent success if it ever stops.
		fmt.Fprintln(errOut, "gropius "+cmd.Verb+": this build lists the verb and does not run it")
		return lifecycle.ExitUsage
	}
	env, err := verbEnvFor()
	if err != nil {
		fmt.Fprintln(errOut, "gropius "+cmd.Verb+": "+err.Error())
		return lifecycle.ExitFailed
	}
	env.Out, env.Err = out, errOut
	env.Term.Out, env.Progress.Out = out, errOut
	return run(env, cmd.Args)
}

// verbEnv is the world the verbs run in: this account's data root, the port the
// server would bind, and the two streams.
//
// The settings are read through loadStartupConfig, which is what the server
// itself starts from, and that is the point rather than a convenience. A file
// that parses and then fails validation is not thrown away: the server keeps
// the configuration it parsed — the bind locked down to loopback, the rest of
// the operator's settings, the port included — so a verb that took the default
// port instead would probe a port nothing is on and report "nothing is serving"
// about a server that is serving.
//
// A verb never refuses to answer over a settings problem. Somebody running one
// is usually trying to find out what is wrong, and a diagnostic that will not
// speak until the thing it diagnoses is fixed is no diagnostic. The problem
// travels on the environment instead, and is stated on standard error.
func verbEnv() (lifecycle.Env, error) {
	root, err := config.DefaultRoot()
	if err != nil {
		return lifecycle.Env{}, err
	}
	paths := config.NewPaths(root)
	start := loadStartupConfig(paths.Config)
	return lifecycle.Env{
		Version:         version,
		Paths:           paths,
		Port:            start.Config.Port,
		Config:          start.Config,
		Out:             os.Stdout,
		Err:             os.Stderr,
		Term:            lifecycle.Detect(os.Stdout, os.LookupEnv),
		Progress:        lifecycle.Detect(os.Stderr, os.LookupEnv),
		SettingsProblem: start.Problem,
	}, nil
}
