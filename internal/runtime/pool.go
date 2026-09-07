package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
)

// ModelSource resolves a repo id to an on-disk model. The registry implements it.
type ModelSource interface {
	// Resolve returns the model's directory and on-disk size.
	Resolve(repoID string) (path string, bytes int64, err error)
}

// Upstream is a ready model server the gateway can proxy to.
type Upstream struct {
	RepoID string
	// BaseURL is the loopback address of the model server.
	BaseURL string
	// ModelArg is the exact string that must appear in the proxied request's
	// "model" field. mlx-lm treats that field as an instruction to *load* a
	// model, so sending the client's friendly name would make the backend try to
	// download a repo by that name from HuggingFace.
	ModelArg string
	// Waits is what this one acquisition spent getting here. An Upstream is
	// built per Acquire rather than shared, so the waits belong on it: the
	// caller that paid them is the caller holding it.
	Waits AcquireStats
}

// AcquireStats separates the two waits an acquisition can incur, which the
// caller cannot tell apart from the single duration it can measure itself.
//
// LoadWait is time spent waiting for the model server to become ready, borne
// by every waiter on that load and not only by the request that triggered it.
// QueueWait is time spent waiting for the machine rather than for the model:
// for room to load it under eviction grace, and for a concurrency slot on a
// model that was already loaded. A request that found its model warm and free
// pays neither.
type AcquireStats struct {
	LoadWait  time.Duration
	QueueWait time.Duration
}

// ResidencyState says how far a model has got towards serving a request
// without a load.
type ResidencyState string

const (
	// ResidencyNotLoaded is a model the pool is not holding at all. The pool
	// never reports it — a model it does not hold is one it has nothing to
	// say about — so it is the value a caller supplies for the models it
	// knows about and the pool does not.
	ResidencyNotLoaded ResidencyState = "not_loaded"
	// ResidencyLoading is a model whose server is up but has not yet answered
	// its readiness probe. A request for it is served, but only after the
	// wait a request for a loaded model does not pay.
	ResidencyLoading ResidencyState = "loading"
	// ResidencyLoaded is a model whose server has answered its readiness
	// probe and can serve a request straight away.
	ResidencyLoaded ResidencyState = "loaded"
)

// Resident describes a model the pool is holding. An entry exists from the
// moment the server process is launched, so State is what separates a model
// that can serve now from one still loading; the control panel shows the rest
// of these fields on loopback and does not read State yet.
type Resident struct {
	RepoID   string         `json:"repo_id"`
	State    ResidencyState `json:"state"`
	Port     int            `json:"port"`
	Bytes    int64          `json:"bytes"`
	LoadedAt time.Time      `json:"loaded_at"`
	LastUsed time.Time      `json:"last_used"`
	InFlight int            `json:"in_flight"`
}

// PoolOptions configures a Pool.
type PoolOptions struct {
	Launcher Launcher
	Models   ModelSource
	// MaxResidentBytes is the pool's memory budget as it starts: the ceiling on
	// the total charged size (LoadCost, 1.2x the size on disk) of the models
	// held at once. Zero or less means the default share of this Mac's memory.
	//
	// The starting value only. SetMemoryBudget replaces it, and the figure the
	// pool enforces after that is the one it holds under p.mu — this field is
	// not updated and must not be read as the budget in force.
	MaxResidentBytes int64
	// IdleTimeout unloads a model after this long without a request. Zero keeps
	// models resident indefinitely.
	IdleTimeout time.Duration
	// DecodeConcurrency is passed to each model server.
	DecodeConcurrency int
	// SamplingFor returns the sampling defaults a model's server should be
	// launched with. It is called when a process is started, not when the pool
	// is built, so a default saved in Settings reaches the next load of every
	// model without the pool being rebuilt — which is also why a change only
	// counts once the model has loaded again. Nil means no defaults.
	SamplingFor func(repoID string) config.Sampling
	// MaxQueueDepth bounds how many requests may wait for one model server
	// beyond the batch it can actively run. Past this, Acquire fails fast rather
	// than letting an unbounded backlog of queued requests pin the model (each
	// waiter counts as in-flight, so it also blocks the model from being evicted)
	// and pile up goroutines and connections. Zero uses a default.
	MaxQueueDepth int
	// EvictionGrace protects a model for this long after it finishes a
	// request: while it is protected it is not chosen as an eviction victim,
	// and a request that needs its memory waits instead of taking it. Zero —
	// the default — switches the whole mechanism off, and a load that finds no
	// room takes a victim at once exactly as it always has.
	//
	// It must not exceed a non-zero IdleTimeout, or the idle reaper unloads the
	// very model a wait is protecting; internal/config holds a save to that.
	// Replaced live with SetEvictionGrace.
	EvictionGrace time.Duration
	// MaxEvictionWait bounds that wait. Past it the request is refused with the
	// same no-room refusal it would have had at once. Zero with a non-zero
	// EvictionGrace means the grace itself.
	MaxEvictionWait time.Duration
	// MaxLoadWaiters bounds how many requests may be waiting for room at once.
	// Past this, Acquire refuses immediately rather than joining them.
	//
	// It is deliberately not MaxQueueDepth. That queue drains at decoding
	// speed; this one drains only when a model is evicted, which is seconds to
	// tens of seconds, and every waiter is a goroutine holding its whole
	// request body in memory meanwhile — twice over, in fact: the gateway
	// holds the bytes it read, up to its 32 MiB limit, and the decoded value
	// copies rather than aliases them. Call it 64 MiB a waiter, so eight of
	// them is about half a gigabyte a client can pin without being admitted to
	// anything, none of it charged against the memory budget, which counts
	// model weights. That is the arithmetic the default is chosen against.
	// Zero uses that default.
	MaxLoadWaiters int
	// ReadyTimeout bounds how long we wait for a model to load. Large models on
	// a cold page cache genuinely take minutes.
	ReadyTimeout time.Duration
	// Pinned lists the repo ids that must stay in memory: a pinned model is
	// never chosen as an eviction victim and is never reaped by IdleTimeout, so
	// a request that would need its memory is refused instead. Ids are matched
	// the way every other repo id is, folded through config.FoldRepoID, so a
	// hand-edited settings file that spells one differently still protects the
	// model it names. Replaced live with SetPinned.
	Pinned []string
	// Log records what the pool did that the requester is not told. The
	// no-room refusal is deliberately generic on the wire — which models the
	// operator protected is not a LAN client's business — so the names go here
	// instead. Nil means slog.Default().
	Log *slog.Logger

	// Observer, when set, is told when a model server loads and when one
	// leaves the pool. Nil (the default) means nobody is watching and every
	// report is a no-op; see PoolObserver for what the pool promises it.
	Observer PoolObserver

	// HTTP is the client used for readiness probes.
	HTTP *http.Client
	// now is injectable for tests.
	now func() time.Time
}

