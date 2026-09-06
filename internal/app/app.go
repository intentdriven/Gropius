// Package app is the composition root: it wires the HuggingFace client, the
// model registry, the process pool and the provisioner into one object that the
// gateway and the UI drive.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/hub"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// App holds everything the daemon needs.
type App struct {
	Paths       config.Paths
	Hub         *hub.Client
	Registry    *registry.Registry
	Pool        *runtime.Pool
	Provisioner *runtime.Provisioner
	Log         *slog.Logger

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
		Log:         opts.Log,
		cfg:         opts.Config,
		downloads:   map[string]*download{},
	}

	launcher := &runtime.ExecLauncher{
		Paths:  opts.Paths,
		LogDir: opts.Paths.Logs,
	}
	// Kill any model servers left running by a previous run that crashed before
	// it could stop them — otherwise they hold GPU memory until reboot.
	if killed := launcher.ReapOrphans(); killed > 0 {
		opts.Log.Warn("reaped model servers left over from a previous run", "count", killed)
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
	})

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
	if err := config.Save(a.Paths.Config, c); err != nil {
		return err
	}
	a.cfgMu.Lock()
	a.cfg = c
	a.cfgMu.Unlock()

	a.Hub.Token = c.HFToken
	return nil
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
		a.finishDownload(repoID)
		return err
	}

	a.dlWG.Add(1)
	go func() {
		defer a.dlWG.Done()
		defer close(dl.done)
		defer a.finishDownload(repoID)

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

		switch {
		case err == nil:
			// Re-derive the size from disk rather than trusting the manifest.
			if perr := a.Registry.Put(registry.Model{
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
			}); perr != nil {
				// The files are on disk; only the index write failed. Surface it —
				// a silently unrecorded model would look missing until a rescan.
				a.Log.Error("model downloaded but could not be recorded", "model", repoID, "err", perr)
			} else {
				a.Log.Info("model downloaded", "model", repoID)
			}

		case errors.Is(err, context.Canceled):
			// A cancelled download leaves .part files behind on purpose: they let
			// the next attempt resume instead of starting over.
			if a.restoreReady(repoID, dest, wasReady, prior) {
				a.Log.Info("download cancelled; the ready model is untouched", "model", repoID)
			} else {
				a.Registry.SetState(repoID, registry.StateFailed, 0, "cancelled")
				a.Log.Info("download cancelled", "model", repoID)
			}

		default:
			if a.restoreReady(repoID, dest, wasReady, prior) {
				a.Log.Warn("download failed; the ready model is untouched", "model", repoID, "err", err)
			} else {
				a.Registry.SetState(repoID, registry.StateFailed, 0, err.Error())
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

func (a *App) finishDownload(repoID string) {
	a.dlMu.Lock()
	delete(a.downloads, dlKey(repoID))
	a.dlMu.Unlock()
}

// dlKey is the in-flight downloads map key: case-folded like the registry's,
// so a case variant of a running download is seen as that download.
func dlKey(repoID string) string { return strings.ToLower(repoID) }

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
