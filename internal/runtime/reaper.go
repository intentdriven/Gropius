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
// time, and the ledger as a whole records the boot session it was written in.
// Both exist to stop the reaper from killing the WRONG process: the OS recycles
// pids/pgids, so a bare pgid recorded before a crash can, by the next launch,
// belong to an entirely unrelated process group (emphatically so after a reboot,
// when every recorded pgid is stale). Verifying identity before SIGKILL prevents
// that.
type pidLedger struct {
	path string
	// uid is the effective uid a ledger must be owned by to be trusted. The
	// ledger now lives in this account's own directory, but the check stays:
	// every identity check below it (boot time, start time) is readable
	// cross-uid, so only provenance stops a ledger this account did not write —
	// left by an older install in a shared root, or planted anywhere the file
	// can be created — from turning the next launch into a kill of arbitrary
	// process groups.
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

// newPIDLedger opens the ledger this account keeps in dir.
//
// An empty dir yields an INERT ledger rather than a relative path. Without
// that, filepath.Join("", pidFileName) is "running-servers.pids" — a file in
// whatever directory the process happens to have been started from, which is
// neither this account's nor stable across launches, and which reapOrphans
// would then read as a list of process groups to kill. A ledger nobody wrote
// is safer than one anybody can leave in a working directory. The only way to
// reach this is a Paths built without a data root at all, which no shipped path
// produces; recording nothing costs the orphan sweep and nothing else.
func newPIDLedger(dir string) *pidLedger {
	l := &pidLedger{uid: os.Geteuid()}
	if dir != "" {
		l.path = filepath.Join(dir, pidFileName)
	}
	return l
}

// inert reports whether this ledger has nowhere to write. Every entry point
// checks it, so an unusable ledger records nothing and kills nothing.
func (l *pidLedger) inert() bool { return l.path == "" }

// add records a process group id together with the leader's start time and the
// current boot session, so a later reap can verify identity before killing.
func (l *pidLedger) add(pgid int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inert() {
		return
	}

	session, entries := l.readLocked()
	// A different boot session means every prior entry is from a dead one; drop
	// them rather than carry stale pgids forward. Within one boot this is never
	// taken, which is the whole point of keying on the session rather than on
	// kern.boottime: that value moves on a calendar clock step, and every entry
	// recorded before the step used to be forgotten here.
	if now := bootSessionUUID(); session != now {
		session = now
		entries = nil
	}
	start, _ := processStartNs(pgid)
	entries = append(entries, pidEntry{pgid: pgid, startNs: start})
	l.writeLocked(session, entries)
}

// remove drops a process group id after a clean stop, rewriting the ledger.
func (l *pidLedger) remove(pgid int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inert() {
		return
	}

	session, entries := l.readLocked()
	kept := entries[:0]
	for _, e := range entries {
		if e.pgid != pgid {
			kept = append(kept, e)
		}
	}
	l.writeLocked(session, kept)
}

// readLocked parses the ledger into its session stamp and entries. A missing,
// malformed, non-regular, oversized, or foreign-owned ledger reads as empty:
// the reaper must never kill on a ledger it cannot attribute to itself, and a
// plain open would block forever on a FIFO planted under this name (see
// config.OpenRegular).
//
// Format is one "session <uuid>" header line followed by "<pgid> <startNs>"
// lines. A ledger from an older version carries a "boot <ns>" header instead,
// which is not a key this reads: it comes back with an empty session, which
// every caller treats as a session that is not this one — the entries are
// dropped and nothing is killed. That is the safe direction for a format it
// cannot vouch for.
func (l *pidLedger) readLocked() (session string, entries []pidEntry) {
	f, info, err := config.OpenRegular(l.path)
	if err != nil {
		return "", nil
	}
	defer f.Close()
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != l.uid {
		return "", nil
	}
	b, err := io.ReadAll(io.LimitReader(f, maxLedgerBytes+1))
	if err != nil || int64(len(b)) > maxLedgerBytes {
		return "", nil
	}

	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "session" && len(fields) == 2 {
			// Only a well-formed identifier is carried forward. The value is
			// compared for equality with this boot's own, so a planted one can
			// only ever fail to match — but a stamp that is not the shape this
			// writes is a ledger this code did not produce, and it is not going
			// to be the authority for a SIGKILL.
			if isBootSessionUUID(fields[1]) {
				session = fields[1]
			}
			continue
		}
		if fields[0] == "boot" {
			continue // an older version's stamp: not this session, by definition
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
	return session, entries
}

func (l *pidLedger) writeLocked(session string, entries []pidEntry) {
	if len(entries) == 0 {
		os.Remove(l.path)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "session %s\n", session)
	for _, e := range entries {
		fmt.Fprintf(&b, "%d %d\n", e.pgid, e.startNs)
	}
	// Write through a random O_EXCL temp, not a predictable "<path>.tmp". The
	// ledger now lives in this account's own directory, but the pattern stays
	// what config.Save and the registry use: a predictable temp name is a
	// symlink to redirect this write wherever the directory is ever writable by
	// anything but its owner. os.CreateTemp uses a random name with O_EXCL and
	// mode 0600.
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
// the group leader is the same process it recorded: the boot session must match
// (else every pgid is from a prior, dead session) and, when a start time was
// recorded, the leader's live start time must still match it. A recycled pid that
// now belongs to an unrelated process is therefore left alone.
func (l *pidLedger) reapOrphans() (killed int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inert() {
		return 0
	}

	session, entries := l.readLocked()
	defer os.Remove(l.path)

	// A different boot session means the recorded pgids no longer refer to our
	// children — the kernel has reassigned them. Killing on a bare existence check
	// here is exactly how the reaper would take out an unrelated process group.
	// An unreadable session on either side is treated as a different one: with
	// nothing to compare, the only safe answer is to reap nothing.
	now := bootSessionUUID()
	if session == "" || now == "" || session != now {
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

// bootSessionUUID returns this boot's identifier, or "" if it cannot be read.
// It is the session marker that makes a recorded pgid meaningful: pgids are only
// comparable within one boot.
//
// It is deliberately not kern.boottime. That value is defined as walltime minus
// uptime, and XNU adjusts the globals behind it by the correction delta on every
// calendar clock STEP — the first post-boot NTP sync, a re-discipline after
// sleep/wake, a manual clock change — so it moves within a single boot. Measured
// on an Apple Silicon Mac while writing this, kern.boottime moved 80 ms inside
// one uninterrupted boot while this UUID did not change at all. Keyed on the
// clock, both the reap at startup and the carry-forward in add() silently became
// no-ops after any such step, leaving orphaned model servers holding gigabytes
// of GPU memory until the next reboot.
//
// kern.bootsessionuuid is generated once per boot and never adjusted. The
// per-pid start-time check below remains the authority on pid recycling; this
// only says which boot the ledger belongs to.
func bootSessionUUID() string {
	s, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || !isBootSessionUUID(s) {
		return ""
	}
	return s
}

// isBootSessionUUID reports whether s has the shape kern.bootsessionuuid
// answers with: 36 characters of upper-case hex in the 8-4-4-4-12 grouping.
//
// The shape is checked rather than assumed because this value is written into
// the ledger as a whitespace-delimited field and read back out of a file that,
// in shared-cache mode, another local account can write. Anything else is
// treated as no session at all, which reaps nothing.
func isBootSessionUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			hex := (r >= '0' && r <= '9') || (r >= 'A' && r <= 'F') || (r >= 'a' && r <= 'f')
			if !hex {
				return false
			}
		}
	}
	return true
}

// processStartNs returns a process's start time in nanoseconds. The (time, ok)
// pair distinguishes "process gone / unreadable" (ok=false) from a real value.
//
// P_starttime is a stored field of the exported extern_proc, stamped once when
// the process was forked and never recomputed: on the same Mac as above, pid 1's
// value did not move across the interval in which kern.boottime did. So the
// anti-recycle check this feeds is itself immune to the clock steps that made
// the boot-time stamp unusable.
func processStartNs(pid int) (int64, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil {
		return 0, false
	}
	return kp.Proc.P_starttime.Nano(), true
}
