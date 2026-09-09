package runtime

import (
	"context"
	"strings"
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
	const size, kv, window = 100, 1, 500
	// The flat charge is 120 each, so a 1,000-byte budget holds both with room
	// to spare. Charged at the window each serves, they cost 120 + 500 = 620
	// each, and it does not.
	charge := capability.LoadCostOf(capability.Load{
		DiskBytes: size, KVChargePerToken: kv, Window: window, Sequences: 1,
	})
	if 2*capability.LoadCost(size) > 1000 || 2*charge <= 1000 {
		t.Fatalf("the fixture no longer separates the two charges: flat %d, measured %d, budget 1000",
			capability.LoadCost(size), charge)
	}

	l := newFakeLauncher()
	src := &fakeSource{
		models: map[string]int64{"org/a": size, "org/b": size},
		facts: map[string]ResolvedModel{
			"org/a": {Bytes: size, ServedContext: window, KVChargePerToken: kv},
			"org/b": {Bytes: size, ServedContext: window, KVChargePerToken: kv},
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
	// The window costs 100 per sequence, so one sequence costs 120 + 100 = 220
	// and two of those fit a 1,000-byte budget; four sequences cost 120 + 400
	// = 520 each, and two do not.
	facts := map[string]ResolvedModel{
		"org/a": {Bytes: size, ServedContext: window, KVChargePerToken: kv},
		"org/b": {Bytes: size, ServedContext: window, KVChargePerToken: kv},
	}
	for _, c := range []struct {
		sequences int
		want      int
	}{{1, 2}, {4, 1}} {
		l := newFakeLauncher()
		src := &fakeSource{models: map[string]int64{"org/a": size, "org/b": size}, facts: facts}
		p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1000, DecodeConcurrency: c.sequences})
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

// What the pool charges a model is what the control plane has to report, or
// the panel shows room the pool will not give out.
func TestResidencyReportsTheChargeAndNotTheSize(t *testing.T) {
	const size, kv, window = 100, 1, 100
	l := newFakeLauncher()
	src := &fakeSource{
		models: map[string]int64{"org/a": size},
		facts:  map[string]ResolvedModel{"org/a": {Bytes: size, ServedContext: window, KVChargePerToken: kv}},
	}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 10000, DecodeConcurrency: 1})
	_, release, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()

	want := capability.LoadCostOf(capability.Load{
		DiskBytes: size, KVChargePerToken: kv, Window: window, Sequences: 1,
	})
	got := p.Resident()
	if len(got) != 1 || got[0].Charge != want {
		t.Errorf("Resident()[0].Charge = %+v, want %d", got, want)
	}
	if got[0].Bytes != size {
		t.Errorf("Resident()[0].Bytes = %d, want the size on disk %d", got[0].Bytes, size)
	}
}

// The charge does not move with the budget, and this is what makes a stale
// charge impossible rather than merely unlikely: what a model costs is its
// weights, the window it is served at and the sequences it admits. A charge
// that tracked the budget would let a model admitted under a small budget go
// on being counted at that budget, and the next load would be admitted
// against a total nothing supports.
func TestTheChargeDoesNotMoveWithTheBudget(t *testing.T) {
	const size, kv, window = 100, 1, 500
	l := newFakeLauncher()
	src := &fakeSource{
		models: map[string]int64{"org/a": size},
		facts:  map[string]ResolvedModel{"org/a": {Bytes: size, ServedContext: window, KVChargePerToken: kv}},
	}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1000, DecodeConcurrency: 1})
	if _, release, err := p.Acquire(context.Background(), "org/a"); err != nil {
		t.Fatalf("Acquire: %v", err)
	} else {
		release()
	}
	want := capability.LoadCostOf(capability.Load{
		DiskBytes: size, KVChargePerToken: kv, Window: window, Sequences: 1,
	})
	for _, budget := range []int64{1000, 100000, 700} {
		p.SetMemoryBudget(budget)
		if got := p.Resident(); len(got) != 1 || got[0].Charge != want {
			t.Errorf("at a budget of %d the model is charged %+v, want %d", budget, got, want)
		}
	}
}

// The other input that moves under a resident model is the model itself: a
// re-download can change the window its configuration declares, and the
// registry's record of it changes with no load in between. The pool asks again
// rather than charging what the model was when it was admitted.
func TestRefreshChargesTakesTheModelsFactsAgain(t *testing.T) {
	const size, kv = 100, 1
	l := newFakeLauncher()
	src := &fakeSource{
		models: map[string]int64{"org/a": size},
		facts:  map[string]ResolvedModel{"org/a": {Bytes: size, ServedContext: 100, KVChargePerToken: kv}},
	}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1000000, DecodeConcurrency: 1})
	if _, release, err := p.Acquire(context.Background(), "org/a"); err != nil {
		t.Fatalf("Acquire: %v", err)
	} else {
		release()
	}

	src.mu.Lock()
	src.facts["org/a"] = ResolvedModel{Bytes: size, ServedContext: 10000, KVChargePerToken: kv}
	src.mu.Unlock()
	p.RefreshCharges()

	want := capability.LoadCostOf(capability.Load{
		DiskBytes: size, KVChargePerToken: kv, Window: 10000, Sequences: 1,
	})
	if got := p.Resident(); len(got) != 1 || got[0].Charge != want {
		t.Errorf("Resident() = %+v after the facts changed, want a charge of %d", got, want)
	}
}

// A model whose charge at the window it is served does not fit the budget is
// refused — not quietly charged the budget and loaded anyway. The refusal is
// the only place an operator learns what would fit, so it names both figures
// they can change and what each would have to become.
func TestAModelWhoseServedWindowDoesNotFitIsRefusedWithWhatWouldFit(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{
		models: map[string]int64{"org/big": 100},
		facts: map[string]ResolvedModel{
			"org/big": {Bytes: 100, ServedContext: 10000, KVChargePerToken: 1},
		},
	}
	// Weights cost 120 of the 1,000-byte budget, leaving 880 for the cache: at
	// two batched requests the model may be served 440 tokens, and at 10,000
	// tokens it may batch nothing.
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1000, DecodeConcurrency: 2})

	_, _, err := p.Acquire(context.Background(), "org/big")
	if err == nil {
		t.Fatal("a model whose served window does not fit the budget was loaded anyway")
	}
	for _, want := range []string{"10000", "440", "2", "served context", "batched requests"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if got := len(p.Resident()); got != 0 {
		t.Errorf("Resident() holds %d models after the refusal, want none", got)
	}
}

// The same model at a window the budget can hold loads, which is the whole
// point of the setting: the operator lowers the window rather than losing the
// model.
func TestTheSameModelLoadsAtAWindowThatFits(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{
		models: map[string]int64{"org/big": 100},
		facts: map[string]ResolvedModel{
			"org/big": {Bytes: 100, ServedContext: 400, KVChargePerToken: 1},
		},
	}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1000, DecodeConcurrency: 2})
	if _, release, err := p.Acquire(context.Background(), "org/big"); err != nil {
		t.Fatalf("Acquire at a window that fits: %v", err)
	} else {
		release()
	}
	if got := p.Resident(); len(got) != 1 || got[0].Charge != 120+400*2 {
		t.Errorf("Resident() = %+v, want the model charged its weights and two windows", got)
	}
}
