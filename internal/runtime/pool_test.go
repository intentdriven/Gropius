package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/mlxtest"
)

// fakeSource resolves models without touching the filesystem.
type fakeSource struct {
	mu     sync.Mutex
	models map[string]int64 // repoID -> size
}

func (s *fakeSource) Resolve(repoID string) (string, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	size, ok := s.models[repoID]
	if !ok {
		return "", 0, fmt.Errorf("%s is not downloaded", repoID)
	}
	return "/models/" + repoID, size, nil
}

// fakeProc is a Process backed by an in-process fake mlx server.
type fakeProc struct {
	srv     *mlxtest.Server
	done    chan struct{}
	stopped chan struct{}
	once    sync.Once
	err     error
}

func (p *fakeProc) Done() <-chan struct{} { return p.done }
func (p *fakeProc) Err() error            { return p.err }
func (p *fakeProc) Pid() int              { return 4242 }
func (p *fakeProc) Stop(ctx context.Context) error {
	p.once.Do(func() {
		p.srv.Close()
		close(p.done)
		close(p.stopped)
	})
	return nil
}

// fakeLauncher stands up a fake mlx server per model, and records launches.
type fakeLauncher struct {
	loadDelay time.Duration
	// failFor makes Launch fail for a repo.
	failFor string
	// failPrecheckFor makes Precheck fail for a repo, simulating a missing venv
	// or a model directory that vanished before Launch runs.
	failPrecheckFor string
	// dieAfter makes the process exit on its own shortly after launch, as a
	// real model server does when the weights are corrupt.
	dieAfter map[string]bool

	mu       sync.Mutex
	launched []string
	// specs records the last Spec each model was launched with, so a test can
	// see what the process would have been given on its command line.
	specs map[string]Spec
	procs map[string]*fakeProc
	// servers maps repoID -> the fake server, so tests can inspect requests.
	servers map[string]*mlxtest.Server
}

func newFakeLauncher() *fakeLauncher {
	return &fakeLauncher{
		specs:    map[string]Spec{},
		procs:    map[string]*fakeProc{},
		servers:  map[string]*mlxtest.Server{},
		dieAfter: map[string]bool{},
	}
}

func (l *fakeLauncher) Precheck(spec Spec) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failPrecheckFor == spec.RepoID {
		return errors.New("simulated precheck failure")
	}
	return nil
}

func (l *fakeLauncher) Launch(ctx context.Context, spec Spec) (Process, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.failFor == spec.RepoID {
		return nil, errors.New("simulated launch failure")
	}

	loadDelay := l.loadDelay
	if l.dieAfter[spec.RepoID] {
		// A process that dies during startup never finished loading, so it must
		// never answer a completion successfully. Keep it "loading" forever; it
		// will be killed below before it could ever become ready.
		loadDelay = time.Hour
	}
	srv := mlxtest.Start(mlxtest.Options{
		ModelArg:  spec.ModelPath,
		LoadDelay: loadDelay,
	})
	// The pool addresses the server by port, so the fake must answer there. We
	// cheat by rewriting the pool's expected port to the httptest port via a
	// custom HTTP client in the tests below.
	p := &fakeProc{srv: srv, done: make(chan struct{}), stopped: make(chan struct{})}

	l.launched = append(l.launched, spec.RepoID)
	l.specs[spec.RepoID] = spec
	l.procs[spec.RepoID] = p
	l.servers[spec.RepoID] = srv

	if l.dieAfter[spec.RepoID] {
		p.err = errors.New("exit status 1")
		go func() {
			time.Sleep(20 * time.Millisecond)
			p.once.Do(func() { srv.Close(); close(p.done); close(p.stopped) })
		}()
	}
	return p, nil
}

func (l *fakeLauncher) launchCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.launched)
}

func (l *fakeLauncher) launchedRepos() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.launched...)
}

func (l *fakeLauncher) specFor(repoID string) Spec {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.specs[repoID]
}

func (l *fakeLauncher) serverFor(repoID string) *mlxtest.Server {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.servers[repoID]
}

func (l *fakeLauncher) procFor(repoID string) *fakeProc {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.procs[repoID]
}

// portRewriter routes the pool's http://127.0.0.1:<allocated-port> probes to
// whichever httptest server the fake launcher actually stood up.
type portRewriter struct {
	l *fakeLauncher
}

