package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/capability"
)

// safeBuf is a log sink a test can read while the pool writes to it from the
// goroutine that drains a stopped server.
type safeBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// A model server that never answered its readiness probe is torn down by
// waitReady, and it is if anything MORE likely to be wedged holding weights
// than an idle victim is. Its memory has to stay charged until its process is
// gone, exactly as an evicted one's does.
func TestALoadThatNeverBecameReadyIsChargedUntilItsProcessExits(t *testing.T) {
	l := newFakeLauncher()
	l.holdExitFor = "org/a"
	// org/a's server comes up but never answers a completion, so the readiness
	// probe times out and waitReady tears it down.
	l.loadDelayFor["org/a"] = time.Hour
	src := &fakeSource{models: map[string]int64{"org/a": 100, "org/b": 100}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 200, ReadyTimeout: 200 * time.Millisecond})

	if _, rel, err := p.Acquire(context.Background(), "org/a"); err == nil {
		if rel != nil {
			rel()
		}
		t.Fatal("a model that never answered its probe was reported ready")
	}
	// The failed entry is out of the pool...
	if got := p.Resident(); len(got) != 0 {
		t.Fatalf("Resident() = %+v after a failed load, want none", got)
	}
	// ...but its process has not exited, so its memory is not back.
	if bytes, _ := p.Draining(); bytes != capability.LoadCost(100) {
		t.Errorf("Draining() = %d bytes, want the failed load still charged %d",
			bytes, capability.LoadCost(100))
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
		t.Fatalf("org/b was admitted on top of a failed load that had not exited (err %v)", err)
	case <-time.After(300 * time.Millisecond):
	}

	l.procFor("org/a").exit()
	select {
	case err := <-loaded:
		if err != nil {
			t.Fatalf("org/b was refused after the failed load exited: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("org/b did not load after the failed load exited")
	}
}

// Stopping a model does not hand its memory back at once, so a caller with no
// wait left must not take a victim: killing an idle model and refusing the
// request in the same breath serves nobody. The sequence is deterministic when
// the maximum wait is no longer than the grace, which the settings allow.
//
// The pool's clock is frozen, so the victim's own protection never elapses and
// the only clause that could take it is the waiter's age — which runs out at
// the same instant it arrives.
func TestNoModelIsStoppedForACallerThatCannotWaitForIt(t *testing.T) {
	l := newFakeLauncher()
	clock := &testClock{now: time.Unix(1_700_000_000, 0)}
	src := &fakeSource{models: map[string]int64{"org/a": 100, "org/b": 100}}
	const bound = 250 * time.Millisecond
	p := newTestPool(t, l, src, PoolOptions{
		MaxResidentBytes: 200,
		EvictionGrace:    bound,
		MaxEvictionWait:  bound,
		now:              clock.Now,
	})

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
		t.Fatal("org/b loaded although nothing could be freed in time")
	}
	if !errors.Is(err, ErrBusy) {
		t.Errorf("Acquire org/b = %v, want a busy refusal", err)
	}
	// The refused request must not have taken org/a down with it.
	if got := p.Resident(); len(got) != 1 || got[0].RepoID != "org/a" {
		t.Errorf("Resident() = %+v, want org/a still loaded", got)
	}
	select {
	case <-l.procFor("org/a").stopped:
		t.Error("a model was stopped for a caller that was then refused")
	default:
	}
	if bytes, _ := p.Draining(); bytes != 0 {
		t.Errorf("Draining() = %d bytes, want nothing stopped", bytes)
	}
}

