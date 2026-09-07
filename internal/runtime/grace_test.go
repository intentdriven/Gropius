package runtime

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// graceModels are three models of the same charged size, with a budget that
// holds exactly one of them, so every request for a second model is a request
// for room.
func graceModels() *fakeSource {
	return &fakeSource{models: map[string]int64{"org/a": 200, "org/b": 200, "org/c": 200}}
}

// LoadCost is 1.2x, so each of the models above is charged 240 against this.
const graceBudget = 250

// waitUntil polls a condition, so a test says what it is waiting for rather
// than how long it decided to sleep.
func waitUntil(t *testing.T, within time.Duration, cond func() bool, unmet string) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%s (waited %s)", unmet, within)
}

// warm loads a model and releases it, so it is resident, idle, and stamped
// with the moment it finished work — which is the clock the grace reads.
func warm(t *testing.T, p *Pool, repoID string) {
	t.Helper()
	_, release, err := p.Acquire(context.Background(), repoID)
	if err != nil {
		t.Fatalf("Acquire(%q): %v", repoID, err)
	}
	release()
}

// Criterion 1. Off is the default and off is today: the model that has to go
// goes at once, and nothing is ever queued.
func TestWithGraceOffAModelIsEvictedAtOnceAndNothingWaits(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, graceModels(), PoolOptions{MaxResidentBytes: graceBudget})

	warm(t, p, "org/a")

	start := time.Now()
	_, release, err := p.Acquire(context.Background(), "org/b")
	if err != nil {
		t.Fatalf("Acquire(org/b): %v", err)
	}
	defer release()
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("the second model took %s to load; with grace off nothing waits", took)
	}
	if ids := residentIDs(p); !slices.Equal(ids, []string{"org/b"}) {
		t.Errorf("resident = %v, want the first model evicted at once", ids)
	}
	if n := p.Waiting(); n != 0 {
		t.Errorf("Waiting() = %d with grace off, want 0", n)
	}
}

// Criterion 2. A model that finished work moments ago survives a competing
// request, and that request is not answered yet.
func TestAModelThatJustFinishedIsNotEvictedAndTheRequestWaits(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    2 * time.Second,
		MaxEvictionWait:  10 * time.Second,
	})

	warm(t, p, "org/a")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, release, err := p.Acquire(ctx, "org/b")
		if err == nil {
			release()
		}
		done <- err
	}()

	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the request never joined the queue")

	time.Sleep(200 * time.Millisecond)
	if ids := residentIDs(p); !slices.Equal(ids, []string{"org/a"}) {
		t.Errorf("resident = %v, want the model that just finished still loaded", ids)
	}
	select {
	case err := <-done:
		t.Fatalf("Acquire(org/b) returned %v inside the grace; it must still be waiting", err)
	default:
	}

	// And the wait is the client's to abandon: cancelling it gives the place
	// in the queue back rather than holding it for the rest of the maximum.
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled wait returned %v, want context.Canceled", err)
	}
	waitUntil(t, time.Second, func() bool { return p.Waiting() == 0 },
		"a cancelled request stayed in the queue")
}

// Criterion 3. Once the protected model falls past its grace the waiting
// request takes it, and the upstream it gets back reports the wait — which is
// what the gateway turns into the two response headers.
func TestARequestIsServedOnceTheProtectedModelFallsPastItsGrace(t *testing.T) {
	l := newFakeLauncher()
	grace := 200 * time.Millisecond
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    grace,
		MaxEvictionWait:  10 * time.Second,
	})

	warm(t, p, "org/a")

	start := time.Now()
	up, release, err := p.Acquire(context.Background(), "org/b")
	if err != nil {
		t.Fatalf("Acquire(org/b): %v", err)
	}
	defer release()

	if took := time.Since(start); took < grace/2 {
		t.Errorf("the request was served after %s; it should have waited out the grace", took)
	}
	if ids := residentIDs(p); !slices.Equal(ids, []string{"org/b"}) {
		t.Errorf("resident = %v, want the protected model evicted once its grace ran out", ids)
	}
	if up.Waits.QueueWait < grace/2 {
		t.Errorf("Waits.QueueWait = %s, want the wait for room it actually paid", up.Waits.QueueWait)
	}
	if n := p.Waiting(); n != 0 {
		t.Errorf("Waiting() = %d after the request was served, want 0", n)
	}
}