func (rt *portRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.l.mu.Lock()
	var target string
	// The readiness probe names the model in its body; match on the model path
	// embedded in the URL is not possible, so route by the single running server
	// whose ModelArg matches. Simplest correct approach: try each server.
	servers := make([]*mlxtest.Server, 0, len(rt.l.servers))
	for _, s := range rt.l.servers {
		servers = append(servers, s)
	}
	rt.l.mu.Unlock()

	// Route to the server whose ModelArg matches the request's model field.
	body, err := readAndRestore(req)
	if err != nil {
		return nil, err
	}
	for _, s := range servers {
		if strings.Contains(body, `"model":"`+s.ModelArg+`"`) ||
			strings.Contains(body, `"model": "`+s.ModelArg+`"`) {
			target = s.URL()
			break
		}
	}
	if target == "" && len(servers) > 0 {
		target = servers[0].URL()
	}
	if target == "" {
		return nil, errors.New("no fake server running")
	}

	u := target + req.URL.Path
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, u, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	return http.DefaultTransport.RoundTrip(newReq)
}

func newTestPool(t *testing.T, l *fakeLauncher, src *fakeSource, opts PoolOptions) *Pool {
	t.Helper()
	opts.Launcher = l
	opts.Models = src
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{
			Timeout:   5 * time.Second,
			Transport: &portRewriter{l: l},
		}
	}
	if opts.ReadyTimeout == 0 {
		opts.ReadyTimeout = 5 * time.Second
	}
	p := NewPool(opts)
	t.Cleanup(func() { p.Close() })
	return p
}

func TestAcquireLaunchesAndReturnsReadyUpstream(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{"org/m": 1 << 20}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30})

	up, release, err := p.Acquire(context.Background(), "org/m")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()

	if up.RepoID != "org/m" {
		t.Errorf("RepoID = %q", up.RepoID)
	}
	// The ModelArg must be the backend's --model value, not the friendly name:
	// mlx-lm would otherwise try to download a repo called "org/m".
	if up.ModelArg != "/models/org/m" {
		t.Errorf("ModelArg = %q, want the backend --model path", up.ModelArg)
	}
	if l.launchCount() != 1 {
		t.Errorf("launched %d processes, want 1", l.launchCount())
	}
}

// The per-model semaphore bounds in-flight requests to 2x DecodeConcurrency, so a
// burst cannot swamp one mlx-lm server's memory. Past the cap, Acquire blocks
// until a slot frees rather than admitting the request.
func TestAcquireBoundsPerModelConcurrency(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{"org/m": 1 << 20}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30, DecodeConcurrency: 1})
	// cap = 2 * DecodeConcurrency = 2.

	r1, rel1, err := p.Acquire(context.Background(), "org/m")
	if err != nil {
		t.Fatal(err)
	}
	_ = r1
	_, rel2, err := p.Acquire(context.Background(), "org/m")
	if err != nil {
		t.Fatal(err)
	}

	// The third acquire must block (both slots are held). Its context cancels first.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, rel3, err := p.Acquire(ctx, "org/m")
	if err == nil {
		rel3()
		t.Fatal("third concurrent Acquire should have blocked past the cap, but succeeded immediately")
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Errorf("third Acquire returned after %v, expected it to block until its context expired", time.Since(start))
	}

	// Freeing a slot lets a new acquire through promptly.
	rel1()
	r4, rel4, err := p.Acquire(context.Background(), "org/m")
	if err != nil {
		t.Fatalf("Acquire after freeing a slot: %v", err)
	}
	_ = r4
	rel4()
	rel2()
}

