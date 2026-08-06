// Package registry tracks which models are on disk and where.
//
// The registry is the source of truth for what Gropius can serve. It is
// deliberately a thin index over the filesystem: the model directories
// themselves are the real artifact, and the registry can always be rebuilt from
// them (see Rescan).
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
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
	// SizeBytes is the model's total download size, known from the start of a
	// download. It is what the UI shows as "downloading X% of <size>".
	SizeBytes int64 `json:"size_bytes,omitempty"`
	State     State `json:"state"`
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

// SetState updates a model's state (and error/progress) in place and persists.
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

// ReconcileInterrupted marks every model still recorded as "downloading" as
// failed, and returns the ids it changed.
//
// Only one Gropius process ever downloads (the singleton that owns the port), so
// any "downloading" entry found when a fresh process starts up is orphaned: the
// goroutine that was fetching it died with the previous process. Left alone it
// stays "downloading" forever — the UI offers only a Cancel button for that
// state, and Cancel fails because there is no live download to cancel, so the
// entry can be neither resumed nor removed. Transitioning it to failed makes the
// UI offer Retry (which resumes from the .part files left on disk) and Remove.
//
// Call this once at startup, after Rescan.
func (r *Registry) ReconcileInterrupted() []string {
	r.mu.Lock()
	var changed []string
	for repoID, m := range r.models {
		if m.State != StateDownloading {
			continue
		}
		m.State = StateFailed
		m.Err = "interrupted — Gropius restarted while this was downloading; retry to resume or remove it"
		r.models[repoID] = m
		changed = append(changed, repoID)
	}
	if len(changed) == 0 {
		r.mu.Unlock()
		return nil
	}
	snapshot := r.listLocked()
	_ = r.saveLocked()
	r.mu.Unlock()

	r.broadcast(snapshot)
	return changed
}

// SetSize records a model's total download size, once. It is a no-op if the
// size is already known, so it is safe to call from a progress callback without
// rewriting the registry file on every tick.
func (r *Registry) SetSize(repoID string, size int64) {
	if size <= 0 {
		return
	}
	r.mu.Lock()
	m, ok := r.models[repoID]
	if !ok || m.SizeBytes > 0 {
		r.mu.Unlock()
		return
	}
	m.SizeBytes = size
	r.models[repoID] = m
	snapshot := r.listLocked()
	_ = r.saveLocked()
	r.mu.Unlock()

	r.broadcast(snapshot)
}

// UpdateProgress records download progress WITHOUT writing to disk.
//
// Download progress ticks arrive ~10 times a second. Persisting the whole
// registry file on each one would hammer the disk (and flash wear) for a value
// that is pure UI state and worthless across a restart — a download does not
// resume from a percentage. Subscribers still get the update so the UI is live;
// only the disk write is skipped. State *transitions* still go through SetState.
func (r *Registry) UpdateProgress(repoID string, progress float64) {
	r.mu.Lock()
	m, ok := r.models[repoID]
	if !ok {
		r.mu.Unlock()
		return
	}
	m.Progress = progress
	r.models[repoID] = m
	snapshot := r.listLocked()
	r.mu.Unlock()

	r.broadcast(snapshot)
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

	// Broadcast unconditionally, like every other mutator in this file: the
	// in-memory index is already changed by this point regardless of whether
	// saveLocked or the file removal below succeeds, so subscribers must hear
	// about it even on the error paths that follow.
	r.broadcast(snapshot)

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
	return nil
}

