// Package config holds Gropius' on-disk layout and user settings.
//
// Everything Gropius creates lives under a single root directory so the whole
// installation — including its private Python interpreter — can be removed by
// deleting one folder.
package config

import (
	"crypto/rand"
	"encoding/base64"
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
	"unicode/utf8"
)

// Paths is the on-disk layout. All fields are absolute.
type Paths struct {
	Root    string // ~/Library/Application Support/Gropius
	Bin     string // uv lives here
	Venv    string // the mlx-lm virtualenv
	Python  string // uv-managed CPython installs (UV_PYTHON_INSTALL_DIR)
	Models  string // downloaded model directories: Models/<org>/<name>
	HFCache string // HF_HUB_CACHE; must exist or mlx_lm.server's /v1/models panics
	// Account is the directory holding everything that belongs to THIS macOS
	// account rather than to the installation. Under the shared root that is
	// the account's own Application Support directory; everywhere else it is
	// the root itself. See accountDir.
	Account string
	// Logs, Config (config.json) and State (registry.json) are this account's
	// own and live under Account. A log is one account's record of what its own
	// subprocess printed, and the settings hold its API key and HuggingFace
	// token; neither belongs in a directory shared with every other account.
	Logs   string
	Config string
	State  string
	// Stats is where the request statistics store keeps its files: under
	// Account, in a "stats" directory. See StatsDir.
	Stats string
}

// SharedRoot is the machine-wide location, used when it exists.
//
// Models are large. If two macOS accounts each keep their own copy, a 70B model
// costs 40 GB twice. When an administrator has created this directory (see
// `make install-shared`), every account shares one set of models.
const SharedRoot = "/Users/Shared/Gropius"

// sharedRoot is the shared root everything actually compares against, so a
// test can stand a temporary directory in its place and exercise the real
// derivation rather than a hand-copied one. It is unexported and never written
// outside this package's own tests: the shipped binary holds one value, the
// constant above.
var sharedRoot = SharedRoot

// userSupportDir is this account's own Gropius directory in Application
// Support — the one place a per-user install lives, and the one place a shared
// install keeps what an account does not share. It is derived once here so the
// default root and accountDir cannot come to disagree about where it is.
func userSupportDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", "Gropius"), nil
}

