package runtime

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

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
