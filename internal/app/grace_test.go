package app

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
)

// The stored intervals reach the pool at start-up, so a Mac that was serving
// with grace on before a restart is serving with it on after one.
func TestStartupAppliesTheStoredEvictionGrace(t *testing.T) {
	cfg := config.Default()
	cfg.EvictionGrace = true
	cfg.EvictionGraceSec = 45
	cfg.EvictionMaxWaitSec = 90

	a, err := New(Options{Paths: config.NewPaths(t.TempDir()), Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	grace, maxWait := a.Pool.EvictionGrace()
	if grace != 45*time.Second || maxWait != 90*time.Second {
		t.Errorf("the pool holds %s/%s, want the stored 45s/90s", grace, maxWait)
	}
}

// Off means off in the pool, not merely two numbers nobody reads: the two
// intervals are stored whatever the switch says, and a build that handed them
// to the pool regardless would make every install wait.
func TestTheSwitchOffLeavesThePoolWithNoGrace(t *testing.T) {
	cfg := config.Default()
	cfg.EvictionGrace = false
	cfg.EvictionGraceSec = 45
	cfg.EvictionMaxWaitSec = 90

	a, err := New(Options{Paths: config.NewPaths(t.TempDir()), Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	if grace, _ := a.Pool.EvictionGrace(); grace != 0 {
		t.Errorf("the pool holds a grace of %s with the switch off, want none", grace)
	}
}

// Switching grace on applies to the requests being served, not to the ones
// after a restart — the same live seam pinning and the budget use.
func TestSetConfigAppliesTheEvictionGraceWithoutARestart(t *testing.T) {
	a := newTestApp(t)

	cfg := a.Config()
	cfg.EvictionGrace = true
	cfg.EvictionGraceSec = 30
	cfg.EvictionMaxWaitSec = 60
	if err := a.SetConfig(cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	grace, maxWait := a.Pool.EvictionGrace()
	if grace != 30*time.Second || maxWait != 60*time.Second {
		t.Errorf("the pool holds %s/%s, want the 30s/60s just saved", grace, maxWait)
	}

	// And switching it off again reaches the pool too, which is what makes
	// "off" the state the machine was in before anyone turned it on.
	cfg.EvictionGrace = false
	if err := a.SetConfig(cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if grace, _ := a.Pool.EvictionGrace(); grace != 0 {
		t.Errorf("the pool holds a grace of %s after the switch was turned off", grace)
	}
}

// A grace longer than the idle timeout is refused at the save, naming both
// figures, because the reaper would unload the model the wait is protecting.
func TestSetConfigRefusesAGraceLongerThanTheIdleTimeout(t *testing.T) {
	a := newTestApp(t)

	cfg := a.Config()
	cfg.EvictionGrace = true
	cfg.EvictionGraceSec = 300
	cfg.IdleTimeoutSec = 60
	if err := a.SetConfig(cfg); err == nil {
		t.Fatal("SetConfig accepted a grace longer than the idle timeout")
	}
	if a.Config().EvictionGrace {
		t.Error("the refused save changed the running settings")
	}
}

// Start-up loading must not wait out a grace. preload loads its models one
// after another, so with grace on a list of models that cannot all fit would
// stall a start by one maximum wait per model before the first request is
// served — and at start-up nobody is being protected from anybody, because no
// client has been served yet.
//
// A source assertion because the regression is a one-word revert with no
// visible symptom on a test machine: the pool's own
// TestAcquireNowTakesAVictimInsideItsGrace holds what AcquireNow does, and
// this holds that preload is what calls it.
func TestPreloadDoesNotWaitOutAnEvictionGrace(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	body := functionBody(t, string(src), "func (a *App) preload(")
	if !strings.Contains(body, "a.Pool.AcquireNow(") {
		t.Error("preload does not call Pool.AcquireNow; a start would wait out a grace per model")
	}
}

// functionBody returns the text of a top-level function, from its signature to
// the closing brace in the first column.
func functionBody(t *testing.T, src, signature string) string {
	t.Helper()
	i := strings.Index(src, signature)
	if i < 0 {
		t.Fatalf("no function %q in the source", signature)
	}
	rest := src[i:]
	if j := strings.Index(rest, "\n}\n"); j >= 0 {
		return rest[:j]
	}
	return rest
}
