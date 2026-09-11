package main

import (
	"fmt"
	"os"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/lifecycle"
)

// lifecycleVerbs is what this build actually runs for each verb it carries.
// The table beside it in args.go says which words are verbs at all; this one
// says which of them have something behind them, and a test holds the two
// together.
var lifecycleVerbs = map[string]func(lifecycle.Env, []string) int{
	"status": lifecycle.RunStatus,
	"doctor": lifecycle.RunDoctor,
}

// runCommandVerb runs one verb and returns the code the process exits with. It
// starts no server, opens no listener and advertises nothing: a verb is a
// question asked in a terminal, and it answers and stops.
func runCommandVerb(cmd commandLine) int {
	switch verbs[cmd.Verb] {
	case verbVersion:
		fmt.Println("gropius " + version)
		return 0
	case verbNotYet:
		// Named rather than dismissed: the person who read the documentation
		// and typed this learns that they typed it correctly.
		fmt.Fprintln(os.Stderr, "gropius "+cmd.Verb+": not in this build yet")
		return lifecycle.ExitUsage
	}

	run, ok := lifecycleVerbs[cmd.Verb]
	if !ok {
		// Unreachable while the table test passes, and a refusal rather than a
		// silent success if it ever stops.
		fmt.Fprintln(os.Stderr, "gropius "+cmd.Verb+": this build lists the verb and does not run it")
		return lifecycle.ExitUsage
	}
	env, err := verbEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gropius "+cmd.Verb+": "+err.Error())
		return lifecycle.ExitFailed
	}
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
		Out:             os.Stdout,
		Err:             os.Stderr,
		Term:            lifecycle.Detect(os.Stdout, os.LookupEnv),
		Progress:        lifecycle.Detect(os.Stderr, os.LookupEnv),
		SettingsProblem: start.Problem,
	}, nil
}
