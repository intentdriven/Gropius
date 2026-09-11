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
// settings name, and the two streams.
//
// The settings are read rather than validated — a verb that refused to answer
// because config.json has a bad value would be useless at exactly the moment
// somebody is trying to find out what is wrong — so a file that will not parse
// leaves the shipping defaults in force here, as it does everywhere else.
func verbEnv() (lifecycle.Env, error) {
	root, err := config.DefaultRoot()
	if err != nil {
		return lifecycle.Env{}, err
	}
	paths := config.NewPaths(root)
	cfg, _, _ := config.Load(paths.Config)
	return lifecycle.Env{
		Version: version,
		Paths:   paths,
		Port:    cfg.Port,
		Out:     os.Stdout,
		Err:     os.Stderr,
		Term:    lifecycle.Detect(os.Stdout, os.LookupEnv),
	}, nil
}