// Past cap(sem)+MaxQueueDepth, Acquire must fail fast with ErrBusy rather than
// letting an unbounded backlog of queued requests pin the model (each waiter
// counts as in-flight, blocking eviction) and pile up goroutines.
func TestAcquireRejectsBeyondQueueDepth(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{"org/m": 1 << 20}}
	// cap(sem) = 2*DecodeConcurrency = 2; MaxQueueDepth = 2; so maxInFlight = 4.
	p := newTestPool(t, l, src, PoolOptions{
		MaxResidentBytes: 1 << 30, DecodeConcurrency: 1, MaxQueueDepth: 2,
	})

	// Two active slots, held for the duration.
	_, rel1, err := p.Acquire(context.Background(), "org/m")
	if err != nil {
		t.Fatal(err)
	}
	_, rel2, err := p.Acquire(context.Background(), "org/m")
	if err != nil {
		t.Fatal(err)
	}

	// Two more queue on the semaphore: each increments in-flight, then blocks
	// waiting for a slot. They fill the allowed queue depth.
	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()
	for i := 0; i < 2; i++ {
		go func() {
			_, rel, err := p.Acquire(bgCtx, "org/m")
			if err == nil {
				defer rel()
				<-bgCtx.Done()
			}
		}()
	}
	waitInFlight(t, p, "org/m", 4)

	// The fifth request is past cap(sem)+MaxQueueDepth and must be rejected at once.
	start := time.Now()
	_, rel5, err := p.Acquire(context.Background(), "org/m")
	if err == nil {
		rel5()
		t.Fatal("Acquire past the queue-depth cap should be rejected, but succeeded")
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy past the cap, got %v", err)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Errorf("rejection took %v — it should fail fast, not block", time.Since(start))
	}

	rel1()
	rel2()
}

// waitInFlight blocks until the pool reports exactly n in-flight requests for
// repoID, or fails the test.
func waitInFlight(t *testing.T, p *Pool, repoID string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, r := range p.Resident() {
			if r.RepoID == repoID && r.InFlight == n {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for in-flight==%d on %s", n, repoID)
}

func TestAcquireReusesRunningModel(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{"org/m": 1 << 20}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30})

	for i := 0; i < 3; i++ {
		_, release, err := p.Acquire(context.Background(), "org/m")
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	if l.launchCount() != 1 {
		t.Errorf("launched %d processes for 3 acquires; the model should be reused", l.launchCount())
	}
}

// Loading a model is slow. Concurrent callers must share one load, not each
// start their own process.
func TestConcurrentAcquireLoadsModelOnlyOnce(t *testing.T) {
	l := newFakeLauncher()
	l.loadDelay = 150 * time.Millisecond
	src := &fakeSource{models: map[string]int64{"org/m": 1 << 20}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30})

	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, release, err := p.Acquire(context.Background(), "org/m")
			if err != nil {
				errs <- err
				return
			}
			release()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("Acquire: %v", err)
	}

	if got := l.launchCount(); got != 1 {
		t.Errorf("launched %d processes for 10 concurrent acquires, want 1", got)
	}
}

func TestAcquireUnknownModelFails(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30})

	if _, _, err := p.Acquire(context.Background(), "org/missing"); err == nil {
		t.Fatal("expected an error for a model that is not downloaded")
	}
}

// A model that cannot load (corrupt weights, missing dep) must surface an error
// promptly instead of hanging every client until the readiness timeout.
func TestProcessThatDiesDuringStartupReportsError(t *testing.T) {
	l := newFakeLauncher()
	l.dieAfter["org/broken"] = true
	src := &fakeSource{models: map[string]int64{"org/broken": 1 << 20}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30, ReadyTimeout: 5 * time.Second})

	start := time.Now()
	_, _, err := p.Acquire(context.Background(), "org/broken")
	if err == nil {
		t.Fatal("expected an error when the model server exits during startup")
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %s to notice a dead process; it should fail fast, not wait for the readiness timeout", time.Since(start))
	}
	if !strings.Contains(err.Error(), "exited") {
		t.Errorf("error should say the server exited, got: %v", err)
	}
}

// A failed model must not stay in the pool consuming the memory budget.
func TestFailedModelIsRemovedFromPool(t *testing.T) {
	l := newFakeLauncher()
	l.dieAfter["org/broken"] = true
	src := &fakeSource{models: map[string]int64{"org/broken": 1 << 20}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30, ReadyTimeout: 5 * time.Second})

	p.Acquire(context.Background(), "org/broken")

	if len(p.Resident()) != 0 {
		t.Errorf("a model that failed to load is still resident: %v", p.Resident())
	}
}

