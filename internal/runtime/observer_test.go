package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// recordingObserver collects what the pool reports. It is deliberately its own
// small type rather than a channel: the pool reports off its own lock and off
// the caller's goroutine, so a test has to wait for a report rather than
// expect it to have happened by the time Acquire returned.
type recordingObserver struct {
	mu       sync.Mutex
	starts   []string
	finishes []loadReport
	stops    []stopReport
	// block, when set, is waited on inside every callback, standing in for an
	// observer that has gone slow.
	block chan struct{}
}

type loadReport struct {
	model  string
	took   time.Duration
	failed bool
}

type stopReport struct {
	model  string
	reason StopReason
}

func (o *recordingObserver) wait() {
	if o.block != nil {
		<-o.block
	}
}

func (o *recordingObserver) LoadStarted(model string) {
	o.wait()
	o.mu.Lock()
	defer o.mu.Unlock()
	o.starts = append(o.starts, model)
}

func (o *recordingObserver) LoadFinished(model string, took time.Duration, err error) {
	o.wait()
	o.mu.Lock()
	defer o.mu.Unlock()
	o.finishes = append(o.finishes, loadReport{model: model, took: took, failed: err != nil})
}

func (o *recordingObserver) EntryStopped(model string, reason StopReason) {
	o.wait()
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stops = append(o.stops, stopReport{model: model, reason: reason})
}

func (o *recordingObserver) stopReasons() []stopReport {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]stopReport(nil), o.stops...)
}

func (o *recordingObserver) loads() []loadReport {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]loadReport(nil), o.finishes...)
}

// awaitStop blocks until the observer has been told of a removal of repoID,
// and returns its reason.
func awaitStop(t *testing.T, o *recordingObserver, repoID string) StopReason {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range o.stopReasons() {
			if s.model == repoID {
				return s.reason
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the observer was never told that %s left the pool", repoID)
	return ""
}

func awaitLoad(t *testing.T, o *recordingObserver, repoID string) loadReport {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, l := range o.loads() {
			if l.model == repoID {
				return l
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the observer was never told that %s finished loading", repoID)
	return loadReport{}
}

// A model taken out to make room for another is an eviction. Nothing else the
// pool does to an entry is one — and the pool does six other things to an
// entry — so each removal says which it was.
func TestThePoolReportsWhyAnEntryLeft(t *testing.T) {
	t.Run("evicted to make room", func(t *testing.T) {
		obs := &recordingObserver{}
		l := newFakeLauncher()
		src := &fakeSource{models: map[string]int64{"org/a": 100, "org/b": 100}}
		// loadCost is 1.2x, so a 200-byte budget fits exactly one model.
		p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 200, Observer: obs})

		_, relA, err := p.Acquire(context.Background(), "org/a")
		if err != nil {
			t.Fatal(err)
		}
		relA()
		_, relB, err := p.Acquire(context.Background(), "org/b")
		if err != nil {
			t.Fatal(err)
		}
		defer relB()

		if got := awaitStop(t, obs, "org/a"); got != StopEvicted {
			t.Errorf("org/a left the pool as %q, want %q", got, StopEvicted)
		}
	})

	t.Run("unloaded by the operator", func(t *testing.T) {
		obs := &recordingObserver{}
		l := newFakeLauncher()
		src := &fakeSource{models: map[string]int64{"org/a": 100}}
		p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30, Observer: obs})

		_, rel, err := p.Acquire(context.Background(), "org/a")
		if err != nil {
			t.Fatal(err)
		}
		rel()
		if err := p.Unload("org/a"); err != nil {
			t.Fatal(err)
		}
		if got := awaitStop(t, obs, "org/a"); got != StopUnloaded {
			t.Errorf("an operator's unload was reported as %q, want %q", got, StopUnloaded)
		}
	})

	t.Run("reaped when idle", func(t *testing.T) {
		obs := &recordingObserver{}
		l := newFakeLauncher()
		src := &fakeSource{models: map[string]int64{"org/a": 100}}
		p := newTestPool(t, l, src, PoolOptions{
			MaxResidentBytes: 1 << 30,
			IdleTimeout:      40 * time.Millisecond,
			Observer:         obs,
		})

		_, rel, err := p.Acquire(context.Background(), "org/a")
		if err != nil {
			t.Fatal(err)
		}
		rel()
		if got := awaitStop(t, obs, "org/a"); got != StopIdle {
			t.Errorf("an idle reap was reported as %q, want %q", got, StopIdle)
		}
	})
}