// Pool runs one model server process per model and routes to them.
//
// mlx-lm can switch models within a single process, but doing so *evicts* the
// resident model and reloads from scratch. Behind a multi-client gateway that
// would thrash weights in and out of memory on every alternating request, so the
// pool gives each model its own process and does the routing itself.
type Pool struct {
	opts PoolOptions

	mu      sync.Mutex
	entries map[string]*entry
	closed  bool
	// maxResident is the ceiling on the total charged size of the models in
	// memory. It lives here rather than in opts because it is a setting the
	// operator can change while the pool is running, and every path that reads
	// it — both eviction paths — already holds mu.
	maxResident int64
	// pinned is the protected set: folded repo id -> the spelling it was given
	// under, so the pool can both match a pin and report one. Guarded by mu,
	// the same lock the eviction paths that read it already hold.
	pinned map[string]string
	// grace and maxWait are the eviction-grace intervals in force. They live
	// here rather than in opts for the reason maxResident does: the operator
	// changes them while the pool is running, and every path that reads them
	// holds mu. A zero grace is the feature switched off.
	grace   time.Duration
	maxWait time.Duration
	// waiters are the loads parked for want of room, oldest first. Guarded by
	// mu; a waiter is never parked while holding it. Serving the head first is
	// the whole fairness rule: serving whichever load is quickest would starve
	// a large model under a stream of requests for small ones.
	waiters []*loadWaiter

	stopIdle chan struct{}
	idleDone chan struct{}
}

// entry is one model server, loaded or loading.
type entry struct {
	repoID   string
	port     int
	bytes    int64
	modelArg string
	proc     Process

	loadedAt time.Time
	lastUsed time.Time
	inFlight int

	// sem bounds how many requests run against this one model server at once. Its
	// capacity is a small multiple of the server's --decode-concurrency: mlx-lm
	// batches only that many decodes, and each extra in-flight sequence holds its
	// own KV cache. On a unified-memory Mac an unbounded burst is a direct path to
	// a GPU-memory blowup that crashes the server and every request with it, so
	// excess requests queue on this channel instead.
	sem chan struct{}

	// ready is closed once the model answers a real completion.
	ready    chan struct{}
	readyErr error
}

// loadWaiter is one request parked for want of room for its model.
//
// Its own clock is real time rather than the pool's injectable one: what it
// measures is a promise to a client about wall-clock seconds, reported back to
// that client in a header, and bounded so that no clock a test injects can
// hold a request open. The grace a candidate is judged by is read from the
// pool's clock instead, because that is what stamps lastFinished.
type loadWaiter struct {
	arrived time.Time
	// need is what this load asked for, remembered from the attempt that
	// failed. A parked waiter is judged on it — can this still ever fit, does
	// a plan for it succeed yet — rather than by asking the registry and
	// stat-ing the launcher's files again. Every wake-up wakes every waiter,
	// so without it each completed request would do filesystem work under p.mu
	// once per parked request.
	//
	// It can go stale: the model can be downloaded again at a different size
	// while its request waits. That decides only whether this waiter is woken
	// early or left parked a little longer, never what is admitted — the load
	// itself resolves the model again, so a model that grew is measured at
	// what it grew to.
	need int64
	// signal is buffered so that waking a waiter never blocks the goroutine
	// holding p.mu, and so that a wake-up arriving between two checks is not
	// lost.
	signal chan struct{}
}

// defaultMaxLoadWaiters is the ceiling on parked loads. See
// PoolOptions.MaxLoadWaiters for the arithmetic it is chosen against.
const defaultMaxLoadWaiters = 8

