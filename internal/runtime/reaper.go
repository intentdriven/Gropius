package runtime

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/intentdriven/Gropius/internal/config"
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
//
// Each entry records not just the process-group id but the group leader's start
// time, and the ledger as a whole records the system boot time. Both exist to
// stop the reaper from killing the WRONG process: the OS recycles pids/pgids, so
// a bare pgid recorded before a crash can, by the next launch, belong to an
// entirely unrelated process group (emphatically so after a reboot, when every
// recorded pgid is stale). Verifying identity before SIGKILL prevents that.
type pidLedger struct {
	path string
	// uid is the effective uid a ledger must be owned by to be trusted. In
	// shared-cache mode the ledger lives in a group-writable root where another
	// local account can plant one — permanently, since the sticky bit blocks
	// our os.Remove — and every identity check below it (boot time, start
	// time) is readable cross-uid, so only provenance stops a planted ledger
	// from turning the next launch into a kill of arbitrary process groups.
	uid int
	mu  sync.Mutex
}

// maxLedgerBytes caps the ledger read; a real ledger is a few lines.
const maxLedgerBytes = 1 << 20

// entry is one recorded process group plus the identity used to confirm, before
// killing, that the group leader is still the process we started and not a
// recycled pid.
type pidEntry struct {
	pgid int
	// startNs is the group leader's start time (ns). Zero means "unknown" — the
	// process was gone or unreadable when recorded, so we cannot identity-check it.
	startNs int64
}

func newPIDLedger(dir string) *pidLedger {
	return &pidLedger{path: filepath.Join(dir, pidFileName), uid: os.Geteuid()}
}

// add records a process group id together with the leader's start time and the
// current boot time, so a later reap can verify identity before killing.
func (l *pidLedger) add(pgid int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	boot, entries := l.readLocked()
	// A boot-time change means every prior entry is from a dead session; drop them
	// rather than carry stale pgids forward.
	if now := bootTimeNs(); boot != now {
		boot = now
		entries = nil
	}
	start, _ := processStartNs(pgid)
	entries = append(entries, pidEntry{pgid: pgid, startNs: start})
	l.writeLocked(boot, entries)
}

// remove drops a process group id after a clean stop, rewriting the ledger.
func (l *pidLedger) remove(pgid int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	boot, entries := l.readLocked()
	kept := entries[:0]
	for _, e := range entries {
		if e.pgid != pgid {
			kept = append(kept, e)
		}
	}
	l.writeLocked(boot, kept)
}

// readLocked parses the ledger into its boot stamp and entries. A missing,
// malformed, non-regular, oversized, or foreign-owned ledger reads as empty:
// the reaper must never kill on a ledger it cannot attribute to itself, and a
// plain open would block forever on a FIFO planted under this name (see
// config.OpenRegular).
//
// Format is one "boot <ns>" header line followed by "<pgid> <startNs>" lines.
func (l *pidLedger) readLocked() (bootNs int64, entries []pidEntry) {
	f, info, err := config.OpenRegular(l.path)
	if err != nil {
		return 0, nil
	}
	defer f.Close()
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != l.uid {
		return 0, nil
	}
	b, err := io.ReadAll(io.LimitReader(f, maxLedgerBytes+1))
	if err != nil || int64(len(b)) > maxLedgerBytes {
		return 0, nil
	}

	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "boot" && len(fields) == 2 {
			bootNs, _ = strconv.ParseInt(fields[1], 10, 64)
			continue
		}
		pgid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		// kill(-1) signals every process this uid may signal, kill(0) our own
		// group, and a negative pgid flips sign into a single-pid kill. None can
		// name a child we started (Setpgid gives pgid == pid > 1), so drop them
		// here rather than let a planted line reach the SIGKILL below.
		if pgid <= 1 {
			continue
		}
		var start int64
		if len(fields) > 1 {
			start, _ = strconv.ParseInt(fields[1], 10, 64)
		}
		entries = append(entries, pidEntry{pgid: pgid, startNs: start})
	}
	return bootNs, entries
}

func (l *pidLedger) writeLocked(bootNs int64, entries []pidEntry) {
	if len(entries) == 0 {
		os.Remove(l.path)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "boot %d\n", bootNs)
	for _, e := range entries {
		fmt.Fprintf(&b, "%d %d\n", e.pgid, e.startNs)
	}
	// Write through a random O_EXCL temp, not a predictable "<path>.tmp". The
	// ledger lives in the data root, which in shared mode is group-writable
	// (/Users/Shared/Gropius); another local account could pre-plant
	// "running-servers.pids.tmp" as a symlink and redirect this write to clobber
	// a file the server user owns. os.CreateTemp uses a random name with O_EXCL
	// and mode 0600, closing that hole — the same fix config.Save already uses.
	dir := filepath.Dir(l.path)
	tmp, err := os.CreateTemp(dir, "running-servers-*.pids.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, l.path); err != nil {
		os.Remove(tmpName)
	}
}

// reapOrphans kills any model-server process groups left over from a previous
// run, then clears the ledger. Called once at startup, before serving.
//
// It signals whole process groups (mlx_lm can spawn helpers), and only those the
// ledger recorded — it never scans and kills by name. Before killing it confirms
// the group leader is the same process it recorded: the boot time must match
// (else every pgid is from a prior, dead session) and, when a start time was
// recorded, the leader's live start time must still match it. A recycled pid that
// now belongs to an unrelated process is therefore left alone.
func (l *pidLedger) reapOrphans() (killed int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	boot, entries := l.readLocked()
	defer os.Remove(l.path)

	// A different boot session means the recorded pgids no longer refer to our
	// children — the kernel has reassigned them. Killing on a bare existence check
	// here is exactly how the reaper would take out an unrelated process group.
	if boot == 0 || boot != bootTimeNs() {
		return 0
	}

	for _, e := range entries {
		// Signal 0 tests whether the group still exists without affecting it.
		if err := syscall.Kill(-e.pgid, syscall.Signal(0)); err != nil {
			continue // already gone
		}
		// If we recorded a start time, the live leader's start time must match, or
		// this pgid has been recycled onto a different process since we recorded it.
		if e.startNs != 0 {
			if start, ok := processStartNs(e.pgid); ok && start != e.startNs {
				continue // recycled pid — not our child
			}
		}
		if err := syscall.Kill(-e.pgid, syscall.SIGKILL); err == nil {
			killed++
		}
	}
	return killed
}

// bootTimeNs returns the system boot time in nanoseconds, or 0 if unavailable.
// It is the session marker that makes a recorded pgid meaningful: pgids are only
// comparable within one boot.
func bootTimeNs() int64 {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil || tv == nil {
		return 0
	}
	return tv.Nano()
}

// processStartNs returns a process's start time in nanoseconds. The (time, ok)
// pair distinguishes "process gone / unreadable" (ok=false) from a real value.
func processStartNs(pid int) (int64, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil {
		return 0, false
	}
	return kp.Proc.P_starttime.Nano(), true
}
