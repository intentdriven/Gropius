// Package app is the composition root: it wires the HuggingFace client, the
// model registry, the process pool and the provisioner into one object that the
// gateway and the UI drive.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/intentdriven/Gropius/internal/capability"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/hub"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
	"github.com/intentdriven/Gropius/internal/stats"
)

// App holds everything the daemon needs.
type App struct {
	Paths       config.Paths
	Hub         *hub.Client
	Registry    *registry.Registry
	Pool        *runtime.Pool
	Provisioner *runtime.Provisioner
	// Stats holds the live view of what this Mac has served, and holds nothing
	// at all until the operator turns recording on (adr-2609061503319212).
	Stats *stats.Recorder
	// StatsStore is where those records outlive the process. It is under the
	// same switch as the live view and no other, and it creates nothing —
	// not a file, not its own directory — until that switch is on.
	StatsStore *stats.FileStore
	Log        *slog.Logger

	// machineRAM is how much memory this Mac has, or 0 when that cannot be
	// read. Read once, at construction: a machine does not grow while the
	// process runs, and every figure derived from it — the default budget, the
	// share Settings shows, the ceiling a save is checked against — has to be
	// the same figure or they contradict each other on screen.
	machineRAM int64

	// saveMu serialises whole settings saves. cfgMu guards the value; this
	// guards the sequence — write the file, swap the value, tell the pool —
	// which is not one step and must not interleave with another save's, or the
	// pool ends up enforcing a pinned set that the file, the panel and
	// /v1/models all say does not exist.
	//
	// It is a lock of its own rather than cfgMu held wider, because cfgMu
	// cannot be held across the pool setter: startLocked runs under the pool's
	// p.mu and calls SamplingFor, which takes cfgMu.RLock, so p.mu -> cfgMu is
	// an established order and cfgMu -> p.mu would invert it. Nothing taken
	// under p.mu or cfgMu takes saveMu, so it adds no order at all.
	saveMu sync.Mutex

	cfgMu sync.RWMutex
	cfg   config.Config

	// dlMu guards in-flight downloads so a repo cannot be downloaded twice at
	// once, and so a download can be cancelled from the UI.
	dlMu      sync.Mutex
	downloads map[string]*download
	// dlWG lets Close wait for cancelled downloads to actually stop writing.
	dlWG sync.WaitGroup
}

// download is one in-flight fetch.
type download struct {
	// repoID is the spelling the download runs under (the registry's
	// canonical one); the map key is its case-folded form.
	repoID string
	cancel context.CancelFunc
	// done closes when the goroutine has stopped touching the model directory.
	// Cancelling only *asks* it to stop; callers that are about to delete those
	// files must wait for this.
	done chan struct{}
}

// Options builds an App.
type Options struct {
	Paths  config.Paths
	Config config.Config
	Log    *slog.Logger
	// PhysicalMemory reads how much memory this Mac has. Nil — the shipping
	// case — means capability.PhysicalMemory, which asks the system. It is a
	// seam because every claim Settings makes about the memory budget is a
	// claim about a figure this process cannot otherwise choose, including the
	// claim it makes when the figure cannot be read at all.
	PhysicalMemory func() int64
	// Launcher starts model server processes. Nil — the shipping case — means
	// the real one, which runs mlx_lm.server out of the managed virtualenv.
	// It is a seam because the wiring between a saved configuration and a
	// model server's command line runs through this composition root, and a
	// test that cannot see a launched process cannot see whether that wiring
	// is connected.
	Launcher runtime.Launcher
}

