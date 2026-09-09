package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
)

// The pid ledger belongs to the account that wrote it, not to the install. Its
// entries are process groups only the uid that started them can signal, so it
// is no use to any other account — and in the data root, where it used to live,
// the second account's rename over the first account's ledger is refused by the
// sticky bit and swallowed, which silently ends orphan reaping for that
// account.
func TestPIDLedgerLivesInThisAccountsOwnDirectory(t *testing.T) {
	root := t.TempDir()
	acct := t.TempDir()
	l := &ExecLauncher{Paths: config.Paths{Root: root, Account: acct}}
	got := l.pidLedger().path
	if want := filepath.Join(acct, pidFileName); got != want {
		t.Errorf("ledger path = %q, want this account's own %q", got, want)
	}
	if strings.HasPrefix(got, root) {
		t.Errorf("ledger path %q is in the data root, which every account shares", got)
	}
}

// A Paths with no account directory falls back to the data root, and a Paths
// with neither gets a ledger that writes nothing. Joining an empty directory
// with the file name would otherwise yield the relative "running-servers.pids"
// — a file in whatever directory the process was started from, which the next
// launch would read as a list of process groups to kill.
func TestPIDLedgerNeverFallsBackToTheWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	l := &ExecLauncher{Paths: config.Paths{Root: root}}
	if got, want := l.pidLedger().path, filepath.Join(root, pidFileName); got != want {
		t.Errorf("ledger path = %q, want the data root's %q when there is no account directory", got, want)
	}

	inert := newPIDLedger("")
	if inert.path != "" {
		t.Fatalf("ledger path = %q, want none: an empty directory must not become a relative path", inert.path)
	}
	// Every entry point must be a no-op, writing nothing and killing nothing.
	inert.add(os.Getpid())
	inert.remove(os.Getpid())
	if killed := inert.reapOrphans(); killed != 0 {
		t.Errorf("an inert ledger reaped %d process groups", killed)
	}
	if _, err := os.Stat(pidFileName); !os.IsNotExist(err) {
		t.Errorf("a ledger was written into the working directory (err=%v)", err)
	}
}

// reapOrphans must kill a process group recorded in the ledger, and leave the
// ledger clean afterwards.
func TestReapOrphansKillsRecordedProcessGroup(t *testing.T) {
	dir := t.TempDir()
	ledger := newPIDLedger(dir)

	// A real child that would outlive us, in its own process group like a model
	// server.
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := cmd.Process.Pid
	ledger.add(pgid)

	// Simulate a crash: a NEW ledger (fresh process) reaps what the old one left.
	reaped := newPIDLedger(dir).reapOrphans()
	if reaped != 1 {
		t.Errorf("reaped %d process groups, want 1", reaped)
	}

	// The child must actually be dead.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done: // killed, as intended
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the orphaned process was not killed")
	}

	// The ledger must be cleared so we don't try to kill a recycled PID next time.
	if _, got := newPIDLedger(dir).readLocked(); len(got) != 0 {
		t.Errorf("ledger still holds %v after reaping", got)
	}
}

func TestLedgerAddRemove(t *testing.T) {
	l := newPIDLedger(t.TempDir())
	l.add(111)
	l.add(222)
	l.remove(111)

	_, got := l.readLocked()
	if len(got) != 1 || got[0].pgid != 222 {
		t.Errorf("ledger = %v, want [222]", got)
	}
	l.remove(222)
	if _, got := l.readLocked(); len(got) != 0 {
		t.Errorf("ledger = %v, want empty", got)
	}
}

// A ledger written in a previous boot session must be discarded WITHOUT killing:
// after a reboot the OS has recycled those pgids onto unrelated processes. This
// is the reaper's main hazard, so guard it explicitly.
func TestReapOrphansSkipsStaleBootSession(t *testing.T) {
	dir := t.TempDir()
	l := newPIDLedger(dir)

	// A real, live child in its own process group — exactly what a recycled pgid
	// could point at. If the boot guard fails, the reaper would kill it.
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
	}()
	pgid := cmd.Process.Pid

	// Write a ledger whose boot stamp is from a different session, with a start
	// time that will NOT match the live process — i.e. a stale record.
	l.writeLocked(bootTimeNs()-1_000_000_000, []pidEntry{{pgid: pgid, startNs: 1}})

	if reaped := newPIDLedger(dir).reapOrphans(); reaped != 0 {
		t.Errorf("reaped %d from a stale boot session, want 0", reaped)
	}
	// The live process must be untouched.
	if err := syscall.Kill(-pgid, syscall.Signal(0)); err != nil {
		t.Errorf("the reaper killed a process from a stale-boot ledger: %v", err)
	}
}