// NewPool creates a pool. Call Close to shut down every model server.
func NewPool(opts PoolOptions) *Pool {
	if opts.HTTP == nil {
		// No client-level timeout: this client is used only by the readiness probe,
		// whose single completion request rides mlx-lm's lazy weight load — which
		// takes minutes for a large model. Each probe request is instead bounded by
		// the ReadyTimeout-scoped context in probeReady. A fixed 30s here would abort
		// mid-load and force wasteful re-probing.
		opts.HTTP = &http.Client{}
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	if opts.MaxResidentBytes <= 0 {
		opts.MaxResidentBytes = defaultResidentBudget()
	}
	if opts.ReadyTimeout == 0 {
		opts.ReadyTimeout = 10 * time.Minute
	}
	if opts.DecodeConcurrency < 1 {
		opts.DecodeConcurrency = 1
	}
	if opts.MaxQueueDepth <= 0 {
		opts.MaxQueueDepth = 64
	}
	opts.EvictionGrace, opts.MaxEvictionWait = normalizeGrace(opts.EvictionGrace, opts.MaxEvictionWait)
	if opts.MaxLoadWaiters <= 0 {
		opts.MaxLoadWaiters = defaultMaxLoadWaiters
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}

	p := &Pool{
		opts:        opts,
		entries:     map[string]*entry{},
		maxResident: opts.MaxResidentBytes,
		pinned:      pinnedSet(opts.Pinned),
		grace:       opts.EvictionGrace,
		maxWait:     opts.MaxEvictionWait,
		stopIdle:    make(chan struct{}),
		idleDone:    make(chan struct{}),
	}
	go p.reapIdle()
	return p
}

// SetPinned replaces the set of models protected from eviction and from the
// idle reaper. It is the seam a settings save uses, so a pin takes effect on
// the models already loaded rather than at the next restart.
//
// It takes p.mu, the lock both eviction paths already hold while they read the
// set, so a pin never lands half-applied between the victim search and the
// stop that follows it.
func (p *Pool) SetPinned(ids []string) {
	set := pinnedSet(ids)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pinned = set
	// A pin changes the answer to "can this load ever fit", in both
	// directions, so a request already waiting for room is re-judged now
	// rather than at the next release.
	p.wakeWaitersLocked()
}

// SetMemoryBudget replaces the ceiling on the total charged size of resident
// models. It is the seam a settings save uses, so a budget the operator
// changes governs the next load rather than the next restart.
//
// It unloads nothing. Both eviction paths read the figure under p.mu, the lock
// this takes, so the next load is measured against the new budget while the
// models already in memory are left alone — the machine sits over a lowered
// budget until they unload by the usual rules. Evicting here would pull a
// model out from under the operator at the moment they pressed Save, which is
// the one moment they were not asking for it.
//
// Zero means the default share of this Mac's memory, the same as it does in
// PoolOptions, so that a budget cleared in Settings needs no second spelling.
func (p *Pool) SetMemoryBudget(n int64) {
	if n <= 0 {
		n = defaultResidentBudget()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.maxResident = n
	// A raise can make room with nothing having to finish, so a request
	// already waiting acts on it at once rather than at the next release.
	p.wakeWaitersLocked()
}

// MemoryBudget is the ceiling on the total charged size of resident models.
//
// It is read rather than a value the caller already has because the pool is
// where the default (a share of physical RAM) is resolved, and the check that
// a pinned set fits has to be against the figure eviction actually uses.
func (p *Pool) MemoryBudget() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxResident
}

// SetEvictionGrace replaces the two eviction-grace intervals. It is the seam a
// settings save uses, so switching grace on or off governs the requests being
// served rather than the ones after a restart.
//
// A zero grace switches the mechanism off, which is the operator saying "swap
// now": every request already waiting is woken and takes its victim rather
// than sitting out a grace nobody wants any more.
func (p *Pool) SetEvictionGrace(grace, maxWait time.Duration) {
	grace, maxWait = normalizeGrace(grace, maxWait)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.grace, p.maxWait = grace, maxWait
	p.wakeWaitersLocked()
}

// EvictionGrace is the pair of intervals in force: how long a model is
// protected after it finishes work, and the longest a request will wait for
// one to fall past that. A zero grace is the mechanism switched off.
//
// It is read from the pool rather than from the stored settings for the reason
// Pinned is: this is what is actually being enforced, and a save reaches the
// two by different paths.
func (p *Pool) EvictionGrace() (grace, maxWait time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.grace, p.maxWait
}

// normalizeGrace resolves the two intervals the one way, so that a pool built
// with PoolOptions and a pool told to change while it runs cannot disagree
// about what a zero means. A negative grace is no grace; a maximum wait of
// zero beside a real grace is the grace itself, since a wait shorter than the
// protection could only ever be served by a model that was already idle.
func normalizeGrace(grace, maxWait time.Duration) (time.Duration, time.Duration) {
	if grace < 0 {
		grace = 0
	}
	if maxWait <= 0 {
		maxWait = grace
	}
	return grace, maxWait
}

// Waiting is how many requests are parked for want of room right now.
//
// It reports the queue rather than the entries, which is what Resident cannot:
// a waiter holds no model and so appears nowhere in that list, and "nothing is
// loading and nothing is refused" is otherwise indistinguishable from "six
// clients are queued behind a model that will not fall idle".
func (p *Pool) Waiting() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.waiters)
}

// wakeWaitersLocked prods every parked load to look again. Callers must hold
// p.mu.
//
// Every wake-up is a prod rather than a decision: the waiter re-reads the pool
// under the lock when it comes back, so it never acts on the view that woke
// it. The sends cannot block — each channel is buffered and the send is
// non-blocking — which is what makes this safe to call from under p.mu.
func (p *Pool) wakeWaitersLocked() {
	for _, w := range p.waiters {
		select {
		case w.signal <- struct{}{}:
		default:
		}
	}
}

// leaveQueueLocked removes a waiter that is no longer waiting. Callers must
// hold p.mu.
func (p *Pool) leaveQueueLocked(w *loadWaiter) {
	if w == nil {
		return
	}
	for i, other := range p.waiters {
		if other == w {
			p.waiters = append(p.waiters[:i], p.waiters[i+1:]...)
			break
		}
	}
	// Whoever is now at the head may take a victim it could not take while
	// somebody older stood in front of it.
	p.wakeWaitersLocked()
}