// New wires the application together.
func New(opts Options) (*App, error) {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if err := opts.Paths.EnsureDirs(); err != nil {
		return nil, err
	}

	reg, err := registry.Open(opts.Paths.State)
	if err != nil {
		return nil, err
	}
	// Adopt whatever is already on disk. This is what lets a second macOS user
	// account — or a reinstall — pick up models without re-downloading them.
	if err := reg.Rescan(opts.Paths.Models); err != nil {
		opts.Log.Warn("could not scan the models directory", "err", err)
	}
	// A download in flight when the previous process died is left recorded as
	// "downloading" with no goroutine behind it. Mark such orphans failed so the
	// UI offers Retry/Remove instead of a Cancel button that cannot work.
	if interrupted := reg.ReconcileInterrupted(); len(interrupted) > 0 {
		opts.Log.Warn("marked interrupted downloads as failed", "models", interrupted)
	}

	hc := hub.New()
	hc.Token = opts.Config.HFToken

	store := stats.NewStore(opts.Paths.Stats, stats.StoreOptions{
		Months:   opts.Config.StatsMonths,
		MaxBytes: opts.Config.StatsMaxBytes,
		Log:      opts.Log,
	})
	readMemory := opts.PhysicalMemory
	if readMemory == nil {
		readMemory = capability.PhysicalMemory
	}

	a := &App{
		machineRAM:  readMemory(),
		Paths:       opts.Paths,
		Hub:         hc,
		Registry:    reg,
		Provisioner: runtime.NewProvisioner(opts.Paths),
		Stats:       stats.New(stats.Options{Store: store}),
		StatsStore:  store,
		Log:         opts.Log,
		cfg:         opts.Config,
		downloads:   map[string]*download{},
	}

	launcher := opts.Launcher
	if launcher == nil {
		exec := &runtime.ExecLauncher{
			Paths:  opts.Paths,
			LogDir: opts.Paths.Logs,
		}
		// Kill any model servers left running by a previous run that crashed
		// before it could stop them — otherwise they hold GPU memory until
		// reboot.
		if killed := exec.ReapOrphans(); killed > 0 {
			opts.Log.Warn("reaped model servers left over from a previous run", "count", killed)
		}
		launcher = exec
	}

	// Settings read from disk have not been through SetConfig's checks, so the
	// pinned list is folded onto the registry's spellings before anything sees
	// it — the pool, the panel and the next save all join on these strings.
	a.cfg.Pinned = a.adoptPinned(a.cfg.Pinned)

	grace, maxWait := evictionGraceFor(opts.Config)
	// Held down here for the same reason SetConfig holds it down below: the
	// pool must never protect a model for longer than the reaper leaves it
	// alone. At a start the two agree, since config.Validate holds a save to
	// the idle timeout it saves and Load repairs a file that does not; this is
	// belt and braces on the one invariant the record wrote a refusal for.
	grace = clampGraceToIdle(grace, time.Duration(opts.Config.IdleTimeoutSec)*time.Second)
	a.Pool = runtime.NewPool(runtime.PoolOptions{
		Launcher:    launcher,
		Models:      modelSource{reg},
		IdleTimeout: time.Duration(opts.Config.IdleTimeoutSec) * time.Second,
		// Resolved here rather than left to the pool, so that the budget the
		// pool enforces, the ceiling a save is checked against and the share
		// Settings shows are all worked out from one reading of this machine.
		MaxResidentBytes:  a.enforcedBudget(opts.Config.MaxResidentBytes),
		DecodeConcurrency: opts.Config.DecodeConcurrency,
		// Read live, at the moment a model server starts, so a default saved in
		// Settings applies the next time each model loads.
		SamplingFor: func(repoID string) config.Sampling {
			return a.Config().EffectiveSampling(repoID)
		},
		// The pool reports loads and removals to the recorder, which ignores
		// them while recording is off. Adapting here keeps internal/stats a
		// leaf package that imports nothing of ours.
		Observer:        poolObserver{rec: a.Stats, log: opts.Log},
		Pinned:          a.cfg.Pinned,
		EvictionGrace:   grace,
		MaxEvictionWait: maxWait,
		Log:             opts.Log,
		// One caller may hold at most a quarter of the queue, so filling it
		// costs that caller its own share and nobody else's.
		MaxLoadWaitersPerSource: 2,
	})
	a.applyStatistics(opts.Config)

	// Settings read from disk have not been through SetConfig's checks: the
	// file can be hand-edited, restored from a backup, or written by another
	// build. Fold them onto the registry's spellings here, where the log
	// exists to say what was dropped.
	a.cfg.PerModel = a.adoptPerModel(a.cfg.PerModel)

	// The fit check cannot refuse a file — a hand-edited one can pin anything —
	// so an over-budget set reaches the pool whatever this says. Passing the
	// same list as both the incoming and the current set is what says "nothing
	// was added here": every problem it finds is warned about, none refused.
	budget := a.Pool.MemoryBudget()
	_ = a.checkPinnedFit(a.cfg.Pinned, a.cfg.Pinned, budget, budget)

	// The ceiling cannot refuse a file, so a budget larger than this Mac is
	// applied and said out loud — the one place a headless install says
	// anything at all. Passing the stored figure as its own current value is
	// what says "nothing was raised here"; what the pool got is the clamp.
	if err := a.checkBudgetFitsTheMachine(a.effectiveBudget(a.cfg.MaxResidentBytes), 0); err != nil {
		a.Log.Warn("the memory budget is larger than this Mac; models are held to what it has",
			"err", err, "enforced", runtime.HumanBytes(budget))
	}

	if len(opts.Config.Preload) > 0 {
		go a.preload(opts.Config.Preload)
	}

	return a, nil
}

// preload warms the configured models so the first real request after a restart
// finds them resident rather than paying a cold start.
//
// Sequential on purpose: several parallel loads would each pin themselves
// (inFlight > 0) before the next tried to evict, so the pool's memory budget
// could find no evictable victim and fail them all. One at a time lets each load
// finish (and, if the budget is tight, be evicted in LRU order) cleanly.
func (a *App) preload(ids []string) {
	for _, id := range ids {
		if !config.ValidRepoID(id) {
			a.Log.Warn("skipping invalid preload model id", "model", id)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), runtime.ProbeTimeout)
		// AcquireNow, not Acquire: this loop is sequential, so a preload list
		// of models that cannot all fit would stall the start by one maximum
		// wait per model — and at start-up there is nobody to protect, since
		// no client has been served yet.
		_, release, err := a.Pool.AcquireNow(ctx, id)
		if err != nil {
			a.Log.Warn("preload failed", "model", id, "err", err)
			cancel()
			continue
		}
		release()
		cancel()
		a.Log.Info("preloaded model", "model", id)
	}
}

// Config returns the current settings.
func (a *App) Config() config.Config {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.cfg
}

// SetConfig persists new settings.
//
// Bind address, port and decode concurrency only take effect on restart: the
// listener and the model servers are already running with the old values, and
// silently pretending otherwise would be worse than saying so.
func (a *App) SetConfig(c config.Config) error {
	a.saveMu.Lock()
	defer a.saveMu.Unlock()

	if err := c.Validate(); err != nil {
		return err
	}
	perModel, err := a.canonicalPerModel(c.PerModel)
	if err != nil {
		return err
	}
	c.PerModel = perModel
	c.Pinned = a.canonicalPinned(c.Pinned)
	// Judged on what the operator asked for, applied as what this Mac can hold:
	// the checks are about their figure, the pool is bounded by the machine.
	asked := a.effectiveBudget(c.MaxResidentBytes)
	inForce := a.effectiveBudget(a.Config().MaxResidentBytes)
	if err := a.checkBudgetFitsTheMachine(asked, inForce); err != nil {
		return err
	}
	budget := a.enforcedBudget(c.MaxResidentBytes)
	if err := a.checkPinnedFit(c.Pinned, a.Config().Pinned, budget, a.Pool.MemoryBudget()); err != nil {
		return err
	}
	if err := config.Save(a.Paths.Config, c); err != nil {
		return err
	}
	a.cfgMu.Lock()
	a.cfg = c
	a.cfgMu.Unlock()

	// The switch applies to the next request, not to the next start. Turning
	// it off also empties what was recorded in memory, which is what makes
	// "off" the same state as a fresh start rather than a hidden one; the
	// records already on disk stay where they are, because switching recording
	// off is asking for it to stop, not for a history to be destroyed.
	a.applyStatistics(c)

	a.Hub.Token = c.HFToken
	// Applied live, so a model already in memory is protected from the next
	// eviction rather than from the one after a restart. The pool takes its own
	// lock, the one both eviction paths hold while they read the set.
	a.Pool.SetPinned(c.Pinned)
	// Applied live for the same reason, and it unloads nothing: a lowered
	// budget governs the next load, so no model is pulled out from under the
	// operator at the moment they pressed Save.
	a.Pool.SetMemoryBudget(budget)
	// Applied live for the same reason again: switching grace on protects the
	// models already in memory, and switching it off releases the requests
	// already waiting rather than leaving them to sit out a grace nobody wants
	// any more.
	a.Pool.SetEvictionGrace(a.enforcedGrace(c))
	return nil
}

