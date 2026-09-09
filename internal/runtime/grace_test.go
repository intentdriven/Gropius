package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
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
	maxWait := 20 * time.Second
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    grace,
		MaxEvictionWait:  maxWait,
	})

	warm(t, p, "org/a")

	// The trickle is stopped by cancelling its own context, not only by a
	// channel it reads between requests. Its loop is sequential and its Acquire
	// blocks, so a stop the blocked call cannot see does not stop it — and by
	// the time this test asks it to finish, the trickle's request for org/a is
	// a request for room that org/b holds in flight, which is a wait no signal
	// can shorten. This test then waited out the whole maximum wait for a
	// goroutine it had already asked to stop (iss-2609081020327017).
	trickleCtx, stopTrickle := context.WithCancel(context.Background())
	defer stopTrickle()

	// The three probes a future failure is read with: how old the waiter was
	// when it was served, when the trickle last actually got the model, and how
	// the trickle ended. Without the second, "the clause lost" and "the premise
	// collapsed and the clause was never tested" are the same line.
	var probeMu sync.Mutex
	var lastAcquired time.Time
	var trickleErr error

	trickled := make(chan struct{})
	go func() {
		defer close(trickled)
		for {
			select {
			case <-trickleCtx.Done():
				return
			case <-time.After(40 * time.Millisecond):
			}
			// Keep asking for the resident model, which is what renews its own
			// idleness clock. Once it has been evicted the acquire fails or
			// reloads it; either way the starving waiter has been served.
			_, release, err := p.Acquire(trickleCtx, "org/a")
			probeMu.Lock()
			if err == nil {
				lastAcquired = time.Now()
			} else {
				trickleErr = err
			}
			probeMu.Unlock()
			if err != nil {
				return
			}
			release()
		}
	}()

	start := time.Now()
	up, release, err := p.Acquire(context.Background(), "org/b")
	// Read before the shutdown handshake below, not after it: this is the
	// waiter's service time, and waiting for the trickle to wind up is not part
	// of it.
	took := time.Since(start)
	stopTrickle()
	<-trickled
	if err != nil {
		t.Fatalf("Acquire(org/b) was starved by a trickle of requests to another model: %v", err)
	}
	release()

	probeMu.Lock()
	lastOK, why := lastAcquired, trickleErr
	probeMu.Unlock()
	probes := func() string {
		last := "never — the trickle got the model no time at all"
		if !lastOK.IsZero() {
			last = fmt.Sprintf("%s into the wait", lastOK.Sub(start))
		}
		ended := "still running when the wait ended"
		if why != nil {
			ended = why.Error()
		}
		return fmt.Sprintf("waiter age at service %s (grace %s, maximum wait %s); "+
			"the trickle's last successful acquire: %s; the trickle ended with: %s",
			took, grace, maxWait, last, ended)
	}

	if lastOK.IsZero() {
		t.Errorf("the trickle never once got org/a, so nothing was competing with the waiter and this test measured something other than its name: %s", probes())
	}
	if took < grace/2 {
		t.Errorf("the waiter was served after %s, before its own age passed the grace: %s", took, probes())
	}
	// Bounded by its own age passing the grace, not by the maximum wait. The
	// margin stays wide on purpose: the property is "far short of the maximum",
	// and 5s is a twenty-fifth of that maximum while being far more than the
	// 300ms grace plus a fake launcher's load — so a slow runner cannot fail it
	// and a waiter parked to the maximum cannot pass it. Tightening it towards
	// the grace would trade a fact for a timing flake.
	if took > 5*time.Second {
		t.Errorf("the waiter took %s to be served, want it bounded by its own age passing the grace: %s", took, probes())
	}
	if up.Waits.QueueWait < grace/2 {
		t.Errorf("Waits.QueueWait = %s, want the wait it paid: %s", up.Waits.QueueWait, probes())
	}
}

