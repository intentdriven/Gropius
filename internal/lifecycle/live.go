package lifecycle

import (
	"fmt"
	"testing"
)

// The live environments — the ones that act on this Mac — refuse to be built
// inside a test binary.
//
// WHY THIS EXISTS. A unit test in cmd/gropius dispatched `install` through the
// verb runner once phase B wired it, and so ran a real install on the
// developer's Mac: it asked the running copy to quit and raised the
// administrator panel twice, hanging the suite (iss-2609111240578491). Nothing
// was written for that account, but the hazard is structural rather than a
// mistake in one test — a verb runner that builds its world from the live
// process has no shape a test can substitute, so every future test is one call
// away from acting on the machine it runs on.
//
// The seams a test SHOULD use are the ones beside them: runInstall and
// runUninstall take an InstallEnv and an UninstallEnv, and every side effect in
// them is a function. This is the backstop under those seams, for the test that
// reaches the live path anyway.
//
// WHY testing.Testing() RATHER THAN A BUILD TAG. A tag has to be remembered at
// the moment somebody is not thinking about it, which is the moment this is
// for. testing.Testing() is true in exactly the binaries `go test` builds and
// false in the shipped one, and it costs the testing package in the binary —
// which is the price of a guard that cannot be forgotten.
//
// The one test that does want the live resolution — the one proving uninstall
// never derives a deletion path from GROPIUS_ROOT — opens it deliberately with
// allowLiveEnvInTest, which is unexported and says in one line what is being
// allowed.
var liveEnvAllowedInTest bool

// allowLiveEnvInTest opens the live builders to the test that calls it, and is
// the only way past the guard. It takes the testing.TB so it cannot be called
// from anything but a test, and restores the guard when that test ends.
func allowLiveEnvInTest(tb testing.TB) {
	tb.Helper()
	liveEnvAllowedInTest = true
	tb.Cleanup(func() { liveEnvAllowedInTest = false })
}

// liveEnvGuard refuses the live environment inside a test binary.
func liveEnvGuard(verb string) error {
	if !testing.Testing() || liveEnvAllowedInTest {
		return nil
	}
	return fmt.Errorf("refusing to build the live environment for `gropius %s` inside a test: it would act on "+
		"this Mac — quit a running copy, raise the authorisation panel, provision a runtime, replace an "+
		"application. Hand run%s a fake environment instead", verb, verbNoun(verb))
}

// verbNoun spells a verb the way its internal runner is named, so the refusal
// names the seam a test should be using.
func verbNoun(verb string) string {
	switch verb {
	case "install":
		return "Install"
	case "uninstall":
		return "Uninstall"
	case "update":
		return "Update"
	}
	return verb
}
