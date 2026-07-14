package capability

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// ramBudgetPercent is the share of physical RAM a loaded model may use. It
// matches runtime.defaultResidentBudget so this filter and the process pool's
// memory budget stay in agreement.
const ramBudgetPercent = 60

// Assess measures this machine's RAM and the free space on the volume that
// holds modelsDir.
func Assess(modelsDir string) Machine {
	ram := physicalMemory()
	return Machine{
		TotalRAM:  ram,
		RAMBudget: ram * ramBudgetPercent / 100,
		FreeDisk:  freeDisk(modelsDir),
	}
}

func physicalMemory() int64 {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
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
