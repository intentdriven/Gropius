// Package config holds Gropius' on-disk layout and user settings.
//
// Everything Gropius creates lives under a single root directory so the whole
// installation — including its private Python interpreter — can be removed by
// deleting one folder.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// Paths is the on-disk layout. All fields are absolute.
type Paths struct {
	Root    string // ~/Library/Application Support/Gropius
	Bin     string // uv lives here
	Venv    string // the mlx-lm virtualenv
	Python  string // uv-managed CPython installs (UV_PYTHON_INSTALL_DIR)
	Models  string // downloaded model directories: Models/<org>/<name>
	HFCache string // HF_HUB_CACHE; must exist or mlx_lm.server's /v1/models panics
	Logs    string
	Config  string // config.json
	State   string // registry.json
	// Stats is where the request statistics store keeps its files. It is the
	// one entry that is not always under Root: see StatsDir.
	Stats string
}

// SharedRoot is the machine-wide location, used when it exists.
//
// Models are large. If two macOS accounts each keep their own copy, a 70B model
// costs 40 GB twice. When an administrator has created this directory (see
// `make install-shared`), every account shares one set of models.
const SharedRoot = "/Users/Shared/Gropius"

// DefaultRoot returns where Gropius keeps its data.
//
// Order: $GROPIUS_ROOT, then the shared directory if an administrator created
// it (see sharedRootShape) and this account can write to it, then the per-user
// Application Support directory.
func DefaultRoot() (string, error) {
	if env := os.Getenv("GROPIUS_ROOT"); env != "" {
		return env, nil
	}
	if sharedRootShape(SharedRoot) == nil && writableDir(SharedRoot) {
		return SharedRoot, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", "Gropius"), nil
}

// sharedRootShape reports whether dir is a shared root an administrator
// created, i.e. what `make install-shared` produces: a real directory (not a
// symlink), owned by root, and not writable by "other".
//
// Existence and writability are not enough. /Users/Shared itself is
// world-writable on stock macOS, so any unprivileged account could pre-create
// the shared root and become the owner of every other account's data —
// config, tokens, registry, and the interpreter the model servers run under.
// A root-owned directory can only have come from an administrator, and a
// later `make install-shared` never changes ownership, so ownership is the
// one property an attacker cannot forge. The exact mode is deliberately not
// required (an administrator may tighten it); other-write is refused because
// the design's protections all rest on the group boundary.
func sharedRootShape(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 {
		return fmt.Errorf("%s is not owned by root — refusing to adopt a shared root an administrator did not create", dir)
	}
	if fi.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("%s is world-writable — refusing to adopt it as the shared root", dir)
	}
	return nil
}

