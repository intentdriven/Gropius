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
	"sync"
	"time"

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
	Log   *slog.Logger

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

	a := &App{
		Paths:       opts.Paths,
		Hub:         hc,
		Registry:    reg,
		Provisioner: runtime.NewProvisioner(opts.Paths),
		Stats:       stats.New(stats.Options{}),
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

	a.Pool = runtime.NewPool(runtime.PoolOptions{
		Launcher:          launcher,
		Models:            modelSource{reg},
		IdleTimeout:       time.Duration(opts.Config.IdleTimeoutSec) * time.Second,
		DecodeConcurrency: opts.Config.DecodeConcurrency,
		// Read live, at the moment a model server starts, so a default saved in
		// Settings applies the next time each model loads.
		SamplingFor: func(repoID string) config.Sampling {
			return a.Config().EffectiveSampling(repoID)
		},
		// The pool reports loads and removals to the recorder, which ignores
		// them while recording is off. Adapting here keeps internal/stats a
		// leaf package that imports nothing of ours.
		Observer: poolObserver{a.Stats},
	})
	a.Stats.SetEnabled(opts.Config.Statistics)

	// Settings read from disk have not been through SetConfig's checks: the
	// file can be hand-edited, restored from a backup, or written by another
	// build. Fold them onto the registry's spellings here, where the log
	// exists to say what was dropped.
	a.cfg.PerModel = a.adoptPerModel(a.cfg.PerModel)

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
		_, release, err := a.Pool.Acquire(ctx, id)
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
	if err := c.Validate(); err != nil {
		return err
	}
	perModel, err := a.canonicalPerModel(c.PerModel)
	if err != nil {
		return err
	}
	c.PerModel = perModel
	if err := config.Save(a.Paths.Config, c); err != nil {
		return err
	}
	a.cfgMu.Lock()
	a.cfg = c
	a.cfgMu.Unlock()

	// The switch applies to the next request, not to the next start. Turning
	// it off also empties what was recorded, which is what makes "off" the
	// same state as a fresh start rather than a hidden one.
	a.Stats.SetEnabled(c.Statistics)

	a.Hub.Token = c.HFToken
	return nil
}

// poolObserver adapts the pool's reports onto the recorder. The pool names its
// own reasons and the recorder names its own; this is the one place that has
// to know both.
type poolObserver struct{ rec *stats.Recorder }

func (o poolObserver) LoadStarted(repoID string) { o.rec.LoadStarted(repoID) }

func (o poolObserver) LoadFinished(repoID string, took time.Duration, err error) {
	o.rec.LoadFinished(repoID, took, err)
}

func (o poolObserver) EntryStopped(repoID string, reason runtime.StopReason) {
	o.rec.Removed(repoID, string(reason))
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
		canonical := a.canonicalPerModelKey(id)
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
		canonical := a.canonicalPerModelKey(id)
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

// canonicalPerModelKey is the registry's spelling of a model id, or the id as
// it was given when this machine does not have that model.
func (a *App) canonicalPerModelKey(id string) string {
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
	return a.Pool.Close()
}
