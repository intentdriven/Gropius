// Package capability assesses whether a model will fit on this machine — both
// on disk to download and within the RAM budget to actually run.
package capability

// Machine describes the resources available to local models.
type Machine struct {
	TotalRAM  int64 `json:"total_ram"`  // installed physical RAM, bytes
	RAMBudget int64 `json:"ram_budget"` // RAM Gropius will let a loaded model use, bytes
	FreeDisk  int64 `json:"free_disk"`  // free space on the models volume, bytes
}

// diskHeadroom is left free so a download never fills the disk to the brim.
const diskHeadroom = 2 << 30 // 2 GiB

// runFootprint estimates the memory a model of the given download size occupies
// once loaded: the weights plus ~20% for the KV cache and activations. This
// mirrors the process pool's loadCost so the "fits" filter and the pool agree —
// a model the filter shows is one the pool will actually load.
func runFootprint(downloadSize int64) int64 {
	return downloadSize + downloadSize/5
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
	if m.RAMBudget > 0 && runFootprint(downloadSize) > m.RAMBudget {
		return "too large for this Mac's memory"
	}
	return ""
}
