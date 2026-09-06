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
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	// ContextLength is the architectural context length the model's own
	// configuration declares — the positional range it was trained or scaled
	// for, not what this Mac can hold at once. Zero means the configuration
	// declares none, or declares one that is not plausible; omitempty keeps
	// an unknown figure absent from the JSON rather than published as 0,
	// which a client that trims its history would read as "no context".
	ContextLength int64 `json:"context_length,omitempty"`
}

// MaxContextLength bounds the context length Gropius will believe. A model
// directory's config.json is, in shared-cache mode, a file another local
// account can write, and the figure it declares is served to the LAN — so a
// hostile or corrupt configuration must not be able to hand a client an
// absurd number to size buffers from. 8,388,608 tokens is far above any
// window in use (the lab verified prompts of about 122,000 tokens) and far
// below anything that could be mistaken for a real one.
const MaxContextLength = 1 << 23

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

// key folds a repo id into the index key. HuggingFace resolves repo ids
// case-insensitively (a re-cased id redirects to the same repo) and macOS's
// default APFS volume aliases the on-disk directory the same way, so two
// spellings are one model. Keying case-sensitively let a re-cased download
// mint a second entry over the same directory — and removing either row
// deleted the other's weights. Entries keep their first-seen spelling.
func key(repoID string) string { return config.FoldRepoID(repoID) }

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

// maxRegistryBytes caps how much of registry.json Open reads. Even a registry
// of hundreds of models is well under a megabyte; the cap exists so a planted
// file (or a link to an endless device) cannot balloon memory before the parse.
const maxRegistryBytes = 8 << 20

// Open loads the registry from path, creating an empty one if absent.
//
// The read is hardened (see config.OpenRegular): registry.json is created
// lazily in the data root, which in shared-cache mode is group-writable, so a
// FIFO planted under its name would otherwise wedge startup after the port is
// claimed, and a symlink would load an index — whose `path` fields feed
// `mlx_lm.server --model` — from outside the root. A non-regular or oversized file is an
// error, not an empty index: the sticky bit means this account can never
// replace the plant, so pretending the index is empty would only hide it.
func Open(path string) (*Registry, error) {
	r := &Registry{
		path:   path,
		models: map[string]Model{},
		subs:   map[int]chan []Model{},
	}
	b, err := config.ReadRegular(path, maxRegistryBytes)
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
		// The same gate Rescan applies: every write path validates the id, so
		// an invalid one here was planted or hand-edited, and loading it would
		// advertise on /v1/models an entry Delete refuses to touch — an
		// undeletable phantom.
		if !config.ValidRepoID(m.RepoID) {
			continue
		}
		// Case variants from an older index collapse onto one entry: prefer a
		// ready one, else the first seen. Both name one directory on APFS, so
		// dropping the record loses nothing on disk; a sideloaded
		// case-sensitive volume holding two genuinely distinct directories
		// collapses to one entry, which a Rescan re-adopts as the directory
		// names dictate. Nothing is deleted here.
		if existing, ok := r.models[key(m.RepoID)]; ok && (existing.Ready() || !m.Ready()) {
			continue
		}
		// The context length is persisted, so a hand-edited or planted index
		// can carry an absurd figure straight to the LAN with no rescan in
		// between. Bound a figure read back exactly as one read from a model
		// directory is bounded.
		if !plausibleContextLength(m.ContextLength) {
			m.ContextLength = 0
		}
		r.models[key(m.RepoID)] = m
	}
	return r, nil
}

// Get returns one model.
func (r *Registry) Get(repoID string) (Model, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[key(repoID)]
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
	// A re-cased Put updates the existing entry but never renames it: the
	// first-seen spelling stays the model's public name. Path is taken from
	// the caller as before — it must be able to move when the root does.
	if existing, ok := r.models[key(m.RepoID)]; ok {
		m.RepoID = existing.RepoID
	}
	r.models[key(m.RepoID)] = m
	snapshot := r.listLocked()
	err := r.saveLocked()
	r.mu.Unlock()

	r.broadcast(snapshot)
	return err
}

// SetState updates a model's state (and error/progress) in place and persists.
func (r *Registry) SetState(repoID string, state State, progress float64, errMsg string) error {
	r.mu.Lock()
	m, ok := r.models[key(repoID)]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%q: %w", repoID, ErrNotFound)
	}
	m.State = state
	m.Progress = progress
	m.Err = errMsg
	r.models[key(repoID)] = m
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
	m, ok := r.models[key(repoID)]
	if !ok || m.SizeBytes > 0 {
		r.mu.Unlock()
		return
	}
	m.SizeBytes = size
	r.models[key(repoID)] = m
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
	m, ok := r.models[key(repoID)]
	if !ok {
		r.mu.Unlock()
		return
	}
	m.Progress = progress
	r.models[key(repoID)] = m
	snapshot := r.listLocked()
	r.mu.Unlock()

	r.broadcast(snapshot)
}