// Criterion 4, taken from the pool's own arithmetic instead of from the clock:
// a waiter that is already past its grace looks again within the grace,
// whatever the models in memory happen to be doing.
//
// wakeDelayLocked is the backstop for a change that nothing signals, and its
// three computable terms can all drop out at once. Past the grace the waiter's
// own term is non-positive, which is right — that moment has gone. A candidate
// with a request in flight or still loading yields no deadline either, which is
// also right: those states end on an event, not at a moment. With nothing left,
// the delay fell back to the whole maximum wait. In practice the request ending
// woke the waiter, so this was one missing signal from a real stall rather than
// a stall; a backstop resting on the signal it backs up is what is fixed here.
//
// The two cases below are the same waiter under the two graces that matter: one
// comfortably above the re-check floor, where the grace itself is the interval,
// and one far below it, where the floor is — because EvictionGrace has no lower
// bound and a millisecond of it must not turn a parked waiter into a thousand
// lock acquisitions a second.
//
// Nothing here is a race against wall time. The candidate's idleness runs on
// the pool's injected clock, which this test freezes; the waiter's age is a
// real-time stamp this test writes, because a waiter is clocked on real time by
// design (see loadWaiter). Both terms are therefore set by the test, and the
// margin between the answer wanted and the answer the fallback gives is the
// whole maximum wait.
func TestAWaiterPastItsGraceDoesNotParkToTheMaximumBehindAnInFlightModel(t *testing.T) {
	maxWait := 20 * time.Second
	for _, tc := range []struct {
		name    string
		grace   time.Duration
		recheck time.Duration
	}{
		{"a grace above the floor sets the re-check", 300 * time.Millisecond, 300 * time.Millisecond},
		{"a grace below the floor is held up by it", time.Millisecond, wakeRecheckFloor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newFakeLauncher()
			clock := &testClock{now: time.Unix(1_700_000_000, 0)}
			p := newTestPool(t, l, graceModels(), PoolOptions{
				MaxResidentBytes: graceBudget,
				EvictionGrace:    tc.grace,
				MaxEvictionWait:  maxWait,
				now:              clock.Now,
			})

			// org/a resident with a request in flight: the release is
			// deliberately held back, so the pool's only eviction candidate is
			// busy and yields no deadline of its own.
			_, release, err := p.Acquire(context.Background(), "org/a")
			if err != nil {
				t.Fatalf("Acquire(org/a): %v", err)
			}
			defer release()

			// A waiter for org/b that arrived two graces ago, which is the
			// state the fallback needed: its own age clause has already fired
			// and contributes nothing further.
			age := 2 * tc.grace
			w := &loadWaiter{
				arrived: time.Now().Add(-age),
				need:    LoadCost(200),
				signal:  make(chan struct{}, 1),
			}

			p.mu.Lock()
			p.waiters = append(p.waiters, w)
			delay := p.wakeDelayLocked(w)
			p.waiters = nil
			p.mu.Unlock()

			if delay > tc.recheck {
				t.Errorf("a waiter %s old, behind a model with a request in flight, was parked for %s; "+
					"want a re-check within %s, not a sleep bounded by the maximum wait (%s)",
					age, delay, tc.recheck, maxWait)
			}
			// The other side of the same bound: the re-check must not be
			// cheaper to satisfy than it is to serve. Nothing here has a
			// deadline shorter than the floor, so a shorter delay is a spin.
			if delay < wakeRecheckFloor {
				t.Errorf("the waiter was parked for only %s with no deadline shorter than that to wake for; "+
					"want at least the re-check floor (%s), or a parked waiter spins on the pool's lock",
					delay, wakeRecheckFloor)
			}
		})
	}
}