// writableDir reports whether dir exists and this process can create files in
// it. A shared directory the current account cannot write to is worse than
// useless: downloads would fail at the last moment, so fall back instead.
func writableDir(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	// A randomly-named temp (os.CreateTemp implies O_CREATE|O_EXCL) avoids the
	// symlink race a fixed name invites in a shared, group-writable directory.
	f, err := os.CreateTemp(dir, ".gropius-write-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// NewPaths derives the layout from a root directory.
func NewPaths(root string) Paths {
	return Paths{
		Root:    root,
		Bin:     filepath.Join(root, "bin"),
		Venv:    filepath.Join(root, "venv"),
		Python:  filepath.Join(root, "python"),
		Models:  filepath.Join(root, "models"),
		HFCache: filepath.Join(root, "hf", "hub"),
		Logs:    filepath.Join(root, "logs"),
		Config:  filepath.Join(root, "config.json"),
		State:   filepath.Join(root, "registry.json"),
		Stats:   StatsDir(root),
	}
}

// StatsDir is where the request statistics store keeps its files for a data
// root.
//
// Everywhere but the shared root that is a "stats" directory under the root
// itself, so an installation is still one folder to delete. The shared root is
// the exception, and the store goes under this account's own Application
// Support directory instead: the shared root is group-writable and sticky by
// design (see the Makefile's install-shared), and a file holding one account's
// own record of what it served has no business in a directory every other
// account on the Mac can write to and this one cannot re-mode. Under an
// explicit GROPIUS_ROOT the store lives under that root, so the rule is total
// and the store never appears somewhere the operator did not point Gropius at.
//
// A home directory that cannot be resolved falls back to the root, where the
// store's own refusal to create itself in a group- or other-writable directory
// is what stops the records being written: this function decides where to
// look, never whether the place is safe.
func StatsDir(root string) string {
	if !sameDir(root, SharedRoot) {
		return filepath.Join(root, "stats")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(root, "stats")
	}
	return filepath.Join(home, "Library", "Application Support", "Gropius", "stats")
}

// sameDir reports whether two paths name the same directory.
//
// Cleaned, and then resolved through any symbolic links when both sides exist:
// the root arrives from GROPIUS_ROOT or the -root flag exactly as it was
// typed, so a trailing slash, a dot segment or a link would otherwise let a
// root that IS the shared root compare unequal to it — and the whole point of
// the comparison is to keep a per-account record file out of a directory every
// account on the Mac can write to.
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return ra == rb
}

// UV is the path to the uv binary Gropius manages.
func (p Paths) UV() string { return filepath.Join(p.Bin, "uv") }

// ValidRepoID reports whether s is a well-formed HuggingFace repo id, i.e.
// exactly "<org>/<name>" using only characters HuggingFace itself allows.
//
// This is a security boundary, not a nicety: a repo id flows unmodified into a
// filesystem path (ModelDir) and, once recorded in the registry, into
// os.RemoveAll on delete. A value like "../../../etc" or "a/b/../../.." would let
// a caller escape the models directory. The allow-list (letters, digits, and
// - _ .) matches HuggingFace's own naming rules while forbidding path separators
// beyond the single required "/" and rejecting any "." path segment.
func ValidRepoID(s string) bool {
	org, name, ok := strings.Cut(s, "/")
	if !ok {
		return false
	}
	return validRepoComponent(org) && validRepoComponent(name)
}

// MaxRepoComponent bounds each half of a repo id. HuggingFace itself allows no
// more, and the bound is what turns "at most MaxModelSampling overrides" into a
// bound on the size of config.json rather than only on its entry count — an
// unbounded key would let a legal number of entries write a file Load then
// refuses to read.
const MaxRepoComponent = 96

// FoldRepoID maps a repo id to the single key that identifies the model it
// names. Two ids naming the same model fold to the same string.
//
// This is the repository's one rule for when two repo ids are the same model,
// and every part that keys anything by a repo id must use it: the registry's
// index, the runtime pool's loaded entries, the models list's join between the
// two, and the downloader's per-model serialization. It lives here, beside
// ValidRepoID, because identity and validity are the same question about the
// same value, and because this package is the one both the registry and the
// runtime already depend on.
//
// It must stay one function rather than one rule copied into several. The
// guarantee the pool and the models list rest on — at most one model per folded
// id — holds only while every site folds identically. Were one site to fold
// more loosely than the registry, two models could share a pool entry and a
// client asking for one would be served the other's weights; were one to fold
// more strictly, a model could be loaded twice.
//
// internal/archtest/repo_id_fold_test.go holds that: in the packages that key
// by a repo id it refuses any case fold that is not on a reasoned allow-list,
// and elsewhere under internal/ it refuses one whose argument is spelled like a
// repo id. The first is what catches a fold hidden behind a local variable; the
// second is a backstop, since those packages legitimately fold file names and
// header names too.
//
// HuggingFace treats repo ids case-insensitively, and ValidRepoID keeps every
// id in the registry to ASCII, so case is the whole of the rule today.
func FoldRepoID(repoID string) string { return strings.ToLower(repoID) }

func validRepoComponent(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > MaxRepoComponent {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

// VenvPython is the interpreter inside the managed virtualenv.
func (p Paths) VenvPython() string { return filepath.Join(p.Venv, "bin", "python") }

// ModelDir is where a HuggingFace repo id is stored on disk.
// "mlx-community/Qwen3-8B-4bit" -> <Models>/mlx-community/Qwen3-8B-4bit
//
// The repo id must be validated with ValidRepoID first: it becomes a filesystem
// path, and an unvalidated value like "../../.." would escape p.Models and, once
// stored in the registry, could be handed to os.RemoveAll on delete.
func (p Paths) ModelDir(repoID string) string {
	return filepath.Join(p.Models, filepath.FromSlash(repoID))
}

// EnsureDirs creates every directory in the layout.
//
// Every entry is created and inspected relative to an os.Root opened at the
// data root. Under a setgid (shared) root each must be a real directory: that
// root is group-writable, so another local account can plant a symlink under
// a layout name before it exists (and the account that launched first owns
// the real ones and can swap them later). A path-based MkdirAll and Chmod
// would follow that link and widen an arbitrary directory the victim owns to
// 3775 — group-writable by every account on the machine. Startup fails
// instead: the sticky bit means this account cannot remove the plant, and
// serving from a redirected layout is never right. A per-user root has no
// such adversary, so a layout directory the user symlinked elsewhere (models
// on an external disk, say) is followed as before.
//
// venv and python are created here too, closed (0755, never widened) like bin:
// they hold the interpreter every model server runs under, and an absent name
// in the shared root is one any account could otherwise claim first.
//
// Under a setgid root — the shared cache, which the installer marks setgid
// group-writable and sticky (mode 3775) so a model one account downloads is
// writable by the next, and only its owner can delete or rename it — the data
// directories are widened to match: MkdirAll can never produce a
// group-writable or sticky directory (0o755 carries neither bit), so without
// the chmod the first account to launch would own the layout 0755 and every
// later account's downloads would fail with a permission error. The sticky bit
// must carry over too — dropping it would let any account in the group delete
// or replace another account's model directories, exactly what the installer's
// mode is chosen to prevent (see the Makefile's install-shared target). bin is
// deliberately left 0755: it holds executables, and a group-writable bin would
// let one account replace the uv binary another account runs. Chmod errors are
// ignored — a directory created by another account cannot be re-moded by this
// one, and startup must not fail over it.
func (p Paths) EnsureDirs() error {
	if err := os.MkdirAll(p.Root, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", p.Root, err)
	}
	layout := []struct {
		abs   string
		widen bool // data directory: group-writable setgid sticky under a setgid root
	}{
		{p.Bin, false}, {p.Venv, false}, {p.Python, false},
		{p.Models, true}, {filepath.Dir(p.HFCache), true}, {p.HFCache, true}, {p.Logs, true},
	}
	fi, err := os.Stat(p.Root)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", p.Root, err)
	}
	if fi.Mode()&os.ModeSetgid == 0 {
		// Per-user root: no co-tenant, so follow whatever the user set up.
		for _, d := range layout {
			if err := os.MkdirAll(d.abs, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", d.abs, err)
			}
		}
		return nil
	}
	root, err := os.OpenRoot(p.Root)
	if err != nil {
		return fmt.Errorf("open %s: %w", p.Root, err)
	}
	defer root.Close()
	for _, d := range layout {
		rel, err := filepath.Rel(p.Root, d.abs)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("%s is outside the data root %s", d.abs, p.Root)
		}
		if err := root.Mkdir(rel, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create %s: %w", d.abs, err)
		}
		fi, err := root.Lstat(rel)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", d.abs, err)
		}
		if !fi.IsDir() {
			return fmt.Errorf("%s is not a directory (something else was planted under that name) — refusing to start", d.abs)
		}
		if d.widen {
			_ = root.Chmod(rel, 0o775|os.ModeSetgid|os.ModeSticky)
		}
	}
	return nil
}

