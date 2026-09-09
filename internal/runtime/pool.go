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

	"github.com/intentdriven/Gropius/internal/capability"
	"github.com/intentdriven/Gropius/internal/config"
)

// ModelSource resolves a repo id to an on-disk model. The registry implements it.
type ModelSource interface {
	// Resolve returns what the pool needs to know about the model to launch it
	// and to charge it against the memory budget.
	Resolve(repoID string) (ResolvedModel, error)
}

// ResolvedModel is one model on disk, as far as the pool is concerned.
//
// The last two fields are what the memory budget charges a model beyond its
// weights: the window it will serve and what a token of that window costs its
// attention cache. Both come from the model's own config.json, read once when
// the directory is scanned, and either being zero means the configuration did
// not say — which is charged the flat figure, as every model was before this.
type ResolvedModel struct {
	// Path is the directory passed to mlx_lm.server --model.
	Path string
	// Bytes is the model's size on disk.
	Bytes int64
	// ContextLength is the window the model declares, and therefore the one
	// the pool intends to serve: nothing between a client and mlx-lm caps it.
	ContextLength int64
	// KVBytesPerToken is what one token of that window costs the attention
	// cache, as the configuration implies it (registry.ReadKVBytesPerToken).
	KVBytesPerToken int64
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
	RepoID string         `json:"repo_id"`
	State  ResidencyState `json:"state"`
	Port   int            `json:"port"`
	Bytes  int64          `json:"bytes"`
	// Charge is what this model costs the memory budget, which is more than
	// its size: the weights, their headroom, and the attention cache the
	// window it serves will build. It is the only figure that can be compared
	// with the budget, so it is the one every surface reporting memory adds up.
	Charge   int64     `json:"charge_bytes"`
	LoadedAt time.Time `json:"loaded_at"`
	LastUsed time.Time `json:"last_used"`
	InFlight int       `json:"in_flight"`
}

