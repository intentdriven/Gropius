package runtime

import (
	"os/exec"
	"strconv"
	"strings"
)

// defaultResidentBudget is how much memory Gropius will let loaded models use.
//
// Apple Silicon has unified memory: whatever the models take is taken from the
// same pool as the window server and everything else the user is running. 60% of
// physical RAM keeps a 64 GB machine usable while still fitting a 4-bit 70B.
func defaultResidentBudget() int64 {
	total := physicalMemory()
	if total <= 0 {
		return 8 << 30 // a conservative fallback if sysctl is unavailable
	}
	return total * 60 / 100
}

// physicalMemory returns installed RAM in bytes, or 0 if it cannot be read.
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