// A model server that exits after becoming ready (a crash mid-serving: GPU
// memory exhaustion, a Python fault, a manual kill) must be removed from the
// pool. Otherwise Acquire keeps returning the dead port — every request a 502,
// with nothing to reap the corpse under the default idle timeout of zero —
// while it still counts against the memory budget.
func TestServerThatDiesAfterReadyIsRemovedAndRelaunched(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{"org/m": 1 << 20}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30})

	_, release, err := p.Acquire(context.Background(), "org/m")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()

	// The server crashes on its own, after it already served requests.
	proc := l.procFor("org/m")
	proc.once.Do(func() { proc.srv.Close(); close(proc.done); close(proc.stopped) })

	// The pool must notice the exit and drop the dead entry.
	deadline := time.Now().Add(5 * time.Second)
	for len(p.Resident()) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("dead model server still resident: %v", p.Resident())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The next Acquire must relaunch rather than route to the corpse.
	_, release2, err := p.Acquire(context.Background(), "org/m")
	if err != nil {
		t.Fatalf("Acquire after crash: %v", err)
	}
	defer release2()
	if got := l.launchCount(); got != 2 {
		t.Errorf("launched %d processes, want 2 (a relaunch after the crash)", got)
	}
}

func TestLaunchFailureIsReported(t *testing.T) {
	l := newFakeLauncher()
	l.failFor = "org/m"
	src := &fakeSource{models: map[string]int64{"org/m": 1 << 20}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30})

	if _, _, err := p.Acquire(context.Background(), "org/m"); err == nil {
		t.Fatal("expected the launch failure to surface")
	}
}

// The memory budget is the whole point of the pool: loading a second model that
// does not fit must evict the first rather than OOM the machine.
func TestEvictsLRUModelWhenBudgetExceeded(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{
		"org/a": 100,
		"org/b": 100,
	}}
	// loadCost is 1.2x, so 100 bytes costs 120. A 200-byte budget fits exactly one.
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 200})

	_, relA, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatal(err)
	}
	relA() // no longer in flight, so it is evictable

	_, relB, err := p.Acquire(context.Background(), "org/b")
	if err != nil {
		t.Fatalf("Acquire b: %v", err)
	}
	defer relB()

	res := p.Resident()
	if len(res) != 1 {
		t.Fatalf("expected exactly 1 resident model after eviction, got %d: %v", len(res), res)
	}
	if res[0].RepoID != "org/b" {
		t.Errorf("resident model = %q, want org/b (org/a should have been evicted)", res[0].RepoID)
	}
}

// A launch that was never going to succeed (missing venv, a model directory
// that vanished in a race with a concurrent delete) must not destroy an
// unrelated, healthy resident model on its way to failing: eviction is not
// reversible, so evicting before Launch's own preconditions are checked
// tears down the victim for zero benefit the moment the new load fails.
func TestFailedLaunchPreconditionDoesNotEvictAnUnrelatedModel(t *testing.T) {
	l := newFakeLauncher()
	l.failPrecheckFor = "org/b"
	src := &fakeSource{models: map[string]int64{
		"org/a": 100,
		"org/b": 100,
	}}
	// loadCost is 1.2x, so 100 bytes costs 120. A 200-byte budget fits exactly
	// one model at a time, so loading org/b would otherwise have to evict org/a.
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 200})

	_, relA, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatal(err)
	}
	relA() // no longer in flight, so it would be evictable

	if _, _, err := p.Acquire(context.Background(), "org/b"); err == nil {
		t.Fatal("expected the launch precondition failure to surface")
	}

	res := p.Resident()
	if len(res) != 1 || res[0].RepoID != "org/a" {
		t.Fatalf("org/a should still be resident after org/b's launch precondition failed, got %v", res)
	}
}

