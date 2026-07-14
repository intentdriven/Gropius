// Package app is the composition root: it wires the HuggingFace client, the
// model registry, the process pool and the provisioner into one object that the
// gateway and the UI drive.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

	a.Pool = runtime.NewPool(runtime.PoolOptions{
		Launcher: &runtime.ExecLauncher{
			Paths:  opts.Paths,
			LogDir: opts.Paths.Logs,
		},
		Models:            modelSource{reg},
		IdleTimeout:       time.Duration(opts.Config.IdleTimeoutSec) * time.Second,
		DecodeConcurrency: opts.Config.DecodeConcurrency,
	})

	return a, nil
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

// Download fetches a model in the background and tracks it in the registry.
//
// It returns as soon as the download starts; progress is reported through the
// registry's subscription channel.
func (a *App) Download(repoID string) error {
	if repoID == "" {
		return errors.New("a model id is required")
	}

	a.dlMu.Lock()
	if _, busy := a.downloads[repoID]; busy {
		a.dlMu.Unlock()
		return ErrAlreadyDownloading
	}
	ctx, cancel := context.WithCancel(context.Background())
	dl := &download{cancel: cancel, done: make(chan struct{})}
	a.downloads[repoID] = dl
	a.dlMu.Unlock()

	dest := a.Paths.ModelDir(repoID)
	if err := a.Registry.Put(registry.Model{
		RepoID: repoID,
		Path:   dest,
		State:  registry.StateDownloading,
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
			Dest:        dest,
			Concurrency: 4,
			OnProgress: func(p hub.Progress) {
				a.Registry.SetState(repoID, registry.StateDownloading, p.Percent(), "")
			},
		})

		switch {
		case err == nil:
			// Re-derive the size from disk rather than trusting the manifest.
			a.Registry.Put(registry.Model{
				RepoID:   repoID,
				Path:     dest,
				Bytes:    dirSize(dest),
				State:    registry.StateReady,
				Progress: 100,
			})
			a.Log.Info("model downloaded", "model", repoID)

		case errors.Is(err, context.Canceled):
			// A cancelled download leaves .part files behind on purpose: they let
			// the next attempt resume instead of starting over.
			a.Registry.SetState(repoID, registry.StateFailed, 0, "cancelled")
			a.Log.Info("download cancelled", "model", repoID)

		default:
			a.Registry.SetState(repoID, registry.StateFailed, 0, err.Error())
			a.Log.Error("download failed", "model", repoID, "err", err)
		}
	}()

	return nil
}

func (a *App) finishDownload(repoID string) {
	a.dlMu.Lock()
	delete(a.downloads, repoID)
	a.dlMu.Unlock()
}

// CancelDownload stops an in-flight download.
func (a *App) CancelDownload(repoID string) error {
	a.dlMu.Lock()
	dl, ok := a.downloads[repoID]
	a.dlMu.Unlock()
	if !ok {
		return fmt.Errorf("%s is not downloading", repoID)
	}
	dl.cancel()
	return nil
}

// Downloading lists the repos currently being fetched.
func (a *App) Downloading() []string {
	a.dlMu.Lock()
	defer a.dlMu.Unlock()
	out := make([]string, 0, len(a.downloads))
	for id := range a.downloads {
		out = append(out, id)
	}
	return out
}

// Delete removes a model: it is unloaded first if it is resident, then its files
// are deleted.
func (a *App) Delete(repoID string) error {
	// Cancel any download of this model AND wait for it to stop. Cancelling alone
	// is not enough: the goroutine would keep writing into the directory we are
	// about to remove, and the model would reappear moments after being deleted.
	a.dlMu.Lock()
	dl, downloading := a.downloads[repoID]
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
	return a.Registry.Remove(repoID)
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
