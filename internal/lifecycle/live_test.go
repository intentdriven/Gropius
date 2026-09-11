package lifecycle

import (
	"strings"
	"testing"
)

// The live environments refuse to be built inside a test binary, which is the
// backstop under every seam in this package: a test that reaches one of these
// by accident would quit an application, raise an authorisation panel, download
// a runtime and replace a bundle on the machine running the suite
// (iss-2609111240578491).
func TestTheLiveEnvironmentsRefuseToBeBuiltInATest(t *testing.T) {
	env, _, _ := testEnv()

	if _, err := liveInstallEnv(env); err == nil {
		t.Error("liveInstallEnv built the world a real install acts on, inside a test")
	} else if !strings.Contains(err.Error(), "inside a test") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if _, err := liveUninstallEnv(env); err == nil {
		t.Error("liveUninstallEnv built the world a real uninstall acts on, inside a test")
	}
}

// And the one way past it is deliberate, named, and lasts for one test.
func TestTheGuardCanBeOpenedForOneTest(t *testing.T) {
	env, _, _ := testEnv()

	t.Run("opened", func(t *testing.T) {
		allowLiveEnvInTest(t)
		if _, err := liveInstallEnv(env); err != nil {
			t.Errorf("the guard was opened and still refused: %v", err)
		}
	})

	// The subtest above has ended, so the guard is back.
	if _, err := liveInstallEnv(env); err == nil {
		t.Error("the guard stayed open after the test that opened it ended")
	}
}