// Config is the user-facing settings file.
type Config struct {
	// Host to bind the gateway to. 0.0.0.0 exposes it to the LAN.
	Host string `json:"host"`
	Port int    `json:"port"`

	// APIKey, when non-empty, requires "Authorization: Bearer <key>" on /v1
	// requests. Empty (the default) means the LAN endpoint is open.
	APIKey string `json:"api_key"`

	// Advertise the service over Bonjour/mDNS so other machines can find it.
	Advertise bool `json:"advertise"`

	// IdleTimeoutSec unloads a model server after this long with no requests.
	// Zero keeps models resident forever.
	IdleTimeoutSec int `json:"idle_timeout_sec"`

	// DecodeConcurrency maps to mlx_lm's --decode-concurrency: how many
	// requests get batched together during token generation.
	DecodeConcurrency int `json:"decode_concurrency"`

	// HFToken authenticates against gated HuggingFace repos.
	HFToken string `json:"hf_token"`

	// Preload lists repo ids to load into memory at startup, so the first request
	// after a restart is not a multi-minute cold start. Loaded sequentially and
	// best-effort — an invalid or too-large entry is logged and skipped, never
	// blocking startup.
	Preload []string `json:"preload,omitempty"`

	// Pinned lists repo ids that stay in memory: a pinned model is never chosen
	// as an eviction victim and is never unloaded by the idle timeout, so a
	// request that would need its memory is refused instead.
	//
	// Separate from Preload, and the two do different things. Preload loads a
	// model at startup and leaves it as evictable as any other; pinning
	// protects a model but loads nothing, so a pinned model is protected from
	// the moment something loads it. A model named in both is loaded at startup
	// and protected from then on.
	Pinned []string `json:"pinned,omitempty"`

	// Sampling holds the machine-wide sampling defaults every model server is
	// launched with, so a request that omits a parameter is served with them.
	Sampling Sampling `json:"sampling,omitzero"`

	// ModelSampling overrides Sampling for individual models, keyed by repo id.
	// A model with no entry is served with the machine-wide set.
	ModelSampling map[string]Sampling `json:"model_sampling,omitempty"`

	// Statistics turns on content-free recording of the requests this Mac
	// serves: which model, how it ended, how many tokens and how long it took.
	// It is off until the operator turns it on, and while it is off nothing
	// worked out from a request is recorded or shown
	// (adr-2609061503319212). Nothing recorded leaves the Mac, and no prompt,
	// completion, key or client address is ever part of it.
	Statistics bool `json:"statistics,omitempty"`

	// StatsMonths and StatsMaxBytes bound how much of that record is kept on
	// disk: a horizon in whole months, and a hard ceiling in bytes. The
	// ceiling always wins — the months figure prunes within it — so the store
	// is bounded however busy the Mac is, and how far back it actually reaches
	// is shown in Settings rather than promised (adr-2609061610107154).
	StatsMonths   int   `json:"stats_months,omitempty"`
	StatsMaxBytes int64 `json:"stats_max_bytes,omitempty"`

	// PerModel holds the per-model settings that are not sampling parameters,
	// keyed by the registry's canonical repo id. A model with no entry runs on
	// the machine-wide settings above, which is what every model does until the
	// operator says otherwise.
	PerModel map[string]ModelSettings `json:"per_model,omitempty"`
}

