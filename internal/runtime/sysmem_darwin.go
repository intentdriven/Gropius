package runtime

import "github.com/intentdriven/Gropius/internal/capability"

// defaultResidentBudget is how much memory Gropius lets loaded models use when
// no budget is configured.
//
// The share and the reading both live in internal/capability, which the search
// filter reads too: the filter hides the models this budget would refuse, so
// one home for the figure is what keeps the two in agreement.
func defaultResidentBudget() int64 {
	return capability.DefaultBudget(capability.PhysicalMemory())
}
