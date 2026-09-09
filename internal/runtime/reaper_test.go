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
	"golang.org/x/sys/unix"
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

	// Write a ledger stamped with a different boot session, with a start time
	// that will NOT match the live process — i.e. a stale record.
	l.writeLocked(otherBootSession, []pidEntry{{pgid: pgid, startNs: 1}})

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
		session string
		entries []pidEntry
	}
	done := make(chan result, 1)
	go func() {
		session, entries := newPIDLedger(dir).readLocked()
		done <- result{session, entries}
	}()
	select {
	case got := <-done:
		if got.session != "" || len(got.entries) != 0 {
			t.Errorf("a FIFO ledger must read as empty, got session=%q entries=%v", got.session, got.entries)
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
	body := "session " + bootSessionUUID() + "\n1 0\n0 0\n-5 0\n7 0\n"
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
	newPIDLedger(dir).writeLocked(bootSessionUUID(), []pidEntry{{pgid: pgid, startNs: 0}})

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

// The stamp that says "these pgids belong to the session running now" must not
// be the calendar clock. kern.boottime is walltime minus uptime, and XNU moves
// it by the correction delta on every clock STEP — the first post-boot NTP
// sync, a re-discipline after sleep/wake, a manual change — so a ledger keyed
// on it stops recognising its own entries within a single boot, and both the
// reap at startup and the carry-forward in add() silently become no-ops.
// kern.bootsessionuuid is the per-boot identifier that does not move.
func TestTheLedgerStampIsTheBootSessionNotTheClock(t *testing.T) {
	dir := t.TempDir()
	newPIDLedger(dir).add(4242)

	b, err := os.ReadFile(filepath.Join(dir, pidFileName))
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Fields(strings.SplitN(string(b), "\n", 2)[0])
	want, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		t.Skip("this Mac does not answer kern.bootsessionuuid")
	}
	if len(head) != 2 || head[0] != "session" || head[1] != want {
		t.Errorf("ledger header = %q, want [session %s]", head, want)
	}
}

// otherBootSession is a well-formed identifier that is not this boot's. Held to
// that by the assertion in TestOtherBootSessionIsNotThisOne, so a test using it
// cannot quietly pass by naming the session it means to differ from.
const otherBootSession = "00000000-0000-4000-8000-000000000000"

func TestOtherBootSessionIsNotThisOne(t *testing.T) {
	if !isBootSessionUUID(otherBootSession) {
		t.Errorf("%s is not the shape a boot session is written in", otherBootSession)
	}
	if otherBootSession == bootSessionUUID() {
		t.Error("otherBootSession is this boot's own session; the stale-session tests prove nothing")
	}
}

// The session stamp must be stable within one boot and well formed, or every
// entry the ledger carries is dropped on the next launch and nothing is ever
// reaped. This is the property the whole design rests on, so read it twice.
func TestBootSessionUUIDIsStableAndWellFormed(t *testing.T) {
	first := bootSessionUUID()
	if first == "" {
		t.Skip("this Mac does not answer kern.bootsessionuuid")
	}
	if !isBootSessionUUID(first) {
		t.Errorf("kern.bootsessionuuid = %q, which is not a UUID", first)
	}
	if second := bootSessionUUID(); second != first {
		t.Errorf("the boot session moved within one boot: %q then %q", first, second)
	}
}

// Within one boot the ledger keeps what it recorded. Keyed on kern.boottime
// this was false the moment the clock stepped: add() saw a stamp it did not
// recognise, dropped every entry it had, and the process groups launched before
// the step could never be reaped again.
func TestAddKeepsEntriesRecordedInTheSameSession(t *testing.T) {
	l := newPIDLedger(t.TempDir())
	l.add(111)
	l.add(222)

	session, entries := l.readLocked()
	if session != bootSessionUUID() {
		t.Errorf("session = %q, want this boot's %q", session, bootSessionUUID())
	}
	if len(entries) != 2 || entries[0].pgid != 111 || entries[1].pgid != 222 {
		t.Errorf("entries = %v, want both pgids carried forward", entries)
	}
}

// A ledger stamped with another boot session names pgids the kernel has since
// reassigned. add() must drop them rather than carry them into a session where
// they could be killed.
func TestAddDropsEntriesFromAnotherSession(t *testing.T) {
	dir := t.TempDir()
	newPIDLedger(dir).writeLocked(otherBootSession, []pidEntry{{pgid: 111, startNs: 7}})

	l := newPIDLedger(dir)
	l.add(222)

	session, entries := l.readLocked()
	if session != bootSessionUUID() {
		t.Errorf("session = %q, want this boot's %q", session, bootSessionUUID())
	}
	if len(entries) != 1 || entries[0].pgid != 222 {
		t.Errorf("entries = %v, want only the pgid recorded in this session", entries)
	}
}

// A ledger written by an older version carries a "boot <ns>" stamp. It must be
// tolerated — read as a session that is not this one, so its entries are
// dropped and nothing is killed — rather than parsed as this session's or
// treated as a reason to fail.
func TestALedgerFromAnOlderVersionIsTreatedAsAnotherSession(t *testing.T) {
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

	legacy := "boot " + strconv.FormatInt(time.Now().UnixNano(), 10) + "\n" +
		strconv.Itoa(pgid) + " 0\n"
	if err := os.WriteFile(filepath.Join(dir, pidFileName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	session, entries := newPIDLedger(dir).readLocked()
	if session != "" {
		t.Errorf("an older version's stamp read as session %q, want none", session)
	}
	if len(entries) != 1 || entries[0].pgid != pgid {
		t.Errorf("entries = %v, want the recorded pgid to still parse", entries)
	}
	if reaped := newPIDLedger(dir).reapOrphans(); reaped != 0 {
		t.Errorf("reaped %d from an older version's ledger, want 0", reaped)
	}
	if err := syscall.Kill(-pgid, syscall.Signal(0)); err != nil {
		t.Errorf("the reaper killed a process named by an older version's ledger: %v", err)
	}

	// add() re-stamps rather than carrying the old entries into this session.
	l := newPIDLedger(dir)
	l.add(4242)
	got, entries := l.readLocked()
	if got != bootSessionUUID() {
		t.Errorf("session = %q after add, want this boot's %q", got, bootSessionUUID())
	}
	if len(entries) != 1 || entries[0].pgid != 4242 {
		t.Errorf("entries = %v, want only the pgid recorded in this session", entries)
	}
}

// A stamp that is not the shape this code writes is not the authority for a
// SIGKILL, however well it might otherwise parse.
func TestAMalformedSessionStampIsNoSession(t *testing.T) {
	for _, stamp := range []string{
		"not-a-uuid",
		"00000000-0000-4000-8000-00000000000",   // 35 characters
		"00000000-0000-4000-8000-0000000000000", // 37
		"0000000000004000800000000000000zzzzz",
		"00000000_0000_4000_8000_000000000000",
	} {
		if isBootSessionUUID(stamp) {
			t.Errorf("isBootSessionUUID(%q) = true, want false", stamp)
		}
		dir := t.TempDir()
		body := "session " + stamp + "\n4242 0\n"
		if err := os.WriteFile(filepath.Join(dir, pidFileName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if session, _ := newPIDLedger(dir).readLocked(); session != "" {
			t.Errorf("stamp %q read as session %q, want none", stamp, session)
		}
	}
}