// ModelSettings are the settings of a single model that are not sampling
// parameters, which have their own map above.
//
// Every field is off or zero by default, so a model gains a behavior only when
// the operator switches it on for that model in Settings.
//
// A file written by a newer build still loads: a setting this build does not
// know is ignored and the ones it does know are unaffected. It does not
// survive a save, though — this build re-marshals what it holds, so running an
// older build and saving settings drops a newer build's per-model fields for
// good. That is the same bargain every field in this file has always made, and
// it is why a rollback is a decision rather than a shrug.
type ModelSettings struct {
	// MergeSystemMessages folds every system-role message of a chat completion
	// request for this model into one leading system message before the request
	// reaches the model server, which is the shape a chat template that refuses
	// a system message anywhere but the front will accept.
	//
	// It is the one case in which Gropius reads the content of a request's
	// messages, it reads them for no other purpose, and it keeps nothing it
	// reads (adr-2609061610102325). Off unless the operator switches it on for
	// this model.
	MergeSystemMessages bool `json:"merge_system_messages,omitempty"`
}

// ValidatePerModelKeys reports whether every key of a per-model settings map
// names a model, i.e. is a well-formed "<org>/<name>" repo id.
//
// A key is matched against the id a request resolves to, so a key of any other
// shape names nothing and would sit in the settings file looking effective
// while applying to no request ever made. Refusing it at the point of saving
// is the only moment the operator is there to see it.
func ValidatePerModelKeys(m map[string]ModelSettings) error {
	// Sorted, so a file with several unusable keys names the same one every
	// time it is refused rather than whichever the map iteration reached first.
	for _, id := range perModelKeys(m) {
		if !ValidRepoID(id) {
			return fmt.Errorf("per-model settings for %q: not a model id of the form <org>/<name>", id)
		}
	}
	if len(m) > MaxPerModel {
		return fmt.Errorf("per-model settings name %d models, more than the %d this holds", len(m), MaxPerModel)
	}
	return nil
}

// MaxPerModel bounds the per-model settings map for the same reason
// MaxModelSampling bounds the sampling overrides beside it, and to the same
// figure: everything saved is written to config.json, which Load refuses above
// MaxConfigBytes, and a config.json that cannot be read sends the next start
// into its fail-closed loopback-only branch. Two per-model maps means two
// levers for that, so both are bounded.
const MaxPerModel = MaxModelSampling

