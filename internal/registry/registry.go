// Package registry tracks which models are on disk and where.
//
// The registry is the source of truth for what Gropius can serve. It is
// deliberately a thin index over the filesystem: the model directories
// themselves are the real artefact, and the registry can always be rebuilt from
// them (see Rescan).
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// State is where a model is in its lifecycle.
type State string

const (
	// StateDownloading means a download is in flight; the directory is incomplete.
	StateDownloading State = "downloading"
	// StateReady means the model is complete and loadable.
	StateReady State = "ready"
	// StateFailed means the last download failed. Err carries the reason.
	StateFailed State = "failed"
)

// Model is one entry in the registry.
type Model struct {
	// RepoID is the HuggingFace repo, e.g. "mlx-community/Qwen3-8B-4bit".
	// It doubles as the model's public name on the OpenAI API.
	RepoID string `json:"repo_id"`
	// Path is the directory passed to mlx_lm.server --model.
	Path string `json:"path"`
	// Bytes is the on-disk size of the model's files.
	Bytes int64 `json:"bytes"`
	State State `json:"state"`
	// Err explains a StateFailed model.
	Err string `json:"err,omitempty"`
	// Progress is 0-100 while downloading.
	Progress float64   `json:"progress"`
	AddedAt  time.Time `json:"added_at"`
}

// Ready reports whether the model can be served.
func (m Model) Ready() bool { return m.State == StateReady }

// Name is the short name shown in UIs ("mlx-community/Qwen3-8B-4bit" -> "Qwen3-8B-4bit").
func (m Model) Name() string {
	if i := strings.Index(m.RepoID, "/"); i >= 0 {
		return m.RepoID[i+1:]
	}
	return m.RepoID
}

// ErrNotFound is returned when a repo id is not in the registry.
var ErrNotFound = errors.New("model not found")

// Registry is a concurrency-safe, file-backed index of local models.
type Registry struct {
	path string // registry.json

	mu     sync.RWMutex
	models map[string]Model

	// subscribers receive a snapshot whenever the registry changes, so the UI
	// can push updates without polling.
	subMu  sync.Mutex
	subs   map[int]chan []Model
	nextID int
}

// Open loads the registry from path, creating an empty one if absent.
func Open(path string) (*Registry, error) {
	r := &Registry{
		path:   path,
		models: map[string]Model{},
		subs:   map[int]chan []Model{},
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	var models []Model
	if err := json.Unmarshal(b, &models); err != nil {
		// A corrupt index must not brick the app: the model directories are the
		// real data, so start empty and let Rescan rebuild from disk.
		return r, nil
	}
	for _, m := range models {
		r.models[m.RepoID] = m
	}
	return r, nil
}

// Get returns one model.
func (r *Registry) Get(repoID string) (Model, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[repoID]
	if !ok {
		return Model{}, fmt.Errorf("%q: %w", repoID, ErrNotFound)
	}
	return m, nil
}

// List returns every model, sorted by repo id.
func (r *Registry) List() []Model {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.listLocked()
}

func (r *Registry) listLocked() []Model {
	out := make([]Model, 0, len(r.models))
	for _, m := range r.models {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RepoID < out[j].RepoID })
	return out
}

// Ready returns only the models that can be served.
func (r *Registry) Ready() []Model {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Model
	for _, m := range r.models {
		if m.Ready() {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RepoID < out[j].RepoID })
	return out
}

// Put inserts or replaces a model and persists the registry.
func (r *Registry) Put(m Model) error {
	if m.RepoID == "" {
		return errors.New("registry: RepoID is required")
	}
	if m.AddedAt.IsZero() {
		m.AddedAt = time.Now()
	}
	r.mu.Lock()
	r.models[m.RepoID] = m
	snapshot := r.listLocked()
	err := r.saveLocked()
	r.mu.Unlock()

	r.broadcast(snapshot)
	return err
}

// SetState updates a model's state (and error/progress) in place.
func (r *Registry) SetState(repoID string, state State, progress float64, errMsg string) error {
	r.mu.Lock()
	m, ok := r.models[repoID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%q: %w", repoID, ErrNotFound)
	}
	m.State = state
	m.Progress = progress
	m.Err = errMsg
	r.models[repoID] = m
	snapshot := r.listLocked()
	err := r.saveLocked()
	r.mu.Unlock()

	r.broadcast(snapshot)
	return err
}