// A model still loading, with a caller genuinely still waiting on it, must
// never be picked as an eviction victim by a concurrent Acquire for a
// different model — that would waste the in-progress load and error the
// waiter still counting on it.
func TestLoadingModelWithAnActiveWaiterIsNotEvicted(t *testing.T) {
	l := newFakeLauncher()
	l.loadDelay = 200 * time.Millisecond
	src := &fakeSource{models: map[string]int64{"org/a": 100, "org/b": 100}}
	// loadCost is 1.2x, so 100 bytes costs 120. A 200-byte budget fits exactly one.
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 200})

	// org/a's caller keeps waiting for the whole (slow, simulated) load.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, rel, err := p.Acquire(context.Background(), "org/a")
		if err != nil {
			t.Errorf("Acquire org/a: %v", err)
			return
		}
		rel()
	}()

	// Give org/a's load a moment to start before org/b competes for the same
	// (single-model) budget.
	time.Sleep(20 * time.Millisecond)
	_, _, err := p.Acquire(context.Background(), "org/b")
	if err == nil {
		t.Fatal("expected an error: org/a is not a legal eviction victim while it has an active waiter")
	}
	if !strings.Contains(err.Error(), "memory") {
		t.Errorf("error should explain the memory pressure, got: %v", err)
	}

	// org/a's process must never have been torn down mid-load.
	proc := l.procFor("org/a")
	select {
	case <-proc.stopped:
		t.Fatal("the still-loading model was evicted and killed mid-load")
	default:
	}

	<-done
	res := p.Resident()
	if len(res) != 1 || res[0].RepoID != "org/a" {
		t.Errorf("org/a should be resident once its load completes: %v", res)
	}
}

// An abandoned load — its last waiter gave up (context cancelled or timed
// out) while the load keeps running in the background — must be torn down
// promptly, not merely left immune to eviction. Immunity alone would let a
// single dropped request pin a large model's full share of the memory
// budget, unkillable, for up to ReadyTimeout: every other model would then
// fail to load for minutes, from one abandoned connection.
func TestAbandonedLoadIsTornDownPromptly(t *testing.T) {
	l := newFakeLauncher()
	l.loadDelay = 200 * time.Millisecond
	src := &fakeSource{models: map[string]int64{"org/a": 100, "org/b": 100}}
	// loadCost is 1.2x, so 100 bytes costs 120. A 200-byte budget fits exactly one.
	p := newTestPool(t, l, src, PoolOptions{
		MaxResidentBytes: 200,
		ReadyTimeout:     10 * time.Minute, // must not matter: teardown is immediate.
	})

	// org/a's sole caller gives up well before the (slow, simulated) load
	// finishes; nobody is left waiting on it.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, err := p.Acquire(ctx, "org/a")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire org/a: got %v, want context.DeadlineExceeded", err)
	}

	// The abandoned entry must already be gone — not merely evictable in
	// principle — so org/b can claim the budget straight away instead of
	// failing with a memory-pressure error for up to ReadyTimeout.
	_, relB, err := p.Acquire(context.Background(), "org/b")
	if err != nil {
		t.Fatalf("Acquire org/b: %v (the abandoned org/a load should have freed its budget immediately)", err)
	}
	relB()

	res := p.Resident()
	if len(res) != 1 || res[0].RepoID != "org/b" {
		t.Errorf("resident model = %v, want only org/b", res)
	}

	// org/a's process must actually have been stopped, not just forgotten.
	proc := l.procFor("org/a")
	select {
	case <-proc.stopped:
	case <-time.After(time.Second):
		t.Error("org/a's process was never stopped after its load was abandoned")
	}
}

// Eviction must never kill a model that is mid-request.
func TestInFlightModelIsNotEvicted(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{"org/a": 100, "org/b": 100}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 200})

	// Hold org/a in flight — do not release it.
	_, relA, err := p.Acquire(context.Background(), "org/a")
	if err != nil {
		t.Fatal(err)
	}
	defer relA()

	// org/b cannot fit, and the only candidate for eviction is pinned.
	_, _, err = p.Acquire(context.Background(), "org/b")
	if err == nil {
		t.Fatal("expected an error: there is no room and the resident model is in use")
	}
	if !strings.Contains(err.Error(), "memory") {
		t.Errorf("error should explain the memory pressure, got: %v", err)
	}

	// org/a must still be alive and serving.
	res := p.Resident()
	if len(res) != 1 || res[0].RepoID != "org/a" {
		t.Errorf("the in-flight model was evicted: %v", res)
	}
}

func TestModelTooLargeForBudgetIsRejectedClearly(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{"org/huge": 1 << 40}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 20})

	_, _, err := p.Acquire(context.Background(), "org/huge")
	if err == nil {
		t.Fatal("expected an error for a model larger than the whole budget")
	}
	if !strings.Contains(err.Error(), "memory") {
		t.Errorf("error should mention memory, got: %v", err)
	}
	if l.launchCount() != 0 {
		t.Error("a model that cannot possibly fit must not be launched at all")
	}
}

