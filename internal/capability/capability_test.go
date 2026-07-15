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
