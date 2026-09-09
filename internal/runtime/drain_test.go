package runtime

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// A model server holds its memory until the process is actually gone, which on
// a wedged server is the SIGTERM grace plus the SIGKILL that follows it. The
// pool used to credit the budget the moment it deleted the entry, so the
// replacement was launched on top of a victim that had not released anything —
// the two coexisting up to the victim's full footprint, which is the GPU-memory
// blowup the budget exists to prevent.
//
// The victim must still be told to go at once: the fix is to wait for it, not
// to spare it.
func TestAReplacementWaitsForTheVictimToExit(t *testing.T) {
	l := newFakeLauncher()
	l.holdExitFor = "org/a"
	src := &fakeSource{models: map[string]int64{"org/a": 100, "org/b": 100}}
	// LoadCost is 1.2x, so 100 bytes costs 120: a 200-byte budget holds exactly
	// one of these at a time.
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 200})

	_, release, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatalf("Acquire org/a: %v", err)
	}
	release() // idle, and therefore the eviction candidate

	loaded := make(chan error, 1)
	go func() {
		_, rel, err := p.Acquire(context.Background(), "org/b")
		if rel != nil {
			rel()
		}
		loaded <- err
	}()

	victim := l.procFor("org/a")
	select {
	case <-victim.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the victim was never stopped; eviction must not wait to begin")
	}

	// The victim is stopped but has not exited. Its memory is still spoken for,
	// so the replacement must not be launched yet.
	select {
	case err := <-loaded:
		t.Fatalf("org/b was admitted while the victim was still exiting (err %v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	if got := l.launchedRepos(); slices.Contains(got, "org/b") {
		t.Fatalf("org/b's server was launched on top of a victim that had not exited: launches %v", got)
	}

	// Once the victim is gone the memory really is free, and the load that was
	// waiting for it goes ahead.
	victim.exit()
	select {
	case err := <-loaded:
		if err != nil {
			t.Fatalf("org/b was refused after the victim exited: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("org/b did not load after the victim exited")
	}
}

// Eviction is not the only way an entry leaves the pool holding its memory: a
// model the operator unloads by hand is exiting too, and a load that arrives
// while it does must be measured against a machine that is still full.
func TestAnUnloadedModelIsStillChargedUntilItsProcessExits(t *testing.T) {
	l := newFakeLauncher()
	l.holdExitFor = "org/a"
	src := &fakeSource{models: map[string]int64{"org/a": 100, "org/b": 100}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 200})

	_, release, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatalf("Acquire org/a: %v", err)
	}
	release()

	if err := p.Unload("org/a"); err != nil {
		t.Fatalf("Unload org/a: %v", err)
	}

	loaded := make(chan error, 1)
	go func() {
		_, rel, err := p.Acquire(context.Background(), "org/b")
		if rel != nil {
			rel()
		}
		loaded <- err
	}()

	select {
	case err := <-loaded:
		t.Fatalf("org/b was admitted while the unloaded model was still exiting (err %v)", err)
	case <-time.After(300 * time.Millisecond):
	}

	l.procFor("org/a").exit()
	select {
	case err := <-loaded:
		if err != nil {
			t.Fatalf("org/b was refused after the unloaded model exited: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("org/b did not load after the unloaded model exited")
	}
}

// The charge has to be handed back when the process goes, or one eviction would
// shrink the budget for the life of the pool.
func TestTheDrainedChargeIsGivenBack(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{"org/a": 100, "org/b": 100, "org/c": 100}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 200})

	for _, id := range []string{"org/a", "org/b", "org/c"} {
		_, release, err := p.Acquire(context.Background(), id)
		if err != nil {
			t.Fatalf("Acquire %s after %d eviction(s): %v", id, len(l.launchedRepos())-1, err)
		}
		release()
	}
	if got := p.Resident(); len(got) != 1 || got[0].RepoID != "org/c" {
		t.Errorf("Resident() = %+v, want org/c alone", got)
	}
}

// A model server that will not exit must not hold a client's connection open
// for ever. Past the bound the request is refused the way every other request
// that cannot be served is — the machine is full, and the reply says only that.
func TestAServerThatWillNotExitDoesNotHoldTheCallerForEver(t *testing.T) {
	l := newFakeLauncher()
	l.holdExitFor = "org/a"
	src := &fakeSource{models: map[string]int64{"org/a": 100, "org/b": 100}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 200, drainWait: 100 * time.Millisecond})

	_, release, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatalf("Acquire org/a: %v", err)
	}
	release()

	_, rel, err := p.Acquire(context.Background(), "org/b")
	if rel != nil {
		rel()
	}
	if err == nil {
		t.Fatal("org/b loaded on top of a server that had not exited")
	}
	if !errors.Is(err, ErrBusy) {
		t.Errorf("Acquire org/b = %v, want a busy refusal", err)
	}
	var noRoom *NoRoomError
	if !errors.As(err, &noRoom) {
		t.Errorf("Acquire org/b = %v, want a *NoRoomError", err)
	}
	// The wire text is the ordinary no-room refusal: what this machine is doing
	// with its own memory is not a network client's business.
	if strings.Contains(err.Error(), "exit") {
		t.Errorf("the refusal told the client about the pool's internals: %q", err)
	}
}

// A client that hangs up while it waits for an evicted server to exit is not
// kept waiting for it, and leaves nothing behind in the queue.
func TestADrainWaitEndsWhenTheClientHangsUp(t *testing.T) {
	l := newFakeLauncher()
	l.holdExitFor = "org/a"
	src := &fakeSource{models: map[string]int64{"org/a": 100, "org/b": 100}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 200})

	_, release, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatalf("Acquire org/a: %v", err)
	}
	release()

	ctx, cancel := context.WithCancel(context.Background())
	failed := make(chan error, 1)
	go func() {
		_, rel, err := p.Acquire(ctx, "org/b")
		if rel != nil {
			rel()
		}
		failed <- err
	}()

	select {
	case <-l.procFor("org/a").stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the victim was never stopped")
	}
	cancel()

	select {
	case err := <-failed:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Acquire after the client hung up = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire did not return when the client hung up")
	}
	if got := p.Waiting(); got != 0 {
		t.Errorf("Waiting() = %d after the client hung up, want 0", got)
	}
}
