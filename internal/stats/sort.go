package stats

import "sort"

// sortModels orders the per-model counters by repo id, so the panel's rows do
// not reshuffle on every event: a Go map iterates in a different order each
// time, and a table that reorders itself twice a second cannot be read.
func sortModels(in []ModelCounters) {
	sort.Slice(in, func(i, j int) bool { return in[i].Model < in[j].Model })
}

// sortRollups puts the minute buckets in time order, oldest first, which is
// the order a chart of them is drawn in.
func sortRollups(in []Rollup) {
	sort.Slice(in, func(i, j int) bool { return in[i].Minute < in[j].Minute })
}