// Pinned lists the protected models, in the spelling they were pinned under.
//
// It answers from the pool rather than from the stored settings on purpose:
// this is the set actually being enforced, which is what the models list is
// reporting on. It is independent of what is loaded — a pinned model the pool
// is not holding is still pinned — which is why the pinned field cannot come
// from a Resident record, one of which exists only for a model in memory.
//
// Sorted, so that two calls with nothing in between answer the same way; a map
// range would not.
func (p *Pool) Pinned() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.pinned))
	for _, id := range p.pinned {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// pinnedSet folds a list of repo ids into the lookup the pool keys by, keeping
// the spelling each was given under as the value.
func pinnedSet(ids []string) map[string]string {
	set := make(map[string]string, len(ids))
	for _, id := range ids {
		set[config.FoldRepoID(id)] = id
	}
	return set
}

// isPinnedLocked reports whether a model is protected. Callers must hold p.mu.
func (p *Pool) isPinnedLocked(repoID string) bool {
	_, ok := p.pinned[config.FoldRepoID(repoID)]
	return ok
}

// ErrClosed is returned once the pool is shut down.
var ErrClosed = errors.New("pool is closed")

// Acquire returns a ready upstream for repoID, loading the model if necessary
// and evicting others to make room.
//
// With eviction grace switched on, a load that finds no room joins a queue
// instead of taking a model that has only just finished work: see
// PoolOptions.EvictionGrace for what it waits for and how long. With grace off
// — the default — it takes its victim at once and nothing ever waits.
//
// The returned release function must be called when the request finishes. Until
// it is, the model is pinned and cannot be evicted out from under the caller.
func (p *Pool) Acquire(ctx context.Context, repoID string) (*Upstream, func(), error) {
	return p.acquire(ctx, repoID, true)
}

// AcquireNow is Acquire for a caller that must not wait out an eviction grace.
//
// Start-up preloading is the case it exists for: it loads its models one after
// another, so a list of models that cannot all fit would otherwise stall a
// start by one maximum wait per model — and at start-up there is nobody to
// protect, since no client has been served yet. Everything a client can reach
// goes through Acquire.
func (p *Pool) AcquireNow(ctx context.Context, repoID string) (*Upstream, func(), error) {
	return p.acquire(ctx, repoID, false)
}

func (p *Pool) acquire(ctx context.Context, repoID string, mayWait bool) (*Upstream, func(), error) {
	key := config.FoldRepoID(repoID)
	// waited is what this acquisition spent parked for want of room, and it is
	// reported to the caller: a request that waited for somebody else's model
	// to fall idle is entitled to know that it did.
	var (
		w      *loadWaiter
		waited time.Duration
	)

	p.mu.Lock()
	var e *entry
	for {
		if p.closed {
			p.leaveQueueLocked(w)
			p.mu.Unlock()
			return nil, nil, ErrClosed
		}
		// Checked on every pass, not only where the wait is parked. A woken
		// waiter blocks on p.mu — the lock every Acquire, Resident, Unload and
		// control-panel snapshot takes — and the client can hang up while it
		// is blocked. Going on to evict a warm model and start a server for a
		// request that no longer exists is the exact outcome this feature is
		// here to prevent, paid for by nobody.
		if err := ctx.Err(); err != nil {
			p.leaveQueueLocked(w)
			p.mu.Unlock()
			return nil, nil, err
		}
		if held, ok := p.entries[key]; ok {
			e = held
			break
		}

		// A caller that will not wait honours neither the grace nor the queue.
		// There is no point protecting a model from a load that is going to
		// take it anyway, and holding a start-up preload behind somebody
		// else's wait would leave the model cold for the rest of the session —
		// which is what the no-wait path exists to prevent.
		age := p.waitedBy(w)
		mayEvict := p.mayEvictLocked(w)
		if !mayWait {
			age, mayEvict = p.grace, true
		}

		var err error
		if p.worthTryingLocked(w, age, mayEvict) {
			var started *entry
			started, err = p.startLocked(repoID, age, mayEvict)
			if err == nil {
				e = started
				// Somebody may have been queued for this very model; it has an
				// entry to join now.
				p.wakeWaitersLocked()
				break
			}
		} else {
			// Nothing has changed that this waiter could act on, and asking
			// again would resolve the model and stat the launcher's files
			// under p.mu — on every wake-up, for every parked request. What is
			// still worth re-checking (can this ever fit now, is it still
			// inside its maximum) needs only what the waiter already carries.
			err = p.noRoomLocked(w.need)
		}
		verdict := p.waitVerdictLocked(mayWait, w, err)
		if verdict != waitYes {
			queued := len(p.waiters)
			p.leaveQueueLocked(w)
			p.mu.Unlock()
			// Logged out here, not where the refusal is built: p.mu is the
			// pool's one lock — every Acquire, Resident, Pinned and Unload
			// takes it, and the models list takes it on every request — so a
			// slow log sink would let a client that can provoke refusals stall
			// every other caller for the length of a write.
			var noRoom *NoRoomError
			if errors.As(err, &noRoom) {
				// The refusal is this goroutine's own, freshly built and held
				// by nobody else, so annotating it after the unlock is safe.
				noRoom.Waited = p.waitedBy(w)
				// Two different facts about this Mac, and an operator reading
				// the log needs to tell them apart: a machine refusing loads
				// because everything in memory is protected is not the same as
				// one refusing them because the queue for memory is full.
				if verdict == waitQueueFull {
					p.opts.Log.Info("refused a model load: as many requests are already waiting for memory as the queue allows",
						"model", repoID, "waiting", queued,
						"limit", HumanBytes(noRoom.Limit))
				} else {
					p.opts.Log.Info("refused a model load: no model in memory could be freed",
						"model", repoID, "protected", noRoom.Protected,
						"limit", HumanBytes(noRoom.Limit), "waited", noRoom.Waited)
				}
			}
			return nil, nil, err
		}
		if w == nil {
			// willWaitLocked said yes, so the refusal is a *NoRoomError and
			// carries what this load asked for.
			var noRoom *NoRoomError
			_ = errors.As(err, &noRoom)
			w = &loadWaiter{arrived: time.Now(), need: noRoom.need, signal: make(chan struct{}, 1)}
			p.waiters = append(p.waiters, w)
		}
		delay := p.wakeDelayLocked(w)
		p.mu.Unlock()

		// Parked off p.mu: Acquire holds it across startLocked, so waiting
		// under it would block every other Acquire, Resident and Unload for
		// the length of the wait.
		timer := time.NewTimer(delay)
		select {
		case <-w.signal:
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			p.mu.Lock()
			p.leaveQueueLocked(w)
			p.mu.Unlock()
			return nil, nil, ctx.Err()
		}
		timer.Stop()
		p.mu.Lock()
	}
	if w != nil {
		waited = p.waitedBy(w)
		p.leaveQueueLocked(w)
	}
	// Refuse once the backlog for this model is already at its ceiling. Every
	// in-flight request (running or merely queued on e.sem) pins the model, so an
	// unbounded backlog would both peg memory and starve loads of other models
	// that need this one evicted. Failing fast here — the gateway maps it to 503 —
	// bounds that, and stops a flood of slow clients from piling up goroutines and
	// connections.
	if e.inFlight >= cap(e.sem)+p.opts.MaxQueueDepth {
		p.mu.Unlock()
		return nil, nil, fmt.Errorf("%s is overloaded (%d requests already in flight): %w",
			repoID, e.inFlight, ErrBusy)
	}
	// Pin it *before* releasing the lock, so a concurrent Acquire for another
	// model cannot evict this one while we are waiting for it to load.
	e.inFlight++
	e.lastUsed = p.opts.now()
	ready := e.ready
	// The load wait is clocked from here, with the entry in hand, rather than
	// from the top of Acquire: everything above is contention on this pool's
	// own lock and, for the request that triggers a load, the launch itself.
	// Billing those as "waiting for the model to load" would report a warm,
	// free model as cold whenever another goroutine happened to hold the lock.
	entered := p.opts.now()
	p.mu.Unlock()

	release := func() {
		p.mu.Lock()
		e.inFlight--
		e.lastUsed = p.opts.now()
		// evictForLocked refuses to evict an entry that is still loading (see
		// its isReady guard), so a caller giving up mid-load must not leave
		// the entry to sit there instead: with nobody left to wait for it,
		// that would pin its full share of the memory budget, unkillable,
		// for up to ReadyTimeout — one abandoned request against a large
		// model could deny every other model from loading for minutes. Tear
		// it down the moment the last waiter is gone. The identity check
		// guards against a load that already failed and removed itself.
		if e.inFlight == 0 && !isReady(e) && p.entries[config.FoldRepoID(e.repoID)] == e {
			p.stopEntryLocked(e, StopAbandoned)
		}
		// A model that has just fallen idle is the commonest way room appears.
		p.wakeWaitersLocked()
		p.mu.Unlock()
	}

	select {
	case <-ready:
		if e.readyErr != nil {
			release()
			return nil, nil, e.readyErr
		}
	case <-ctx.Done():
		release()
		return nil, nil, ctx.Err()
	}
	// Every waiter on a load pays the wait, not only the request that started
	// it: a request that arrives halfway through someone else's load is still
	// a request that waited for a model to load, and reporting it as a queue
	// wait would say the model was busy when it was cold.
	loaded := p.opts.now()

	// The model is ready; now claim a concurrency slot on it. Beyond the batch the
	// server can actually decode, extra requests wait here rather than piling into
	// mlx-lm and blowing its memory. The wait is bounded by the caller's context.
	// This is done only on the success path, so the early `release()` calls above
	// (which never took a slot) stay correct; the returned closure drains both.
	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		release()
		return nil, nil, ctx.Err()
	}
	releaseSlot := func() {
		<-e.sem
		release()
	}

	return &Upstream{
		RepoID:   repoID,
		BaseURL:  fmt.Sprintf("http://127.0.0.1:%d", e.port),
		ModelArg: e.modelArg,
		Waits: AcquireStats{
			LoadWait: loaded.Sub(entered),
			// The wait for room is a queue wait, not a load wait: nothing was
			// loading, the machine was full. Reporting it as a load wait would
			// say a model was cold when what it was, was somebody else's.
			QueueWait: waited + p.opts.now().Sub(loaded),
		},
	}, releaseSlot, nil
}