// The same bound, on the path that has nothing to do with a busy model: a
// waiter behind another waiter. Only the oldest may evict, so a second waiter
// past its grace, with every candidate idle and past its grace too, reads no
// deadline from any of them either — every remaining term has already fired.
// It is woken when the head leaves the queue, and that is a signal; the point
// of the re-check is that no term rests on one arriving.
func TestAWaiterBehindAnotherWaiterAlsoRechecksWithinTheGrace(t *testing.T) {
	l := newFakeLauncher()
	clock := &testClock{now: time.Unix(1_700_000_000, 0)}
	grace := 300 * time.Millisecond
	maxWait := 20 * time.Second
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    grace,
		MaxEvictionWait:  maxWait,
		now:              clock.Now,
	})

	// org/a resident, idle, and stamped long enough ago that its own grace has
	// run out: it offers no deadline either.
	warm(t, p, "org/a")
	clock.advance(10 * grace)

	head := &loadWaiter{arrived: time.Now().Add(-4 * grace), need: LoadCost(200), signal: make(chan struct{}, 1)}
	behind := &loadWaiter{arrived: time.Now().Add(-2 * grace), need: LoadCost(200), signal: make(chan struct{}, 1)}

	p.mu.Lock()
	p.waiters = append(p.waiters, head, behind)
	delay := p.wakeDelayLocked(behind)
	p.waiters = nil
	p.mu.Unlock()

	if delay > grace {
		t.Errorf("the second waiter was parked for %s; want a re-check within the grace (%s), "+
			"not a sleep bounded by the maximum wait (%s)", delay, grace, maxWait)
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
	var logged safeBuffer
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    10 * time.Second,
		MaxEvictionWait:  30 * time.Second,
		MaxLoadWaiters:   1,
		Log:              slog.New(slog.NewTextHandler(&logged, nil)),
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
	// The wire says nothing, deliberately; this Mac's own log has to say which
	// of the two facts it is, or an operator cannot tell a full queue from a
	// machine whose memory is all spoken for.
	if got := logged.String(); !strings.Contains(got, "already waiting for memory") {
		t.Errorf("the log says %q; it does not say the queue was full", got)
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

// Switching grace off is the operator saying "swap now". Every request already
// waiting is released to take the path it would have taken with grace off all
// along — which is to evict, and to be refused only if there is genuinely
// nothing to evict. Releasing only the one at the head and refusing the rest
// would turn the off switch into a 503 for every client already queued.
//
// Seven waiters, because the failure needs one to reach the pool's lock while
// another is still at the head of the queue: with one waiter there is no such
// moment, and with seven the odds of them all arriving in queue order are
// negligible.
func TestSwitchingGraceOffReleasesEveryWaitingRequest(t *testing.T) {
	l := newFakeLauncher()
	models := map[string]int64{"org/held": 4000} // charged 4800 of a 5000 budget
	for i := range 7 {
		models[fmt.Sprintf("org/w%d", i)] = 200 // charged 240 each
	}
	p := newTestPool(t, l, &fakeSource{models: models}, PoolOptions{
		MaxResidentBytes: 5000,
		EvictionGrace:    30 * time.Second,
		MaxEvictionWait:  20 * time.Second,
		MaxLoadWaiters:   7,
	})

	// Resident, idle and protected, and large enough that nothing else fits
	// beside it — so every request behind it queues, and once it goes they all
	// fit at once with nothing further to evict.
	warm(t, p, "org/held")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errs := make(chan error, 7)
	for i := range 7 {
		go func(i int) {
			_, release, err := p.Acquire(ctx, fmt.Sprintf("org/w%d", i))
			if err == nil {
				release()
			}
			errs <- err
		}(i)
	}
	waitUntil(t, 5*time.Second, func() bool { return p.Waiting() == 7 },
		"the requests never all joined the queue")

	p.SetEvictionGrace(0, 0)

	for i := range 7 {
		select {
		case err := <-errs:
			if err != nil {
				t.Errorf("a request waiting when grace was switched off was refused "+
					"rather than released: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of the 7 waiting requests were answered", i)
		}
	}
	if n := p.Waiting(); n != 0 {
		t.Errorf("Waiting() = %d after the queue was drained, want 0", n)
	}
}

// The wait is bounded. A model that is never free leaves the waiting request
// with the refusal it would have had at once without grace, and the refusal
// says how long it waited.
//
// The model is held open rather than merely protected, because a maximum wait
// shorter than the grace is no longer a thing that can be configured — it
// would make the waiter-age clause unreachable — so the case that reaches the
// maximum is a model that is never a candidate at all.
func TestAWaitingRequestGivesUpAfterTheMaximumWait(t *testing.T) {
	l := newFakeLauncher()
	const bound = 250 * time.Millisecond
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    bound,
		MaxEvictionWait:  bound,
	})

	_, hold, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatalf("Acquire(org/a): %v", err)
	}
	defer hold()

	start := time.Now()
	_, _, err = p.Acquire(context.Background(), "org/b")
	took := time.Since(start)
	if err == nil {
		t.Fatal("the request was served although nothing ever fell idle")
	}
	if !errors.Is(err, ErrBusy) {
		t.Errorf("err = %v, want it to wrap ErrBusy", err)
	}
	if took < bound-50*time.Millisecond {
		t.Errorf("the request gave up after %s, before the maximum wait of %s", took, bound)
	}
	if took > 5*time.Second {
		t.Errorf("the request waited %s, past the maximum wait of %s", took, bound)
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

// fairnessModels are three small models and one that needs two of them out of
// the way, so a test can tell a waiter that fits in the room going spare from
// one that does not.
func fairnessModels() *fakeSource {
	return &fakeSource{models: map[string]int64{
		"org/s1": 200, "org/s2": 200, "org/s3": 200, "org/big": 400,
	}}
}

// LoadCost is 1.2x: the small models are charged 240 and the big one 480,
// against a budget of 500 that holds two small ones or one big one.
const fairnessBudget = 500

// Waiters are served oldest first, and that binds a request needing no
// eviction at all. Gating only the eviction would let a stream of small
// requests take the room as it appears while the waiter at the head — needing
// more of it than any one release frees — never fits, which is the
// "smallest-to-load first starves a large model" failure the queue exists to
// prevent, reached by the other door.
func TestARequestThatFitsDoesNotStepOverTheWaiterAtTheHead(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, fairnessModels(), PoolOptions{
		MaxResidentBytes: fairnessBudget,
		EvictionGrace:    30 * time.Second,
		MaxEvictionWait:  20 * time.Second,
	})

	// One small model resident and protected; 260 bytes of the budget spare,
	// which is room for another small model but not for the big one.
	warm(t, p, "org/s1")

	head, cancelHead := context.WithCancel(context.Background())
	defer cancelHead()
	go func() {
		if _, rel, err := p.Acquire(head, "org/big"); err == nil {
			rel()
		}
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the big model's request never joined the queue")

	behind, cancelBehind := context.WithCancel(context.Background())
	defer cancelBehind()
	go func() {
		if _, rel, err := p.Acquire(behind, "org/s2"); err == nil {
			rel()
		}
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 2 },
		"the second request was served rather than queued behind the head")

	time.Sleep(200 * time.Millisecond)
	if ids := residentIDs(p); slices.Contains(ids, "org/s2") {
		t.Errorf("resident = %v; a request that fits stepped over the waiter at the head", ids)
	}

	// And when the head gives up, the one behind it is served — which is the
	// wake-up a waiter owes the queue as it leaves.
	cancelHead()
	waitUntil(t, 3*time.Second, func() bool { return slices.Contains(residentIDs(p), "org/s2") },
		"the waiter behind the head was not woken when the head left the queue")
}

// A model falling idle is the commonest way room appears, and the release is
// what says so. Without that wake-up a waiter sleeps until its next timer,
// which for a model that is busy rather than protected is the whole maximum
// wait.
func TestReleasingAModelWakesAWaitingRequest(t *testing.T) {
	l := newFakeLauncher()
	grace := 50 * time.Millisecond
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    grace,
		MaxEvictionWait:  20 * time.Second,
	})

	// Held open, so it is not a candidate at all — no grace timer covers it.
	_, release, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatalf("Acquire(org/a): %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, rel, err := p.Acquire(context.Background(), "org/b")
		if err == nil {
			rel()
		}
		done <- err
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the request never joined the queue")
	// Past its own grace, so the model is eligible the moment it is free.
	time.Sleep(4 * grace)

	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Acquire(org/b) after the model was released: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("releasing the model did not wake the waiting request")
	}
}

