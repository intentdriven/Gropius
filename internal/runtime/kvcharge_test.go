package runtime

import (
	"context"
	"testing"

	"github.com/intentdriven/Gropius/internal/capability"
)

// Two long-context models that fit the budget on their weights alone do not
// fit it once the cache each of them will build is charged, and the pool must
// stop holding both. This is the over-admission the memory budget was charging
// nothing for: the weights fit, the machine did not.
//
// The figures are small stand-ins for the shape the lab measured: a model
// whose configuration implies a cache cost per token, served at the window it
// declares, one sequence at a time.
func TestTwoLongContextModelsNoLongerCoReside(t *testing.T) {
	const size, kv, window = 100, 1, 100
	// The flat charge is 120 each, so a 1,000-byte budget held both with room
	// to spare. The measured charge is 120 + 7x1x100 = 820 each.
	charge := capability.LoadCostOf(capability.Load{
		DiskBytes: size, KVBytesPerToken: kv, Window: window, Sequences: 1,
	})
	if 2*capability.LoadCost(size) > 1000 || 2*charge <= 1000 {
		t.Fatalf("the fixture no longer separates the two charges: flat %d, measured %d, budget 1000",
			capability.LoadCost(size), charge)
	}

	l := newFakeLauncher()
	src := &fakeSource{
		models: map[string]int64{"org/a": size, "org/b": size},
		facts: map[string]ResolvedModel{
			"org/a": {Bytes: size, ContextLength: window, KVBytesPerToken: kv},
			"org/b": {Bytes: size, ContextLength: window, KVBytesPerToken: kv},
		},
	}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1000, DecodeConcurrency: 1})

	for _, id := range []string{"org/a", "org/b"} {
		_, release, err := p.Acquire(context.Background(), id)
		if err != nil {
			t.Fatalf("Acquire(%q): %v", id, err)
		}
		release()
	}
	if got := p.Resident(); len(got) != 1 || got[0].RepoID != "org/b" {
		t.Errorf("Resident() = %+v, want only org/b: both models' caches cannot fit this budget", got)
	}
}

// The same pool holds both when neither configuration says what its cache
// costs — the charge falls back to the flat figure, which is what every model
// was charged before this. Without this the test above would pass for the
// wrong reason.
func TestTwoModelsWithNoCacheFigureStillCoReside(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{"org/a": 100, "org/b": 100}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1000, DecodeConcurrency: 1})

	for _, id := range []string{"org/a", "org/b"} {
		_, release, err := p.Acquire(context.Background(), id)
		if err != nil {
			t.Fatalf("Acquire(%q): %v", id, err)
		}
		release()
	}
	if got := p.Resident(); len(got) != 2 {
		t.Errorf("Resident() holds %d models, want both", len(got))
	}
}

// Every sequence a server may decode at once holds its own cache, so the
// concurrency the pool launches a model with is part of what that model is
// charged: the same two models share a budget when each server decodes one
// sequence and cannot when each may decode four.
func TestTheChargeCountsTheSequencesTheServerAdmits(t *testing.T) {
	const size, kv, window = 100, 1, 100
	// One sequence costs 120 + 700 = 820, so two fit a 3,000-byte budget.
	// Four sequences cost 120 + 4x700 = 2,920, and two do not.
	facts := map[string]ResolvedModel{
		"org/a": {Bytes: size, ContextLength: window, KVBytesPerToken: kv},
		"org/b": {Bytes: size, ContextLength: window, KVBytesPerToken: kv},
	}
	for _, c := range []struct {
		sequences int
		want      int
	}{{1, 2}, {4, 1}} {
		l := newFakeLauncher()
		src := &fakeSource{models: map[string]int64{"org/a": size, "org/b": size}, facts: facts}
		p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 3000, DecodeConcurrency: c.sequences})
		for _, id := range []string{"org/a", "org/b"} {
			if _, release, err := p.Acquire(context.Background(), id); err != nil {
				t.Fatalf("decode concurrency %d: Acquire(%q): %v", c.sequences, id, err)
			} else {
				release()
			}
		}
		if got := len(p.Resident()); got != c.want {
			t.Errorf("decode concurrency %d holds %d models, want %d", c.sequences, got, c.want)
		}
	}
}

// A model that fills the budget by itself still loads. The charge decides what
// may share this Mac's memory; a model whose window costs more than the whole
// budget is one that loads alone, and refusing it outright would take a model
// this Mac serves today away from the person serving it.
func TestAModelWhoseWindowFillsTheBudgetStillLoadsAlone(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{
		models: map[string]int64{"org/big": 100, "org/small": 100},
		facts: map[string]ResolvedModel{
			"org/big": {Bytes: 100, ContextLength: 100000, KVBytesPerToken: 1},
		},
	}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1000, DecodeConcurrency: 1})

	if _, release, err := p.Acquire(context.Background(), "org/big"); err != nil {
		t.Fatalf("Acquire(org/big): %v", err)
	} else {
		release()
	}
	if got := p.Resident(); len(got) != 1 {
		t.Fatalf("Resident() = %+v, want the one model", got)
	}
	// And it is alone: the whole budget is spoken for, so anything else has to
	// take its place rather than sit beside it.
	if _, release, err := p.Acquire(context.Background(), "org/small"); err != nil {
		t.Fatalf("Acquire(org/small): %v", err)
	} else {
		release()
	}
	if got := p.Resident(); len(got) != 1 || got[0].RepoID != "org/small" {
		t.Errorf("Resident() = %+v, want only org/small", got)
	}
}

// What the pool charges a model is what the control plane has to report, or
// the panel shows room the pool will not give out.
func TestResidencyReportsTheChargeAndNotTheSize(t *testing.T) {
	const size, kv, window = 100, 1, 100
	l := newFakeLauncher()
	src := &fakeSource{
		models: map[string]int64{"org/a": size},
		facts:  map[string]ResolvedModel{"org/a": {Bytes: size, ContextLength: window, KVBytesPerToken: kv}},
	}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 10000, DecodeConcurrency: 1})
	_, release, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()

	want := capability.LoadCostOf(capability.Load{
		DiskBytes: size, KVBytesPerToken: kv, Window: window, Sequences: 1, Budget: 10000,
	})
	got := p.Resident()
	if len(got) != 1 || got[0].Charge != want {
		t.Errorf("Resident()[0].Charge = %+v, want %d", got, want)
	}
	if got[0].Bytes != size {
		t.Errorf("Resident()[0].Bytes = %d, want the size on disk %d", got[0].Bytes, size)
	}
}