// startLocked launches a model server. Callers must hold p.mu.
//
// waited is how long the caller has already been queued for room, which is
// what bounds the protection an eviction grace gives; mayEvict is false for a
// caller that is not at the head of that queue, and is what keeps the queue
// first-in, first-out.
func (p *Pool) startLocked(repoID string, waited time.Duration, mayEvict bool) (*entry, error) {
	path, size, err := p.opts.Models.Resolve(repoID)
	if err != nil {
		return nil, err
	}

	need := LoadCost(size)
	if need > p.maxResident {
		return nil, fmt.Errorf(
			"%s needs about %s of memory but the limit is %s — raise the memory budget or choose a smaller quantization",
			repoID, HumanBytes(need), HumanBytes(p.maxResident))
	}
	// Check cheap launch preconditions before evicting anything. Eviction is not
	// reversible (stopEntryLocked's Stop cannot be undone), so if we evicted
	// first and Launch failed moments later — a missing venv, or this repoID's
	// own directory vanishing in a race with a concurrent delete — a healthy,
	// unrelated resident model would be torn down for zero benefit.
	if err := p.opts.Launcher.Precheck(Spec{RepoID: repoID, ModelPath: path}); err != nil {
		return nil, fmt.Errorf("start model server for %s: %w", repoID, &LaunchError{Err: err})
	}
	if err := p.evictForLocked(need, waited, mayEvict); err != nil {
		return nil, err
	}

	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("allocate port: %w", err)
	}

	e := &entry{
		repoID:   repoID,
		port:     port,
		bytes:    size,
		modelArg: path,
		loadedAt: p.opts.now(),
		lastUsed: p.opts.now(),
		ready:    make(chan struct{}),
		// Allow twice the decode batch size in flight: enough to keep mlx-lm's
		// batching full without letting an unbounded burst exhaust GPU memory.
		sem: make(chan struct{}, 2*p.opts.DecodeConcurrency),
	}

	var sampling config.Sampling
	if p.opts.SamplingFor != nil {
		sampling = p.opts.SamplingFor(repoID)
	}
	proc, err := p.opts.Launcher.Launch(context.Background(), Spec{
		RepoID:            repoID,
		ModelPath:         path,
		Port:              port,
		DecodeConcurrency: p.opts.DecodeConcurrency,
		Sampling:          sampling,
	})
	if err != nil {
		return nil, fmt.Errorf("start model server for %s: %w", repoID, &LaunchError{Err: err})
	}
	e.proc = proc
	p.entries[config.FoldRepoID(repoID)] = e
	p.notify(func(o PoolObserver) { o.LoadStarted(repoID) })

	go p.waitReady(e)
	return e, nil
}

// waitReady probes until the model actually answers a completion, then unblocks
// everyone waiting on it.
func (p *Pool) waitReady(e *entry) {
	ctx, cancel := context.WithTimeout(context.Background(), p.opts.ReadyTimeout)
	defer cancel()

	started := p.opts.now()
	err := p.probeReady(ctx, e)
	took := p.opts.now().Sub(started)
	p.notify(func(o PoolObserver) { o.LoadFinished(e.repoID, took, err) })

	p.mu.Lock()
	e.readyErr = err
	close(e.ready)
	if err != nil {
		// A model that never became ready must not linger in the pool holding a
		// slice of the memory budget. Guard on identity: while this entry was
		// loading it could have been evicted and a *new* entry created under the
		// same repoID key. Deleting by key alone would then orphan that healthy
		// replacement — its process would leak and its memory would stop counting
		// against the budget.
		if p.entries[config.FoldRepoID(e.repoID)] == e {
			delete(p.entries, config.FoldRepoID(e.repoID))
			p.notify(func(o PoolObserver) { o.EntryStopped(e.repoID, StopLoadFailed) })
			p.wakeWaitersLocked()
		}
	}
	p.mu.Unlock()

	if err != nil && e.proc != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = e.proc.Stop(stopCtx)
		stopCancel()
	}
	if err == nil && e.proc != nil {
		go p.watchExit(e)
	}
}

// watchExit removes a model whose server process exits after it became ready —
// a crash mid-serving (GPU memory exhaustion during a long generation, a Python
// fault, a manual kill). Without it the dead entry would stay in the pool
// forever: Acquire would keep returning its port, so every request to that
// model gets a 502 with nothing to reap it (the default idle timeout is zero,
// and each failed request refreshes lastUsed), while the corpse keeps counting
// against the memory budget. Removing the entry lets the next Acquire relaunch.
//
// Guard on identity, like the failure path in waitReady: on a normal stop
// (evict, Unload, Close) the entry is already gone from the map, and a new
// entry may exist under the same key.
func (p *Pool) watchExit(e *entry) {
	<-e.proc.Done()
	p.mu.Lock()
	if p.entries[config.FoldRepoID(e.repoID)] == e {
		delete(p.entries, config.FoldRepoID(e.repoID))
		p.notify(func(o PoolObserver) { o.EntryStopped(e.repoID, StopCrashed) })
		p.wakeWaitersLocked()
	}
	p.mu.Unlock()
}