// Remove deletes a model from the index and removes dir — its files — from
// disk. The directory is a parameter, derived by the caller from the validated
// repo id, and never the entry's stored Path: registry.json is created lazily
// in the data root, which in shared-cache mode is group-writable, so another
// local account can plant an index whose `path` names a directory this
// account owns, and one click on Remove would then os.RemoveAll it under this
// account's privileges. Every write path derives Path from the same root and
// id, so recomputing loses nothing legitimate. An empty dir removes only the
// index entry.
func (r *Registry) Remove(repoID, dir string) error {
	r.mu.Lock()
	if _, ok := r.models[key(repoID)]; !ok {
		r.mu.Unlock()
		return fmt.Errorf("%q: %w", repoID, ErrNotFound)
	}
	delete(r.models, key(repoID))
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
	if dir != "" {
		if err := removeModelDir(dir); err != nil {
			return fmt.Errorf("delete model files: %w", err)
		}
	}
	return nil
}

// removeModelDir deletes a <models>/<org>/<name> directory without following
// a symlink at <org>. os.RemoveAll by path resolves ancestors with ordinary
// symlink semantics, and in the shared cache any account can plant
// models/<org> as a link to a directory the victim owns — so the removal is
// done relative to an os.Root at the models directory, after an Lstat has
// confirmed the org entry is a real directory. A missing org or model is not
// an error: the index entry is already gone and there is nothing to delete.
func removeModelDir(dir string) error {
	name := filepath.Base(dir)
	org := filepath.Base(filepath.Dir(dir))
	models := filepath.Dir(filepath.Dir(dir))
	root, err := os.OpenRoot(models)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	fi, err := root.Lstat(org)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory — refusing to delete through it", filepath.Join(models, org))
	}
	if err := root.RemoveAll(filepath.Join(org, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
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
			complete, size, contextLength := inspectModelDir(dir)
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
				RepoID:        repoID,
				Path:          dir,
				Bytes:         size,
				ContextLength: contextLength,
				State:         StateReady,
				AddedAt:       time.Now(),
			}
		}
	}

	r.mu.Lock()
	for repoID, m := range found {
		if existing, ok := r.models[key(repoID)]; ok {
			// Don't clobber an in-flight download with a "ready" verdict.
			if existing.State == StateDownloading {
				continue
			}
			existing.Path = m.Path
			existing.Bytes = m.Bytes
			// Assigned like every other field the scan re-derives, so a model
			// recorded by a build that predates the figure gains it at the
			// next startup rescan rather than only on a re-download.
			existing.ContextLength = m.ContextLength
			existing.State = StateReady
			existing.Err = ""
			r.models[key(repoID)] = existing
			continue
		}
		r.models[key(repoID)] = m
	}
	foundKeys := make(map[string]bool, len(found))
	for repoID := range found {
		foundKeys[key(repoID)] = true
	}
	// Drop an entry ONLY when its directory has genuinely vanished (deleted
	// outside the app). An entry that is merely absent from `found` might just be
	// mid-write — for a shared cache, another account could be part-way through
	// downloading it right now, so inspectModelDir transiently reports it
	// incomplete. Dropping it then would wipe a healthy model from the index on a
	// race. Distinguish "gone" from "incomplete" with an explicit stat.
	for k, m := range r.models {
		if foundKeys[k] {
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
		delete(r.models, k)
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
func inspectModelDir(dir string) (complete bool, size int64, contextLength int64) {
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
		return false, 0, 0
	}
	// The cheap flags gate the reads, as they always have: a directory with
	// no config.json, a .part marker, or an irregular weight file is skipped
	// without opening anything. Past that gate one decode of config.json
	// serves both questions the scan asks of it — whether the directory is a
	// model at all, and what positional range it declares — so the figure
	// costs the scan no read it was not already making.
	if !hasConfig || !hasWeights || partial || irregular {
		return false, size, 0
	}
	cfg, err := readModelConfig(dir)
	if err != nil || !plausibleConfig(cfg) || CheckShards(dir) != nil {
		return false, size, 0
	}
	return true, size, contextLengthFrom(cfg)
}

// maxManifestJSON caps how much of config.json or the shard index we read. A
// real file is a few KB (the index a few hundred KB for very large models);
// anything approaching this cap is broken or hostile, and reading it whole
// would balloon memory during a scan.
const maxManifestJSON = 8 << 20

// readManifest reads a capped JSON file from dir into v. It says how the file
// failed — missing, not a regular file, oversized, or not valid JSON — so a
// caller that has to explain a rejection to a person can.
//
// In the shared cache another account can plant a FIFO — or a symlink to one —
// under a manifest name, and a plain Open would block until a writer appears,
// wedging the startup rescan for every account; the downloader only ever
// writes manifests as regular files. config.ReadRegular refuses both.
func readManifest(dir, name string, v any) error {
	b, err := config.ReadRegular(filepath.Join(dir, name), maxManifestJSON)
	if err != nil {
		return fmt.Errorf("%s is missing, unreadable, or oversized: %w", name, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s is not valid JSON: %w", name, err)
	}
	return nil
}

// readModelConfig decodes dir's config.json. This is the only decoder of a
// model's configuration in Gropius; every question asked of that file —
// whether the directory is a model, what positional range it declares, and
// whether a finished download is worth advertising — is answered from the map
// it returns.
func readModelConfig(dir string) (map[string]any, error) {
	var cfg map[string]any
	if err := readManifest(dir, "config.json", &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// plausibleConfig reports whether a decoded config.json can be a model
// config: mlx-lm keys off model_type, or architectures for some models.
func plausibleConfig(cfg map[string]any) bool {
	return cfg["model_type"] != nil || cfg["architectures"] != nil
}

// CheckModelConfig reports an error unless dir holds a config.json that is
// readable, parseable, and can be a model config.
//
// It is exported so the app layer's download validation applies exactly the
// rule the rescan applies, rather than a second copy of it — the same reason
// CheckShards is exported. Two copies drifting apart would let a directory
// pass validation as a finished download and then be refused by every
// rescan, or the reverse.
func CheckModelConfig(dir string) error {
	cfg, err := readModelConfig(dir)
	if err != nil {
		return err
	}
	if !plausibleConfig(cfg) {
		return errors.New("config.json has neither model_type nor architectures — not a loadable model")
	}
	return nil
}

// ReadContextLength reports the architectural context length declared by the
// model configuration in dir, or 0 when it declares none or declares one that
// is not plausible. It is exported so the app layer's download paths read the
// figure through this one primitive, rather than a second copy of the key
// rule that could drift from the rescan's — the same reason CheckShards is
// exported.
func ReadContextLength(dir string) int64 {
	cfg, err := readModelConfig(dir)
	if err != nil {
		return 0
	}
	return contextLengthFrom(cfg)
}

// contextLengthFrom applies the key rule, sampled on 2026-09-06 against the
// four models the lab benchmarked and seven further configurations on disk
// (research note 2026-09-06-model-bench-findings):
//
//   - max_position_embeddings at the top level is authoritative. Every plain
//     causal-LM configuration sampled declares it there.
//   - Failing that, text_config.max_position_embeddings, which is where
//     multimodal and composite configurations nest the text model's settings.
//     A configuration that states both can disagree (the sampled qwen2_vl
//     declares 32768 at the top level and 8192 under text_config), so the
//     nested key is a fallback and never an override.
//   - No scaling arithmetic is ever applied. Where rope_scaling carries
//     original_max_position_embeddings, the top-level figure is already the
//     scaled window; where it declares a factor and no pre-scaling figure,
//     the declared range is published as it stands. Under-reporting is the
//     safe direction for a client that trims its history to fit;
//     over-reporting would hand it a number the model was never scaled to.
//
// A top-level key that is present but implausible yields nothing: falling
// through to the nested key would publish a figure the configuration's own
// authoritative key contradicts.
func contextLengthFrom(cfg map[string]any) int64 {
	if _, present := cfg["max_position_embeddings"]; present {
		return positionalRange(cfg)
	}
	if text, ok := cfg["text_config"].(map[string]any); ok {
		return positionalRange(text)
	}
	return 0
}

// positionalRange reads max_position_embeddings out of one configuration
// level, accepting only a JSON number that is integral, positive and within
// the ceiling. Anything else — a string, an object, a fraction, a negative,
// an absurd magnitude — yields 0, which omits the figure.
func positionalRange(level map[string]any) int64 {
	// The bounds are checked on the float, before any conversion: a Go
	// float-to-integer conversion whose value does not fit is undefined, and
	// a configuration is free to declare 1e300.
	n, ok := level["max_position_embeddings"].(float64)
	if !ok || n != math.Trunc(n) || n <= 0 || n > MaxContextLength {
		return 0
	}
	return int64(n)
}

// plausibleContextLength is the bound applied wherever a context length
// enters the registry: on a scan, and again when one is read back from the
// index file.
func plausibleContextLength(n int64) bool {
	return n > 0 && n <= MaxContextLength
}

// CheckShards reports an error unless every weight shard named by
// model.safetensors.index.json exists in dir as a regular file. Single-file
// models have no index, which counts as complete. A present-but-unreadable
// index counts as incomplete: we cannot attest the shard set, and "not
// complete" only means the directory is skipped or a failed record keeps its
// state — never that a ready model is dropped. It is exported so the app's
// download validation applies exactly the same rule as the rescan, rather
// than a drifting copy.
func CheckShards(dir string) error {
	const indexName = "model.safetensors.index.json"
	if _, err := os.Lstat(filepath.Join(dir, indexName)); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var index struct {
		WeightMap map[string]string `json:"weight_map"`
	}
	if err := readManifest(dir, indexName, &index); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, shard := range index.WeightMap {
		// A shard entry that is not a plain filename is not something the
		// downloader would have produced; refuse to attest completeness.
		// (filepath.Base passes "." and ".." through, so name them explicitly.)
		if shard == "" || shard == "." || shard == ".." || shard != filepath.Base(shard) {
			return fmt.Errorf("%s names %q, which is not a plain file name", indexName, shard)
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
			return fmt.Errorf("%s names %s, which is missing or not a regular file", indexName, shard)
		}
	}
	return nil
}
