package capability

import "testing"

const gb = 1 << 30

func TestFits(t *testing.T) {
	// 64 GB machine: budget 38.4 GB, plenty of disk.
	m := Machine{TotalRAM: 64 * gb, RAMBudget: 38 * gb, FreeDisk: 400 * gb}

	cases := []struct {
		name string
		size int64
		want bool
	}{
		{"tiny model fits", 1 * gb, true},
		{"mid model fits", 20 * gb, true},
		{"just under the RAM budget fits", 31 * gb, true},    // 31*1.2 = 37.2 < 38
		{"over the RAM budget does not fit", 34 * gb, false}, // 34*1.2 = 40.8 > 38
		{"unknown size always fits", 0, true},
		{"negative size always fits", -1, true},
	}
	for _, c := range cases {
		if got := m.Fits(c.size); got != c.want {
			t.Errorf("%s: Fits(%d) = %v, want %v (reason %q)", c.name, c.size, got, c.want, m.Reason(c.size))
		}
	}
}

func TestFitsDiskConstrained(t *testing.T) {
	// Roomy RAM, but only 5 GB free disk.
	m := Machine{TotalRAM: 64 * gb, RAMBudget: 38 * gb, FreeDisk: 5 * gb}
	if m.Fits(4 * gb) { // 4 GB + 2 GB headroom = 6 GB > 5 GB free
		t.Error("a 4 GB model should not fit in 5 GB of free disk (2 GB headroom)")
	}
	if m.Reason(4*gb) == "" {
		t.Error("expected a disk-space reason")
	}
	if !m.Fits(2 * gb) { // 2 GB + 2 GB headroom = 4 GB < 5 GB
		t.Error("a 2 GB model should fit in 5 GB of free disk")
	}
}

func TestReasonMentionsTheConstraint(t *testing.T) {
	m := Machine{RAMBudget: 8 * gb, FreeDisk: 500 * gb}
	if got := m.Reason(40 * gb); got == "" {
		t.Fatal("a 40 GB model should not fit an 8 GB budget")
	}
}

func TestZeroMachineDoesNotHideModels(t *testing.T) {
	// If we could not measure the machine (all zero), nothing should be filtered.
	var m Machine
	if !m.Fits(500 * gb) {
		t.Error("an unmeasured machine must not hide models")
	}
}

// The 1.2x charge a loaded model costs the memory budget lives here and
// nowhere else in Go: the process pool measures every admission against it,
// the app measures a pinned set against it, and the control panel's JavaScript
// copy is bound to it by a test in internal/ui. A second Go copy is what let
// the "fits" filter and the pool come to disagree about which models this Mac
// can run.
func TestLoadCostIsTheChargeTheFitsFilterApplies(t *testing.T) {
	if got := LoadCost(1000); got != 1200 {
		t.Errorf("LoadCost(1000) = %d, want 1200 (weights + KV-cache headroom)", got)
	}
	// The memory verdict is LoadCost against the budget to the byte: a model
	// charged exactly the budget fits, and one byte less of budget does not.
	const size = 5 * gb
	m := Machine{RAMBudget: LoadCost(size), FreeDisk: 500 * gb}
	if got := m.Reason(size); got != "" {
		t.Errorf("a model charged exactly the budget was filtered out: %q", got)
	}
	m.RAMBudget = LoadCost(size) - 1
	if m.Reason(size) == "" {
		t.Error("a model charged one byte over the budget was shown as fitting")
	}
}