// saveLocked persists the index. Callers must hold r.mu.
func (r *Registry) saveLocked() error {
	b, err := json.MarshalIndent(r.listLocked(), "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// A random-named temp (O_EXCL) rather than a predictable "registry.json.tmp":
	// in a group-writable shared root another local account could pre-plant that
	// fixed name as a symlink and redirect the write. registry.json holds no
	// secrets (a lost one rebuilds via Rescan), but the unsafe pattern is the same
	// one hardened in config.Save, so keep them consistent.
	tmp, err := os.CreateTemp(dir, "registry-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, r.path)
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
// A directory counts as a model when it holds a plausible model config.json,
// at least one .safetensors file, and every weight shard its
// model.safetensors.index.json names. Anything mid-download (a .gropius-part
// file present) or incomplete is skipped rather than adopted as ready — and an
// existing failed record for it keeps its state and diagnostic.
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
			// Adopt only well-formed repo ids. Every write path (Download/Delete)
			// gates on ValidRepoID, so an id that fails it here — a directory name
			// another account chose in the shared cache, say — would be served on
			// /v1/models yet could never be deleted through the app. Skip it rather
			// than create an undeletable phantom.
			if !config.ValidRepoID(repoID) {
				continue
			}
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
	// Drop an entry ONLY when its directory has genuinely vanished (deleted
	// outside the app). An entry that is merely absent from `found` might just be
	// mid-write — for a shared cache, another account could be part-way through
	// downloading it right now, so inspectModelDir transiently reports it
	// incomplete. Dropping it then would wipe a healthy model from the index on a
	// race. Distinguish "gone" from "incomplete" with an explicit stat.
	for repoID, m := range r.models {
		if _, ok := found[repoID]; ok {
			continue
		}
		if m.State == StateDownloading {
			continue
		}
		if m.Path != "" {
			if _, err := os.Stat(m.Path); err == nil || !errors.Is(err, fs.ErrNotExist) {
				// Directory still exists, or the stat failed for some other reason
				// (a permission hiccup, a transient I/O error) — that is not proof
				// of deletion, so leave the entry alone rather than risk dropping a
				// healthy model from the index.
				continue
			}
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
// It walks the whole tree: downloads may create nested files, whose bytes
// must count toward the size that feeds the memory budget and whose .part
// markers still mean the download is incomplete. The loadability signals
// (config.json, weights) stay top-level, matching what mlx_lm.server loads.
//
// Presence alone is not completeness: a verification failure removes its
// .part marker, so a directory can hold config.json and some weights while
// missing a shard or holding a junk config — and the failed record it belongs
// to must not be resurrected as ready (nor the directory adopted) on the next
// rescan. Two offline checks close those holes: config.json must plausibly be
// a model config (mirroring the app layer's structural validation), and every
// weight shard named by model.safetensors.index.json must be present as a
// regular file.
func inspectModelDir(dir string) (complete bool, size int64) {
	var hasConfig, hasWeights, partial, irregular bool
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".gropius-part") {
			partial = true
		}
		if filepath.Dir(path) == dir {
			if name == "config.json" {
				hasConfig = true
			}
			if strings.HasSuffix(name, ".safetensors") {
				// Weights must be regular files: the downloader only ever
				// writes regular files, and a symlink would serve weights
				// from outside the directory while the lstat-based size
				// below charges the memory budget the link's own few dozen
				// bytes instead of the target's gigabytes.
				if d.Type().IsRegular() {
					hasWeights = true
				} else {
					irregular = true
				}
			}
		}
		if info, err := d.Info(); err == nil {
			size += info.Size()
		}
		return nil
	})
	if err != nil {
		return false, 0
	}
	complete = hasConfig && hasWeights && !partial && !irregular &&
		plausibleModelConfig(dir) && shardsPresent(dir)
	return complete, size
}

// maxManifestJSON caps how much of config.json or the shard index we read. A
// real file is a few KB (the index a few hundred KB for very large models);
// anything approaching this cap is broken or hostile, and reading it whole
// would balloon memory during a scan.
const maxManifestJSON = 8 << 20

// readManifest reads a capped JSON file from dir into v. It reports false when
// the file is missing, not a regular file, oversized, or not valid JSON.
//
// In the shared cache another account can plant a FIFO — or a symlink to one —
// under a manifest name, and a plain Open would block until a writer appears,
// wedging the startup rescan for every account. Opening with O_NONBLOCK makes
// the open itself unblockable, O_NOFOLLOW refuses symlinks outright (the
// downloader only ever writes manifests as regular files, and following a
// link would probe files outside the model directory with this account's
// privileges), and the fstat on the opened handle (not the path, so a swap
// between check and open cannot be raced in) refuses anything but a regular
// file before any read.
func readManifest(dir, name string, v any) bool {
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	b, err := io.ReadAll(io.LimitReader(f, maxManifestJSON+1))
	if err != nil || int64(len(b)) > maxManifestJSON {
		return false
	}
	return json.Unmarshal(b, v) == nil
}

// plausibleModelConfig reports whether dir's config.json can be a model
// config. The criteria match the app layer's structural validation (mlx-lm
// keys off model_type, or architectures for some models), so a download that
// failed that validation cannot be re-adopted as ready by a rescan.
func plausibleModelConfig(dir string) bool {
	var cfg map[string]any
	if !readManifest(dir, "config.json", &cfg) {
		return false
	}
	return cfg["model_type"] != nil || cfg["architectures"] != nil
}

// shardsPresent reports whether every weight shard named by
// model.safetensors.index.json exists in dir. Single-file models have no
// index, which counts as complete. A present-but-unreadable index counts as
// incomplete: we cannot attest the shard set, and "not complete" only means
// the directory is skipped or a failed record keeps its state — never that a
// ready model is dropped.
func shardsPresent(dir string) bool {
	const indexName = "model.safetensors.index.json"
	if _, err := os.Stat(filepath.Join(dir, indexName)); errors.Is(err, fs.ErrNotExist) {
		return true
	}
	var index struct {
		WeightMap map[string]string `json:"weight_map"`
	}
	if !readManifest(dir, indexName, &index) {
		return false
	}
	seen := map[string]bool{}
	for _, shard := range index.WeightMap {
		// A shard entry that is not a plain filename is not something the
		// downloader would have produced; refuse to attest completeness.
		// (filepath.Base passes "." and ".." through, so name them explicitly.)
		if shard == "" || shard == "." || shard == ".." || shard != filepath.Base(shard) {
			return false
		}
		if seen[shard] {
			continue
		}
		seen[shard] = true
		// Lstat, not Stat: a symlinked shard must fail the regular-file
		// check itself, not have its target attested in its place — the
		// downloader only ever writes regular files.
		info, err := os.Lstat(filepath.Join(dir, shard))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}
