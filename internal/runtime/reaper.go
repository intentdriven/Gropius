package runtime

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// pidFileName is where the launcher records the process groups of the model
// servers it starts, so a later run can kill any that outlived a crash.
const pidFileName = "running-servers.pids"

// pidLedger records live model-server process groups on disk.
//
// A model server holds gigabytes of GPU memory. If Gropius is force-quit or
// crashes, os/exec cannot run any cleanup, and those children keep that memory
// pinned until the machine reboots. The ledger lets the next launch find and
// kill them.
type pidLedger struct {
	path string
	mu   sync.Mutex
}

func newPIDLedger(dir string) *pidLedger {
	return &pidLedger{path: filepath.Join(dir, pidFileName)}
}

// add records a process group id.
func (l *pidLedger) add(pgid int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return // best-effort; a missing ledger only costs us orphan reaping
	}
	defer f.Close()
	fmt.Fprintf(f, "%d\n", pgid)
}

// remove drops a process group id after a clean stop, rewriting the ledger.
func (l *pidLedger) remove(pgid int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	existing := l.readLocked()
	var kept []int
	for _, p := range existing {
		if p != pgid {
			kept = append(kept, p)
		}
	}
	l.writeLocked(kept)
}

func (l *pidLedger) readLocked() []int {
	f, err := os.Open(l.path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if n, err := strconv.Atoi(strings.TrimSpace(sc.Text())); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func (l *pidLedger) writeLocked(pgids []int) {
	if len(pgids) == 0 {
		os.Remove(l.path)
		return
	}
	var b strings.Builder
	for _, p := range pgids {
		fmt.Fprintf(&b, "%d\n", p)
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err == nil {
		os.Rename(tmp, l.path)
	}
}

// reapOrphans kills any model-server process groups left over from a previous
// run, then clears the ledger. Called once at startup, before serving.
//
// It signals whole process groups (mlx_lm can spawn helpers), and only those the
// ledger recorded — it never scans and kills by name, so an unrelated Python
// process is safe.
func (l *pidLedger) reapOrphans() (killed int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, pgid := range l.readLocked() {
		// Signal 0 tests whether the group still exists without affecting it.
		if err := syscall.Kill(-pgid, syscall.Signal(0)); err != nil {
			continue // already gone
		}
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
			killed++
		}
	}
	os.Remove(l.path)
	return killed
}