// The operator's own unload is the other way room appears, and every removal
// goes through the same place, so this holds the wake-up for eviction, the
// idle reaper and a crash as well.
func TestUnloadingAModelWakesAWaitingRequest(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    30 * time.Second,
		MaxEvictionWait:  20 * time.Second,
	})

	warm(t, p, "org/a")

	done := make(chan error, 1)
	go func() {
		_, rel, err := p.Acquire(context.Background(), "org/b")
		if err == nil {
			rel()
		}
		done <- err
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the request never joined the queue")

	if err := p.Unload("org/a"); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Acquire(org/b) after the resident model was unloaded: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unloading the resident model did not wake the waiting request")
	}
}

// The plan takes the least recently used candidate first. Every other eviction
// test in the suite has one eligible candidate, so the order the plan produces
// is otherwise asserted nowhere — and this change rewrote the code that
// produces it.
func TestTheEvictionPlanTakesTheLeastRecentlyUsedFirst(t *testing.T) {
	l := newFakeLauncher()
	clock := &testClock{now: time.Unix(1_700_000_000, 0)}
	p := newTestPool(t, l, fairnessModels(), PoolOptions{
		MaxResidentBytes: fairnessBudget,
		now:              clock.Now,
	})

	for _, id := range []string{"org/s1", "org/s2"} {
		warm(t, p, id)
		clock.advance(time.Minute)
	}
	// Both fit; the third needs one of them out, and s1 is the older.
	_, release, err := p.Acquire(context.Background(), "org/s3")
	if err != nil {
		t.Fatalf("Acquire(org/s3): %v", err)
	}
	defer release()

	if ids := residentIDs(p); !slices.Equal(ids, []string{"org/s2", "org/s3"}) {
		t.Errorf("resident = %v, want the least recently used model evicted", ids)
	}
}