// evictionGraceFor turns the stored settings into the two intervals the pool
// enforces, at start-up and at every save alike, so "is grace on" has one
// answer.
//
// Off is a grace of zero rather than a flag of its own: the pool has one
// question to answer on the path of every load, and "is the grace non-zero" is
// that question. The two figures stay stored while the switch is off, so
// turning it back on restores what the operator chose.
func evictionGraceFor(c config.Config) (grace, maxWait time.Duration) {
	if !c.EvictionGrace {
		return 0, 0
	}
	return time.Duration(c.GraceSeconds()) * time.Second,
		time.Duration(c.MaxWaitSeconds()) * time.Second
}

// enforcedGrace is what the pool is given: the operator's figures, with the
// grace held down to the idle timeout the pool is actually reaping on.
//
// config.Validate holds a save to the idle timeout it saves, but the idle
// timeout only reaches the pool at a restart while the grace is applied live,
// so between a save that raises the timeout and that restart the two disagree.
// The pool must not be where that disagreement shows: a grace longer than the
// idle timeout in force means the reaper unloads the very model a request is
// waiting on, which is the one thing this record wrote a refusal for.
//
// Clamped rather than refused, following enforcedBudget: the operator's own
// figures stay stored as they wrote them, and a save that changes something
// else is never refused over a pair this Mac is not yet able to honour. The
// operator restarts and gets what they asked for.
func (a *App) enforcedGrace(c config.Config) (grace, maxWait time.Duration) {
	grace, maxWait = evictionGraceFor(c)
	held := clampGraceToIdle(grace, a.Pool.IdleTimeout())
	if held != grace {
		a.Log.Warn("the eviction grace is longer than the idle timeout this Gropius is running with; models are protected for the shorter figure until a restart",
			"grace", grace, "idle_timeout", a.Pool.IdleTimeout(), "enforced", held)
	}
	return held, maxWait
}

// clampGraceToIdle is the rule itself, with no logging, so both callers apply
// exactly one of it. A zero idle timeout means nothing is reaped, and then no
// grace is too long.
func clampGraceToIdle(grace, idle time.Duration) time.Duration {
	if grace > 0 && idle > 0 && grace > idle {
		return idle
	}
	return grace
}

// MachineRAM is how much memory this Mac has, or 0 when that cannot be read.
// Settings shows the budget as a share of it, and a save is checked against
// it; both say nothing rather than guess when it is 0.
func (a *App) MachineRAM() int64 { return a.machineRAM }

// effectiveBudget resolves a stored budget to the figure the pool enforces:
// zero — what a fresh install stores — means the default share of this Mac's
// memory.
//
// Every figure the operator is shown or measured against goes through here, so
// they all come from the one reading of this machine that App holds. The pool
// and capability.Assess apply the same zero rule for a caller that reaches them
// directly (a test, mostly), and resolve it from a fresh reading of the machine
// rather than from that one; App never takes those branches, because it always
// passes a positive figure.
func (a *App) effectiveBudget(stored int64) int64 {
	if stored > 0 {
		return stored
	}
	return capability.DefaultBudget(a.machineRAM)
}

// enforcedBudget is what the pool is given: the resolved budget, held down to
// the memory this Mac actually has.
//
// The stored figure is not rewritten and the save path judges the operator's
// own number — this bounds what gets *enforced*. A budget above physical memory
// is not satisfiable anyway, so nothing legitimate is lost, and without the
// bound a figure planted in a settings file another local account can write
// (the shared install) would have the pool admitting every model a client names
// until the machine swaps, across restarts, and a panel whose own save cannot
// replace that account's file could not clear it.
//
// A Mac whose memory could not be read has nothing to hold the figure down to;
// that residual is what the conservative unmeasured default and the panel
// warning are for.
func (a *App) enforcedBudget(stored int64) int64 {
	budget := a.effectiveBudget(stored)
	if a.machineRAM > 0 && budget > a.machineRAM {
		return a.machineRAM
	}
	return budget
}

// checkBudgetFitsTheMachine refuses a save that raises the budget above this
// Mac's memory.
//
// It is checked here, at a save, and deliberately not in config.Validate:
// Validate runs at every read, and a config that fails it takes the whole
// install down to loopback with the shipping defaults — so a config.json
// carried from a 128 GB Mac to a 64 GB one would silently reset the bind
// address and the API key along with the budget. A machine whose memory cannot
// be read has no ceiling to check against, so the figure is taken at its word.
//
// What is refused is a save that makes it worse, for the reason checkPinnedFit
// gives: a figure already in force can have arrived from a larger Mac, or from
// a start where sysctl could not read this one, and a settings page that will
// not save an API key until an unrelated memory figure is fixed is the wedge
// this repository has now met twice. Such a budget is warned about at start-up
// and on the panel, and the pool is enforcing it either way — refusing the save
// protects nothing and blocks the operator from closing an open endpoint.
func (a *App) checkBudgetFitsTheMachine(budget, current int64) error {
	if a.machineRAM <= 0 || budget <= a.machineRAM || budget <= current {
		return nil
	}
	return fmt.Errorf(
		"a memory budget of %s is more than this Mac has (%s) — models can only be held in the memory that exists",
		runtime.HumanBytes(budget), runtime.HumanBytes(a.machineRAM))
}

// warnAbovePercent is the share of this Mac's memory beyond which a budget is
// advice rather than arithmetic. It is a threshold on a warning, not a limit:
// the operator is the one who knows what else this Mac runs.
const warnAbovePercent = 85

// BudgetWarnAbove is the budget above which the panel warns, or 0 when this
// Mac's memory cannot be read and there is nothing to take a share of.
func (a *App) BudgetWarnAbove() int64 {
	if a.machineRAM <= 0 {
		return 0
	}
	return a.machineRAM * warnAbovePercent / 100
}

