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
	"net"
	"os"
	"path/filepath"
	"strings"
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
}

// SharedRoot is the machine-wide location, used when it exists.
//
// Models are large. If two macOS accounts each keep their own copy, a 70B model
// costs 40 GB twice. When an administrator has created this directory (see
// `make install-shared`), every account shares one set of models.
const SharedRoot = "/Users/Shared/Gropius"

// DefaultRoot returns where Gropius keeps its data.
//
// Order: $GROPIUS_ROOT, then the shared directory if it exists and this account
// can write to it, then the per-user Application Support directory.
func DefaultRoot() (string, error) {
	if env := os.Getenv("GROPIUS_ROOT"); env != "" {
		return env, nil
	}
	if writableDir(SharedRoot) {
		return SharedRoot, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", "Gropius"), nil
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
	}
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

func validRepoComponent(s string) bool {
	if s == "" || s == "." || s == ".." {
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
// Under a setgid root — the shared cache, which the installer marks setgid
// group-writable (mode 3775) so a model one account downloads is writable by
// the next — the data directories are widened to match: MkdirAll can never
// produce a group-writable directory (0o755 carries no group-write bit), so
// without the chmod the first account to launch would own the layout 0755 and
// every later account's downloads would fail with a permission error. bin is
// deliberately left 0755: it holds executables, and a group-writable bin would
// let one account replace the uv binary another account runs. Chmod errors are
// ignored — a directory created by another account cannot be re-moded by this
// one, and startup must not fail over it.
func (p Paths) EnsureDirs() error {
	for _, d := range []string{p.Root, p.Bin, p.Models, p.HFCache, p.Logs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	if fi, err := os.Stat(p.Root); err == nil && fi.Mode()&os.ModeSetgid != 0 {
		for _, d := range []string{p.Models, filepath.Dir(p.HFCache), p.HFCache, p.Logs} {
			_ = os.Chmod(d, 0o775|os.ModeSetgid)
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
}

// Default returns the shipping defaults: LAN-exposed, unauthenticated.
func Default() Config {
	return Config{
		Host:              "0.0.0.0",
		Port:              11535,
		APIKey:            "",
		Advertise:         true,
		IdleTimeoutSec:    0,
		DecodeConcurrency: 4,
	}
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
	return nil
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
func Load(path string) (Config, error) {
	cfg := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Default(), fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return Default(), fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

// Save atomically writes config to path.
func Save(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
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