// Criterion 4. A trickle of short requests to one model must not deny another
// client indefinitely: the waiter's own age bounds the protection.
func TestATrickleOfRequestsCannotStarveAWaitingRequest(t *testing.T) {
	l := newFakeLauncher()
	grace := 300 * time.Millisecond
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    grace,
		MaxEvictionWait:  20 * time.Second,
	})

	warm(t, p, "org/a")

	stop := make(chan struct{})
	trickled := make(chan struct{})
	go func() {
		defer close(trickled)
		for {
			select {
			case <-stop:
				return
			case <-time.After(40 * time.Millisecond):
			}
			// Keep asking for the resident model, which is what renews its own
			// idleness clock. Once it has been evicted the acquire fails or
			// reloads it; either way the starving waiter has been served.
			if _, release, err := p.Acquire(context.Background(), "org/a"); err == nil {
				release()
			} else {
				return
			}
		}
	}()

	start := time.Now()
	up, release, err := p.Acquire(context.Background(), "org/b")
	close(stop)
	<-trickled
	if err != nil {
		t.Fatalf("Acquire(org/b) was starved by a trickle of requests to another model: %v", err)
	}
	release()

	took := time.Since(start)
	if took < grace/2 {
		t.Errorf("the waiter was served after %s, before its own age passed the grace", took)
	}
	if took > 10*grace {
		t.Errorf("the waiter took %s to be served, want it bounded by its own age passing the grace", took)
	}
	if up.Waits.QueueWait < grace/2 {
		t.Errorf("Waits.QueueWait = %s, want the wait it paid", up.Waits.QueueWait)
	}
}

// Criterion 5. A request that could never fit — the pinned models plus what it
// needs are over the budget — is refused at once rather than holding a
// connection open for the whole maximum wait.
func TestARequestThatCanNeverFitIsRefusedWithoutWaiting(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		Pinned:           []string{"org/a"},
		EvictionGrace:    2 * time.Second,
		MaxEvictionWait:  30 * time.Second,
	})

	warm(t, p, "org/a")

	start := time.Now()
	_, _, err := p.Acquire(context.Background(), "org/b")
	took := time.Since(start)
	if err == nil {
		t.Fatal("a request that can never fit was served")
	}
	if !errors.Is(err, ErrBusy) {
		t.Errorf("err = %v, want it to wrap ErrBusy", err)
	}
	if took > time.Second {
		t.Errorf("the refusal took %s; a request that can never fit must not wait", took)
	}
	for _, id := range []string{"org/a", "org/b"} {
		if strings.Contains(err.Error(), id) {
			t.Errorf("the refusal names %q; it reaches an unauthenticated LAN client: %v", id, err)
		}
	}
	if n := p.Waiting(); n != 0 {
		t.Errorf("Waiting() = %d, want the request never to have queued", n)
	}
}