// A caller waiting for a stopped server to exit is a caller waiting for memory,
// and it is held to the same queue caps as any other: it counts towards
// MaxLoadWaiters, it shows up in Waiting(), and the callers that cannot join
// are refused at once rather than all being parked for the drain bound with a
// connection and a buffered request body each.
func TestDrainWaitersAreHeldToTheQueueCap(t *testing.T) {
	l := newFakeLauncher()
	l.holdExitFor = "org/a"
	models := map[string]int64{"org/a": 100}
	var others []string
	for i := range 12 {
		id := fmt.Sprintf("org/w%d", i)
		models[id] = 100
		others = append(others, id)
	}
	src := &fakeSource{models: models}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 200, MaxLoadWaiters: 1})

	_, release, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatalf("Acquire org/a: %v", err)
	}
	release()

	type outcome struct {
		id  string
		err error
	}
	results := make(chan outcome, len(others))
	for _, id := range others {
		go func(id string) {
			_, rel, err := p.Acquire(context.Background(), id)
			if rel != nil {
				rel()
			}
			results <- outcome{id: id, err: err}
		}(id)
	}

	// Eleven of the twelve cannot join the queue and must be answered at once,
	// not held for the drain bound with a request body each.
	refused := 0
	deadline := time.After(5 * time.Second)
	for refused < len(others)-1 {
		select {
		case got := <-results:
			if got.err == nil {
				t.Fatalf("%s loaded while the machine was full", got.id)
			}
			if !errors.Is(got.err, ErrBusy) {
				t.Errorf("%s: %v, want a busy refusal", got.id, got.err)
			}
			refused++
		case <-deadline:
			t.Fatalf("only %d of %d callers were refused; the rest are parked past the queue cap",
				refused, len(others)-1)
		}
	}
	if n := p.Waiting(); n != 1 {
		t.Errorf("Waiting() = %d while a caller waits for a stopped server, want 1", n)
	}

	// The one that did join the queue gets its model once the memory is back.
	l.procFor("org/a").exit()
	select {
	case got := <-results:
		if got.err != nil {
			t.Errorf("the queued caller was refused after the memory came back: %v", got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the queued caller never got its model")
	}
}

// A caller that never queued — its victim was already past the grace, so it
// evicts on its first pass — is bounded by the same maximum wait as one that
// did, and the refusal reports the time it really spent rather than the zero a
// caller that never joined the queue would report.
func TestACallerThatEvictsOnItsFirstPassIsStillBounded(t *testing.T) {
	l := newFakeLauncher()
	l.holdExitFor = "org/a"
	src := &fakeSource{models: map[string]int64{"org/a": 100, "org/b": 100}}
	p := newTestPool(t, l, src, PoolOptions{
		MaxResidentBytes: 200,
		EvictionGrace:    50 * time.Millisecond,
		MaxEvictionWait:  200 * time.Millisecond,
	})

	_, release, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatalf("Acquire org/a: %v", err)
	}
	release()
	// Let org/a fall past its own protection, so the next caller may take it
	// without queueing for the grace first.
	time.Sleep(120 * time.Millisecond)

	started := time.Now()
	_, rel, err := p.Acquire(context.Background(), "org/b")
	if rel != nil {
		rel()
	}
	took := time.Since(started)
	if err == nil {
		t.Fatal("org/b loaded on top of a server that had not exited")
	}
	// maxDrainWait is far longer than the maximum wait; anything near it means
	// the caller was never held to its own bound.
	if took > 3*time.Second {
		t.Errorf("the refusal took %s against a maximum wait of 200ms", took)
	}
	var noRoom *NoRoomError
	if !errors.As(err, &noRoom) {
		t.Fatalf("Acquire org/b = %v, want a *NoRoomError", err)
	}
	// The client held a connection for the whole of took, and both the
	// Retry-After header and the statistics are built from this figure.
	if noRoom.Waited < took/2 {
		t.Errorf("the refusal reports a wait of %s after holding the caller %s", noRoom.Waited, took)
	}
}

// The start-up preload honours no eviction grace, but it still cannot start a
// server on top of one that has not exited. It waits for that, and it answers
// its own context while it does.
func TestAcquireNowWaitsForADrainAndHonoursItsContext(t *testing.T) {
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
		_, rel, err := p.AcquireNow(ctx, "org/b")
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
	select {
	case err := <-failed:
		t.Fatalf("AcquireNow started a server on top of one still exiting (err %v)", err)
	case <-time.After(300 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-failed:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("AcquireNow after cancellation = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AcquireNow ignored its context while waiting for a drain")
	}
	if n := p.Waiting(); n != 0 {
		t.Errorf("Waiting() = %d after the preload gave up, want 0", n)
	}
}

// A server that survives SIGTERM and SIGKILL holds its memory for as long as
// the kernel keeps it. The charge stays — the memory really is gone — but it
// stops being invisible: the pool says so once, in a warning, and reports it on
// the snapshot the control panel reads, so an operator is not left looking at a
// budget that has silently shrunk.
func TestAServerThatWillNotDieIsReportedAsStuck(t *testing.T) {
	l := newFakeLauncher()
	l.holdExitFor = "org/a"
	var logged safeBuf
	src := &fakeSource{models: map[string]int64{"org/a": 100, "org/b": 100}}
	p := newTestPool(t, l, src, PoolOptions{
		MaxResidentBytes: 200,
		drainWait:        100 * time.Millisecond,
		Log:              slog.New(slog.NewTextHandler(&logged, nil)),
	})

	_, release, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatalf("Acquire org/a: %v", err)
	}
	release()
	if err := p.Unload("org/a"); err != nil {
		t.Fatalf("Unload org/a: %v", err)
	}

	waitFor(t, "the stuck server to be counted", func() bool {
		bytes, stuck := p.Draining()
		return stuck == 1 && bytes == capability.LoadCost(100)
	})
	if got := logged.String(); !strings.Contains(got, "has not exited") {
		t.Errorf("nothing was logged about a server that would not die: %q", got)
	}

	// When the kernel does let go, the charge and the count both come back.
	l.procFor("org/a").exit()
	waitFor(t, "the charge to come back", func() bool {
		bytes, stuck := p.Draining()
		return bytes == 0 && stuck == 0
	})
}

// waitFor polls until cond holds, or fails the test saying what it was waiting
// for. Used where the thing being awaited is a background goroutine's effect on
// the pool rather than something with a channel to select on.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