// DefaultRoot returns where Gropius keeps its data.
//
// Order: $GROPIUS_ROOT, then the shared directory if an administrator created
// it (see sharedRootShape) and this account can write to it, then the per-user
// Application Support directory.
func DefaultRoot() (string, error) {
	if env := os.Getenv("GROPIUS_ROOT"); env != "" {
		return env, nil
	}
	if sharedRootShape(sharedRoot) == nil && writableDir(sharedRoot) {
		return sharedRoot, nil
	}
	return userSupportDir()
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

// ExecRoot is where this account's EXECUTABLES live for a data root: uv, the
// virtualenv, and the uv-managed CPython tree.
//
// Everywhere but the shared root that is the root itself. The shared root is
// the exception, and the executables go under this account's own Application
// Support directory instead, for a reason the data does not share: models are
// inert bytes every account may read, while these are programs every account
// EXECUTES. One shared copy means whichever account provisioned it owns those
// files and can rewrite them at any time, and every other account then runs
// the result under its own uid — an owner-trust residue no mode check can
// remove, because the owner is legitimately allowed to write their own files.
// Per-account executables remove it by construction: no account ever executes
// another account's binaries. Models stay shared, which is what the shared
// root exists for; a 70 GB model is not duplicated to buy this.
//
// Follows StatsDir's rule and its fallback: a home directory that cannot be
// resolved falls back to the root, where the provisioner's own refusal to run
// an interpreter that is not owned by this account or root is what stops it.
// This function decides where to look, never whether the place is safe.
func ExecRoot(root string) string { return accountDir(root) }

// accountDir is this repository's one rule for "the directory that belongs to
// THIS macOS account" given a data root, and every part of the layout that is
// not shared resolves through it: the executables (ExecRoot), the statistics
// store (StatsDir), and this account's own state files — config.json and
// registry.json.
//
// Everywhere but the shared root that is the root itself, so an installation
// stays one folder to delete. The shared root is the exception: it is
// group-writable and sticky by design (see the Makefile's install-shared), and
// anything that belongs to one account has no business in a directory every
// other account on the Mac can write to and this one cannot re-mode.
//
// It must stay one function rather than one rule copied into several. The three
// callers ask the same question — where does this account keep what it does not
// share — and an answer that differed between them would put one account's
// secrets where another account's rule said it was safe to look.
//
// A home directory that cannot be resolved falls back to the root, where each
// caller's own refusal is what stops the unsafe write: this function decides
// where to look, never whether the place is safe.
func accountDir(root string) string {
	if !sameDir(root, sharedRoot) {
		return root
	}
	dir, err := userSupportDir()
	if err != nil {
		return root
	}
	return dir
}

// NewPaths derives the layout from a root directory.
//
// What is shared and what is this account's own is the whole of the split: the
// models and the HuggingFace cache they arrive through sit in the root, and
// everything that belongs to one account — its executables, its settings, its
// registry, its logs and its statistics — resolves through accountDir.
func NewPaths(root string) Paths {
	acct := accountDir(root)
	return Paths{
		Root:    root,
		Bin:     filepath.Join(acct, "bin"),
		Venv:    filepath.Join(acct, "venv"),
		Python:  filepath.Join(acct, "python"),
		Models:  filepath.Join(root, "models"),
		HFCache: filepath.Join(root, "hf", "hub"),
		Account: acct,
		Logs:    filepath.Join(acct, "logs"),
		Config:  filepath.Join(acct, "config.json"),
		State:   filepath.Join(acct, "registry.json"),
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
func StatsDir(root string) string { return filepath.Join(accountDir(root), "stats") }

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
// Every entry that is UNDER the data root is created and inspected relative to
// an os.Root opened there. Under a shared cache the layout straddles two
// directories — accountDir holds this account's executables and state files
// outside the root — and those entries are created plainly: they sit in a
// directory no other account can write to, so the co-tenant this walk defends
// against cannot reach them. A shared data directory (one marked widen below)
// that resolves outside the root is still refused, since widening one to
// group-writable elsewhere is the very thing being prevented.
//
// Under a setgid (shared) root each entry under the root must be a real
// directory: that
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
	// This account's own state directory, created closed and closed if it is
	// already there. Under the shared root it is the one directory holding
	// things no other account may have — config.json's API key and HuggingFace
	// token, and the registry that decides what this account's gateway serves.
	// The chmod is not belt and braces: an account that ran a per-user install
	// before joining a shared cache already has this directory at 0755, and
	// MkdirAll would leave it there. Everywhere else it IS the root, created
	// just above, and nothing changes.
	//
	// A chmod that fails is not fatal, as for every other mode in this
	// function: the files inside are written 0600 whatever the directory says,
	// and a directory this account cannot re-mode is one it does not own.
	if acct := p.accountStateDir(); acct != filepath.Clean(p.Root) {
		if err := os.MkdirAll(acct, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", acct, err)
		}
		_ = os.Chmod(acct, 0o700)
	}
	layout := []struct {
		abs   string
		widen bool // SHARED data directory: group-writable setgid sticky under a setgid root
	}{
		{p.Bin, false}, {p.Venv, false}, {p.Python, false}, {p.Logs, false},
		{p.Models, true}, {filepath.Dir(p.HFCache), true}, {p.HFCache, true},
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
			// Outside the data root. Under a shared cache the layout straddles
			// two directories by design: accountDir puts this account's
			// executables and its own files in its Application Support
			// directory, so no account ever executes another's binaries or
			// writes into another's files. Those entries are created plainly,
			// like the per-user branch above and for the same reason — the
			// adversary the os.Root walk defends against is a co-tenant of the
			// group-writable root, and a path outside that root is one they
			// cannot reach.
			//
			// What makes the plain MkdirAll safe is that the path is inside this
			// account's home directory, which macOS creates at 0700: no other
			// account can create a component of it, so there is nothing to
			// re-check between the creation and the use. The account's own state
			// directory is held to 0700 above for the same reason. A path
			// outside the root that is NOT in this account's home would need the
			// same re-check the in-root branch does; nothing in the layout puts
			// one there.
			//
			// A SHARED data directory outside the root is a different matter and
			// is still refused: the models and the HuggingFace cache are
			// what the root exists to hold, and widening one to group-writable
			// somewhere else is what the refusal was written to stop.
			if d.widen {
				return fmt.Errorf("%s is shared with every account but is outside the data root %s", d.abs, p.Root)
			}
			if err := os.MkdirAll(d.abs, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", d.abs, err)
			}
			continue
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

	// UpstreamHeaderTimeoutSec bounds how long the gateway waits for a model
	// server to return response headers, i.e. to finish prefill.
	//
	// Zero means automatic: the bound is derived from the prompt's size, which
	// is what actually decides prefill time. A fixed bound cannot be right for
	// both a 4K prompt and a 256K one — measured prefill on this hardware falls
	// from about 1,300 tokens per second at 8K to under 200 at the largest
	// verified sizes, so a bound sized from small prompts is wrong by two to
	// four times exactly where it matters.
	//
	// A positive value overrides the derivation with a fixed number of seconds.
	// It exists because the derivation encodes a measurement, and a measurement
	// can be wrong for hardware or a model nobody tested: an operator who finds
	// it so must be able to say so without waiting for a release.
	UpstreamHeaderTimeoutSec int `json:"upstream_header_timeout_sec"`

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

	// MaxResidentBytes caps the total charged size of the models that may be
	// in memory at once. Zero — the default, and what a fresh install stores —
	// means a share of this Mac's physical memory, resolved where that size is
	// known; it is not a second representation of the same figure.
	//
	// Bytes, because that is what the pool's own messages already speak in and
	// what a load is measured against; the panel types gigabytes and converts.
	// Validate keeps to the one rule that holds on every Mac — the figure must
	// not be negative — since a ceiling checked here would make a config.json
	// written on a large Mac invalid on a smaller one, and an invalid config
	// takes the whole install down to loopback with the shipping defaults.
	// The ceiling belongs where the machine's size is known, at a settings
	// save.
	MaxResidentBytes int64 `json:"max_resident_bytes,omitempty"`

	// EvictionGrace turns on the bounded wait a request pays instead of
	// evicting a model that has only just finished work. It is off unless the
	// operator turns it on, and while it is off a request for a model that
	// does not fit takes an eviction victim at once, exactly as it always has.
	EvictionGrace bool `json:"eviction_grace,omitempty"`

	// EvictionGraceSec is how long a model is protected after it finishes a
	// request, and EvictionMaxWaitSec is the longest a request will wait for
	// room before it is refused. Both are whole seconds and both apply only
	// while EvictionGrace is on.
	//
	// Zero means the default, the way it does for MaxResidentBytes: a field
	// cleared in Settings, a file written by a build that had no such field,
	// and a fresh install all mean the same thing, and none of them may stand
	// between the operator and saving an unrelated setting. Read them through
	// GraceSeconds and MaxWaitSeconds, which resolve that.
	//
	// The grace may not exceed a non-zero IdleTimeoutSec: the idle reaper
	// would otherwise unload the very model a wait is protecting, which would
	// make the promise false.
	EvictionGraceSec   int `json:"eviction_grace_sec,omitempty"`
	EvictionMaxWaitSec int `json:"eviction_max_wait_sec,omitempty"`

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

// MaxPreload bounds the preload list, for the reason MaxPinned bounds the
// pinned list beside it and to the same figure: a config.json grown without
// limit is one the next start cannot read, and a start that cannot read it
// locks the server down to loopback. The list names models to load into memory
// at startup, so it cannot usefully be longer than the models this Mac can
// hold — a bound the memory budget puts in the single digits — while Validate
// has to be machine-independent, so what is bounded here is the count, at the
// figure the two per-model maps already use.
const MaxPreload = MaxPinned

// MaxAPIKeyBytes bounds the API key, for the same reason and against the same
// hazard.
//
// GenerateAPIKey produces 43 characters (32 random bytes, base64 without
// padding), which is what a fresh install carries and what almost every
// install keeps. The ceiling is an order of magnitude above that, so a
// passphrase a person chose or a password manager produced fits with room to
// spare, and the field stops being a lever for growing config.json towards
// MaxConfigBytes from the settings endpoint.
const MaxAPIKeyBytes = 512

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

// sanitizeBudget drops a memory budget this build cannot use and names what it
// dropped, so a hand-edited file still loads.
//
// Falling back to the default is the whole point: refusing the file would send
// main into its fail-closed loopback-only branch, so the API key and the bind
// address the operator set would go unused over one figure that has a perfectly
// good default sitting behind it.
func (c *Config) sanitizeBudget() []string {
	if c.MaxResidentBytes >= 0 {
		return nil
	}
	dropped := []string{"max_resident_bytes[" + strconv.FormatInt(c.MaxResidentBytes, 10) + "]"}
	c.MaxResidentBytes = 0
	return dropped
}

// sanitizePreload cuts a preload list this build cannot use down to the
// ceiling and names what it cut, so a hand-edited file, a backup or another
// build's settings still load.
//
// Cut rather than refused, for the reason sanitizePinned gives: the panel
// serves the stored settings into its form and the form posts them back, so a
// list that is refused rather than trimmed would come back on the next save
// and be refused there — wedging every settings change there is, the API key
// included, until someone edited the file by hand.
//
// The entries kept are the ones at the front. They are all equally
// well-formed — only their position is the problem — so one line naming the
// field and the count says everything an operator can act on, where a line per
// entry would say the same thing two hundred times.
func (c *Config) sanitizePreload() []string {
	if len(c.Preload) <= MaxPreload {
		return nil
	}
	cut := len(c.Preload) - MaxPreload
	c.Preload = c.Preload[:MaxPreload]
	return []string{"preload (" + strconv.Itoa(cut) + " entries beyond the " +
		strconv.Itoa(MaxPreload) + "-model ceiling)"}
}

// sanitizeAPIKey trims an API key this build cannot use and says that it did,
// so a hand-edited file, a backup or another build's settings still load.
//
// Trimmed rather than cleared: clearing it would turn one over-long value in a
// file into a LAN-exposed server that anyone on the network can use, which is
// the one outcome this configuration is never allowed to arrive at by
// accident. Trimmed rather than refused, for the reason the sanitizers above
// give — a refused file locks the next start down to loopback, and the key
// would come back on the next save and be refused there.
//
// What is left is still a key, and a client using the old one is refused: the
// file was already carrying a value no save would have written, and the panel
// shows the operator exactly what is in force now. The value is never named in
// what is reported — it is the secret this field exists to hold.
func (c *Config) sanitizeAPIKey() []string {
	if len(c.APIKey) <= MaxAPIKeyBytes {
		return nil
	}
	key := c.APIKey[:MaxAPIKeyBytes]
	// A cut through the middle of a multi-byte character would leave a string
	// that is not valid UTF-8, which Save would re-encode as something else
	// again. Step back to the last whole character; at most three steps for a
	// value that is UTF-8 at all.
	whole := key
	for len(whole) > 0 && !utf8.ValidString(whole) {
		whole = whole[:len(whole)-1]
	}
	// A value with no whole character anywhere in it leaves nothing to step
	// back to, and the raw cut is kept rather than the empty string this would
	// otherwise produce. Nothing reaches here with one today — Load's decoder
	// coerces invalid UTF-8 to U+FFFD — and the guard does not depend on that
	// staying true: an empty key is an open server on a LAN-exposed install,
	// which is the one repair this function must never make.
	if whole != "" {
		key = whole
	}
	c.APIKey = key
	return []string{"api_key (trimmed to the " + strconv.Itoa(MaxAPIKeyBytes) + "-byte ceiling)"}
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
// The defaults are the ADR's starting values, with its arithmetic corrected by
// measurement: internal/stats' BenchmarkLatestAtTheCap fills the cap and
// reports about 230 bytes a record — a JSON Lines record carries its field
// names on every line — so 200 MB is roughly three months of ten thousand
// requests a day, not the four the ADR reasoned to from 150 bytes. The size
// cap therefore bites at about half the six-month horizon on a Mac that busy,
// which is what makes it the hard bound rather than a formality. The floor
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

// Bounds for the two eviction-grace intervals.
//
// The defaults are the record's: 120 seconds covers the pause an agent takes
// between turns, which is the eviction this feature exists to prevent, and 300
// seconds is longer than most clients will wait but short enough that a
// request that will never be served fails rather than hangs. The ceiling is an
// hour: past that the wait is longer than any interactive client's own
// timeout, so a figure above it is a typing mistake rather than a policy. Zero
// is not a figure but the absence of one, and resolves to the default: see
// Config.GraceSeconds.
const (
	DefaultEvictionGraceSec   = 120
	DefaultEvictionMaxWaitSec = 300
	MaxEvictionWaitSec        = 3600
)

// validateGrace holds the two intervals to their ranges and to the idle
// timeout.
//
// The idle rule applies only while the switch is on: with grace off the two
// figures decide nothing, and refusing a save over an inert pair would put a
// setting the operator is not using between them and the API key they came to
// set.
func (c Config) validateGrace() error {
	if c.EvictionGraceSec < 0 || c.EvictionGraceSec > MaxEvictionWaitSec {
		return fmt.Errorf("the eviction grace must be between 0 and %d seconds, got %d",
			MaxEvictionWaitSec, c.EvictionGraceSec)
	}
	if c.EvictionMaxWaitSec < 0 || c.EvictionMaxWaitSec > MaxEvictionWaitSec {
		return fmt.Errorf("the maximum wait must be between 0 and %d seconds, got %d",
			MaxEvictionWaitSec, c.EvictionMaxWaitSec)
	}
	if c.EvictionGrace && c.IdleTimeoutSec > 0 && c.GraceSeconds() > c.IdleTimeoutSec {
		return fmt.Errorf(
			"the eviction grace (%d s) must not be longer than the idle timeout (%d s), "+
				"or the idle timeout unloads the model the wait is protecting",
			c.GraceSeconds(), c.IdleTimeoutSec)
	}
	// A maximum wait below the grace does not shorten the wait, it silently
	// disables the rule that stops one client starving another: a waiting
	// request may override a model's protection once its own age reaches the
	// grace, and it is refused once its age reaches the maximum, so with the
	// maximum the smaller of the two the first can never happen. Refused
	// rather than raised at a save because the two are one pair, edited
	// together in one fieldset, and telling the operator is better than
	// quietly serving them a different figure.
	if c.EvictionGrace && c.MaxWaitSeconds() < c.GraceSeconds() {
		return fmt.Errorf(
			"the maximum wait (%d s) must not be shorter than the eviction grace (%d s), "+
				"or a waiting request is refused before its own wait can override the grace",
			c.MaxWaitSeconds(), c.GraceSeconds())
	}
	return nil
}

// GraceSeconds and MaxWaitSeconds are the two intervals in force, with zero
// resolved to its default. Everything that acts on them reads them here, so
// "unset" has one meaning and not one per caller.
func (c Config) GraceSeconds() int {
	if c.EvictionGraceSec <= 0 {
		return DefaultEvictionGraceSec
	}
	return c.EvictionGraceSec
}

func (c Config) MaxWaitSeconds() int {
	if c.EvictionMaxWaitSec <= 0 {
		return DefaultEvictionMaxWaitSec
	}
	return c.EvictionMaxWaitSec
}

// sanitizeGrace repairs eviction-grace figures this build cannot use and
// returns what it repaired, so a hand-edited file, a backup or another build's
// settings still load.
//
// Repaired rather than refused, for the reason sanitizeStats gives: a refused
// config.json sends the next start into its fail-closed loopback-only branch.
// An out-of-range interval falls back to its default; a grace longer than the
// idle timeout is clamped to that timeout rather than dropped, because the
// operator asked for a grace and the longest one the reaper leaves intact is
// the timeout itself.
func (c *Config) sanitizeGrace() []string {
	var repaired []string
	if c.EvictionGraceSec < 0 || c.EvictionGraceSec > MaxEvictionWaitSec {
		repaired = append(repaired, "eviction_grace_sec="+strconv.Itoa(c.EvictionGraceSec))
		c.EvictionGraceSec = DefaultEvictionGraceSec
	}
	if c.EvictionMaxWaitSec < 0 || c.EvictionMaxWaitSec > MaxEvictionWaitSec {
		repaired = append(repaired, "eviction_max_wait_sec="+strconv.Itoa(c.EvictionMaxWaitSec))
		c.EvictionMaxWaitSec = DefaultEvictionMaxWaitSec
	}
	if c.EvictionGrace && c.IdleTimeoutSec > 0 && c.GraceSeconds() > c.IdleTimeoutSec {
		repaired = append(repaired, "eviction_grace_sec="+strconv.Itoa(c.GraceSeconds()))
		c.EvictionGraceSec = c.IdleTimeoutSec
	}
	// Raised rather than refused on this path, and raised after the clamp
	// above so it is measured against the grace that survives it. A file is
	// repaired; a save is told (see validateGrace).
	if c.EvictionGrace && c.MaxWaitSeconds() < c.GraceSeconds() {
		repaired = append(repaired, "eviction_max_wait_sec="+strconv.Itoa(c.MaxWaitSeconds()))
		c.EvictionMaxWaitSec = c.GraceSeconds()
	}
	return repaired
}

// Default returns the shipping defaults: LAN-exposed, unauthenticated.
func Default() Config {
	return Config{
		Host:                     "0.0.0.0",
		Port:                     11535,
		APIKey:                   "",
		UpstreamHeaderTimeoutSec: 0,
		Advertise:                true,
		IdleTimeoutSec:           0,
		DecodeConcurrency:        4,
		StatsMonths:              DefaultStatsMonths,
		StatsMaxBytes:            DefaultStatsMaxBytes,
		// Stored even though the feature is off, so that switching it on in
		// Settings is one tick rather than one tick and two numbers.
		EvictionGraceSec:   DefaultEvictionGraceSec,
		EvictionMaxWaitSec: DefaultEvictionMaxWaitSec,
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
	// A Host that cannot be bound is refused here rather than at the listener.
	// It used to travel two ways: cmd/gropius built "<host>:<port>" and the
	// process exited when that would not listen, and — before it got that far —
	// gateway.Endpoints pasted the same value into the base URL the panel, the
	// menu bar and the clipboard hand out. A value carrying CR or LF in a base
	// URL is a header-injection primitive in whichever client takes it, so the
	// fence belongs where the value is first read rather than at each surface
	// that repeats it. Load turns a refusal here into a lock-down to loopback,
	// which is the same fail-closed path a corrupt file takes.
	if !ValidBindHost(c.Host) {
		return fmt.Errorf("host %q is neither an IP address (bracketed, as \"[::1]\", for IPv6) nor a host name", c.Host)
	}
	if c.DecodeConcurrency < 1 {
		return fmt.Errorf("decode_concurrency must be >= 1, got %d", c.DecodeConcurrency)
	}
	// Named fields, both of them: this is the settings path, where a person is
	// waiting to be told which of a form's worth of settings was refused.
	if len(c.APIKey) > MaxAPIKeyBytes {
		return fmt.Errorf("api_key must be at most %d bytes, got %d", MaxAPIKeyBytes, len(c.APIKey))
	}
	if len(c.Preload) > MaxPreload {
		return fmt.Errorf("preload names %d models, more than the %d this holds", len(c.Preload), MaxPreload)
	}
	if err := c.validatePinned(); err != nil {
		return err
	}
	// Eviction grace on an open endpoint is a denial-of-service lever: a parked
	// waiter that cannot progress blocks every cold load needing an eviction
	// for as long as the maximum wait allows, and the queue is shared out per
	// API key — which means it cannot be shared out at all when there is no key
	// to tell callers apart. Loopback-only installs are unaffected: there is no
	// network caller to defend against, and the check turns on exposure rather
	// than on the key alone.
	if c.EvictionGrace && c.ExposedToLAN() && c.APIKey == "" {
		return errors.New("eviction grace needs an API key on a LAN-exposed server: without one the wait queue cannot be shared out between callers, and one client can hold up model loading for everyone")
	}
	if c.StatsMonths < 1 || c.StatsMonths > MaxStatsMonths {
		return fmt.Errorf("keep statistics for between 1 and %d months, got %d", MaxStatsMonths, c.StatsMonths)
	}
	if c.StatsMaxBytes < MinStatsMaxBytes || c.StatsMaxBytes > MaxStatsMaxBytes {
		return fmt.Errorf("the statistics store's limit must be between %d and %d bytes, got %d",
			MinStatsMaxBytes, MaxStatsMaxBytes, c.StatsMaxBytes)
	}
	if c.MaxResidentBytes < 0 {
		return fmt.Errorf("max_resident_bytes must not be negative, got %d", c.MaxResidentBytes)
	}
	if err := c.validateGrace(); err != nil {
		return err
	}
	// A sampling default becomes a launch flag on every model server, and the
	// model server validates the effective value of every request against it:
	// a value it rejects turns one save into a 400 on every request that omits
	// that parameter. Load sanitizes before it validates, so this strictness
	// only ever refuses a save, never a start-up.
	return c.validateSampling()
}

// ValidBindHost reports whether a value is something cmd/gropius can bind.
//
// It is as wide as the listener and no wider, and that is checked rather than
// asserted: every value the table in host_test.go marks bindable was watched
// to produce a listener, and every value it refuses was watched to fail.
//
// The address is built as "<host>:<port>", so an IPv6 literal binds only when
// the configuration carries it bracketed. That is a rule about spelling, not
// about which addresses exist: "[::1]:11535" listens and "::1:11535" is
// refused by net.SplitHostPort as "too many colons in address" — at every
// port, for every IPv6 address, on every machine. An earlier version of this
// comment said both spellings had to stay legal "or a working install stops
// starting", which had it exactly backwards: an unbracketed IPv6 host is one
// no working install can be carrying, because main.go logs "cannot listen"
// and exits 1 before it serves anything. Accepting it turned a hand-edited
// typo into an app that would not start; refusing it sends Load down the
// narrow-to-loopback path, and the app starts and says why. A zone
// ("[fe80::1%en0]") binds and stays legal, even though URLHost refuses to put
// one in a URL.
func ValidBindHost(host string) bool {
	bare, ok := unbracket(host)
	if !ok {
		return false
	}
	// Unbracketed, a colon is the port separator, so no value carrying one is
	// a host cmd/gropius can bind — neither "::1" nor "192.168.1.5:8080".
	if !strings.HasPrefix(host, "[") && strings.Contains(bare, ":") {
		return false
	}
	if addr, zone, hasZone := strings.Cut(bare, "%"); hasZone {
		return net.ParseIP(addr) != nil && validHostLabel(zone)
	}
	return net.ParseIP(bare) != nil || validHostName(bare)
}

// URLHost returns the host as a URL must spell it, and reports whether it can
// appear in one at all.
//
// The brackets a bind needs come off exactly once here: net.JoinHostPort adds
// its own, and passing it a host that is already bracketed produced
// "http://[[::1]]:11535/v1" — the address of nothing, handed out as the base
// URL of everything. A zone is refused rather than carried: the "%" that
// separates it is an escape introducer in a URL and not a literal, so there is
// no spelling of "fe80::1%en0" that both means what it says and parses.
// Refusing leaves that address off the list, which is the same answer the list
// gives for every other address it cannot describe truthfully.
func URLHost(host string) (string, bool) {
	bare, ok := unbracket(host)
	if !ok || strings.Contains(bare, "%") {
		return "", false
	}
	if net.ParseIP(bare) == nil && !validHostName(bare) {
		return "", false
	}
	return bare, true
}

// unbracket removes the brackets an IPv6 bind is written with, and refuses a
// value that is bracketed on one side only or bracketed around nothing.
func unbracket(host string) (string, bool) {
	if host == "" {
		return "", false
	}
	opened, closed := strings.HasPrefix(host, "["), strings.HasSuffix(host, "]")
	switch {
	case opened && closed:
		inner := host[1 : len(host)-1]
		if inner == "" || strings.ContainsAny(inner, "[]") {
			return "", false
		}
		return inner, true
	case opened || closed:
		return "", false
	}
	return host, !strings.ContainsAny(host, "[]")
}

// validHostName reports whether a value is a host name: dot-separated labels,
// optionally fully qualified with a trailing dot. Underscores are allowed
// inside a label — they are not RFC 1123, and they are handed out by real
// networks, and refusing one here would stop a server that binds today.
//
// A name is refused when it is really an address, and that takes two rules
// rather than one, because RFC 1123 §2.1's "the top label is alphabetic" is
// not the same test as "contains a letter" — which is what this used to
// apply, and hex spells an address with letters in it. Measured, every one of
// "0x0", "0X0", "0x00000000", "0x0.0x0.0x0.0x0" and "0.0.0.0x0" was accepted
// and bound "[::]", every interface on this Mac, while "0x7f000001",
// "0x7f.0x0.0x0.0x1" and "127.0.0.0x1" were accepted and bound 127.0.0.1.
//
// So: the top label must carry a letter, AND the labels must not all be
// numeric in one of the forms inet_aton reads. Measured on this platform,
// net.Listen takes "0:0" and returns a listener on "[::]" — the unspecified
// address, every interface on the machine — while "127.1", "2130706433",
// "0x7f.1" and "0177.0.0.1" all come back 127.0.0.1.
//
// Both directions are wrong, and in opposite ways. `{"host":"0"}` bound
// everything while everything downstream read it as a specific bind by name,
// so the control panel offered "http://0:11535/v1" and listed nothing else: a
// wildcard under-reported, which is the dead-address fault seen from the other
// side. `{"host":"127.1"}` bound loopback while ExposedToLAN read a name and
// told the operator they bind a LAN address.
//
// Refusing is the closed direction: Validate refuses the file, and cmd/gropius
// locks the bind down to loopback rather than binding wider than the panel
// says. Nobody writes "0" meaning the wildcard; they write "0.0.0.0" or leave
// it empty, and both still work.
func validHostName(s string) bool {
	if len(s) > 253 {
		return false
	}
	s = strings.TrimSuffix(s, ".")
	if s == "" {
		return false
	}
	labels := strings.Split(s, ".")
	numeric := true
	for _, label := range labels {
		if !validHostLabel(label) {
			return false
		}
		if !numericLabel(label) {
			numeric = false
		}
	}
	if numeric {
		return false
	}
	return hasLetter(labels[len(labels)-1])
}

// numericLabel reports whether a label is one of the numeric forms inet_aton
// reads a part of an address as: decimal, octal written with a leading zero,
// or hex written with a leading "0x". A value whose every label is one of
// these is an address however many parts it has, so it is not a name, whatever
// letters the hex spelling happens to contain.
//
// It is deliberately this grammar rather than strconv.ParseUint(label, 0, 64),
// which also reads Go's own "0b"/"0o" prefixes and digit-separating
// underscores — spellings getaddrinfo does not accept, so a value Go's parser
// calls numeric is not necessarily an address this platform would resolve as
// one. Borrowing it would decide the question by a different language's
// literal syntax than the one the resolver speaks.
//
// The earlier version of this comment justified that with "1_0.2_0", a name it
// claimed resolves perfectly well and Go's parser would refuse. That example
// is wrong about this code: validHostName refuses "1_0.2_0" above, on the rule
// that the top label must carry a letter, and refused it before this grammar
// existed. The reasoning stands; the example never did.
func numericLabel(s string) bool {
	if s == "" {
		return false
	}
	if len(s) > 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		for i := 2; i < len(s); i++ {
			c := s[i]
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
		return true
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// hasLetter reports whether a label contains an ASCII letter. It is what
// separates a host name's top label from a legacy spelling of an IPv4 address.
func hasLetter(label string) bool {
	for i := 0; i < len(label); i++ {
		if c := label[i]; (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return false
}

// validHostLabel reports whether one dot-separated label is well formed. It is
// also what an IPv6 zone is held to: an interface name, which on this platform
// is letters and digits.
func validHostLabel(label string) bool {
	if label == "" || len(label) > 63 {
		return false
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
		case c == '-' && i != 0 && i != len(label)-1:
		default:
			return false
		}
	}
	return true
}

// ExposedToLAN reports whether the bind address accepts non-loopback traffic.
// Anything that is not loopback counts: a specific interface address exposes
// the gateway to the LAN just as the wildcard does, and must trigger the same
// security warnings.
//
// It is read by everything that decides how open this server is — the
// generate-a-key-or-drop-to-loopback branch in cmd/gropius, the eviction-grace
// key requirement, the panel's warning, whether Bonjour advertises at all, and
// whether the endpoint list enumerates this machine's addresses — so it has to
// understand the same spellings of the bind that the listener does. It did
// not: it compared c.Host as a string, and an IPv6 literal binds only when
// config.json carries it bracketed, so a "[::1]" bind was read as LAN-exposed.
// That errs closed — a key was generated for a server nothing off this Mac can
// reach — but it also told the operator "this server binds a LAN address" and
// advertised a Bonjour service no machine on the LAN could connect to, which
// is a false statement about their exposure (adr-2609081118587999 rule 4).
//
// Everything this cannot resolve to a loopback address is exposed. That is the
// direction the errors have to run: a name resolves to whatever the resolver
// says today, and a malformed value binds nothing at all, and neither is a
// reason to stand down.
func (c Config) ExposedToLAN() bool {
	bare, ok := unbracket(c.Host)
	if !ok {
		return true
	}
	// A zone belongs to the interface, not to the address: "[::1%lo0]" is the
	// same loopback bind as "[::1]".
	if addr, _, hasZone := strings.Cut(bare, "%"); hasZone {
		bare = addr
	}
	// The resolver folds case and ignores a trailing dot, and this compared
	// bytes: "LOCALHOST", "LocalHost" and "localhost." each bind 127.0.0.1 and
	// nothing else, and each was read here as a LAN bind. Same class as the
	// "[::1]" fault above, and the same four false statements follow from it.
	if strings.EqualFold(strings.TrimSuffix(bare, "."), "localhost") {
		return false
	}
	if ip := net.ParseIP(bare); ip != nil {
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
func Load(path string) (Config, Notices, error) {
	cfg := Default()
	b, err := ReadRegular(path, MaxConfigBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, Notices{}, nil
	}
	if err != nil {
		return cfg, Notices{}, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Default(), Notices{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	var n Notices
	// Ignored: the setting is not in force at all, and setting it again is the
	// only way to get it.
	n.Ignored = append(n.Ignored, cfg.sanitizeSampling()...)
	n.Ignored = append(n.Ignored, cfg.sanitizePerModel()...)
	n.Ignored = append(n.Ignored, cfg.sanitizePinned()...)
	n.Ignored = append(n.Ignored, cfg.sanitizePreload()...)
	// Repaired: the setting IS in force, in a changed form. Telling an
	// operator to set it again would send them looking for a value that is
	// working — and for the API key it would be worse than that, because the
	// trimmed key is the one their clients must now send.
	n.Repaired = append(n.Repaired, cfg.sanitizeStats()...)
	n.Repaired = append(n.Repaired, cfg.sanitizeBudget()...)
	n.Repaired = append(n.Repaired, cfg.sanitizeGrace()...)
	n.Repaired = append(n.Repaired, cfg.sanitizeAPIKey()...)
	if err := cfg.Validate(); err != nil {
		return Default(), Notices{}, &InvalidError{Path: path, Err: err, Parsed: cfg, Notices: n}
	}
	return cfg, n, nil
}

// Notices is what Load had to change about a settings file to make it usable,
// kept in two lists because the difference is the whole of what an operator
// needs to hear.
//
// A setting in Ignored is not in force at all: it named nothing this build can
// use, and setting it again is the only way to get it. A setting in Repaired
// IS in force, in a changed form — trimmed, clamped, or replaced by the
// default that stands behind it. One message for both said "ignoring settings
// the model server would not accept — set them again", which is untrue of
// every repair and dangerous for exactly one of them: a trimmed API key is the
// key clients must send from that moment on, and an operator told it was
// ignored has been told the opposite of what happened.
type Notices struct {
	Ignored  []string
	Repaired []string
}

// Empty reports whether the file needed no changing at all.
func (n Notices) Empty() bool { return len(n.Ignored) == 0 && len(n.Repaired) == 0 }

// All names everything Load changed, ignored and repaired together, for a
// caller that wants the fields and not the distinction.
func (n Notices) All() []string {
	out := make([]string, 0, len(n.Ignored)+len(n.Repaired))
	out = append(out, n.Ignored...)
	return append(out, n.Repaired...)
}

// InvalidError reports a config.json that read and parsed cleanly and then
// failed Validate, and carries the configuration it parsed.
//
// The distinction is the whole point of the type. A file that cannot be read
// or cannot be parsed tells us nothing about what the operator wanted, and the
// only safe answer is the shipping defaults with the bind locked down. A file
// that parsed tells us everything except the one field Validate objected to —
// and the caller was throwing all of it away: an API key, a port, pinned
// models, a memory budget and a statistics retention period were silently
// replaced by defaults because a hand-edited Host would not bind, under a log
// line reading "config.json could not be read" about a file that read fine.
//
// The first return value of Load stays Default() so a caller that ignores the
// error is unchanged. A caller that handles it can narrow the lockdown to the
// bind.
type InvalidError struct {
	Path string
	Err  error
	// Parsed is the configuration as it was read: sanitized, and invalid in
	// whatever way Err names. It is not safe to run as it stands.
	Parsed Config
	// Notices names what sanitizing changed on the way here, split the way
	// Load's second return value would have carried it: settings that are not
	// in force at all, and settings that are in force in a changed form.
	Notices Notices
}

func (e *InvalidError) Error() string { return "invalid config " + e.Path + ": " + e.Err.Error() }

func (e *InvalidError) Unwrap() error { return e.Err }

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
	return writeSettingsFile(path, append(b, '\n'))
}

// writeSettingsFile writes a settings file atomically and closed (0600). It is
// the one writer of config.json — Save and the shared-root adoption below both
// go through it — so the hardening below is stated once and cannot drift.
func writeSettingsFile(path string, b []byte) error {
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
	if _, err := tmp.Write(b); err != nil {
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

// accountStateDir is Account, cleaned so it can be compared with the root. It
// falls back to the directory holding config.json for a Paths built by hand
// without the field.
func (p Paths) accountStateDir() string {
	if p.Account != "" {
		return filepath.Clean(p.Account)
	}
	return filepath.Dir(p.Config)
}

// AdoptSharedConfig copies the settings this account left in the shared root
// into its own state directory, once, and reports whether it did.
//
// Before per-account state, every account under a shared cache wrote its
// settings to one config.json beside the models — which worked for exactly one
// account and left every later one unable to read or write anything. Those
// settings are not derivable from anything else (an API key, a HuggingFace
// token, a port, a memory budget), so the first start after the change carries
// them over rather than silently resetting the account to the shipping
// defaults. A registry needs no such rescue: it is derived from the model
// directories, and the startup rescan rebuilds it.
//
// Three rules make this safe to run against a directory every account on the
// Mac can write to:
//
//   - Only the account that OWNS the file adopts it. The shared config.json is
//     0600 and belongs to whichever account wrote it; a file this account does
//     not own is either another account's settings — whose API key and token
//     must never cross the boundary, however readable the mode has been made —
//     or something planted under that name. Neither is adopted.
//   - The read is the hardened one (ReadRegular): a symlink, a FIFO, or an
//     oversized file is refused rather than followed or blocked on.
//   - The original is REMOVED once the copy is in place. The sticky bit stops
//     other accounts unlinking it, not its owner, and its owner is the only
//     account that ever gets here — so leaving it would leave a copy of an API
//     key and a HuggingFace token in a group-writable directory for as long as
//     the install lasts, still live for anything that reads that path. An
//     operator who rotates the key and later runs an older build would put the
//     superseded key back into service; starting that build from the shipping
//     defaults, which generates a fresh key for an exposed bind, is the safer
//     of the two failures.
//
// Adoption happens only when this account has no settings of its own yet, so it
// can never overwrite what the operator has saved since.
func (p Paths) AdoptSharedConfig() (bool, error) {
	acct := p.accountStateDir()
	if acct == filepath.Clean(p.Root) {
		return false, nil // per-user layout: the settings are already here
	}
	if _, err := os.Lstat(p.Config); err == nil {
		return false, nil // this account has its own settings
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	legacy := filepath.Join(p.Root, "config.json")
	b, info, err := ReadRegularInfo(legacy, MaxConfigBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		// A file another account owns is unreadable to this one, which is the
		// expected case and not a fault: this account simply has no settings to
		// carry over. Anything else is worth saying out loud.
		if errors.Is(err, fs.ErrPermission) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", legacy, err)
	}
	if err := privateToThisAccount(info); err != nil {
		// Not "no settings to carry over" but "settings that are not this
		// account's alone", which is worth naming: the operator is left on the
		// shipping defaults and the file is still sitting there.
		return false, fmt.Errorf("refusing to adopt %s: %w", legacy, err)
	}
	if err := os.MkdirAll(acct, 0o700); err != nil {
		return false, err
	}
	if err := writeSettingsFile(p.Config, b); err != nil {
		return false, err
	}
	// Make the copy durable before unlinking the original. writeSettingsFile
	// fsyncs the file's contents, but the rename that gives it its name lives in
	// the directory, and a power loss can make the unlink durable while that
	// rename is still only in the page cache — leaving neither copy, and with it
	// the API key and the HuggingFace token gone for good. Fsyncing the
	// directory orders the two.
	if err := syncDir(acct); err != nil {
		return false, fmt.Errorf("flush %s: %w", acct, err)
	}
	if err := os.Remove(legacy); err != nil {
		// The copy is in place, so the settings are not lost; what is left is a
		// stale secret in a directory shared with every account, which the
		// operator should hear about.
		return true, fmt.Errorf("remove %s once copied: %w", legacy, err)
	}
	return true, nil
}

// syncDir flushes a directory's own entries to disk, which is what makes a
// rename or an unlink inside it durable. Opening a directory read-only and
// calling Sync is the portable spelling on this platform.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}

// privateToThisAccount reports whether a file read out of a group-writable
// directory is one only this account could have written, and says why not when
// it is not. It is the account boundary for the settings adoption, and takes
// three facts from the fstat of the handle the bytes came from — never a second
// stat of the path, which could be raced.
//
// Ownership alone is not the boundary, which an earlier version of this comment
// claimed. Two things a co-tenant can do defeat it:
//
//   - A hard link. Any account that can write the shared root can link a file
//     THIS account owns — a log, a model's config.json — under the name being
//     adopted. The uid then reads as ours while the content is whatever the
//     linked file holds. A file this account wrote through writeSettingsFile
//     has exactly one link, so more than one means someone else made it.
//   - A loose mode. A settings file left group- or world-writable (what a
//     recursive chmod of the shared root produces, which the installer's
//     comment warns against) is one another account could have written an
//     api_key or a host into before this start read it. Anything outside 0600
//     is refused rather than adopted.
func privateToThisAccount(info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot read the file's ownership on this platform")
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("it belongs to another account (uid %d)", st.Uid)
	}
	if st.Nlink != 1 {
		return fmt.Errorf("it has %d hard links, so another account may have linked it here", st.Nlink)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("its mode is %#o, so another account could have read or written it", perm)
	}
	return nil
}

// GenerateAPIKey returns a fresh random API key, 32 bytes of crypto/rand
// rendered as URL-safe base64 without padding.
//
// Used to fail closed rather than open: a server that binds a LAN address with
// no key configured is reachable, unauthenticated, by everyone on the network,
// and the warning that said so was the only thing standing between a fresh
// install and an open endpoint. A generated key is announced loudly, persisted,
// and shown in the control panel, so the operator can use it or replace it —
// but there is no window in which the endpoint is open by default.
func GenerateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Challenge coordination: how a starting Gropius proves the process already on
// its port shares its data root, without either side keeping a secret.
//
// The prober writes a random answer into a random-named file in the data root
// and asks the holder, over loopback, to read that file back. Only a process
// that can read this root can answer, which is exactly the claim being tested.
// Nothing is stored between probes and nothing replayable crosses the wire: the
// file is single-use and deleted, so a peer who observes one answer learns
// nothing about the next.
//
// This replaces a durable per-run token, which failed in both directions. It
// could not authenticate a peer account (each root holds a different token), and
// it was replayable: the token was published to every loopback caller, survived
// shutdown on disk, and was read by the next start before a fresh one was
// written — so a local account could harvest it, wait, squat the port, and have
// the real server adopt it as its own.
const (
	// ChallengeFilePrefix names a challenge file. The leading dot keeps it out
	// of ordinary listings; the name after it is the caller's nonce.
	ChallengeFilePrefix = ".gropius-challenge-"
	// MaxChallengeBytes caps the answer read. A real answer is 64 hex chars.
	MaxChallengeBytes = 4096
	// challengeNameLen is the nonce length in hex characters (16 random bytes).
	challengeNameLen = 32
)

// ValidChallengeName reports whether name is a well-formed nonce.
//
// This is a path-traversal guard, not a formatting nicety: the name is supplied
// by the caller and used to build a path the server then READS. Without it,
// "../../../etc/passwd" would turn the control plane into an arbitrary-file-read
// oracle for anything the server's uid can open. Exactly 32 lowercase hex
// characters admits no separator, no dot, and no escape.
func ValidChallengeName(name string) bool {
	if len(name) != challengeNameLen {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ChallengePath returns the file a challenge name refers to under root, or ""
// when the name is not well-formed. Callers must treat "" as a refusal: it is
// the single point where an untrusted name is turned into a path.
func ChallengePath(root, name string) string {
	if !ValidChallengeName(name) {
		return ""
	}
	return filepath.Join(root, ChallengeFilePrefix+name)
}