// Criterion 6. The queue has its own ceiling, and past it a request is refused
// at once with the refusal that names no model.
func TestPastTheLoadWaiterCapARequestIsRefusedAtOnce(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    10 * time.Second,
		MaxEvictionWait:  30 * time.Second,
		MaxLoadWaiters:   1,
	})

	warm(t, p, "org/a")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if _, release, err := p.Acquire(ctx, "org/b"); err == nil {
			release()
		}
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the first request never joined the queue")

	start := time.Now()
	_, _, err := p.Acquire(context.Background(), "org/c")
	took := time.Since(start)
	if err == nil {
		t.Fatal("a request past the load-waiter cap was served")
	}
	if !errors.Is(err, ErrBusy) {
		t.Errorf("err = %v, want it to wrap ErrBusy", err)
	}
	if took > time.Second {
		t.Errorf("the refusal took %s; past the cap a request must not wait", took)
	}
	for _, id := range []string{"org/a", "org/b", "org/c"} {
		if strings.Contains(err.Error(), id) {
			t.Errorf("the refusal names %q: %v", id, err)
		}
	}
	if n := p.Waiting(); n != 1 {
		t.Errorf("Waiting() = %d, want the cap's worth and no more", n)
	}
}

// A cancelled waiter gives its place back, so the queue is a bound on
// concurrent waiters rather than a bound on how many requests may ever wait.
func TestACancelledWaiterGivesItsPlaceBack(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    10 * time.Second,
		MaxEvictionWait:  30 * time.Second,
		MaxLoadWaiters:   1,
	})

	warm(t, p, "org/a")

	first, cancelFirst := context.WithCancel(context.Background())
	go func() {
		if _, release, err := p.Acquire(first, "org/b"); err == nil {
			release()
		}
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the first request never joined the queue")
	cancelFirst()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 0 },
		"the cancelled request kept its place in the queue")

	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	go func() {
		if _, release, err := p.Acquire(second, "org/c"); err == nil {
			release()
		}
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the next request was refused although the queue had emptied")
}

// Criterion 7. A budget the operator raises while a request is waiting makes
// room without anything having to finish, and the waiting request acts on it
// at once rather than at the next release.
func TestRaisingTheMemoryBudgetWakesAWaitingRequest(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    10 * time.Second,
		MaxEvictionWait:  30 * time.Second,
	})

	warm(t, p, "org/a")

	done := make(chan error, 1)
	go func() {
		_, release, err := p.Acquire(context.Background(), "org/b")
		if err == nil {
			release()
		}
		done <- err
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the request never joined the queue")

	p.SetMemoryBudget(1 << 30)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Acquire(org/b) after the budget was raised: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("raising the budget did not wake the waiting request")
	}
	if ids := residentIDs(p); !slices.Equal(ids, []string{"org/a", "org/b"}) {
		t.Errorf("resident = %v, want both models with nothing evicted", ids)
	}
}

// A pin is the other live change that decides whether a wait can ever end.
// Pinning the only candidate makes the waiting request impossible, and it is
// told so at once rather than after the whole maximum wait.
func TestPinningTheOnlyCandidateReleasesAWaitingRequestAtOnce(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    10 * time.Second,
		MaxEvictionWait:  30 * time.Second,
	})

	warm(t, p, "org/a")

	done := make(chan error, 1)
	go func() {
		_, release, err := p.Acquire(context.Background(), "org/b")
		if err == nil {
			release()
		}
		done <- err
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the request never joined the queue")

	p.SetPinned([]string{"org/a"})

	select {
	case err := <-done:
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("err = %v, want the no-room refusal", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pinning the only candidate left the request waiting")
	}
}

// Switching grace off is the operator saying "swap now". A request already
// waiting takes its victim rather than sitting out a grace nobody wants any
// more.
func TestSwitchingGraceOffReleasesAWaitingRequest(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    10 * time.Second,
		MaxEvictionWait:  30 * time.Second,
	})

	warm(t, p, "org/a")

	done := make(chan error, 1)
	go func() {
		_, release, err := p.Acquire(context.Background(), "org/b")
		if err == nil {
			release()
		}
		done <- err
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the request never joined the queue")

	p.SetEvictionGrace(0, 0)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Acquire(org/b) after grace was switched off: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("switching grace off left the request waiting")
	}
	if ids := residentIDs(p); !slices.Equal(ids, []string{"org/b"}) {
		t.Errorf("resident = %v, want the swap the operator asked for", ids)
	}
}