// perModelKeys returns a per-model settings map's keys in a stable order, so
// that a map with more than one problem in it names the same one every time
// rather than whichever the map iteration reached first.
func perModelKeys(m map[string]ModelSettings) []string {
	return slices.Sorted(maps.Keys(m))
}

// sanitizePerModel drops every per-model entry this build cannot use and
// returns what it dropped, so a settings file written by hand, restored from a
// backup, or produced by another build still loads.
//
// Refusing the file instead would be worse than useless. The panel serves the
// stored settings into its form and the form posts them back, so one unusable
// key would return on the next save and be refused there — wedging every
// settings change there is, the API key included, until someone edited the
// file by hand. This is the same treatment the sampling overrides beside it
// get, for the same reason.
func (c *Config) sanitizePerModel() []string {
	if len(c.PerModel) == 0 {
		return nil
	}
	var dropped []string
	kept := make(map[string]ModelSettings, len(c.PerModel))
	seen := map[string]string{} // folded id -> the spelling kept
	for _, id := range perModelKeys(c.PerModel) {
		if !ValidRepoID(id) {
			dropped = append(dropped, "per_model["+id+"]")
			continue
		}
		// Two spellings of one repo id would make the effective settings
		// depend on map iteration order. Keep the first in sorted order so the
		// outcome is the same on every start.
		folded := FoldRepoID(id)
		if first, ok := seen[folded]; ok {
			dropped = append(dropped, "per_model["+id+"] (duplicate of "+first+")")
			continue
		}
		if len(kept) >= MaxPerModel {
			dropped = append(dropped, "per_model["+id+"] (beyond the "+
				strconv.Itoa(MaxPerModel)+"-model ceiling)")
			continue
		}
		seen[folded] = id
		kept[id] = c.PerModel[id]
	}
	if len(kept) == 0 {
		kept = nil
	}
	c.PerModel = kept
	return dropped
}

// MaxPinned bounds the pinned list for the same reason MaxPerModel bounds the
// per-model settings beside it, and to the same figure: everything saved is
// written to config.json, which Load refuses above MaxConfigBytes, and a
// config.json that cannot be read sends the next start into its fail-closed
// loopback-only branch. The memory budget bounds how many pins can be *useful*,
// but Validate is machine-independent, so the count is what is bounded here.
const MaxPinned = MaxPerModel

// validatePinned checks the pinned list the way validateSampling checks the
// sampling overrides: this is the settings path, where a human is waiting for
// an answer, so an entry that names no model is refused rather than dropped.
func (c Config) validatePinned() error {
	if len(c.Pinned) > MaxPinned {
		return fmt.Errorf("at most %d models may be pinned, got %d", MaxPinned, len(c.Pinned))
	}
	seen := map[string]string{}
	for _, id := range c.Pinned {
		if !ValidRepoID(id) {
			return fmt.Errorf("pinned model %q: not a model id of the form <org>/<name>", id)
		}
		// Two spellings of one repo id are two entries but one model. The pool
		// folds its lookup, so the duplicate would protect nothing extra while
		// counting twice against the fit check the save is about to run.
		folded := FoldRepoID(id)
		if first, ok := seen[folded]; ok {
			return fmt.Errorf("pinned models %q and %q name the same model", first, id)
		}
		seen[folded] = id
	}
	return nil
}

// sanitizePinned drops every pinned entry this build cannot use and returns
// what it dropped, so a settings file written by hand, restored from a backup,
// or produced by another build still loads.
//
// Refusing the file instead would be worse than useless, for the reason
// sanitizePerModel gives: the panel serves the stored settings into its form
// and the form posts them back, so one unusable entry would return on the next
// save and be refused there, wedging every settings change there is.
func (c *Config) sanitizePinned() []string {
	if len(c.Pinned) == 0 {
		return nil
	}
	var dropped []string
	kept := make([]string, 0, len(c.Pinned))
	seen := map[string]string{} // folded id -> the spelling kept
	for _, id := range c.Pinned {
		if !ValidRepoID(id) {
			dropped = append(dropped, "pinned["+id+"]")
			continue
		}
		folded := FoldRepoID(id)
		if first, ok := seen[folded]; ok {
			dropped = append(dropped, "pinned["+id+"] (duplicate of "+first+")")
			continue
		}
		if len(kept) >= MaxPinned {
			dropped = append(dropped, "pinned["+id+"] (beyond the "+
				strconv.Itoa(MaxPinned)+"-model ceiling)")
			continue
		}
		seen[folded] = id
		kept = append(kept, id)
	}
	if len(kept) == 0 {
		kept = nil
	}
	c.Pinned = kept
	return dropped
}

