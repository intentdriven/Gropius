package app

import (
	"testing"

	"github.com/intentdriven/Gropius/internal/capability"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/registry"
)

// The window a model is served at is what it is charged for, and the pinned
// check is the surface an operator meets that charge on: a set that fits at
// 32K and not at 262K must be reported as fitting once they have lowered the
// window.
func TestALoweredServedContextLowersWhatAPinnedModelCosts(t *testing.T) {
	cfg := config.Default()
	cfg.DecodeConcurrency = 1
	a := newBudgetApp(t, 128*gb, cfg)
	a.Registry.Put(registry.Model{
		RepoID: "org/long", Path: "/models/org/long", Bytes: 10 * gb,
		ContextLength: 262144, KVChargePerToken: 100, State: registry.StateReady,
	})

	m, err := a.Registry.Get("org/long")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	declared := a.chargeOf(m, m.Bytes)
	if want := capability.LoadCostOf(capability.Load{
		DiskBytes: 10 * gb, KVChargePerToken: 100, Window: 262144, Sequences: 1,
	}); declared != want {
		t.Errorf("charge = %d at the declared window, want %d", declared, want)
	}

	c := a.Config()
	c.Models = map[string]config.ModelSettings{"org/long": {ServedContext: 32768}}
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	lowered := a.chargeOf(m, m.Bytes)
	if want := capability.LoadCostOf(capability.Load{
		DiskBytes: 10 * gb, KVChargePerToken: 100, Window: 32768, Sequences: 1,
	}); lowered != want {
		t.Errorf("charge = %d at the lowered window, want %d", lowered, want)
	}
	if lowered >= declared {
		t.Errorf("the lowered window charges %d, no less than the declared window's %d", lowered, declared)
	}
}

// A save that says nothing about a model's window leaves it alone. This is the
// wedge this repository has built three times: a settings save refused, or
// silently changed, over a field the operator did not touch.
func TestSavingAnUnrelatedSettingKeepsTheServedContext(t *testing.T) {
	cfg := config.Default()
	cfg.Models = map[string]config.ModelSettings{"org/long": {ServedContext: 32768}}
	a := newBudgetApp(t, 128*gb, cfg)

	c := a.Config()
	c.IdleTimeoutSec = 900
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("a save that touched the idle timeout was refused: %v", err)
	}
	if got := a.Config().Models["org/long"].ServedContext; got != 32768 {
		t.Errorf("served context = %d after saving an unrelated setting, want 32768", got)
	}
	if got := a.Config().IdleTimeoutSec; got != 900 {
		t.Errorf("idle timeout = %d, want the figure that was saved", got)
	}
}