// The wait is bounded. A model that is never free long enough leaves the
// waiting request with the refusal it would have had at once without grace,
// and the refusal says how long it waited.
func TestAWaitingRequestGivesUpAfterTheMaximumWait(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    30 * time.Second,
		MaxEvictionWait:  250 * time.Millisecond,
	})

	warm(t, p, "org/a")

	start := time.Now()
	_, _, err := p.Acquire(context.Background(), "org/b")
	took := time.Since(start)
	if err == nil {
		t.Fatal("the request was served although nothing ever fell idle")
	}
	if !errors.Is(err, ErrBusy) {
		t.Errorf("err = %v, want it to wrap ErrBusy", err)
	}
	if took < 200*time.Millisecond {
		t.Errorf("the request gave up after %s, before the maximum wait", took)
	}
	if took > 5*time.Second {
		t.Errorf("the request waited %s, past the maximum wait", took)
	}
	if !strings.Contains(err.Error(), "waiting") {
		t.Errorf("the refusal %q does not say that the request waited", err)
	}
	for _, id := range []string{"org/a", "org/b"} {
		if strings.Contains(err.Error(), id) {
			t.Errorf("the refusal names %q: %v", id, err)
		}
	}
	if n := p.Waiting(); n != 0 {
		t.Errorf("Waiting() = %d after the refusal, want 0", n)
	}
}

// Shutdown is not a wait anyone can outlast: every parked request is answered
// rather than left holding a connection while the process goes away.
func TestClosingThePoolRefusesEveryWaitingRequest(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    30 * time.Second,
		MaxEvictionWait:  30 * time.Second,
	})

	warm(t, p, "org/a")

	done := make(chan error, 1)
	go func() {
		_, release, err := p.Acquire(context.Background(), "org/b")
		if err == nil {
			release()
		}
		done <- err
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the request never joined the queue")

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("err = %v, want ErrClosed", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown left a request waiting")
	}
}

// Start-up loading is sequential, so a preload list of models that cannot all
// fit would stall a start by one maximum wait per model. AcquireNow is the
// path that does not wait; it is the only caller allowed to swap under a
// grace, because at start-up nobody is being protected from anybody.
func TestAcquireNowTakesAVictimInsideItsGrace(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    30 * time.Second,
		MaxEvictionWait:  30 * time.Second,
	})

	warm(t, p, "org/a")

	start := time.Now()
	_, release, err := p.AcquireNow(context.Background(), "org/b")
	if err != nil {
		t.Fatalf("AcquireNow(org/b): %v", err)
	}
	defer release()
	if took := time.Since(start); took > time.Second {
		t.Errorf("AcquireNow took %s; it does not wait out a grace", took)
	}
	if ids := residentIDs(p); !slices.Equal(ids, []string{"org/b"}) {
		t.Errorf("resident = %v, want the swap AcquireNow asked for", ids)
	}
}

// The grace runs from the moment a model stops working, not from the moment
// the request that kept it busy arrived: a model held open for longer than the
// whole grace is protected from the instant it is let go, not already past it.
//
// This holds the rule, not the field. release stamps lastUsed as well, so an
// idle entry carries the same instant in both today; what would fail here is a
// grace read from when the model loaded, or from when its last request began.
func TestAModelHeldPastTheGraceIsProtectedFromWhenItIsReleased(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    150 * time.Millisecond,
		MaxEvictionWait:  10 * time.Second,
	})

	_, release, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatalf("Acquire(org/a): %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	release()

	start := time.Now()
	_, releaseB, err := p.Acquire(context.Background(), "org/b")
	if err != nil {
		t.Fatalf("Acquire(org/b): %v", err)
	}
	defer releaseB()
	if took := time.Since(start); took < 100*time.Millisecond {
		t.Errorf("the competing request was served after %s; the model it evicted had "+
			"only just finished work", took)
	}
}