// PoolOptions configures a Pool.
type PoolOptions struct {
	Launcher Launcher
	Models   ModelSource
	// MaxResidentBytes is the pool's memory budget as it starts: the ceiling on
	// the total charged size (capability.LoadCostOf) of the models held at
	// once. Zero or less means the default share of this Mac's
	// memory.
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

	// MaxLoadWaitersPerSource bounds how many of those waiters any one caller
	// may hold. Zero disables the per-source cap and leaves only the global
	// one, which is the pre-existing behaviour.
	//
	// Identity is the presented API key, so this is only meaningful where a key
	// is required — which is why enabling eviction grace on a LAN-exposed
	// server requires one. Unkeyed and loopback callers share a single bucket:
	// the pool cannot distinguish two anonymous callers, and an address is not
	// a client.
	MaxLoadWaitersPerSource int
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

	// DrainWait is how long the pool waits for a stopped model server to exit
	// before it stops counting on that memory coming back: past it a load is
	// refused rather than held, the server is reported as stuck, and the
	// eviction plan starts working around its charge instead of waiting for it.
	// It bounds nothing the operator asked for and everything the machine has
	// to do, so it is derived — zero, the only value anything but a test
	// passes, means maxDrainWait, which is what stopping a server is allowed to
	// take plus a margin.
	DrainWait time.Duration

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
	// drainBytes is the charged size of the model servers that have left the
	// pool and are still exiting, within the time stopping one is allowed to
	// take. A stopped server holds its memory until the kernel reclaims it — up
	// to the SIGTERM grace and the SIGKILL that follows — so crediting the
	// budget at the moment the entry is deleted would let a replacement load on
	// top of a victim that has released nothing. This is the charge a load
	// WAITS for: it is coming back. Guarded by mu.
	drainBytes int64
	// stuck are the servers that did not exit even after SIGKILL, by the id
	// their watcher holds. Their memory is not something to wait for — nothing
	// further can be done to them — but it is charged (it really is spent) and
	// it is EVICTABLE-AGAINST: the eviction plan counts it, so a load can still
	// take an idle model to make room around it. A late exit is still credited:
	// the watcher goes on waiting on Done with nothing else to do. Guarded by
	// mu.
	stuck map[uint64]stuckServer
	// nextStuck names the next one, so a watcher can find its own entry to
	// remove without depending on a position in a slice. Guarded by mu.
	nextStuck uint64
	// drainGen changes whenever either of the two above does. A waiter records
	// it, so a wake-up that cannot have changed the answer costs a comparison
	// rather than a walk of the pool. Guarded by mu.
	drainGen uint64
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
	repoID string
	port   int
	bytes  int64
	// charge is what this model costs the memory budget: its weights and the
	// caches the window it serves will build (capability.LoadCostOf). It is
	// worked out once, when the model is admitted, and every admission
	// decision after that counts this figure — so the memory a model is
	// holding is never accounted at two different rates.
	charge   int64
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

// stuckServer is a model server that survived being stopped and then killed.
// It is remembered rather than merely counted so that shutdown can name the
// process groups still holding memory: the orphan reaper's ledger is what the
// next start uses to finish them off.
type stuckServer struct {
	repoID string
	pid    int
	charge int64
}

// stuckChargeLocked is the memory held by servers that would not die. Callers
// must hold p.mu.
func (p *Pool) stuckChargeLocked() int64 {
	var sum int64
	for _, s := range p.stuck {
		sum += s.charge
	}
	return sum
}

// stuckServersLocked lists them in the order they got stuck, so shutdown names
// them the same way twice. Callers must hold p.mu.
func (p *Pool) stuckServersLocked() []stuckServer {
	ids := make([]uint64, 0, len(p.stuck))
	for id := range p.stuck {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]stuckServer, 0, len(ids))
	for _, id := range ids {
		out = append(out, p.stuck[id])
	}
	return out
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
	// source identifies who this load is waiting for, so the queue can be
	// shared out rather than filled by one caller. It is the presented API key
	// on a keyed install and "" for a loopback or unkeyed caller, which is a
	// single shared bucket — the pool cannot tell two anonymous callers apart,
	// and an address is not a client.
	source string
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
	// drainGen is the pool's drain generation as of this waiter's last attempt.
	// A periodic re-check that finds it unchanged, on a waiter the drain is
	// blocking, is answered from what the waiter already carries — no resolving
	// the model, no stat-ing the launcher's files under p.mu.
	drainGen uint64
	// mayWait says whether this acquisition honours the eviction grace, which
	// is what decides the bound it is held to and therefore how long it may
	// sleep. It is a property of the call — Acquire or AcquireNow — so it is
	// fixed for the life of the waiter, unlike the intervals it selects
	// between, which the operator can change while the request waits.
	mayWait bool
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
	if opts.DrainWait <= 0 {
		opts.DrainWait = maxDrainWait
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
	if opts.MaxLoadWaitersPerSource < 0 {
		opts.MaxLoadWaitersPerSource = 0
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
		stuck:       map[uint64]stuckServer{},
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
// about them. A negative grace is no grace, and a maximum wait shorter than
// the grace — zero included — is raised to the grace.
func normalizeGrace(grace, maxWait time.Duration) (time.Duration, time.Duration) {
	if grace < 0 {
		grace = 0
	}
	// A maximum wait below the grace makes the waiter-age clause in
	// graceElapsedLocked unreachable — a waiter would be refused before its
	// own wait could ever override a model's protection — which is the
	// indefinite starvation this whole mechanism exists to prevent.
	// internal/config refuses such a pair at a save and repairs one in a file;
	// this is the pool refusing to be configured into the hole by any caller.
	if maxWait < grace {
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

// errDraining marks the one refusal a caller answers by waiting rather than by
// giving up: the room this load needs exists, but a model server that has been
// stopped is still holding it. It is wrapped around the ordinary no-room
// refusal, so a caller that stops waiting reports what every other full machine
// reports and says nothing on the wire about what the pool is doing.
//
// Unexported: it never leaves this package. Acquire either waits it out or
// returns the *NoRoomError beside it.
var errDraining = errors.New("a stopped model server has not exited yet")

// drainMargin is what a stop is given beyond stopBound before the pool calls
// the process stuck: SIGKILL has been delivered and not landed, so what is
// holding the memory now is the kernel, not the server.
const drainMargin = 5 * time.Second

// maxDrainWait bounds how long one acquisition waits for stopped model servers
// to exit before it gives up and takes the no-room refusal. It is derived from
// the launcher's own bound rather than chosen: a caller should outlast a stop
// that is going to work, and not one that is not.
const maxDrainWait = stopBound + drainMargin

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
//
// It still waits for a model server it has evicted to exit before starting its
// own. That is not a grace: nothing is being protected and no policy is being
// applied, the machine simply has not handed the memory back yet, and starting
// a second server on top of the first is the overlap the budget exists to
// prevent. The wait is bounded by maxDrainWait.
func (p *Pool) AcquireNow(ctx context.Context, repoID string) (*Upstream, func(), error) {
	return p.acquire(ctx, repoID, false)
}

func (p *Pool) acquire(ctx context.Context, repoID string, mayWait bool) (*Upstream, func(), error) {
	src := sourceFrom(ctx)
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
		adm := p.admissionLocked(w)
		if !mayWait {
			age, adm = p.grace, admitEvict
		}

		var err error
		if p.worthTryingLocked(w, age, adm) {
			var started *entry
			// A load that would have to wait for memory to come back must be
			// able to wait before it destroys anything: a model killed for a
			// caller that is then refused served nobody.
			started, err = p.startLocked(repoID, age, adm, p.canParkLocked(mayWait, w, src))
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
			err = p.parkedRefusalLocked(w, age)
		}
		verdict := p.waitVerdictLocked(mayWait, w, src, err)
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
				// Return the refusal itself, never the errDraining sentinel
				// wrapped around it: the gateway writes a pool error's text
				// verbatim into the 503 body every LAN client reads, and what
				// this machine is doing with its own memory is not a client's
				// business.
				err = noRoom
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
			w = &loadWaiter{
				arrived: time.Now(),
				need:    noRoom.need,
				source:  src,
				mayWait: mayWait,
				signal:  make(chan struct{}, 1),
			}
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
	// served records what a queued load is about to be told, for the log line
	// below: how old the waiter was at the moment it got room, against the two
	// figures that bound it. A waiter served at its grace and one served at its
	// maximum are the same success to the client and the opposite outcomes to
	// this queue's fairness rule, and nothing else on this path tells them
	// apart (iss-2609081516178867).
	var servedGrace, servedMax time.Duration
	if w != nil {
		waited = p.waitedBy(w)
		servedGrace, servedMax = p.grace, p.maxWait
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

	// Written off the lock, like the refusal above and for the same reason: a
	// slow log sink must not stall every other caller of the pool's one lock.
	if w != nil {
		p.opts.Log.Debug("a queued model load got room",
			"model", repoID, "waited", waited, "grace", servedGrace, "max_wait", servedMax)
	}

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

// chargeLocked is what a model costs the budget in force. Callers must hold
// p.mu, because the budget it is measured against is the one the pool holds
// there and may be replaced while the pool runs.
//
// One home for the inputs the pool supplies: the decode concurrency every
// server is launched with, which is how many caches one model may be building
// at once, and the budget itself, which is the ceiling on what any single
// model is charged.
func (p *Pool) chargeLocked(m ResolvedModel) int64 {
	return capability.LoadCostOf(capability.Load{
		DiskBytes:       m.Bytes,
		KVBytesPerToken: m.KVBytesPerToken,
		Window:          m.ContextLength,
		Sequences:       int64(p.opts.DecodeConcurrency),
		Budget:          p.maxResident,
	})
}

// startLocked launches a model server. Callers must hold p.mu.
//
// waited is how long the caller has already been queued for room, which is
// what bounds the protection an eviction grace gives; adm is what this caller
// is allowed to do to the models in memory, and is what keeps the queue
// first-in, first-out.
func (p *Pool) startLocked(repoID string, waited time.Duration, adm admission, canPark bool) (*entry, error) {
	m, err := p.opts.Models.Resolve(repoID)
	if err != nil {
		return nil, err
	}
	path, size := m.Path, m.Bytes

	need := p.chargeLocked(m)
	if need > p.maxResident {
		return nil, fmt.Errorf(
			"%s needs about %s of memory but the limit is %s — raise the memory budget or choose a smaller quantization",
			repoID, HumanBytes(need), HumanBytes(p.maxResident))
	}
	// Plan the eviction before the precheck, and refuse from the plan alone.
	// Precheck stats the launcher's files, and this runs under p.mu — the
	// pool's one lock, which every Acquire, release, Resident, Unload and
	// control-panel snapshot takes — so a refusal that did it would let a
	// client set the rate at which this machine does filesystem work under
	// that lock, simply by asking for loads that cannot be served.
	victims, enough := p.evictionPlanLocked(need, waited)
	if p.grace > 0 && (!enough || !allows(adm, victims)) {
		return nil, p.noRoomLocked(need)
	}
	// Stopping a model does not hand its memory back, it moves the charge into
	// the drain tally until the process exits — so any load that has to make
	// room will have to wait for that exit. A caller that cannot wait must not
	// take a victim it will never get to use: with a maximum wait no longer
	// than the grace, the head waiter would otherwise wake at the grace, kill
	// an idle model, and be refused in the same breath.
	if !canPark && p.residentChargeLocked()+need > p.maxResident {
		return nil, p.noRoomLocked(need)
	}
	// Check cheap launch preconditions before evicting anything. Eviction is not
	// reversible (stopEntryLocked's Stop cannot be undone), so if we evicted
	// first and Launch failed moments later — a missing venv, or this repoID's
	// own directory vanishing in a race with a concurrent delete — a healthy,
	// unrelated resident model would be torn down for zero benefit.
	if err := p.opts.Launcher.Precheck(Spec{RepoID: repoID, ModelPath: path}); err != nil {
		return nil, fmt.Errorf("start model server for %s: %w", repoID, &LaunchError{Err: err})
	}
	for _, v := range victims {
		p.stopEntryLocked(v, StopEvicted)
	}
	// With grace off the pool has always freed what it could and then refused,
	// and that is what the off path still does: the plan above is executed
	// whole before this, and only the on path returns before touching
	// anything.
	if !enough {
		return nil, p.noRoomLocked(need)
	}
	// The victims stopped just above — and any model another caller stopped a
	// moment ago — are out of the pool but not out of memory. Launching now is
	// exactly the overlap the budget exists to prevent, so this load waits for
	// those processes to go instead. Bounded: the caller gives up after
	// maxDrainWait and takes the ordinary no-room refusal.
	if p.residentChargeLocked()+need > p.maxResident {
		return nil, fmt.Errorf("%w: %w", errDraining, p.noRoomLocked(need))
	}

	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("allocate port: %w", err)
	}

	e := &entry{
		repoID:   repoID,
		port:     port,
		bytes:    size,
		charge:   need,
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
	stopped := false
	close(e.ready)
	if err != nil {
		// A model that never became ready must not linger in the pool holding a
		// slice of the memory budget. Guard on identity: while this entry was
		// loading it could have been evicted and a *new* entry created under the
		// same repoID key. Deleting by key alone would then orphan that healthy
		// replacement — its process would leak and its memory would stop counting
		// against the budget.
		if p.entries[config.FoldRepoID(e.repoID)] == e {
			// Through the same path an eviction takes, so this server's memory
			// stays charged until its process is gone. A server that never
			// answered its readiness probe is if anything more likely to be
			// wedged holding weights than an idle victim is, and crediting the
			// budget here would admit a replacement on top of it.
			p.stopEntryLocked(e, StopLoadFailed)
			stopped = true
		}
	}
	p.mu.Unlock()

	if err != nil && !stopped && e.proc != nil {
		// Another path took this entry out of the pool while it was loading and
		// owns the stop of its process. Stop it here too rather than rely on
		// that: this path is what would otherwise leak it, and Stop is
		// idempotent. Nothing is charged, because whoever removed the entry
		// charged it.
		stopCtx, stopCancel := context.WithTimeout(context.Background(), stopBound)
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

// allows reports whether a caller with this admission may take this plan.
//
// admitFreeRoom is the narrow one: a caller that could not join the queue
// because it was full may still load into memory nothing is using, but may not
// take a victim. Refusing it would be a denial of service over a place to
// wait that it does not need, and it takes nothing from the waiter at the head
// — that waiter is parked precisely because the free room is not enough for
// it.
func allows(adm admission, victims []*entry) bool {
	switch adm {
	case admitEvict:
		return true
	case admitFreeRoom:
		return len(victims) == 0
	default:
		return false
	}
}

// liveChargeLocked is what the models the pool is holding are charged against
// the memory budget. Callers must hold p.mu.
func (p *Pool) liveChargeLocked() int64 {
	var used int64
	for _, e := range p.entries {
		used += e.charge
	}
	return used
}

// residentChargeLocked is what this machine's memory is actually spoken for:
// every model the pool is holding, plus every one it has stopped that has not
// exited yet — whether that exit is still coming or never will. It is what a
// launch is measured against, because a victim told to go a moment ago is still
// holding its weights. Callers must hold p.mu.
func (p *Pool) residentChargeLocked() int64 {
	return p.evictableChargeLocked() + p.drainBytes
}

// evictableChargeLocked is the charge an eviction plan is measured against: the
// models in memory, plus the servers that will not die. Callers must hold p.mu.
//
// The second is the difference between this and residentChargeLocked, and it is
// the whole point of the split. Memory held by a stuck server is not coming
// back, so a plan that ignored it would refuse every load that needed room for
// the rest of the process's life; counting it lets a load take an idle model
// and be served around the loss. Memory held by a server that is still exiting
// IS coming back, and evicting a healthy model to cover a wait of seconds would
// be destroying something for nothing.
func (p *Pool) evictableChargeLocked() int64 {
	return p.liveChargeLocked() + p.stuckChargeLocked()
}

// evictionPlanLocked names the models that would have to go for need bytes to
// fit, least recently used first, and says whether taking them all is enough.
// Callers must hold p.mu.
func (p *Pool) evictionPlanLocked(need int64, waited time.Duration) ([]*entry, bool) {
	// The models in memory and the servers that will never exit; not the ones
	// that are still exiting. A plan that counted those would name victims to
	// free room a process is about to hand back, killing a healthy model to
	// cover a wait of a few seconds — and a plan that did not count the stuck
	// ones would refuse every load needing room until the app restarted.
	used := p.evictableChargeLocked()
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
		used -= e.charge
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
func (p *Pool) waitVerdictLocked(mayWait bool, w *loadWaiter, src string, err error) waitVerdict {
	// Waiting for a stopped model server to exit is not the eviction grace:
	// nothing is being protected and no policy is being applied, the machine
	// simply has not handed the memory back yet. Every caller waits for that,
	// including one that honours no grace at all — but as a queued waiter,
	// counted by Waiting() and held to both queue caps like everyone else.
	if !errors.Is(err, errDraining) && (!mayWait || p.grace <= 0) {
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
		if !p.queueHasRoomLocked(src) {
			return waitQueueFull
		}
		return waitYes
	}
	if time.Since(w.arrived) >= p.waitBoundLocked(mayWait) {
		return waitTimedOut
	}
	return waitYes
}

// queueHasRoomLocked reports whether another load may join the queue for
// memory. Callers must hold p.mu.
//
// A per-source cap as well as the global one. The global cap alone counts
// REQUESTS, so one caller could fill the queue and deny every cold load that
// needs an eviction to everyone else on the network — eight connections, for as
// long as the maximum wait allows. Counting per source makes filling the queue
// cost one caller its own share and nobody else's.
func (p *Pool) queueHasRoomLocked(src string) bool {
	if len(p.waiters) >= p.opts.MaxLoadWaiters {
		return false
	}
	if p.opts.MaxLoadWaitersPerSource > 0 {
		mine := 0
		for _, other := range p.waiters {
			if other.source == src {
				mine++
			}
		}
		if mine >= p.opts.MaxLoadWaitersPerSource {
			return false
		}
	}
	return true
}

// waitBoundLocked is the longest this caller may stay parked. Callers must hold
// p.mu.
//
// Under a grace that is the maximum wait the operator configured, which is a
// promise about wall-clock seconds made to the client. A caller that honours no
// grace — the start-up preload — and every caller when grace is off can only be
// parked for a stopped server to exit, and that is bounded by the drain bound
// instead: there is no grace to promise anything about, and the wait ends when
// the process does.
func (p *Pool) waitBoundLocked(mayWait bool) time.Duration {
	if mayWait && p.grace > 0 {
		return p.maxWait
	}
	return p.opts.DrainWait
}

// canParkLocked reports whether this caller could park for a drain if the load
// it is about to attempt needed one. Callers must hold p.mu.
//
// It is the same pair of clauses waitVerdictLocked applies to a waiter — a
// place in the queue, and time left on this caller's bound — asked one step
// earlier, because startLocked must not stop a model for a caller that would
// then be refused before the memory came back.
func (p *Pool) canParkLocked(mayWait bool, w *loadWaiter, src string) bool {
	if w == nil {
		return p.queueHasRoomLocked(src)
	}
	return time.Since(w.arrived) < p.waitBoundLocked(mayWait)
}

// parkedRefusalLocked builds the refusal a parked waiter gets without asking
// the registry or the launcher for anything. Callers must hold p.mu.
//
// It says when what stands between this waiter and its memory is a process that
// has not exited: that is a wait every caller may serve out, grace or no grace,
// and the exit is what ends it.
func (p *Pool) parkedRefusalLocked(w *loadWaiter, age time.Duration) error {
	refusal := p.noRoomLocked(w.need)
	if !p.drainBlocksLocked(w.need) {
		return refusal
	}
	if _, enough := p.evictionPlanLocked(w.need, age); !enough {
		// Even with the memory back this load would not fit, so the exit is not
		// what it is waiting for.
		return refusal
	}
	return fmt.Errorf("%w: %w", errDraining, refusal)
}

// drainBlocksLocked reports whether a model server that has not exited is what
// stands between the pool and room for need bytes. Callers must hold p.mu.
func (p *Pool) drainBlocksLocked(need int64) bool {
	return p.drainBytes > 0 && p.residentChargeLocked()+need > p.maxResident
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
//
// It records on the waiter the drain generation it judged against, which is
// what makes the next answer cheap. That is a write, so this is not the pure
// predicate its name suggests; it is called once per pass round the acquire
// loop, which is where the judgement belongs.
func (p *Pool) worthTryingLocked(w *loadWaiter, age time.Duration, adm admission) bool {
	// A caller that has not queued always asks: it has nothing to judge itself
	// by yet.
	if w == nil {
		return true
	}
	// A waiter whose room is still being handed back by an exiting process must
	// not take a victim meanwhile: the machine is already over its budget with
	// memory it has not got back. Nor is there anything for it to learn by
	// resolving the model and stat-ing the launcher's files again while the
	// tally is where it was — the periodic re-check every waiter does still
	// happens, it just costs a comparison here instead of filesystem work under
	// p.mu.
	if w.drainGen == p.drainGen && p.drainBlocksLocked(w.need) {
		return false
	}
	w.drainGen = p.drainGen
	// Every caller asks once grace is off: the off path evicts what it can
	// before it refuses, and skipping the attempt would refuse a request that a
	// swap would have served.
	if p.grace <= 0 {
		return true
	}
	if adm == admitNothing {
		return false
	}
	_, enough := p.evictionPlanLocked(w.need, age)
	return enough
}

// canEverFitLocked reports whether evicting every model that is not pinned
// would leave room for need bytes. Callers must hold p.mu.
func (p *Pool) canEverFitLocked(need int64) bool {
	// A server that would not die is counted with the pinned models: its memory
	// is not coming back while this process lives, so a load that does not fit
	// around it is one no amount of waiting will serve.
	protected := p.stuckChargeLocked()
	for _, e := range p.entries {
		if p.isPinnedLocked(e.repoID) {
			protected += e.charge
		}
	}
	return protected+need <= p.maxResident
}

// admission says what a caller may do to the models in memory to make room for
// its own. It is the whole of the queue's fairness rule.
type admission int

const (
	// admitEvict: take victims. The oldest waiter, anyone at all when nobody
	// is waiting, and any caller once grace is off.
	admitEvict admission = iota
	// admitFreeRoom: load into memory nothing is using, but evict nothing. A
	// caller that cannot join the queue because it is full — refusing it would
	// deny service over a place to wait it does not need, and the request at
	// the head is not waiting for that memory, or the free room would have
	// served it already.
	admitFreeRoom
	// admitNothing: wait your turn.
	admitNothing
)

// admissionLocked places this caller against the queue. Callers must hold
// p.mu.
//
// Only the oldest waiter may evict, which is the whole of the fairness rule —
// and it binds a request that has not queued at all, or a new arrival would
// step over everyone already waiting. With grace off there is no queue to be
// fair to: whatever is parked is being drained, the operator having just said
// "swap now", so every caller takes the ordinary path.
func (p *Pool) admissionLocked(w *loadWaiter) admission {
	if p.grace <= 0 {
		return admitEvict
	}
	if len(p.waiters) == 0 || (w != nil && p.waiters[0] == w) {
		return admitEvict
	}
	if w == nil && len(p.waiters) >= p.opts.MaxLoadWaiters {
		return admitFreeRoom
	}
	return admitNothing
}

// waitedBy is how long a waiter has been parked; zero for a caller that has
// not parked at all.
func (p *Pool) waitedBy(w *loadWaiter) time.Duration {
	if w == nil {
		return 0
	}
	return time.Since(w.arrived)
}

// wakeRecheckFloor is the shortest interval wakeDelayLocked will impose as its
// unconditional re-check. The re-check tracks the grace, and EvictionGrace is
// an operator setting with no lower bound: a grace of a millisecond would
// otherwise have every parked waiter taking the pool's one lock a thousand
// times a second. A quarter-second is far below any wait a person notices and
// far above any rate that matters. It bounds only the re-check — a real
// deadline this function can compute, an idle model's grace running out or the
// waiter's own age reaching it, is still slept to exactly.
const wakeRecheckFloor = 250 * time.Millisecond

// wakeDelayLocked is how long a waiter may sleep before it looks again.
// Callers must hold p.mu.
//
// Three of its terms are moments this function can compute, and it sleeps to
// the nearest: the waiter's own age reaching the grace, an idle candidate's
// grace running out, and the maximum wait expiring.
//
// The fourth is a bound rather than a moment, and it is why this is not simply
// a deadline calculator. Every change that could free room does signal — a
// release, a stop, a failed load, a crashed process, a budget or pin change —
// and the waiter is queued and its delay computed under p.mu with a buffered
// signal channel, so a wake arriving between the unlock and the select is taken
// rather than lost. That is the mechanism, and this is its backstop. A backstop
// whose own terms rest on the mechanism it backs up is not one, so a parked
// waiter also looks again at least once per grace whatever the entries look
// like (iss-2609081516178867).
//
// The sharpest case for that, and the one that found it, is a candidate with a
// request in flight or still loading: those states end on an event, not at a
// moment, so the loop below rightly reads no deadline from them — and a waiter
// past its own grace behind a single busy candidate was then left with no term
// at all and fell back to the whole maximum wait. It was woken in practice,
// by that request ending; it was one missing signal away from not being.
//
// wakeRecheckFloor keeps the re-check from becoming a spin on a very short
// grace, and the millisecond floor keeps a stopped clock from spinning.
//
// The bound the first term is measured against is this waiter's own — see
// waitBoundLocked — and not p.maxWait. With grace off, which is the default,
// that maximum is zero: a waiter for a stopped server's memory is held to the
// drain bound instead, and computing its sleep from the wrong figure gave a
// negative delay that no later term could shorten, so every such waiter fell
// through to the millisecond floor and re-took p.mu a thousand times a second
// for as long as it waited.
func (p *Pool) wakeDelayLocked(w *loadWaiter) time.Duration {
	waited := time.Since(w.arrived)
	delay := p.waitBoundLocked(w.mayWait) - waited
	if own := p.grace - waited; own > 0 && own < delay {
		delay = own
	}
	// The re-check is a ceiling on any sleep, and the answer outright for a
	// waiter whose bound has already run out: it is about to be refused on its
	// next pass, and the delay must not be a negative number that only the
	// floor catches.
	if recheck := max(p.grace, wakeRecheckFloor); delay <= 0 || recheck < delay {
		delay = recheck
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
//
// The entry leaves the pool at once — it must never serve another request, and
// it must not be a candidate for a second stop — but its memory does not come
// back until the process does. Until then the charge sits in the drain tally,
// where every admission decision still counts it.
func (p *Pool) stopEntryLocked(e *entry, reason StopReason) {
	delete(p.entries, config.FoldRepoID(e.repoID))
	p.notify(func(o PoolObserver) { o.EntryStopped(e.repoID, reason) })
	proc := e.proc
	if proc == nil {
		// Nothing is holding the memory, so nothing has to be waited for.
		p.wakeWaitersLocked()
		return
	}
	charge := e.charge
	p.drainBytes += charge
	p.drainGen++
	// A prod, not a promise of room: a waiter re-reads the pool when it comes
	// back and will find this charge still counted until the process exits.
	p.wakeWaitersLocked()
	go p.drainEntry(e.repoID, proc, charge)
}

// drainEntry stops a model server that has left the pool and gives its charge
// back when the process is gone. Runs off p.mu.
func (p *Pool) drainEntry(repoID string, proc Process, charge int64) {
	ctx, cancel := context.WithTimeout(context.Background(), stopBound)
	stopErr := proc.Stop(ctx)
	cancel()

	// Stop returning is not the process being gone: it gives up on a server
	// that will not die even after SIGKILL, and such a server is still holding
	// its memory. Done is the only honest signal, so the charge waits for it —
	// but not for ever. Past the drain bound the kernel has not let go, and
	// this goroutine stops waiting rather than living as long as the app: the
	// charge moves to the stuck list, which no load waits for and every
	// eviction plan counts, and the operator is told which process took it.
	timer := time.NewTimer(p.opts.DrainWait)
	select {
	case <-proc.Done():
		timer.Stop()
		p.mu.Lock()
		p.drainBytes -= charge
		p.drainGen++
		// Every load parked for this memory is woken to look again.
		p.wakeWaitersLocked()
		p.mu.Unlock()
		return
	case <-timer.C:
	}

	p.mu.Lock()
	p.drainBytes -= charge
	id := p.nextStuck
	p.nextStuck++
	p.stuck[id] = stuckServer{repoID: repoID, pid: proc.Pid(), charge: charge}
	p.drainGen++
	// Nothing is going to be woken by this memory coming back, so wake every
	// waiter now: one that could be served by evicting an idle model instead
	// should go and do that rather than sit out a wait for an exit that may
	// never come.
	p.wakeWaitersLocked()
	p.mu.Unlock()

	// Logged off the lock: p.mu is the pool's one lock, and a slow sink would
	// stall every other caller for the length of a write.
	p.opts.Log.Warn("a stopped model server has not exited; its memory stays charged against the budget until it does",
		"model", repoID, "pid", proc.Pid(), "charged", HumanBytes(charge),
		"waited", p.opts.DrainWait, "stop_error", stopErr)

	// Keep watching, with nothing else to do and nobody waiting on it. A
	// process the kernel reaps late — minutes later, at shutdown, whenever —
	// still closes Done, and its memory is as real as anyone else's: dropping
	// the watch here would charge the budget for it until Gropius restarted,
	// however long ago it actually went.
	<-proc.Done()

	p.mu.Lock()
	delete(p.stuck, id)
	p.drainGen++
	p.wakeWaitersLocked()
	p.mu.Unlock()
	p.opts.Log.Info("a model server that would not stop has now exited; its memory is back",
		"model", repoID, "pid", proc.Pid(), "charged", HumanBytes(charge))
}

// Residency is one consistent answer to what this pool is holding: the models
// it has, and the memory it has not got back. Taken under a single lock, so a
// stop landing between two reads cannot make a caller count the same server
// twice or miss it altogether.
type Residency struct {
	// Models is what the pool is holding, most recently used first.
	Models []Resident
	// ExitingBytes is charged to model servers that have left the pool and
	// whose processes have not gone: they appear in no models list, but their
	// memory is not back and a load is measured against it.
	ExitingBytes int64
	// StuckServers is how many of those did not exit even after SIGKILL. Their
	// memory is held until this process restarts.
	StuckServers int
}

// Residency reports the models in memory and the memory not yet handed back.
//
// The control panel reads this rather than Resident alone: a stuck server
// shrinks the budget for as long as it lives, and a panel that showed only the
// models it is holding would report room the pool will not give out.
func (p *Pool) Residency() Residency {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Residency{
		Models:       p.residentLocked(),
		ExitingBytes: p.drainBytes + p.stuckChargeLocked(),
		StuckServers: len(p.stuck),
	}
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
	return p.residentLocked()
}

// residentLocked is Resident's body, so that a caller needing the models and
// the memory not yet handed back gets both from one lock acquisition. Callers
// must hold p.mu.
func (p *Pool) residentLocked() []Resident {
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
			Charge:   e.charge,
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
	stuck := p.stuckServersLocked()
	// Every parked request is answered rather than left holding a connection
	// while the process goes away; each sees p.closed and returns ErrClosed.
	p.wakeWaitersLocked()
	p.mu.Unlock()

	// A server that outlived SIGKILL outlives us too, and it is still holding
	// its memory. Name the process groups on the way out: the crash-recovery
	// ledger is what the next start reads, and an operator reading this log is
	// being told why a restart got the memory back.
	for _, s := range stuck {
		p.opts.Log.Warn("shutting down while a stopped model server is still running; it holds its memory until it goes",
			"model", s.repoID, "pid", s.pid, "charged", HumanBytes(s.charge))
	}

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

// sourceKey types the context value carrying a caller's identity.
type sourceKey struct{}

// WithSource tags a request context with the caller's identity, which the pool
// uses to share the load-waiter queue out rather than serve it first-come.
//
// The value is the presented API key. It is carried in the context rather than
// added to Acquire's signature because it is request-scoped metadata that only
// one decision consults, and threading it through every call site and test
// would say the pool needs an identity to load a model. It does not; it needs
// one to decide whose turn it is when there is no room.
func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceKey{}, source)
}

// sourceFrom returns the caller identity tagged onto ctx, or "" for a caller
// that carries none — a loopback client, or any caller on an unkeyed server.
// All of them share one bucket, which is the honest answer: an unauthenticated
// endpoint cannot tell its callers apart.
func sourceFrom(ctx context.Context) string {
	s, _ := ctx.Value(sourceKey{}).(string)
	return s
}
