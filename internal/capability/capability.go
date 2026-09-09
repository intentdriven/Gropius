// Package capability assesses whether a model will fit on this machine — both
// on disk to download and within the RAM budget to actually run.
package capability

// Machine describes the resources available to local models.
type Machine struct {
	TotalRAM int64 `json:"total_ram"` // installed physical RAM, bytes
	// RAMBudget is the memory Gropius will let loaded models use. It is spelled
	// the way the control plane's own machine object spells it: both reach the
	// same panel, and one number under two names is what this figure was
	// centralized to prevent.
	RAMBudget int64 `json:"budget"`
	FreeDisk  int64 `json:"free_disk"` // free space on the models volume, bytes
}

// diskHeadroom is left free so a download never fills the disk to the brim.
const diskHeadroom = 2 << 30 // 2 GiB

// LoadCost estimates the memory a model of the given size on disk occupies once
// loaded: the weights plus ~20% for the KV cache and activations.
//
// This is the one home of that charge. It is what the "fits" filter measures a
// model against, what the process pool charges a resident model against the
// memory budget, what the app measures a pinned set against, and — through a
// test in internal/ui — what the control panel's own copy is held to. It lives
// here because internal/runtime reads this package and not the other way round,
// so a second copy in the pool is what let the filter show a model the pool
// would refuse.
func LoadCost(diskBytes int64) int64 {
	return diskBytes + diskBytes/5 // 1.2x
}

// Fits reports whether a model of downloadSize bytes can both be stored and run
// on this machine. A zero or negative size means "unknown" and always fits: we
// would rather show a model we cannot measure than hide it.
func (m Machine) Fits(downloadSize int64) bool {
	return m.Reason(downloadSize) == ""
}

// Reason returns a short explanation of why a model does not fit, or "" if it
// does (or its size is unknown).
func (m Machine) Reason(downloadSize int64) string {
	if downloadSize <= 0 {
		return ""
	}
	if m.FreeDisk > 0 && downloadSize+diskHeadroom > m.FreeDisk {
		return "not enough free disk space"
	}
	if m.RAMBudget > 0 && LoadCost(downloadSize) > m.RAMBudget {
		return "too large for this Mac's memory"
	}
	return ""
}
