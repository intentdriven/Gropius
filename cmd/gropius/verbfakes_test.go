package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/lifecycle"
)

// No test in this package may reach a verb that acts on this Mac.
//
// One did. TestAVerbThisBuildDoesNotCarryIsRefusedByName was written when
// install and uninstall were listed and unbuilt; phase B wired them, and the
// same loop then ran a real install on the developer's machine — it asked the
// running copy to quit and raised the administrator panel twice, hanging the
// suite for ten minutes (iss-2609111240578491). Nothing was written for that
// account, and that was luck rather than design.
//
// The repair is structural, in three parts, because a comment saying "do not
// run install in a test" is the thing that failed:
//
//   - the verb runner takes its environment from verbEnvFor, a variable, so a
//     test can put a temporary root and captured streams in its place;
//   - TestMain below replaces verbEnvFor AND the writing verbs with fakes
//     before a single test runs, so the default in this binary is the fake and
//     reaching the live path takes a deliberate edit rather than a mistake;
//   - internal/lifecycle refuses to BUILD a live install or uninstall
//     environment inside a test binary at all, which is what catches the
//     deliberate edit.

// installCalls and uninstallCalls record what the fakes were asked to do, so a
// test can assert a verb was dispatched without anything happening.
var installCalls, uninstallCalls, updateCalls []string

// fakeVerbEnv is the environment every verb in this test binary runs in: a
// temporary root that no part of this account's installation shares.
func fakeVerbEnv() (lifecycle.Env, error) {
	root, err := os.MkdirTemp("", "gropius-verb-env")
	if err != nil {
		return lifecycle.Env{}, err
	}
	return lifecycle.Env{
		Version: version,
		Paths:   config.NewPaths(root),
		Port:    11535,
		Out:     os.Stdout,
		Err:     os.Stderr,
	}, nil
}

func TestMain(m *testing.M) {
	verbEnvFor = fakeVerbEnv
	lifecycleVerbs["install"] = func(_ lifecycle.Env, args []string) int {
		installCalls = append(installCalls, strings.Join(args, " "))
		return lifecycle.ExitOK
	}
	lifecycleVerbs["uninstall"] = func(_ lifecycle.Env, args []string) int {
		uninstallCalls = append(uninstallCalls, strings.Join(args, " "))
		return lifecycle.ExitOK
	}
	// update is the one a missing fake would hurt most: the live verb
	// downloads a release, quits the running copy, raises the authorisation
	// panel and replaces the application this suite is running from.
	lifecycleVerbs["update"] = func(_ lifecycle.Env, args []string) int {
		updateCalls = append(updateCalls, strings.Join(args, " "))
		return lifecycle.ExitOK
	}
	os.Exit(m.Run())
}

// The guard on the guard: what this binary will run for a writing verb is the
// fake, and the environment it hands it is the fake one. A test that puts the
// live ones back is doing it on purpose.
func TestTheTestBinaryCannotReachTheLiveVerbPath(t *testing.T) {
	if same(verbEnvFor, verbEnv) {
		t.Error("verbEnvFor is the LIVE environment builder in a test binary: a verb dispatched here would read " +
			"this account's own root and act on this Mac")
	}
	for _, verb := range writingVerbs {
		if same(lifecycleVerbs[verb], liveVerb(verb)) {
			t.Errorf("%q is wired to the live verb in a test binary; TestMain must replace it with a fake", verb)
		}
	}
}