// Clone returns a copy that shares no slice, map or pointer with the original.
//
// The settings endpoint decodes a posted body into a copy of the live config
// so that fields the form does not own keep their values. A shallow copy is
// not enough for that: encoding/json writes through an existing non-nil
// pointer into its target, and reuses an existing slice's and map's storage,
// so a posted value would reach the running configuration before Validate had
// a chance to refuse it — and would stay there once it had.
func (c Config) Clone() Config {
	out := c
	if c.Preload != nil {
		out.Preload = append([]string(nil), c.Preload...)
	}
	if c.Pinned != nil {
		out.Pinned = append([]string(nil), c.Pinned...)
	}
	out.Sampling = c.Sampling.Clone()
	if c.ModelSampling != nil {
		out.ModelSampling = make(map[string]Sampling, len(c.ModelSampling))
		for k, v := range c.ModelSampling {
			out.ModelSampling[k] = v.Clone()
		}
	}
	if c.PerModel != nil {
		out.PerModel = make(map[string]ModelSettings, len(c.PerModel))
		for k, v := range c.PerModel {
			out.PerModel[k] = v
		}
	}
	return out
}

// Retention bounds for the statistics store.
//
// The defaults are the ADR's starting values: at about 150 bytes a record,
// 200 MB is over four months of ten thousand requests a day, so the ceiling
// and the horizon are of the same order and neither is decorative. The floor
// on the ceiling is two rotated files, below which the store would drop a file
// it had only just opened; the ceiling on the ceiling and the horizon are
// there so a mistyped figure is refused rather than filling a disk or being
// read as "forever". Ten gigabytes is decades of records at the rate above,
// and small enough that a mistyped figure cannot quietly become the whole
// disk — nothing here asks the filesystem how much room is left.
const (
	DefaultStatsMonths   = 6
	DefaultStatsMaxBytes = 200 << 20
	MinStatsMaxBytes     = 10 << 20
	MaxStatsMaxBytes     = 10 << 30
	MaxStatsMonths       = 120
)

// Default returns the shipping defaults: LAN-exposed, unauthenticated.
func Default() Config {
	return Config{
		Host:              "0.0.0.0",
		Port:              11535,
		APIKey:            "",
		Advertise:         true,
		IdleTimeoutSec:    0,
		DecodeConcurrency: 4,
		StatsMonths:       DefaultStatsMonths,
		StatsMaxBytes:     DefaultStatsMaxBytes,
	}
}

// sanitizeStats repairs a retention figure this build cannot use and returns
// what it repaired, so a hand-edited file, a backup or another build's
// settings still load.
//
// Repaired rather than refused, for the reason Load's own comment gives: a
// refused config.json sends the next start into its fail-closed loopback-only
// branch, and a machine-wide outage is far too much to pay for a number that
// decides how long a statistics file is kept. Save still refuses the same
// values outright, which is the moment the operator is there to read why.
func (c *Config) sanitizeStats() []string {
	var repaired []string
	if c.StatsMonths < 1 || c.StatsMonths > MaxStatsMonths {
		repaired = append(repaired, "stats_months="+strconv.Itoa(c.StatsMonths))
		c.StatsMonths = DefaultStatsMonths
	}
	if c.StatsMaxBytes < MinStatsMaxBytes || c.StatsMaxBytes > MaxStatsMaxBytes {
		repaired = append(repaired, "stats_max_bytes="+strconv.FormatInt(c.StatsMaxBytes, 10))
		c.StatsMaxBytes = DefaultStatsMaxBytes
	}
	return repaired
}