// MemoryBudgetWarning is what the control panel says about a budget at either
// end of its range, and "" for one in the middle.
//
// Advice rather than a refusal at both ends. At the top, what a Mac can carry
// is not a figure Gropius knows: a loaded model is charged its weights and a
// fifth, and not the cache a long conversation adds (iss-3), so a machine fully
// committed on paper can still run out under load — and equally, a Mac that
// runs nothing else can carry more than the default share. At the bottom, a
// budget under the smallest model's charge refuses every request and hides
// every model from the search tab, and the operator should hear that from the
// panel rather than from the first client to be turned away.
func (a *App) MemoryBudgetWarning() string {
	if w := a.tooSmallWarning(); w != "" {
		return w
	}
	above := a.BudgetWarnAbove()
	if above <= 0 {
		return ""
	}
	budget := a.Pool.MemoryBudget()
	if budget <= above {
		return ""
	}
	return fmt.Sprintf(
		"The memory budget (%s) is most of this Mac's memory (%s). macOS and everything else running share it, and a model is charged what it loads rather than what a long conversation adds to it, so requests can still run the machine out of memory.",
		runtime.HumanBytes(budget), runtime.HumanBytes(a.machineRAM))
}

// tooSmallWarning is the bottom end of that range: a budget that cannot hold
// the smallest model on this Mac.
//
// A warning rather than a floor — refusing the save is the wedge this setting
// has been through twice — and a Mac with no models on it yet has no figure to
// judge against, so it says nothing.
func (a *App) tooSmallWarning() string {
	smallest, id := a.smallestChargeableModel()
	if smallest <= 0 {
		return ""
	}
	budget := a.Pool.MemoryBudget()
	if budget >= smallest {
		return ""
	}
	return fmt.Sprintf(
		"The memory budget (%s) is smaller than the smallest model on this Mac (%s needs about %s), so every request is refused until it is raised.",
		runtime.HumanBytes(budget), id, runtime.HumanBytes(smallest))
}

// smallestChargeableModel is the least a model on this Mac would cost the
// budget, and which model that is. It walks the registry the way pinnedCharge
// does, charges what the pool charges, and counts only a model the pool could
// actually load.
func (a *App) smallestChargeableModel() (int64, string) {
	var smallest int64
	var id string
	for _, m := range a.Registry.List() {
		if !chargeable(m) {
			continue
		}
		size := chargedSize(m)
		if size <= 0 {
			continue
		}
		if cost := runtime.LoadCost(size); smallest == 0 || cost < smallest {
			smallest, id = cost, m.RepoID
		}
	}
	return smallest, id
}

// canonicalPinned rewrites each pinned id to the registry's spelling of the
// model it names, for the reason canonicalPerModel does: that spelling is the
// one the operator sees everywhere else, the one the panel joins its boxes on,
// and the one Settings shows back to them. A pin for a model this machine does
// not have is kept as it was typed, so a model can be pinned before it is
// downloaded.
//
// Unlike the per-model maps, nothing here can fail: config.Validate has already
// refused an entry that is not a well-formed repo id and refused two spellings
// of one model, and folding onto the registry cannot turn two distinct ids into
// one — two ids that fold differently name different models.
func (a *App) canonicalPinned(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, id := range in {
		out = append(out, a.canonicalModelKey(id))
	}
	return out
}

