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
	if got := newPIDLedger(dir).readLocked(); len(got) != 0 {
		t.Errorf("ledger still holds %v after reaping", got)
	}
}

func TestLedgerAddRemove(t *testing.T) {
	l := newPIDLedger(t.TempDir())
	l.add(111)
	l.add(222)
	l.remove(111)

	got := l.readLocked()
	if len(got) != 1 || got[0] != 222 {
		t.Errorf("ledger = %v, want [222]", got)
	}
	l.remove(222)
	if got := l.readLocked(); len(got) != 0 {
		t.Errorf("ledger = %v, want empty", got)
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