// A load is reported when it starts and when it ends, with how long it took
// and whether it worked, so a slow first request has an explanation beside it.
func TestThePoolReportsLoads(t *testing.T) {
	obs := &recordingObserver{}
	l := newFakeLauncher()
	l.loadDelay = 60 * time.Millisecond
	src := &fakeSource{models: map[string]int64{"org/a": 100}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30, Observer: obs})

	_, rel, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatal(err)
	}
	defer rel()

	got := awaitLoad(t, obs, "org/a")
	if got.failed {
		t.Error("a load that succeeded was reported as a failure")
	}
	if got.took < 60*time.Millisecond {
		t.Errorf("the load was reported as taking %v, want at least the 60ms it waited", got.took)
	}
	// Polled rather than read: the pool promises the reports, not the order
	// they arrive in, and a test that assumes an ordering the interface
	// refuses to promise is a test that will fail for the wrong reason.
	awaitStarts(t, obs, 1)
}

// awaitStarts blocks until the observer has been told of n load starts, and
// fails if a different number arrives.
func awaitStarts(t *testing.T, o *recordingObserver, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		o.mu.Lock()
		got := len(o.starts)
		o.mu.Unlock()
		if got == n {
			return
		}
		if got > n || time.Now().After(deadline) {
			t.Fatalf("the observer was told of %d load starts, want %d", got, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A load that failed is reported as one, and the entry that never became ready
// leaves the pool as a failed load rather than as an eviction.
func TestAFailedLoadIsReportedAsOne(t *testing.T) {
	obs := &recordingObserver{}
	l := newFakeLauncher()
	l.dieAfter = map[string]bool{"org/broken": true}
	src := &fakeSource{models: map[string]int64{"org/broken": 100}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30, Observer: obs, ReadyTimeout: 2 * time.Second})

	if _, _, err := p.Acquire(context.Background(), "org/broken"); err == nil {
		t.Fatal("a model whose process died during startup was acquired successfully")
	}
	if got := awaitLoad(t, obs, "org/broken"); !got.failed {
		t.Error("a load that never became ready was reported as a success")
	}
	if got := awaitStop(t, obs, "org/broken"); got != StopLoadFailed {
		t.Errorf("the failed entry left the pool as %q, want %q", got, StopLoadFailed)
	}
}

// A model server that starts and then never answers is a different failure
// from one that could not be started at all: the first is worth waiting
// through once more, the second is not. The caller can tell them apart.
func TestAReadinessFailureIsItsOwnKindOfError(t *testing.T) {
	l := newFakeLauncher()
	l.dieAfter = map[string]bool{"org/broken": true}
	src := &fakeSource{models: map[string]int64{"org/broken": 100}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30, ReadyTimeout: 2 * time.Second})

	_, _, err := p.Acquire(context.Background(), "org/broken")
	if err == nil {
		t.Fatal("a model whose process died during startup was acquired successfully")
	}
	var notReady *NotReadyError
	if !errors.As(err, &notReady) {
		t.Fatalf("Acquire returned %v, which is not a readiness failure", err)
	}
	var launch *LaunchError
	if errors.As(err, &launch) {
		t.Error("a readiness failure also reports itself as a launch failure; the two are different")
	}

	l2 := newFakeLauncher()
	l2.failFor = "org/nolaunch"
	src2 := &fakeSource{models: map[string]int64{"org/nolaunch": 100}}
	p2 := newTestPool(t, l2, src2, PoolOptions{MaxResidentBytes: 1 << 30})
	_, _, err = p2.Acquire(context.Background(), "org/nolaunch")
	if err == nil {
		t.Fatal("a launch that failed was acquired successfully")
	}
	if errors.As(err, &notReady) {
		t.Error("a launch failure also reports itself as a readiness failure; the two are different")
	}
}

// The waits a request incurs are two different things and a caller cannot tell
// them apart from one duration: waiting for a model to load is the price of a
// cold model, waiting for a slot is the price of a busy one.
func TestAcquireSeparatesTheLoadWaitFromTheQueueWait(t *testing.T) {
	l := newFakeLauncher()
	l.loadDelay = 80 * time.Millisecond
	src := &fakeSource{models: map[string]int64{"org/m": 1 << 20}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30, DecodeConcurrency: 1})
	// cap(sem) = 2 * DecodeConcurrency = 2.

	up, rel1, err := p.Acquire(context.Background(), "org/m")
	if err != nil {
		t.Fatal(err)
	}
	if up.Waits.LoadWait < 80*time.Millisecond {
		t.Errorf("the first request waited %v for the model to load, want at least 80ms", up.Waits.LoadWait)
	}
	if up.Waits.QueueWait > 40*time.Millisecond {
		t.Errorf("the first request is reported as queueing for %v; nothing was ahead of it", up.Waits.QueueWait)
	}

	_, rel2, err := p.Acquire(context.Background(), "org/m")
	if err != nil {
		t.Fatal(err)
	}

	// Both slots are taken; the third request queues until one frees.
	type result struct {
		up  *Upstream
		rel func()
		err error
	}
	done := make(chan result, 1)
	go func() {
		u, rel, err := p.Acquire(context.Background(), "org/m")
		done <- result{u, rel, err}
	}()
	waitInFlight(t, p, "org/m", 3)
	time.Sleep(60 * time.Millisecond)
	rel1()

	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	defer got.rel()
	defer rel2()
	if got.up.Waits.QueueWait < 50*time.Millisecond {
		t.Errorf("the queued request is reported as waiting %v for a slot, want at least the 60ms it waited",
			got.up.Waits.QueueWait)
	}
	if got.up.Waits.LoadWait > 40*time.Millisecond {
		t.Errorf("the queued request is reported as waiting %v for a load; the model was already loaded",
			got.up.Waits.LoadWait)
	}
}