// adoptPinned is canonicalPinned for a list read from disk rather than
// submitted through Settings: an entry it cannot use is dropped and named in
// the log, the way an unusable per-model key is, rather than refused.
//
// Without this pass a pin spelled in another case than the registry's protects
// the model — the pool folds — while the panel, which joins its boxes on the
// exact string, draws it unticked. Ticking that box then posts both spellings,
// which Validate refuses as two names for one model, and every settings change
// there is, the API key included, is refused with it until someone edits the
// file by hand.
func (a *App) adoptPinned(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	var dropped []string
	for _, id := range in {
		if !config.ValidRepoID(id) {
			dropped = append(dropped, id)
			continue
		}
		canonical := a.canonicalModelKey(id)
		if seen[config.FoldRepoID(canonical)] {
			dropped = append(dropped, id)
			continue
		}
		seen[config.FoldRepoID(canonical)] = true
		out = append(out, canonical)
	}
	if len(dropped) > 0 {
		a.Log.Warn("dropped pinned models whose id is not a well-formed model id, or names one another entry already names",
			"models", dropped)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// checkPinnedFit refuses a save that pins more than can be in memory at once.
//
// A pinned model is never evicted, so a set that overshoots the budget does not
// fail at save time by itself — it fails later, as a refusal of every request
// for a model that is not pinned, with no hint of why.
//
// What is refused is a save that makes the set worse: one that adds a pin, and
// one that lowers the memory budget under it. A set the operator did not touch,
// under a budget they did not lower, is accepted and warned about however badly
// it fits, because the fit is a fact about this Mac and the set may have
// arrived from another one — and a settings page that will not save an API key
// until an unrelated setting is fixed is the wedge adoptPinned exists to
// prevent. The budget is judged at its incoming value, so one save that changes
// both the pins and the budget is measured on what it is asking for.
//
// A pin that names a model this Mac cannot measure is refused as it is added,
// for the same reason: a fit check that silently skips a model is a promise it
// cannot keep. One already in the set is warned about, not refused.
func (a *App) checkPinnedFit(incoming, current []string, budget, currentBudget int64) error {
	problem := a.pinnedFitProblem(incoming, budget)
	if problem == nil {
		return nil
	}
	if addsAPin(incoming, current) || a.lowersUnderAFittingSet(incoming, budget, currentBudget) {
		return problem
	}
	a.Log.Warn("the pinned models cannot all be kept in memory as configured", "err", problem)
	return nil
}

// lowersUnderAFittingSet reports whether this save is what stops the pinned set
// fitting — a budget lowered under a set that the budget in force could hold.
//
// A set that fits neither figure is inherited, not caused here, and the panel
// posting a figure a few bytes under the one in force (it renders gigabytes) is
// not the operator asking for less. Judging on the bare comparison turned both
// into refusals of every settings change there is, which is the wedge again.
func (a *App) lowersUnderAFittingSet(incoming []string, budget, currentBudget int64) bool {
	return budget < currentBudget && a.pinnedFitProblem(incoming, currentBudget) == nil
}

// pinnedFitProblem says why a pinned set cannot be held, or nil when it can.
//
// A model it cannot measure is reported before the sum, because a sum with a
// model missing from it is not a figure to act on.
func (a *App) pinnedFitProblem(pinned []string, budget int64) error {
	sum, unsized := a.pinnedCharge(pinned)
	if len(unsized) > 0 {
		return fmt.Errorf(
			"cannot measure %s against the memory budget — this Mac does not record how large it is",
			strings.Join(unsized, ", "))
	}
	if sum > budget {
		return fmt.Errorf(
			"the pinned models need about %s of memory but the budget is %s — pin fewer models, or choose smaller quantizations",
			runtime.HumanBytes(sum), runtime.HumanBytes(budget))
	}
	return nil
}

// applyStatistics puts the switch and the two retention figures into effect,
// on the live view and on the store together.
//
// One function, called from the composition root and from every save, because
// the recorder and the store must never disagree about whether recording is
// on: two switches kept in step by hand is exactly how a store comes to hold
// records made after someone switched recording off.
func (a *App) applyStatistics(c config.Config) {
	a.StatsStore.SetRetention(c.StatsMonths, c.StatsMaxBytes)
	if !c.Statistics {
		a.Stats.SetEnabled(false)
		_ = a.StatsStore.SetEnabled(false)
		return
	}
	// A store that cannot be opened is not a reason to stop serving, or even
	// to stop recording: the figures stay in memory, where they were before
	// there was a store at all. It says so once, in SetEnabled.
	_ = a.StatsStore.SetEnabled(true)
	a.Stats.SetEnabled(true)
	a.Stats.RecordSettings(a.effectiveSettings(c))
}

// effectiveSettings is what Gropius is actually serving under, which is not
// always what is saved: the memory budget, decode concurrency and the idle
// timeout are read once when the pool is built and a change to any of them
// takes a restart. Recording a saved value that is not yet in force would put
// a reader's "before and after I changed this" line in the wrong place.
func (a *App) effectiveSettings(c config.Config) stats.Settings {
	return stats.Settings{
		At:                time.Now().UTC().Unix(),
		BudgetBytes:       a.Pool.MemoryBudget(),
		DecodeConcurrency: a.Pool.DecodeConcurrency(),
		IdleTimeoutSec:    int(a.Pool.IdleTimeout() / time.Second),
		Months:            c.StatsMonths,
		MaxBytes:          c.StatsMaxBytes,
	}
}

// ClearStats throws away everything recording has produced: the files and the
// live view both. It is the only thing that removes a record, and it leaves
// recording on — a person clearing the figures is discarding what was
// collected, not changing their mind about collecting it.
func (a *App) ClearStats() error {
	// Said out loud in the log. On a Mac several people log into, the control
	// plane asks nobody for a password, so the person who cleared the records
	// is not necessarily the person who recorded them — and a destructive
	// action nothing anywhere notes is one nobody can ask about afterwards.
	a.Log.Info("clearing the request statistics kept on this Mac")
	a.Stats.Clear()
	return a.StatsStore.Clear()
}

// poolObserver adapts the pool's reports onto the recorder. The pool names its
// own reasons and the recorder names its own; this is the one place that has
// to know both.
type poolObserver struct {
	rec *stats.Recorder
	log *slog.Logger
}

func (o poolObserver) LoadStarted(repoID string) { o.rec.LoadStarted(repoID) }

func (o poolObserver) LoadFinished(repoID string, took time.Duration, err error) {
	o.rec.LoadFinished(repoID, took, err)
}

func (o poolObserver) EntryStopped(repoID string, reason runtime.StopReason) {
	mapped, ok := stopReasons[reason]
	if !ok {
		// The two vocabularies are declared in two packages and a cast between
		// them would have gone on agreeing forever after one of them changed
		// its spelling — silently, since the only figure that reads a reason
		// is the eviction count, and a count that stops rising looks like a
		// Mac with room to spare. An unmapped reason is recorded under its own
		// name and said out loud.
		o.log.Warn("a model left the pool for a reason the statistics do not know", "reason", reason)
		mapped = string(reason)
	}
	o.rec.Removed(repoID, mapped)
}

// stopReasons maps every reason the pool can give onto the recorder's own. It
// is a total mapping on purpose: a reason added to one side and not the other
// shows up as a missing key, which is a line in the log rather than a figure
// that quietly stops moving.
var stopReasons = map[runtime.StopReason]string{
	runtime.StopEvicted:    stats.ReasonEvicted,
	runtime.StopIdle:       stats.ReasonIdle,
	runtime.StopUnloaded:   stats.ReasonUnloaded,
	runtime.StopAbandoned:  stats.ReasonAbandoned,
	runtime.StopLoadFailed: stats.ReasonLoadFailed,
	runtime.StopCrashed:    stats.ReasonCrashed,
	runtime.StopShutdown:   stats.ReasonShutdown,
}

// PinnedFitWarning is what the control panel says when the pinned models can no
// longer be held together, and "" when they can.
//
// A settings save is the only moment a pinned set is refused, and the set can
// stop fitting without one: a pinned model that was deleted — and so charged
// nothing — is charged in full again when it is downloaded back, and a model
// re-downloaded at a larger quantization grows. Nothing refuses either, so the
// panel says so instead, beside the warning about an open LAN endpoint.
func (a *App) PinnedFitWarning() string {
	problem := a.pinnedFitProblem(a.Config().Pinned, a.Pool.MemoryBudget())
	if problem == nil {
		return ""
	}
	return "The pinned models can no longer all be kept in memory: " + problem.Error() + "."
}

// adoptPinnedSpelling re-folds the pinned list onto the registry's spellings
// once a model has arrived.
//
// A pin may be set before its model is downloaded, and is kept as it was typed
// because there is nothing to fold it onto yet. Every surface that joins on the
// id joins on the registry's spelling, so a pin left in another one shows the
// model as unpinned on its card and draws a second, ticked box for a model
// "not on this Mac" — while the pool, which folds, protects it.
//
// It changes the running settings only. config.json keeps the operator's
// spelling until the next save, which folds it through canonicalPinned anyway;
// writing the file from here would make this a second writer of it.
func (a *App) adoptPinnedSpelling() {
	a.saveMu.Lock()
	defer a.saveMu.Unlock()

	a.cfgMu.RLock()
	pinned := append([]string(nil), a.cfg.Pinned...)
	a.cfgMu.RUnlock()
	if len(pinned) == 0 {
		return
	}

	folded := make([]string, 0, len(pinned))
	changed := false
	for _, id := range pinned {
		canonical := a.canonicalModelKey(id)
		changed = changed || canonical != id
		folded = append(folded, canonical)
	}
	if !changed {
		return
	}
	a.cfgMu.Lock()
	a.cfg.Pinned = folded
	a.cfgMu.Unlock()
	a.Pool.SetPinned(folded)
	a.Log.Info("a pinned model arrived; its pin now names it as the registry does", "pinned", folded)
}

// pinnedCharge is what a pinned set costs the memory budget, and the names of
// any pinned models this Mac cannot measure.
//
// Only a model the pool could actually load is charged: one that is ready, and
// one that is still downloading, which is charged the size it declares —
// charging a download nothing is how a pinned pair that cannot possibly fit
// gets accepted while the bytes are still arriving. A failed download is
// charged nothing, because modelSource.Resolve refuses anything that is not
// ready, so it can never occupy a byte however large it declared itself. A pin
// naming a model this Mac does not have at all is not counted either; it
// protects nothing until something loads it.
func (a *App) pinnedCharge(pinned []string) (sum int64, unsized []string) {
	for _, id := range pinned {
		m, err := a.Registry.Get(id)
		if err != nil || !chargeable(m) {
			continue
		}
		size := chargedSize(m)
		if size <= 0 {
			unsized = append(unsized, m.RepoID)
			continue
		}
		sum += runtime.LoadCost(size)
	}
	return sum, unsized
}

// chargeable reports whether a model could occupy memory at all. Anything the
// pool would refuse to load costs the budget nothing, whatever its record says
// about its size.
func chargeable(m registry.Model) bool {
	return m.Ready() || m.State == registry.StateDownloading
}

// addsAPin reports whether the incoming set names a model the current one does
// not. Folded, because two spellings of one id are one pin.
func addsAPin(incoming, current []string) bool {
	have := make(map[string]bool, len(current))
	for _, id := range current {
		have[config.FoldRepoID(id)] = true
	}
	for _, id := range incoming {
		if !have[config.FoldRepoID(id)] {
			return true
		}
	}
	return false
}

// canonicalPerModel checks the keys of a per-model settings map submitted
// through Settings and rewrites each to the registry's spelling of the model
// it names.
//
// The registry matches an id case-insensitively but answers under one
// spelling, and that spelling is what a request resolves to. A key stored in
// another case would therefore name a model the operator can see and still
// match no request, so the case is folded once here — on the way in, where the
// operator is present to be told about a key that names nothing — rather than
// on every request. A key for a model this machine does not have is kept as it
// was typed, and folded onto the registry's spelling at the next startup after
// the model arrives (see adoptPerModel), so setting a model up before
// downloading it works whatever case it is typed in.
func (a *App) canonicalPerModel(in map[string]config.ModelSettings) (map[string]config.ModelSettings, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if err := config.ValidatePerModelKeys(in); err != nil {
		return nil, err
	}
	out := make(map[string]config.ModelSettings, len(in))
	for _, id := range perModelKeys(in) {
		canonical := a.canonicalModelKey(id)
		if _, dup := out[canonical]; dup {
			return nil, fmt.Errorf("per-model settings name %s more than once", canonical)
		}
		out[canonical] = in[id]
	}
	return out, nil
}

// adoptPerModel is canonicalPerModel for settings read from disk rather than
// submitted through Settings: a key it cannot use is dropped and named in the
// log, the way an unusable preload entry is, rather than refused.
//
// Refusing at startup would be worse than useless. The panel serves the stored
// settings into its form and the form posts them back, so one unusable key
// would return on the next save and be refused — wedging every settings change
// there is, the API key included, until someone edited the file by hand.
func (a *App) adoptPerModel(in map[string]config.ModelSettings) map[string]config.ModelSettings {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]config.ModelSettings, len(in))
	var dropped []string
	for _, id := range perModelKeys(in) {
		canonical := a.canonicalModelKey(id)
		if !config.ValidRepoID(id) {
			dropped = append(dropped, id)
			continue
		}
		if _, dup := out[canonical]; dup {
			dropped = append(dropped, id)
			continue
		}
		out[canonical] = in[id]
	}
	if len(dropped) > 0 {
		a.Log.Warn("dropped per-model settings whose key names no model, or names one another key already names",
			"keys", dropped)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// chargedSize is what a model costs the memory budget: what is on disk once it
// is downloaded, and what the download declares before that. The two are the
// same figure at different moments, and a model in flight is the case the
// check exists for. The disk figure wins where both are recorded, because it
// is the one the pool charges. Zero from both means this Mac does not know how
// large the model is — see pinnedCharge, which refuses to guess.
func chargedSize(m registry.Model) int64 {
	if m.Bytes > 0 {
		return m.Bytes
	}
	return m.SizeBytes
}

// canonicalModelKey is the registry's spelling of a model id, or the id as it
// was given when this machine does not have that model. It is what every
// setting keyed or listed by a model id is stored under, so that the spelling
// the operator sees in Settings is the one a request resolves to.
func (a *App) canonicalModelKey(id string) string {
	if m, err := a.Registry.Get(id); err == nil {
		return m.RepoID
	}
	return id
}

// perModelKeys returns a per-model map's keys in a stable order, so that a map
// with more than one problem in it reports the same one every time rather than
// whichever the map iteration reached first.
func perModelKeys(in map[string]config.ModelSettings) []string {
	return slices.Sorted(maps.Keys(in))
}

// modelSource adapts the registry to runtime.ModelSource.
type modelSource struct{ reg *registry.Registry }

func (s modelSource) Resolve(repoID string) (string, int64, error) {
	m, err := s.reg.Get(repoID)
	if err != nil {
		return "", 0, fmt.Errorf("%s is not downloaded", repoID)
	}
	if !m.Ready() {
		return "", 0, fmt.Errorf("%s is not ready (%s)", repoID, m.State)
	}
	return m.Path, m.Bytes, nil
}

// ErrAlreadyDownloading is returned when a download is requested twice.
var ErrAlreadyDownloading = errors.New("already downloading")

// ErrInvalidRepoID is returned when a model id is not a well-formed
// "<org>/<name>". Callers (the control plane) map it to 400, not 409.
var ErrInvalidRepoID = errors.New("invalid model id")

// Download fetches a model in the background and tracks it in the registry.
//
// It returns as soon as the download starts; progress is reported through the
// registry's subscription channel.
func (a *App) Download(repoID string) error {
	if repoID == "" {
		return errors.New("a model id is required")
	}
	// A repo id becomes a filesystem path (ModelDir) and is later passed to
	// os.RemoveAll on delete. Reject anything that isn't a clean "<org>/<name>"
	// before it can escape the models directory.
	if !config.ValidRepoID(repoID) {
		return fmt.Errorf("%q is not a valid model id (expected <org>/<name>): %w", repoID, ErrInvalidRepoID)
	}
	// A known model keeps its recorded spelling: repo ids are case-insensitive
	// on the Hub and alias one directory on APFS, so a re-cased id must reuse
	// the existing entry and directory rather than mint a second row over it.
	// The registry folds the lookup.
	if known, err := a.Registry.Get(repoID); err == nil {
		repoID = known.RepoID
	}

	a.dlMu.Lock()
	if _, busy := a.downloads[dlKey(repoID)]; busy {
		a.dlMu.Unlock()
		return ErrAlreadyDownloading
	}
	ctx, cancel := context.WithCancel(context.Background())
	dl := &download{repoID: repoID, cancel: cancel, done: make(chan struct{})}
	a.downloads[dlKey(repoID)] = dl
	a.dlMu.Unlock()

	dest := a.Paths.ModelDir(repoID)
	// Remember whether a ready model is already being served from dest: a
	// failed re-download must not take away files that still validate
	// (downloads stage into .part files and only replace a file once it
	// completes, so an attempt that fails before any file finishes leaves
	// the served set untouched). Read after the busy check, so a download
	// that finished in between is seen as ready, not as still in flight.
	prior, priorErr := a.Registry.Get(repoID)
	wasReady := priorErr == nil && prior.State == registry.StateReady
	// A retry or re-download of a known repo must keep its original AddedAt;
	// only a genuinely new repo gets Put's zero-value-defaults-to-now behavior.
	addedAt := time.Time{}
	if priorErr == nil {
		addedAt = prior.AddedAt
	}
	if err := a.Registry.Put(registry.Model{
		RepoID:  repoID,
		Path:    dest,
		State:   registry.StateDownloading,
		AddedAt: addedAt,
	}); err != nil {
		a.finishDownload(dl, nil)
		return err
	}

	a.dlWG.Add(1)
	go func() {
		defer a.dlWG.Done()
		defer close(dl.done)
		// A safety net, not the normal path: every branch below deregisters
		// itself as it publishes its final state. Deregistering twice is
		// harmless, and leaving a download registered forever would refuse
		// every later attempt at that model.
		defer a.finishDownload(dl, nil)

		err := a.Hub.Download(ctx, hub.DownloadRequest{
			RepoID:      repoID,
			ModelsDir:   a.Paths.Models,
			Dest:        dest,
			Concurrency: 4,
			OnProgress: func(p hub.Progress) {
				// In-memory only: progress ticks are frequent and ephemeral, so
				// they must not write the registry file to disk each time.
				a.Registry.UpdateProgress(repoID, p.Percent())
				// Record the total size once, so the UI can show "X% of <size>".
				if p.Total > 0 {
					a.Registry.SetSize(repoID, p.Total)
				}
			},
		})

		// A completed byte-for-byte download can still be junk (a config that
		// won't parse, no usable weights). Validate before advertising it as
		// ready, so /v1/models and the mDNS count only ever list models that are
		// at least structurally loadable.
		if err == nil {
			if verr := validateModelDir(dest); verr != nil {
				err = fmt.Errorf("downloaded but not a usable MLX model: %w", verr)
			}
		}

		// Each branch publishes its final state and deregisters the download
		// in one step (see finishDownload). Logging stays outside it: the log
		// is not what another goroutine is waiting to see.
		switch {
		case err == nil:
			var perr error
			a.finishDownload(dl, func() {
				// Re-derive the size from disk rather than trusting the manifest.
				perr = a.Registry.Put(registry.Model{
					RepoID: repoID,
					Path:   dest,
					Bytes:  dirSize(dest),
					// Read through the registry's own primitive, so the download
					// path and the rescan apply one key rule; a model carries its
					// context length from the moment it is ready, not only after
					// the next startup rescan.
					ContextLength: registry.ReadContextLength(dest),
					State:         registry.StateReady,
					Progress:      100,
					AddedAt:       addedAt,
				})
			})
			if perr != nil {
				// The files are on disk; only the index write failed. Surface it —
				// a silently unrecorded model would look missing until a rescan.
				a.Log.Error("model downloaded but could not be recorded", "model", repoID, "err", perr)
			} else {
				a.Log.Info("model downloaded", "model", repoID)
			}
			// A model arriving changes two things a save is not present for:
			// which spelling its pin should carry, and what the pinned set
			// costs. Neither refuses anything here — there is no save to
			// refuse — so the second is a warning the panel repeats.
			a.adoptPinnedSpelling()
			if w := a.PinnedFitWarning(); w != "" {
				a.Log.Warn("a model arrived and the pinned set no longer fits", "warning", w)
			}

		case errors.Is(err, context.Canceled):
			// A cancelled download leaves .part files behind on purpose: they let
			// the next attempt resume instead of starting over.
			var restored bool
			a.finishDownload(dl, func() {
				restored = a.restoreReady(repoID, dest, wasReady, prior)
				if !restored {
					a.Registry.SetState(repoID, registry.StateFailed, 0, "cancelled")
				}
			})
			if restored {
				a.Log.Info("download cancelled; the ready model is untouched", "model", repoID)
			} else {
				a.Log.Info("download cancelled", "model", repoID)
			}

		default:
			var restored bool
			a.finishDownload(dl, func() {
				restored = a.restoreReady(repoID, dest, wasReady, prior)
				if !restored {
					a.Registry.SetState(repoID, registry.StateFailed, 0, err.Error())
				}
			})
			if restored {
				a.Log.Warn("download failed; the ready model is untouched", "model", repoID, "err", err)
			} else {
				a.Log.Error("download failed", "model", repoID, "err", err)
			}
		}
	}()

	return nil
}

// restoreReady puts a model back into the ready state after a failed or
// cancelled download attempt, provided it was ready before the attempt and its
// files still validate. It reports whether the model was restored.
//
// Everything it restores comes from prior — the record the model had before
// the attempt — rather than from the directory: measuring the directory now
// would count the failed attempt's .part leftovers, and a config.json the
// attempt had already replaced before failing would hand back the new
// revision's context length beside the old revision's size. A record that
// predates the figure still gains it, because the startup rescan re-derives
// it from the directory that is actually being served.
func (a *App) restoreReady(repoID, dest string, wasReady bool, prior registry.Model) bool {
	if !wasReady || validateModelDir(dest) != nil {
		return false
	}
	if perr := a.Registry.Put(registry.Model{
		RepoID:        repoID,
		Path:          dest,
		Bytes:         prior.Bytes,
		ContextLength: prior.ContextLength,
		State:         registry.StateReady,
		Progress:      100,
		AddedAt:       prior.AddedAt,
	}); perr != nil {
		a.Log.Error("could not restore the ready model record", "model", repoID, "err", perr)
		return false
	}
	return true
}

// finishDownload publishes a download's final state and deregisters it as one
// step, then reports whether this call was the one that deregistered it.
//
// One step is the whole point. The registry is what everything else watches —
// the control panel renders every change the event stream pushes, and the
// tests wait on it — so publishing "ready" before releasing the model left a
// window in which a caller could see the model finished and still be refused
// its next Download with ErrAlreadyDownloading. A caller cannot observe that
// window now: reaching the in-flight map means taking this lock, and the state
// that says the download ended is written inside it.
//
// publish may be nil, for a caller that has nothing to publish.
//
// The identity check matters for the deferred safety-net call: by the time it
// runs, the branch above has already deregistered this download and the id may
// belong to a newer attempt, which must not be cancelled out from under itself.
func (a *App) finishDownload(dl *download, publish func()) {
	a.dlMu.Lock()
	defer a.dlMu.Unlock()
	if publish != nil {
		publish()
	}
	if cur, ok := a.downloads[dlKey(dl.repoID)]; ok && cur == dl {
		delete(a.downloads, dlKey(dl.repoID))
	}
}

// dlKey is the in-flight downloads map key: case-folded like the registry's,
// so a case variant of a running download is seen as that download.
func dlKey(repoID string) string { return config.FoldRepoID(repoID) }

// CancelDownload stops an in-flight download.
//
// If there is no live download but the registry still records the model as
// downloading — an orphan left by a crash or restart — its state is cleared to
// failed so it can be retried or removed, rather than reporting a spurious error.
func (a *App) CancelDownload(repoID string) error {
	a.dlMu.Lock()
	dl, ok := a.downloads[dlKey(repoID)]
	a.dlMu.Unlock()
	if ok {
		dl.cancel()
		return nil
	}
	if m, err := a.Registry.Get(repoID); err == nil && m.State == registry.StateDownloading {
		return a.Registry.SetState(repoID, registry.StateFailed, m.Progress, "cancelled")
	}
	return fmt.Errorf("%s is not downloading", repoID)
}

// Downloading lists the repos currently being fetched.
func (a *App) Downloading() []string {
	a.dlMu.Lock()
	defer a.dlMu.Unlock()
	out := make([]string, 0, len(a.downloads))
	for _, dl := range a.downloads {
		out = append(out, dl.repoID)
	}
	return out
}

// Delete removes a model: it is unloaded first if it is resident, then its files
// are deleted.
func (a *App) Delete(repoID string) error {
	// Defense in depth: never hand an un-validated id to os.RemoveAll, even one
	// that somehow reached the registry (e.g. from an older build).
	if !config.ValidRepoID(repoID) {
		return fmt.Errorf("%q is not a valid model id: %w", repoID, ErrInvalidRepoID)
	}
	// Use the recorded spelling (see Download) so the directory removed and the
	// pool entry unloaded are the model's own.
	if m, err := a.Registry.Get(repoID); err == nil {
		repoID = m.RepoID
	}
	// Cancel any download of this model AND wait for it to stop. Cancelling alone
	// is not enough: the goroutine would keep writing into the directory we are
	// about to remove, and the model would reappear moments after being deleted.
	a.dlMu.Lock()
	dl, downloading := a.downloads[dlKey(repoID)]
	a.dlMu.Unlock()
	if downloading {
		dl.cancel()
		<-dl.done
	}

	if err := a.Pool.Unload(repoID); err != nil {
		// "Not loaded" is expected and fine — we are about to delete it anyway.
		// "Busy" is not: deleting a model mid-request would pull the weights out
		// from under an in-flight completion.
		if errors.Is(err, runtime.ErrBusy) {
			return err
		}
	}
	// The directory comes from the validated id, never from the registry's
	// stored path (see Registry.Remove).
	return a.Registry.Remove(repoID, a.Paths.ModelDir(repoID))
}

// Close shuts the app down.
//
// It waits for cancelled downloads to actually finish: cancelling only *asks*
// them to stop, and a goroutine still mid-write would otherwise keep touching
// files after the app believed it had shut down.
func (a *App) Close() error {
	a.dlMu.Lock()
	for _, dl := range a.downloads {
		dl.cancel()
	}
	a.dlMu.Unlock()

	a.dlWG.Wait()
	// The pool first: shutting it down produces a removal record for every
	// model still resident, and closing the store before that would throw
	// those away. Closing the store then flushes whatever the last few seconds
	// of requests recorded.
	err := a.Pool.Close()
	if cerr := a.StatsStore.Close(); err == nil {
		err = cerr
	}
	return err
}