// probeReady waits for the model to serve a real one-token completion.
//
// /health is not sufficient: mlx_lm.server answers it "ok" the moment the socket
// is up, long before the weights are in memory. The only trustworthy readiness
// signal is a completion that succeeds.
func (p *Pool) probeReady(ctx context.Context, e *entry) error {
	base := fmt.Sprintf("http://127.0.0.1:%d", e.port)

	body, _ := json.Marshal(map[string]any{
		"model":      e.modelArg,
		"messages":   []any{map[string]string{"role": "user", "content": "hi"}},
		"max_tokens": 1,
		"stream":     false,
	})

	backoff := 200 * time.Millisecond
	for {
		// If the process died (bad weights, OOM, missing dependency), stop
		// probing and report it rather than spinning until the timeout.
		select {
		case <-e.proc.Done():
			if err := e.proc.Err(); err != nil {
				return &NotReadyError{Err: fmt.Errorf("model server for %s exited during startup: %w", e.repoID, err)}
			}
			return &NotReadyError{Err: fmt.Errorf("model server for %s exited during startup", e.repoID)}
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			base+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := p.opts.HTTP.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return &NotReadyError{Err: fmt.Errorf("%s did not become ready within %s", e.repoID, p.opts.ReadyTimeout)}
		case <-time.After(backoff):
		}
		// Cap the retry interval low: this loop only spins while the server socket
		// is not yet up (a ready request returns the instant the model loads). A
		// short ramp avoids a busy-spin without adding up to ~2s of dead time
		// between the model becoming ready and the next probe noticing.
		if backoff < 400*time.Millisecond {
			backoff *= 2
		}
	}
}

// evictForLocked frees enough budget for need bytes. Callers must hold p.mu.
//
// With grace off it evicts as it goes, which is what it has always done: every
// candidate is taken, in least-recently-used order, and if that is still not
// enough the load is refused having freed what it could.
//
// With grace on the whole set is planned first and taken only if the plan
// works. A request that is about to join the queue must not have torn a model
// down on its way there — it would be waiting for room it had already spent
// somebody else's warm model to fail to make.
//
// mayEvict is false for anyone but the oldest waiter, and it refuses the load
// whether or not the plan needs a victim. Gating only the eviction would let a
// stream of small requests take the room as it appears while the waiter at the
// head, needing more of it than any single release frees, never fits — which
// is the "smallest first starves a large model" failure the queue exists to
// prevent, reached by the other door.
func (p *Pool) evictForLocked(need int64, waited time.Duration, mayEvict bool) error {
	victims, enough := p.evictionPlanLocked(need, waited)
	if p.grace > 0 && (!enough || !mayEvict) {
		return p.noRoomLocked(need)
	}
	for _, v := range victims {
		p.stopEntryLocked(v, StopEvicted)
	}
	if !enough {
		return p.noRoomLocked(need)
	}
	return nil
}

// evictionPlanLocked names the models that would have to go for need bytes to
// fit, least recently used first, and says whether taking them all is enough.
// Callers must hold p.mu.
func (p *Pool) evictionPlanLocked(need int64, waited time.Duration) ([]*entry, bool) {
	var used int64
	for _, e := range p.entries {
		used += LoadCost(e.bytes)
	}
	if used+need <= p.maxResident {
		return nil, true
	}

	candidates := make([]*entry, 0, len(p.entries))
	for _, e := range p.entries {
		// Never evict a model that is still loading: its lone waiter can have
		// already given up (context cancelled or timed out) and dropped
		// inFlight to 0 while waitReady keeps running in the background.
		// Killing it here would waste the in-progress load; isReady checks
		// without blocking. Same reasoning as reapIdle's guard below.
		if e.inFlight > 0 || !isReady(e) {
			continue
		}
		// A pinned model is removed from the candidate set before the
		// least-recently-used comparison runs, so it survives even when it
		// is the better victim by age. That is the whole of the promise:
		// the load that needed the room fails instead.
		if p.isPinnedLocked(e.repoID) {
			continue
		}
		if !p.graceElapsedLocked(e, waited) {
			continue
		}
		candidates = append(candidates, e)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].lastUsed.Before(candidates[j].lastUsed)
	})

	victims := make([]*entry, 0, len(candidates))
	for _, e := range candidates {
		victims = append(victims, e)
		used -= LoadCost(e.bytes)
		if used+need <= p.maxResident {
			return victims, true
		}
	}
	return victims, false
}

// graceElapsedLocked reports whether an idle model may be taken. Callers must
// hold p.mu.
//
// Two clauses, and the second is the one that matters. Protection runs from
// when the model last finished a request — but it is bounded by the waiting
// request's own age, so a client sending a one-token request to one model
// every few seconds cannot deny another client its model indefinitely. That
// starvation is the failure the least-recently-used rule does not have, and
// grace must not introduce it.
func (p *Pool) graceElapsedLocked(e *entry, waited time.Duration) bool {
	if p.grace <= 0 || waited >= p.grace {
		return true
	}
	// A model with work in flight has not finished anything, so there is no
	// idleness to measure. Its caller skips such an entry before it gets here,
	// but the premise belongs to the rule: without this, a second caller added
	// later would read a busy model's arrival stamp and quietly get the wrong
	// answer.
	if e.inFlight > 0 {
		return false
	}
	// lastUsed is the clock, and for a candidate it is the moment the model
	// stopped working: release stamps it when a request ends, and every caller
	// of this has already skipped the entries with a request in flight, so
	// nothing has touched it since. It is stamped on the way in as well, which
	// is why that skip is part of this rule rather than merely part of the
	// caller's — read from a busy entry, this would measure the wrong thing.
	return p.opts.now().Sub(e.lastUsed) >= p.grace
}

// noRoomLocked builds the refusal a load gets when the budget cannot be made
// to hold it. Callers must hold p.mu.
func (p *Pool) noRoomLocked(need int64) *NoRoomError {
	return &NoRoomError{
		Limit:     p.maxResident,
		Protected: p.pinnedResidentLocked(),
		need:      need,
	}
}