// With grace off, a load that cannot be made to fit still frees everything it
// could before it is refused. That is what the pool has always done and the
// record says the off path is unchanged, so it is pinned here rather than left
// to be rediscovered — it is also why the off path keeps the incremental
// behavior the on path deliberately does not have.
func TestWithGraceOffALoadThatCannotFitStillFreesWhatItCan(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, fairnessModels(), PoolOptions{MaxResidentBytes: fairnessBudget})

	// One model held open, so it is not a candidate, and one idle beside it.
	_, holdS1, err := p.Acquire(context.Background(), "org/s1")
	if err != nil {
		t.Fatalf("Acquire(org/s1): %v", err)
	}
	defer holdS1()
	warm(t, p, "org/s2")

	if _, _, err := p.Acquire(context.Background(), "org/big"); err == nil {
		t.Fatal("the big model loaded although only one small model could be freed")
	}
	if ids := residentIDs(p); !slices.Equal(ids, []string{"org/s1"}) {
		t.Errorf("resident = %v, want the idle model freed even though it was not enough", ids)
	}
}

// A client that hangs up while its waiter is blocked on the pool's lock must
// not go on to evict a warm model and start a server for a request that no
// longer exists — the exact outcome grace is here to prevent, paid for by
// nobody. The window is real: a woken waiter blocks on p.mu, which every
// Acquire, Resident, Unload and control-panel snapshot takes.
//
// The window is held open here by taking p.mu from the test, which is what
// makes this reproducible rather than a race the suite would hit once a
// month. If the waiter happens to notice the cancellation in its own select
// instead, the test still passes: that is the same correct answer by the
// other door, so this can be quietly green but never wrongly red.
func TestACancelledWaiterDoesNotEvictOnItsWayOut(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    30 * time.Second,
		MaxEvictionWait:  20 * time.Second,
	})

	warm(t, p, "org/a")

	ctx, cancel := context.WithCancel(context.Background())
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

	// Hold the lock, then wake the waiter and remove the one thing standing
	// between it and the victim, so that a waiter which does not check its
	// context would certainly evict.
	p.mu.Lock()
	p.grace = 0
	p.wakeWaitersLocked()
	time.Sleep(50 * time.Millisecond) // the waiter reaches p.mu and blocks
	cancel()
	time.Sleep(20 * time.Millisecond) // the cancellation lands before it runs
	p.mu.Unlock()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Acquire returned %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the cancelled request never returned")
	}
	if ids := residentIDs(p); !slices.Equal(ids, []string{"org/a"}) {
		t.Errorf("resident = %v; a request that had hung up evicted a warm model", ids)
	}
	if got := l.launchedRepos(); !slices.Equal(got, []string{"org/a"}) {
		t.Errorf("launched %v; a request that had hung up started a model server", got)
	}
}

// A caller that will not wait is not held behind the queue either. The
// fairness rule is about who gets to wait for room; a start-up preload has
// already been told it may take what it needs, and refusing it because
// somebody else is queued would leave the model cold for the rest of the
// session — the failure the no-wait path exists to prevent, reached by the
// other door.
func TestAcquireNowIsNotHeldBehindTheQueue(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, fairnessModels(), PoolOptions{
		MaxResidentBytes: fairnessBudget,
		EvictionGrace:    30 * time.Second,
		MaxEvictionWait:  20 * time.Second,
	})

	// One small model resident and protected, leaving room for another small
	// one but not for the big one.
	warm(t, p, "org/s1")

	head, cancelHead := context.WithCancel(context.Background())
	defer cancelHead()
	go func() {
		if _, rel, err := p.Acquire(head, "org/big"); err == nil {
			rel()
		}
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the big model's request never joined the queue")

	start := time.Now()
	_, release, err := p.AcquireNow(context.Background(), "org/s2")
	if err != nil {
		t.Fatalf("AcquireNow(org/s2) while another request was queued: %v", err)
	}
	defer release()
	if took := time.Since(start); took > time.Second {
		t.Errorf("AcquireNow took %s; it does not queue", took)
	}
	if ids := residentIDs(p); !slices.Contains(ids, "org/s2") {
		t.Errorf("resident = %v, want the model AcquireNow asked for", ids)
	}
}