func TestUnloadStopsTheModelServer(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{"org/m": 1 << 20}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30})

	_, release, err := p.Acquire(context.Background(), "org/m")
	if err != nil {
		t.Fatal(err)
	}
	release()

	if err := p.Unload("org/m"); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	if len(p.Resident()) != 0 {
		t.Error("model still resident after Unload")
	}
	if err := p.Unload("org/m"); err == nil {
		t.Error("unloading an already-unloaded model should error")
	}
}

func TestUnloadRefusesWhileRequestInFlight(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{"org/m": 1 << 20}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30})

	_, release, err := p.Acquire(context.Background(), "org/m")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if err := p.Unload("org/m"); err == nil {
		t.Error("Unload should refuse to kill a model that is serving a request")
	}
}

// Idle models should give their memory back.
func TestIdleModelIsUnloaded(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{"org/m": 1 << 20}}
	p := newTestPool(t, l, src, PoolOptions{
		MaxResidentBytes: 1 << 30,
		IdleTimeout:      200 * time.Millisecond,
	})

	_, release, err := p.Acquire(context.Background(), "org/m")
	if err != nil {
		t.Fatal(err)
	}
	release()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(p.Resident()) == 0 {
			return // reaped, as intended
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Error("an idle model was never unloaded; its memory is stranded")
}

func TestCloseStopsEverything(t *testing.T) {
	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{"org/a": 100, "org/b": 100}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30})

	_, relA, _ := p.Acquire(context.Background(), "org/a")
	relA()

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(p.Resident()) != 0 {
		t.Error("models still resident after Close")
	}
	if _, _, err := p.Acquire(context.Background(), "org/a"); !errors.Is(err, ErrClosed) {
		t.Errorf("Acquire after Close should return ErrClosed, got %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close should be idempotent, got %v", err)
	}
}

func TestLoadCostAddsHeadroom(t *testing.T) {
	if got := loadCost(1000); got != 1200 {
		t.Errorf("loadCost(1000) = %d, want 1200 (weights + KV-cache headroom)", got)
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{512, "512 B"},
		{2048, "2.0 KB"},
		{5 << 30, "5.0 GB"},
	}
	for _, tt := range tests {
		if got := humanBytes(tt.in); got != tt.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// The pool is the source of truth for residency, and a model mid-load is
// neither warm nor cold: a caller told "loaded" would send work and wait for
// the load anyway, one told "not loaded" might start a second, competing load.
// Loading means exactly "the entry is in the pool, its readiness probe has not
// answered yet"; the read must not block on that probe.
func TestResidentDistinguishesLoadingFromLoaded(t *testing.T) {
	l := newFakeLauncher()
	l.loadDelay = 300 * time.Millisecond
	src := &fakeSource{models: map[string]int64{"org/m": 1 << 20}}
	p := newTestPool(t, l, src, PoolOptions{MaxResidentBytes: 1 << 30})

	acquired := make(chan struct{})
	go func() {
		defer close(acquired)
		_, release, err := p.Acquire(context.Background(), "org/m")
		if err != nil {
			t.Errorf("Acquire: %v", err)
			return
		}
		release()
	}()

	// The load is under way and the probe has not answered.
	deadline := time.Now().Add(2 * time.Second)
	var loading []Resident
	for time.Now().Before(deadline) {
		start := time.Now()
		loading = p.Resident()
		// A read that waited on the readiness probe would be the bug: the
		// answer is already known without it.
		if took := time.Since(start); took > 100*time.Millisecond {
			t.Fatalf("Resident() blocked for %s during a load", took)
		}
		if len(loading) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(loading) != 1 {
		t.Fatalf("Resident() = %+v during a load, want the loading entry", loading)
	}
	if loading[0].State != ResidencyLoading {
		t.Errorf("State = %q while the model was loading, want %q", loading[0].State, ResidencyLoading)
	}

	<-acquired

	loaded := p.Resident()
	if len(loaded) != 1 {
		t.Fatalf("Resident() = %+v after the load, want one entry", loaded)
	}
	if loaded[0].State != ResidencyLoaded {
		t.Errorf("State = %q once the probe answered, want %q", loaded[0].State, ResidencyLoaded)
	}
}