// waitVerdict says whether a load that found no room joins the queue, and if
// not, why not — which is what lets the log tell a full queue apart from a
// machine whose memory is all spoken for.
type waitVerdict int

const (
	// waitYes: park it.
	waitYes waitVerdict = iota
	// waitNoGrace: grace is off, or this caller does not wait, or the refusal
	// is not one that waiting could cure.
	waitNoGrace
	// waitNeverFits: no eviction could make room for this load, however long
	// anyone waits.
	waitNeverFits
	// waitQueueFull: as many requests are already waiting as the cap allows.
	waitQueueFull
	// waitTimedOut: this one has waited its maximum.
	waitTimedOut
)

// waitVerdictLocked decides whether a load that found no room joins the queue
// rather than being refused now. Callers must hold p.mu.
func (p *Pool) waitVerdictLocked(mayWait bool, w *loadWaiter, err error) waitVerdict {
	if !mayWait || p.grace <= 0 {
		return waitNoGrace
	}
	// Only a refusal about the machine being full can be cured by waiting. A
	// missing model, a failed precheck or a model larger than the whole budget
	// is the same answer however long anyone waits for it.
	var noRoom *NoRoomError
	if !errors.As(err, &noRoom) {
		return waitNoGrace
	}
	// Nor is there anything to wait for when the pinned models plus what this
	// load needs are already over the budget: no eviction can ever make that
	// fit, and waiting would hold a connection and a buffered request body
	// open for the whole maximum wait to reach the same refusal.
	if !p.canEverFitLocked(noRoom.need) {
		return waitNeverFits
	}
	if w == nil {
		if len(p.waiters) >= p.opts.MaxLoadWaiters {
			return waitQueueFull
		}
		return waitYes
	}
	if time.Since(w.arrived) >= p.maxWait {
		return waitTimedOut
	}
	return waitYes
}

// worthTryingLocked reports whether this caller should ask the registry and
// the launcher for a load. Callers must hold p.mu.
//
// A caller that has not queued always asks: it has no remembered need to
// judge itself by, and one attempt is what it takes to learn one. A waiter
// asks only when it could actually proceed, because every wake-up wakes every
// waiter and startLocked resolves the model and stats two files before it
// reaches the eviction plan — filesystem work under the pool's one lock, at
// whatever rate the machine completes requests.
func (p *Pool) worthTryingLocked(w *loadWaiter, age time.Duration, mayEvict bool) bool {
	// A caller that has not queued always asks, and so does every caller once
	// grace is off: the off path evicts what it can before it refuses, and
	// skipping the attempt would refuse a request that a swap would have
	// served.
	if w == nil || p.grace <= 0 {
		return true
	}
	if !mayEvict {
		return false
	}
	_, enough := p.evictionPlanLocked(w.need, age)
	return enough
}

// canEverFitLocked reports whether evicting every model that is not pinned
// would leave room for need bytes. Callers must hold p.mu.
func (p *Pool) canEverFitLocked(need int64) bool {
	var protected int64
	for _, e := range p.entries {
		if p.isPinnedLocked(e.repoID) {
			protected += LoadCost(e.bytes)
		}
	}
	return protected+need <= p.maxResident
}

// mayEvictLocked reports whether this caller is allowed to load at all while
// others are queued. Callers must hold p.mu.
//
// Only the oldest waiter may, which is the whole of the fairness rule — and it
// applies to a request that has not queued at all, or a new arrival would step
// over everyone already waiting. It gates the load and not merely the
// eviction: free room is as much the head waiter's as a victim is.
func (p *Pool) mayEvictLocked(w *loadWaiter) bool {
	// With grace off there is no queue to be fair to. Whatever is parked is
	// being drained — the operator has just said "swap now" — and every one of
	// them takes the ordinary path, which evicts. Holding all but the head
	// back here would turn the off switch into a refusal for every client
	// already waiting.
	if p.grace <= 0 {
		return true
	}
	return len(p.waiters) == 0 || p.waiters[0] == w
}

// waitedBy is how long a waiter has been parked; zero for a caller that has
// not parked at all.
func (p *Pool) waitedBy(w *loadWaiter) time.Duration {
	if w == nil {
		return 0
	}
	return time.Since(w.arrived)
}

// wakeDelayLocked is how long a waiter may sleep before something could have
// changed that nothing will signal. Callers must hold p.mu.
//
// Releases, stops, budget and pin changes all signal, so this covers only the
// passage of time: the waiter's own age reaching the grace, the oldest
// protected candidate's grace running out, and the maximum wait expiring. The
// floor keeps a stopped clock from spinning.
func (p *Pool) wakeDelayLocked(w *loadWaiter) time.Duration {
	waited := time.Since(w.arrived)
	delay := p.maxWait - waited
	if own := p.grace - waited; own > 0 && own < delay {
		delay = own
	}
	now := p.opts.now()
	for _, e := range p.entries {
		if e.inFlight > 0 || !isReady(e) || p.isPinnedLocked(e.repoID) {
			continue
		}
		if left := p.grace - now.Sub(e.lastUsed); left > 0 && left < delay {
			delay = left
		}
	}
	if delay < time.Millisecond {
		delay = time.Millisecond
	}
	return delay
}

// stopEntryLocked removes an entry and stops its process, reporting why it
// went. Callers must hold p.mu.
func (p *Pool) stopEntryLocked(e *entry, reason StopReason) {
	delete(p.entries, config.FoldRepoID(e.repoID))
	p.notify(func(o PoolObserver) { o.EntryStopped(e.repoID, reason) })
	// Room has just appeared, whatever took it away.
	p.wakeWaitersLocked()
	proc := e.proc
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if proc != nil {
			_ = proc.Stop(ctx)
		}
	}()
}

// ErrNotLoaded is returned by Unload when the model is not resident.
var ErrNotLoaded = errors.New("model is not loaded")

// ErrBusy marks every refusal that is about the machine being occupied rather
// than about the caller's request: Unload of a model that is serving one,
// Acquire past a model's queue ceiling, and Acquire when nothing in memory can
// be freed to make room. Callers can test for it with errors.Is rather than
// matching on message text.
var ErrBusy = errors.New("model is busy")

