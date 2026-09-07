package capability

import "testing"

// The share of memory a default budget takes has one home. Two copies of the
// figure — one in the pool, one in this filter — is how the search tab and the
// pool come to disagree about which models this Mac can run.
func TestDefaultBudgetIsAShareOfTheMachine(t *testing.T) {
	if got, want := DefaultBudget(64*gb), int64(64*gb)*60/100; got != want {
		t.Errorf("DefaultBudget(64 GB) = %d, want %d", got, want)
	}
	// An unmeasurable machine gets a conservative figure rather than a budget
	// of nothing, which would refuse every model.
	if got, want := DefaultBudget(0), int64(8*gb); got != want {
		t.Errorf("DefaultBudget(0) = %d, want the %d fallback", got, want)
	}
	if got, want := DefaultBudget(-1), int64(8*gb); got != want {
		t.Errorf("DefaultBudget(-1) = %d, want the %d fallback", got, want)
	}
}

// Assess measures the machine but is told the budget, so the filter hides
// exactly what the pool would refuse — including after the operator has
// changed the figure.
func TestAssessTakesTheBudgetItIsGiven(t *testing.T) {
	m := Assess(t.TempDir(), 12*gb)
	if m.RAMBudget != 12*gb {
		t.Errorf("RAMBudget = %d, want the budget it was given (%d)", m.RAMBudget, int64(12*gb))
	}
	// Zero is what a fresh install stores, so it must resolve to the default
	// rather than hide every model behind a budget of nothing.
	d := Assess(t.TempDir(), 0)
	if want := DefaultBudget(PhysicalMemory()); d.RAMBudget != want {
		t.Errorf("RAMBudget = %d for a zero budget, want the default %d", d.RAMBudget, want)
	}
	if d.TotalRAM != PhysicalMemory() {
		t.Errorf("TotalRAM = %d, want this machine's memory %d", d.TotalRAM, PhysicalMemory())
	}
}
