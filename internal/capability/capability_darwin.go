package capability

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// ramBudgetPercent is the share of physical memory a loaded model may use when
// the operator has set no figure of their own.
//
// Apple Silicon has unified memory: whatever the models take is taken from the
// same pool as the window server and everything else running. 60% keeps a 64 GB
// machine usable while still fitting a 4-bit 70B. This is the one home of that
// share — the process pool resolves its own default through DefaultBudget — so
// the "fits" filter and the pool cannot come to disagree about it.
const ramBudgetPercent = 60

// unmeasuredBudget is what a machine whose memory cannot be read is allowed.
// A budget of nothing would refuse every model, which is worse than a
// conservative guess.
const unmeasuredBudget = 8 << 30

// DefaultBudget is how much memory loaded models may use on a machine of the
// given size, when no budget has been configured.
func DefaultBudget(totalRAM int64) int64 {
	if totalRAM <= 0 {
		return unmeasuredBudget
	}
	return totalRAM * ramBudgetPercent / 100
}

// Assess measures this machine's memory and the free space on the volume that
// holds modelsDir, and reports them against the given budget.
//
// The budget is a parameter rather than a figure this package resolves,
// because it is the operator's setting: the filter has to hide exactly what
// the process pool would refuse, and the pool is measuring against whatever is
// configured. Zero — what a fresh install stores — means the default share
// above.
func Assess(modelsDir string, budget int64) Machine {
	ram := PhysicalMemory()
	if budget <= 0 {
		budget = DefaultBudget(ram)
	}
	return Machine{
		TotalRAM:  ram,
		RAMBudget: budget,
		FreeDisk:  freeDisk(modelsDir),
	}
}

// PhysicalMemory returns installed memory in bytes, or 0 if it cannot be read.
//
// Exported and living here so that the process pool, this filter and the app
// all read one figure: the app needs it to check a saved budget against the
// machine and to show what share of it a budget is, and a second reading is a
// second answer waiting to happen.
func PhysicalMemory() int64 {
	// The absolute path, not the name: this figure decides how much memory
	// Gropius will fill, and resolving the command through PATH would let
	// anything earlier on it answer that question.
	out, err := exec.Command("/usr/sbin/sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// freeDisk returns the bytes available to an unprivileged user on the volume
// containing dir, falling back to the root volume if dir does not exist yet.
func freeDisk(dir string) int64 {
	var st syscall.Statfs_t
	if dir == "" {
		dir = "/"
	}
	if err := syscall.Statfs(dir, &st); err != nil {
		if err := syscall.Statfs("/", &st); err != nil {
			return 0
		}
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