// And the backstop under that, which is what catches a test that wires the live
// verb back: internal/lifecycle refuses to build an environment that acts on
// this Mac while it is running inside a test binary.
func TestTheLiveVerbsRefuseToRunInsideATest(t *testing.T) {
	for _, tc := range []struct {
		verb string
		run  func(lifecycle.Env, []string) int
	}{
		{"install", lifecycle.RunInstall},
		{"uninstall", lifecycle.RunUninstall},
		{"update", lifecycle.RunUpdate},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			var out, errOut bytes.Buffer
			env, err := fakeVerbEnv()
			if err != nil {
				t.Fatal(err)
			}
			env.Out, env.Err = &out, &errOut

			if code := tc.run(env, nil); code != lifecycle.ExitFailed {
				t.Fatalf("exit = %d, want %d: the live verb ran inside a test", code, lifecycle.ExitFailed)
			}
			if !strings.Contains(errOut.String(), "inside a test") {
				t.Errorf("the refusal does not say why it refused:\n%s", errOut.String())
			}
		})
	}
}

// A writing verb dispatched through the runner reaches the fake, with its
// arguments, and writes nothing anywhere.
func TestAWritingVerbIsDispatchedToTheFake(t *testing.T) {
	installCalls, uninstallCalls, updateCalls = nil, nil, nil
	home := t.TempDir()
	t.Setenv("HOME", home)

	var out, errOut bytes.Buffer
	if code := runCommandVerb(commandLine{Kind: kindVerb, Verb: "install", Args: []string{"--place-only"}}, &out, &errOut); code != lifecycle.ExitOK {
		t.Fatalf("install: exit = %d (%s)", code, errOut.String())
	}
	if code := runCommandVerb(commandLine{Kind: kindVerb, Verb: "uninstall", Args: nil}, &out, &errOut); code != lifecycle.ExitOK {
		t.Fatalf("uninstall: exit = %d (%s)", code, errOut.String())
	}
	if code := runCommandVerb(commandLine{Kind: kindVerb, Verb: "update", Args: nil}, &out, &errOut); code != lifecycle.ExitOK {
		t.Fatalf("update: exit = %d (%s)", code, errOut.String())
	}

	if len(installCalls) != 1 || installCalls[0] != "--place-only" {
		t.Errorf("install was dispatched as %v, want its arguments carried through once", installCalls)
	}
	if len(uninstallCalls) != 1 {
		t.Errorf("uninstall was dispatched %d times, want once", len(uninstallCalls))
	}
	if len(updateCalls) != 1 {
		t.Errorf("update was dispatched %d times, want once", len(updateCalls))
	}
	// And the fake did what a fake does: nothing on the filesystem.
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Errorf("dispatching the writing verbs wrote %v into the home directory", entries)
	}
	if _, err := os.Lstat(filepath.Join(home, ".local", "bin", "gropius")); err == nil {
		t.Error("dispatching install created a command link")
	}
}

// same reports whether two function values are the same function.
func same(a, b any) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// liveVerb is what the table holds for a verb in the shipped binary.
func liveVerb(verb string) func(lifecycle.Env, []string) int {
	switch verb {
	case "install":
		return lifecycle.RunInstall
	case "uninstall":
		return lifecycle.RunUninstall
	case "update":
		return lifecycle.RunUpdate
	}
	return nil
}

// writingVerbs is every verb that acts on this Mac. A verb added to
// lifecycleVerbs and not to this list is one the guard above stops covering,
// so the list is checked against the table rather than kept by hand.
var writingVerbs = []string{"install", "uninstall", "update"}

// The guard's own coverage: every verb this build carries is either a reading
// verb, which a test may dispatch freely, or on the writing list above, which
// TestMain replaces with a fake.
func TestEveryWritingVerbHasAFake(t *testing.T) {
	readingVerbs := map[string]bool{"status": true, "doctor": true}
	writing := map[string]bool{}
	for _, verb := range writingVerbs {
		writing[verb] = true
	}
	for verb := range lifecycleVerbs {
		if readingVerbs[verb] || writing[verb] {
			continue
		}
		t.Errorf("%q is a verb this build carries and is on neither list: a reading verb a test may dispatch, "+
			"or a writing verb TestMain must replace with a fake. Decide which it is — a writing verb with no "+
			"fake is a test one call away from acting on this Mac (iss-2609111240578491)", verb)
	}
}