// Every wake-up wakes every waiter, and startLocked resolves the model and
// stats two files before it reaches the eviction plan. Done on each wake-up
// that is a parked request, that is filesystem work under the pool's one lock
// — the lock every Acquire, Resident, Unload and control-panel snapshot takes
// — at whatever rate the machine completes requests. A waiter asks only when
// it could actually proceed.
func TestAParkedWaiterDoesNoFilesystemWorkOnEveryCompletedRequest(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, fairnessModels(), PoolOptions{
		MaxResidentBytes: fairnessBudget,
		EvictionGrace:    30 * time.Second,
		MaxEvictionWait:  20 * time.Second,
	})

	// Resident and protected, so the waiter for the big model can never make
	// a plan while this test runs.
	warm(t, p, "org/s1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if _, rel, err := p.Acquire(ctx, "org/big"); err == nil {
			rel()
		}
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the request never joined the queue")

	before := l.precheckCount()
	for range 20 {
		_, release, err := p.Acquire(context.Background(), "org/s1")
		if err != nil {
			t.Fatalf("Acquire(org/s1): %v", err)
		}
		release()
		// Spaced, so each release is its own wake-up: the signal channel holds
		// one, so a burst would be collapsed into a single wake and the cost
		// this measures would be hidden.
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond) // let the last wake-up be acted on
	if got := l.precheckCount() - before; got != 0 {
		t.Errorf("20 requests to a resident model cost %d launch prechecks on the "+
			"parked waiter's behalf, want none: it could not have proceeded", got)
	}
}

// safeBuffer collects log output from whichever goroutine writes it.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// queueVictimModels: one small model to keep warm, one big one for the waiter
// at the head, and a second small one for the request that arrives with the
// queue full. Against a 1000-byte budget the smalls are charged 240 and the
// big one 960.
func queueVictimModels() *fakeSource {
	return &fakeSource{models: map[string]int64{
		"org/warm": 200, "org/warm2": 200, "org/held": 200,
		"org/big": 800, "org/small": 200, "org/mid": 400,
	}}
}