// DecodeConcurrency and IdleTimeout are what the pool is actually running
// with, which is not always what is saved: both are taken from the settings
// once, when the pool is built, and a change to either takes a restart. The
// statistics store records the effective values rather than the saved ones,
// so that a reader comparing figures either side of a change sees the line in
// the right place. MemoryBudget, declared above, is the third figure it records.
func (p *Pool) DecodeConcurrency() int { return p.opts.DecodeConcurrency }

func (p *Pool) IdleTimeout() time.Duration { return p.opts.IdleTimeout }

// Unload stops a model server.
func (p *Pool) Unload(repoID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	e, ok := p.entries[config.FoldRepoID(repoID)]
	if !ok {
		return fmt.Errorf("%s: %w", repoID, ErrNotLoaded)
	}
	if e.inFlight > 0 {
		return fmt.Errorf("%s is serving %d request(s); try again in a moment: %w",
			repoID, e.inFlight, ErrBusy)
	}
	p.stopEntryLocked(e, StopUnloaded)
	return nil
}

// Resident lists the models the pool is holding, most recently used first.
// Models still loading are included, carrying ResidencyLoading; the snapshot
// is taken under the pool's lock and reserves nothing.
func (p *Pool) Resident() []Resident {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]Resident, 0, len(p.entries))
	for _, e := range p.entries {
		// isReady reads the ready channel without blocking, so a model in the
		// middle of a load is reported as loading rather than making every
		// caller of Resident wait for it.
		state := ResidencyLoading
		if isReady(e) {
			state = ResidencyLoaded
		}
		out = append(out, Resident{
			RepoID:   e.repoID,
			State:    state,
			Port:     e.port,
			Bytes:    e.bytes,
			LoadedAt: e.loadedAt,
			LastUsed: e.lastUsed,
			InFlight: e.inFlight,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUsed.After(out[j].LastUsed) })
	return out
}

// reapIdle unloads models that have gone untouched for IdleTimeout.
func (p *Pool) reapIdle() {
	defer close(p.idleDone)

	if p.opts.IdleTimeout <= 0 {
		<-p.stopIdle
		return
	}
	tick := time.NewTicker(p.opts.IdleTimeout / 4)
	defer tick.Stop()

	for {
		select {
		case <-p.stopIdle:
			return
		case <-tick.C:
			p.mu.Lock()
			now := p.opts.now()
			for _, e := range p.entries {
				// Never reap a model that is still loading: its ready channel is
				// open, so tearing it down would waste the load and error every
				// caller waiting on it. isReady checks without blocking.
				// A pinned model ignores the timeout. Reaping is the second
				// eviction path, with the same effect as the first, so
				// protecting one and not the other would make the promise
				// false after IdleTimeout of quiet.
				if e.inFlight == 0 && isReady(e) && !p.isPinnedLocked(e.repoID) &&
					now.Sub(e.lastUsed) >= p.opts.IdleTimeout {
					p.stopEntryLocked(e, StopIdle)
				}
			}
			p.mu.Unlock()
		}
	}
}

// Close stops every model server.
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	procs := make([]Process, 0, len(p.entries))
	for _, e := range p.entries {
		procs = append(procs, e.proc)
		p.notify(func(o PoolObserver) { o.EntryStopped(e.repoID, StopShutdown) })
	}
	p.entries = map[string]*entry{}
	// Every parked request is answered rather than left holding a connection
	// while the process goes away; each sees p.closed and returns ErrClosed.
	p.wakeWaitersLocked()
	p.mu.Unlock()

	close(p.stopIdle)
	<-p.idleDone

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for _, proc := range procs {
		if proc == nil {
			continue
		}
		wg.Add(1)
		go func(pr Process) {
			defer wg.Done()
			_ = pr.Stop(ctx)
		}(proc)
	}
	wg.Wait()
	return nil
}

// NoRoomError is the refusal a load gets when nothing in memory can be freed
// for it — every model there is protected by a pin or serving a request.
//
// The protected names are a field rather than part of the message on purpose:
// handleCompletions writes a pool error verbatim into the 503 body every LAN
// client reads, and which models the operator chose to protect is not a
// client's business. Error() is what goes on the wire; Protected is what goes
// to this machine's own log.
type NoRoomError struct {
	// Limit is the memory budget the load was measured against.
	Limit int64
	// Protected names the pinned models in memory, for the log only.
	Protected []string
	// Waited is how long the refused request spent queued for room under an
	// eviction grace, and zero when it did not queue at all. It is on the wire
	// and in the response header, because a client that held a connection open
	// for minutes is owed the reason; it names no model and says only that
	// this machine was busy, which a stopwatch would have told it anyway.
	Waited time.Duration
	// need is what the refused load asked for, so Acquire can tell "no room
	// now" from "no room however long anyone waits" without resolving the
	// model a second time. Unexported: it is this package's arithmetic, not
	// something to put on the wire.
	need int64
}

// Error is the text a network client reads. It names no model.
func (e *NoRoomError) Error() string {
	msg := fmt.Sprintf(
		"not enough memory to load another model, and no model in memory can be freed (limit %s)",
		HumanBytes(e.Limit))
	if e.Waited > 0 {
		msg += fmt.Sprintf(" after waiting %s", e.Waited.Round(time.Millisecond))
	}
	return msg
}

// Unwrap makes this one of the refusals errors.Is(err, ErrBusy) matches: the
// machine is occupied, rather than anything being wrong with the request.
func (e *NoRoomError) Unwrap() error { return ErrBusy }

// pinnedResidentLocked names the protected models currently in memory, for the
// log line Acquire writes once the lock is released. Callers must hold p.mu.
func (p *Pool) pinnedResidentLocked() []string {
	var out []string
	for _, e := range p.entries {
		if p.isPinnedLocked(e.repoID) {
			out = append(out, e.repoID)
		}
	}
	sort.Strings(out)
	return out
}

// isReady reports whether an entry has finished loading (its ready channel is
// closed) without blocking.
func isReady(e *entry) bool {
	select {
	case <-e.ready:
		return true
	default:
		return false
	}
}

// LoadCost estimates the memory a model occupies once loaded: its weights plus
// headroom for the KV cache and activations. It is what a model is charged
// against the memory budget, so the check that a pinned set fits has to use
// this figure and not the size on disk.
func LoadCost(diskBytes int64) int64 {
	return diskBytes + diskBytes/5 // 1.2x
}

// HumanBytes renders a byte count the way the pool's own messages do, so a
// figure quoted elsewhere reads the same as the one in a refusal.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