// Remove deletes a model from the index and removes its files from disk.
func (r *Registry) Remove(repoID string) error {
	r.mu.Lock()
	m, ok := r.models[repoID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%q: %w", repoID, ErrNotFound)
	}
	delete(r.models, repoID)
	snapshot := r.listLocked()
	err := r.saveLocked()
	r.mu.Unlock()

	if err != nil {
		return err
	}
	// Remove the files last: if this fails the index is still consistent, and a
	// Rescan would simply re-adopt the directory.
	if m.Path != "" {
		if err := os.RemoveAll(m.Path); err != nil {
			return fmt.Errorf("delete model files: %w", err)
		}
	}
	r.broadcast(snapshot)
	return nil
}

// saveLocked persists the index. Callers must hold r.mu.
func (r *Registry) saveLocked() error {
	b, err := json.MarshalIndent(r.listLocked(), "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// Subscribe returns a channel that receives a snapshot on every change, plus a
// function to unsubscribe. The channel is buffered and drops updates rather than
// blocking a writer, so a slow consumer can never stall a download.
func (r *Registry) Subscribe() (<-chan []Model, func()) {
	r.subMu.Lock()
	defer r.subMu.Unlock()

	id := r.nextID
	r.nextID++
	ch := make(chan []Model, 8)
	r.subs[id] = ch

	return ch, func() {
		r.subMu.Lock()
		defer r.subMu.Unlock()
		if c, ok := r.subs[id]; ok {
			delete(r.subs, id)
			close(c)
		}
	}
}

func (r *Registry) broadcast(snapshot []Model) {
	r.subMu.Lock()
	defer r.subMu.Unlock()
	for _, ch := range r.subs {
		select {
		case ch <- snapshot:
		default: // slow consumer: drop this update, it will get the next one
		}
	}
}

// Rescan rebuilds the index from the model directory tree. It adopts any
// complete model directory it finds — which is what makes a shared cache work:
// a second user account sees models the first account downloaded.
//
// A directory counts as a model when it holds a config.json and at least one
// .safetensors file. Anything mid-download (a .gropius-part file present) is
// skipped rather than adopted as ready.
func (r *Registry) Rescan(modelsDir string) error {
	found := map[string]Model{}

	// Models live at <modelsDir>/<org>/<name>, so walk exactly two levels.
	orgs, err := os.ReadDir(modelsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("scan models dir: %w", err)
	}

	for _, org := range orgs {
		if !org.IsDir() {
			continue
		}
		repos, err := os.ReadDir(filepath.Join(modelsDir, org.Name()))
		if err != nil {
			continue
		}
		for _, repo := range repos {
			if !repo.IsDir() {
				continue
			}
			dir := filepath.Join(modelsDir, org.Name(), repo.Name())
			complete, size := inspectModelDir(dir)
			if !complete {
				continue
			}
			repoID := org.Name() + "/" + repo.Name()
			found[repoID] = Model{
				RepoID:  repoID,
				Path:    dir,
				Bytes:   size,
				State:   StateReady,
				AddedAt: time.Now(),
			}
		}
	}

	r.mu.Lock()
	for repoID, m := range found {
		if existing, ok := r.models[repoID]; ok {
			// Don't clobber an in-flight download with a "ready" verdict.
			if existing.State == StateDownloading {
				continue
			}
			existing.Path = m.Path
			existing.Bytes = m.Bytes
			existing.State = StateReady
			existing.Err = ""
			r.models[repoID] = existing
			continue
		}
		r.models[repoID] = m
	}
	// Drop entries whose directory has vanished (deleted outside the app), but
	// keep in-flight downloads, whose directories are legitimately incomplete.
	for repoID, m := range r.models {
		if _, ok := found[repoID]; ok {
			continue
		}
		if m.State == StateDownloading {
			continue
		}
		delete(r.models, repoID)
	}
	snapshot := r.listLocked()
	err = r.saveLocked()
	r.mu.Unlock()

	r.broadcast(snapshot)
	return err
}

// inspectModelDir reports whether dir holds a loadable model, and its size.
func inspectModelDir(dir string) (complete bool, size int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, 0
	}
	var hasConfig, hasWeights, partial bool
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".gropius-part") {
			partial = true
		}
		if name == "config.json" {
			hasConfig = true
		}
		if strings.HasSuffix(name, ".safetensors") {
			hasWeights = true
		}
		if info, err := e.Info(); err == nil && !e.IsDir() {
			size += info.Size()
		}
	}
	return hasConfig && hasWeights && !partial, size
}