// A full queue is a bound on how many requests may *wait*. It must not become
// a bound on how many may be served: a request whose model is already in
// memory, and one that fits in memory nobody is using, take nothing from the
// queue and nothing from the request at its head — which is waiting precisely
// because the free room is not enough for it.
//
// Refusing them is a denial of service that costs an attacker eight
// connections, and it is not the fairness rule: the queue those requests
// cannot join is not waiting for what they need.
func TestAFullQueueDoesNotRefuseALoadThatNeedsNoEviction(t *testing.T) {
	l := newFakeLauncher()
	p := newTestPool(t, l, queueVictimModels(), PoolOptions{
		MaxResidentBytes: 1000,
		EvictionGrace:    30 * time.Second,
		MaxEvictionWait:  30 * time.Second,
		MaxLoadWaiters:   1,
	})

	// Resident, idle and protected, leaving 760 bytes free: room for the small
	// model, not for the big one.
	warm(t, p, "org/warm")

	head, cancelHead := context.WithCancel(context.Background())
	defer cancelHead()
	go func() {
		if _, rel, err := p.Acquire(head, "org/big"); err == nil {
			rel()
		}
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the big model's request never joined the queue")

	t.Run("a model already in memory", func(t *testing.T) {
		_, release, err := p.Acquire(context.Background(), "org/warm")
		if err != nil {
			t.Fatalf("a request for a resident model was refused with the queue full: %v", err)
		}
		release()
	})

	t.Run("a model that fits in memory nobody is using", func(t *testing.T) {
		before := l.precheckCount()
		_, release, err := p.Acquire(context.Background(), "org/small")
		if err != nil {
			t.Fatalf("a request needing no eviction was refused with the queue full: %v", err)
		}
		defer release()
		if ids := residentIDs(p); !slices.Contains(ids, "org/small") {
			t.Errorf("resident = %v, want the model that fitted without evicting anything", ids)
		}
		if got := l.precheckCount() - before; got != 1 {
			t.Errorf("the load ran %d launch prechecks, want exactly the one it needed", got)
		}
	})
}

// The concession a full queue makes is exactly one: load into memory nothing
// is using. It is not permission to take a victim — that would step over the
// waiter at the head, which is the fairness rule the queue exists for — and
// the refusal it does hand out must be cheap. Every arrival past the cap used
// to run the launcher's preconditions, two filesystem calls in production,
// under the pool's one lock, at whatever rate a client can send.
func TestAQueueFullArrivalTakesNoVictimAndCostsNoSyscall(t *testing.T) {
	l := newFakeLauncher()
	const grace = 100 * time.Millisecond
	p := newTestPool(t, l, queueVictimModels(), PoolOptions{
		MaxResidentBytes: 1000,
		EvictionGrace:    grace,
		MaxEvictionWait:  20 * time.Second,
		MaxLoadWaiters:   1,
	})

	// Two idle models past their grace — so they are eviction candidates — and
	// one held open, which never is. 720 bytes of the budget are spoken for.
	warm(t, p, "org/warm")
	warm(t, p, "org/warm2")
	_, hold, err := p.Acquire(context.Background(), "org/held")
	if err != nil {
		t.Fatalf("Acquire(org/held): %v", err)
	}
	defer hold()
	time.Sleep(3 * grace)

	// The big model needs more than both candidates together, so it parks and
	// stays parked.
	head, cancelHead := context.WithCancel(context.Background())
	defer cancelHead()
	go func() {
		if _, rel, err := p.Acquire(head, "org/big"); err == nil {
			rel()
		}
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the big model's request never joined the queue")

	// This one could be served by evicting a candidate, and must not be: the
	// queue it cannot join is waiting for that memory.
	before := l.precheckCount()
	for range 20 {
		if _, release, err := p.Acquire(context.Background(), "org/mid"); err == nil {
			release()
			t.Fatal("a request past the queue cap took a victim the waiter at the head was owed")
		}
	}
	if ids := residentIDs(p); !slices.Contains(ids, "org/warm") || !slices.Contains(ids, "org/warm2") {
		t.Errorf("resident = %v, want both candidates still in memory", ids)
	}
	if got := l.precheckCount() - before; got != 0 {
		t.Errorf("20 refusals past the queue cap cost %d launch prechecks under the "+
			"pool's lock, want none", got)
	}
}

// Criterion 7 with more than one waiter. A budget raise makes room without
// anything having to finish, and every request the raise fits must be served
// on it — in queue order, but without waiting for a model to fall idle or a
// request to end, which is what the criterion rules out.
func TestRaisingTheMemoryBudgetServesEveryWaiterItFits(t *testing.T) {
	l := newFakeLauncher()
	models := map[string]int64{"org/warm": 200}
	for i := range 4 {
		models[fmt.Sprintf("org/w%d", i)] = 200 // charged 240 each
	}
	p := newTestPool(t, l, &fakeSource{models: models}, PoolOptions{
		MaxResidentBytes: 250, // one model
		EvictionGrace:    30 * time.Second,
		MaxEvictionWait:  30 * time.Second,
	})

	warm(t, p, "org/warm")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errs := make(chan error, 4)
	held := make(chan func(), 4)
	for i := range 4 {
		go func(i int) {
			up, release, err := p.Acquire(ctx, fmt.Sprintf("org/w%d", i))
			if err == nil {
				// Held, not released: a waiter that let go at once would free
				// the room the next one needs, and then this would pass on a
				// build that served them one model at a time.
				held <- release
				_ = up
			}
			errs <- err
		}(i)
	}
	waitUntil(t, 5*time.Second, func() bool { return p.Waiting() == 4 },
		"the requests never all joined the queue")

	// Room for the resident model and all four waiters at once.
	p.SetMemoryBudget(5000)

	for i := range 4 {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatalf("a waiting request was not served by a budget raise that fits it: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of the 4 waiting requests were served by the budget raise", i)
		}
	}
	for range 4 {
		(<-held)()
	}
	if ids := residentIDs(p); len(ids) != 5 {
		t.Errorf("resident = %v, want the warm model and all four that were waiting", ids)
	}
}