// Validate reports whether the config is usable.
func (c Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port %d out of range", c.Port)
	}
	if c.Host == "" {
		return errors.New("host must not be empty")
	}
	if c.DecodeConcurrency < 1 {
		return fmt.Errorf("decode_concurrency must be >= 1, got %d", c.DecodeConcurrency)
	}
	if err := c.validatePinned(); err != nil {
		return err
	}
	if c.StatsMonths < 1 || c.StatsMonths > MaxStatsMonths {
		return fmt.Errorf("keep statistics for between 1 and %d months, got %d", MaxStatsMonths, c.StatsMonths)
	}
	if c.StatsMaxBytes < MinStatsMaxBytes || c.StatsMaxBytes > MaxStatsMaxBytes {
		return fmt.Errorf("the statistics store's limit must be between %d and %d bytes, got %d",
			MinStatsMaxBytes, MaxStatsMaxBytes, c.StatsMaxBytes)
	}
	// A sampling default becomes a launch flag on every model server, and the
	// model server validates the effective value of every request against it:
	// a value it rejects turns one save into a 400 on every request that omits
	// that parameter. Load sanitizes before it validates, so this strictness
	// only ever refuses a save, never a start-up.
	return c.validateSampling()
}

// ExposedToLAN reports whether the bind address accepts non-loopback traffic.
// Anything that is not loopback counts: a specific interface address exposes
// the gateway to the LAN just as the wildcard does, and must trigger the same
// security warnings.
func (c Config) ExposedToLAN() bool {
	if c.Host == "localhost" {
		return false
	}
	if ip := net.ParseIP(c.Host); ip != nil {
		return !ip.IsLoopback()
	}
	return true
}

// Load reads config from path, returning defaults if the file does not exist.
// Unknown or missing fields fall back to their defaults, so a config written by
// an older build still loads.
//
// The second return value names the sampling preferences that were dropped
// because the model server would not accept them, or because they name no
// addressable model; the caller logs them. They are dropped rather than
// refused because a sampling value is a preference, not a serving invariant:
// refusing the file sends main into its fail-closed loopback-only mode, which
// is a machine-wide outage to pay for one number that could simply be
// ignored. Everything that decides how the server is reachable is still
// validated, and still an error.
//
// This covers a value out of range, not a value of the wrong shape. A
// sampling field holding a string, or a number too large for a float64, fails
// in the unmarshal above and is an error like any other malformed config.json
// — sampling is not special enough to warrant a second decoding path through
// json.RawMessage in a file another local account can write.
//
// The read is hardened (see OpenRegular): Load runs before the port is
// claimed, so a FIFO planted under this name in a shared root would otherwise
// hang startup before the fail-closed branch in main could ever run, and a
// symlinked or oversized file is refused rather than applied. Any such refusal
// is an error, which main treats as "lock down to loopback".
func Load(path string) (Config, []string, error) {
	cfg := Default()
	b, err := ReadRegular(path, MaxConfigBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil, nil
	}
	if err != nil {
		return cfg, nil, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Default(), nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	dropped := append(cfg.sanitizeSampling(), cfg.sanitizePerModel()...)
	dropped = append(dropped, cfg.sanitizePinned()...)
	dropped = append(dropped, cfg.sanitizeStats()...)
	if err := cfg.Validate(); err != nil {
		return Default(), nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, dropped, nil
}

// Save atomically writes config to path.
//
// A config that would not load again is refused rather than written. Load caps
// what it will read, and a config.json over that cap is not a smaller problem
// than a corrupt one: main falls back to loopback-only with the shipping
// defaults, so the API key and the bind address a user set are silently
// unused until someone edits the file by hand. The check is here rather than
// in Validate because it is a property of the encoded bytes — MarshalIndent's
// output is larger than the body it came from, so bounding the request that
// carried it is not enough.
func Save(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if len(b)+1 > MaxConfigBytes {
		return fmt.Errorf("settings are %d bytes, over the %d-byte limit config.json can be read back from",
			len(b)+1, MaxConfigBytes)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// config.json holds the API key and HuggingFace token. Never write it through
	// a predictable temp name: in a shared, group-writable root another local
	// account could pre-create "config.json.tmp" as a symlink (redirecting the
	// write) or with loose permissions the rename would then preserve. os.CreateTemp
	// uses a random name with O_EXCL and mode 0600, closing both holes.
	tmp, err := os.CreateTemp(dir, "config-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // harmless no-op once the rename succeeds
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	// Flush to disk before the rename. The rename is atomic against a process
	// crash, but not against a power loss that makes the rename durable before the
	// data — which would leave a truncated config.json. A truncated config fails to
	// parse and reverts to defaults, so durability here is a security concern, not
	// just a tidiness one.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