// The observer is a bystander. One that has gone slow, or that panics, must
// not stall a request or take the server down with it — it is told about the
// pool, it does not get a say in it.
func TestASlowOrPanickingObserverDoesNotStallTheAcquire(t *testing.T) {
	obs := &recordingObserver{block: make(chan struct{})}
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{"org/a": 100}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30, Observer: obs})

	done := make(chan error, 1)
	go func() {
		_, rel, err := p.Acquire(context.Background(), "org/a")
		if rel != nil {
			rel()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("an observer that has gone slow blocked the acquire")
	}
	close(obs.block)

	panicking := panickingObserver{}
	p2 := newTestPool(t, newFakeLauncher(), &fakeSource{models: map[string]int64{"org/a": 100}},
		PoolOptions{MaxResidentBytes: 1 << 30, Observer: panicking})
	_, rel, err := p2.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatal(err)
	}
	rel()
	if err := p2.Unload("org/a"); err != nil {
		t.Fatal(err)
	}
	// Reaching here at all is the assertion: a panic in a callback would have
	// taken the process, and this test with it.
	time.Sleep(50 * time.Millisecond)
}

type panickingObserver struct{}

func (panickingObserver) LoadStarted(string)                        { panic("observer panic") }
func (panickingObserver) LoadFinished(string, time.Duration, error) { panic("observer panic") }
func (panickingObserver) EntryStopped(string, StopReason)           { panic("observer panic") }