// Reaping a PID that is already gone must not error or kill an unrelated PID.
func TestReapOrphansIgnoresDeadPIDs(t *testing.T) {
	dir := t.TempDir()
	l := newPIDLedger(dir)
	l.add(999999) // almost certainly not a live pid

	if reaped := l.reapOrphans(); reaped != 0 {
		t.Errorf("reaped %d, want 0 for a dead pid", reaped)
	}
}

// The ledger lives in the data root, which in shared mode is group-writable
// and where the file is created lazily — so another local account can plant a
// FIFO under its name. readLocked runs at startup (after the port is claimed)
// and on every model launch, holding the ledger mutex; a blocking open would
// wedge both with no way to recover from the app.
func TestReadLockedDoesNotBlockOnFIFOLedger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, pidFileName)
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			syscall.Close(fd)
		}
	})

	type result struct {
		boot    int64
		entries []pidEntry
	}
	done := make(chan result, 1)
	go func() {
		boot, entries := newPIDLedger(dir).readLocked()
		done <- result{boot, entries}
	}()
	select {
	case got := <-done:
		if got.boot != 0 || len(got.entries) != 0 {
			t.Errorf("a FIFO ledger must read as empty, got boot=%d entries=%v", got.boot, got.entries)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readLocked blocked on a FIFO planted as the ledger")
	}
}

// A symlinked ledger is never something writeLocked produced (it renames a
// regular temp file into place); following it would parse — and later kill
// process groups named by — a file from outside the root.
func TestReadLockedDoesNotFollowSymlinkedLedger(t *testing.T) {
	target := filepath.Join(t.TempDir(), "planted.pids")
	if err := os.WriteFile(target, []byte("boot 1\n4242 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, pidFileName)); err != nil {
		t.Fatal(err)
	}
	if _, entries := newPIDLedger(dir).readLocked(); len(entries) != 0 {
		t.Errorf("readLocked followed a symlinked ledger: %v", entries)
	}
}

// A planted ledger can name process groups Gropius never started. kill(-1, sig)
// signals every process this uid may signal, kill(0, sig) our own group, and a
// negative pgid flips sign into a single-pid kill — none can ever be a child we
// recorded, so they are dropped at parse time before any signal is sent.
func TestReadLockedDropsUnkillableProcessGroupIDs(t *testing.T) {
	dir := t.TempDir()
	body := "boot " + strconv.FormatInt(bootTimeNs(), 10) + "\n1 0\n0 0\n-5 0\n7 0\n"
	if err := os.WriteFile(filepath.Join(dir, pidFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, entries := newPIDLedger(dir).readLocked()
	if len(entries) != 1 || entries[0].pgid != 7 {
		t.Errorf("entries = %v, want only pgid 7", entries)
	}
}

// In shared-cache mode another local account can plant a regular ledger in the
// group-writable root, permanently (the sticky bit blocks our os.Remove), and
// kern.boottime and kern.proc start times are readable cross-uid — so only
// provenance protects the reaper. A ledger not owned by our euid is ignored.
func TestReapOrphansIgnoresLedgerNotOwnedByUs(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
	}()
	pgid := cmd.Process.Pid
	newPIDLedger(dir).writeLocked(bootTimeNs(), []pidEntry{{pgid: pgid, startNs: 0}})

	// Simulate a foreign owner: a real chown needs root, so the ledger's notion
	// of "our uid" is the seam.
	l := newPIDLedger(dir)
	l.uid = os.Geteuid() + 1
	if reaped := l.reapOrphans(); reaped != 0 {
		t.Errorf("reaped %d from a ledger we do not own, want 0", reaped)
	}
	if err := syscall.Kill(-pgid, syscall.Signal(0)); err != nil {
		t.Errorf("the reaper killed a process named by a foreign ledger: %v", err)
	}
}
