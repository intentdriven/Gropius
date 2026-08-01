package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNewPathsDerivesLayoutFromRoot(t *testing.T) {
	p := NewPaths("/root")

	if got, want := p.UV(), "/root/bin/uv"; got != want {
		t.Errorf("UV() = %q, want %q", got, want)
	}
	if got, want := p.VenvPython(), "/root/venv/bin/python"; got != want {
		t.Errorf("VenvPython() = %q, want %q", got, want)
	}
	// HF_HUB_CACHE must be a real directory or mlx_lm.server's request handler
	// raises CacheNotFound and returns an empty 200.
	if got, want := p.HFCache, "/root/hf/hub"; got != want {
		t.Errorf("HFCache = %q, want %q", got, want)
	}
}

func TestModelDirMapsRepoIDToNestedPath(t *testing.T) {
	p := NewPaths("/root")
	got := p.ModelDir("mlx-community/Qwen3-8B-4bit")
	want := "/root/models/mlx-community/Qwen3-8B-4bit"
	if got != want {
		t.Errorf("ModelDir = %q, want %q", got, want)
	}
}

func TestEnsureDirsCreatesLayout(t *testing.T) {
	p := NewPaths(t.TempDir())
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, d := range []string{p.Root, p.Bin, p.Models, p.HFCache, p.Logs} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("expected directory %s to exist (err=%v)", d, err)
		}
	}
}

// The shared cache only works if the layout directories the first account
// creates are writable by the next: the installer marks the shared root setgid
// group-writable (mode 3775), and EnsureDirs must carry that on to the data
// directories it creates, or every later account's downloads fail EACCES.
func TestEnsureDirsWidensDataDirsUnderSetgidSharedRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o775|os.ModeSetgid|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	p := NewPaths(root)
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, d := range []string{p.Models, filepath.Dir(p.HFCache), p.HFCache, p.Logs} {
		fi, err := os.Stat(d)
		if err != nil {
			t.Fatalf("stat %s: %v", d, err)
		}
		if fi.Mode()&0o020 == 0 || fi.Mode()&os.ModeSetgid == 0 {
			t.Errorf("%s mode = %v, want group-writable setgid so a later account can write it", d, fi.Mode())
		}
		// The installer's sticky bit must survive the widening, or any account
		// in the group could delete or replace another account's files here.
		if fi.Mode()&os.ModeSticky == 0 {
			t.Errorf("%s mode = %v, want the sticky bit preserved so only its owner can delete/rename it", d, fi.Mode())
		}
	}
	// bin must NOT be widened: a group-writable bin would let one account
	// replace the uv binary another account executes.
	fi, err := os.Stat(p.Bin)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o020 != 0 {
		t.Errorf("bin mode = %v, must not be group-writable (it holds executables)", fi.Mode())
	}
}

// A per-user root has no setgid bit; its layout stays private to the account.
func TestEnsureDirsKeepsPerUserLayoutPrivate(t *testing.T) {
	p := NewPaths(t.TempDir())
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, d := range []string{p.Bin, p.Models, p.HFCache, p.Logs} {
		fi, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&0o020 != 0 {
			t.Errorf("%s mode = %v, want no group-write in a per-user install", d, fi.Mode())
		}
	}
}

func TestEnsureDirsIsIdempotent(t *testing.T) {
	p := NewPaths(t.TempDir())
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("first EnsureDirs: %v", err)
	}
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("second EnsureDirs should be a no-op, got: %v", err)
	}
}

func TestDefaultIsLANExposedAndUnauthenticated(t *testing.T) {
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatalf("defaults must be valid: %v", err)
	}
	if !c.ExposedToLAN() {
		t.Error("default config should bind the LAN")
	}
	if c.APIKey != "" {
		t.Error("default config should have no API key (auth off)")
	}
}

func TestExposedToLAN(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"0.0.0.0", true},
		{"::", true},
		{"", true},
		{"127.0.0.1", false},
		{"localhost", false},
		{"::1", false},
		// A specific interface address is just as reachable from the LAN as
		// the wildcard; the security warnings must not be suppressed by it.
		{"192.168.1.10", true},
		{"10.0.0.5", true},
		{"fe80::1", true},
		{"mac-studio.local", true},
	}
	for _, tt := range tests {
		c := Default()
		c.Host = tt.host
		if got := c.ExposedToLAN(); got != tt.want {
			t.Errorf("host %q: ExposedToLAN = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"port zero", func(c *Config) { c.Port = 0 }},
		{"port too high", func(c *Config) { c.Port = 70000 }},
		{"empty host", func(c *Config) { c.Host = "" }},
		{"zero concurrency", func(c *Config) { c.DecodeConcurrency = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Default()
			tt.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Error("expected validation error, got nil")
			}
		})
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Load of missing file should succeed: %v", err)
	}
	if cfg.Port != Default().Port {
		t.Errorf("Port = %d, want default %d", cfg.Port, Default().Port)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	want := Default()
	want.Port = 12345
	want.APIKey = "bh_secret"
	want.IdleTimeoutSec = 600

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// A config file written by an older build (missing newer keys) must still load,
// with the absent keys taking their default values.
func TestLoadPartialFileKeepsDefaultsForMissingKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"port": 9999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9999 {
		t.Errorf("Port = %d, want 9999", cfg.Port)
	}
	if cfg.Host != Default().Host {
		t.Errorf("Host = %q, want default %q", cfg.Host, Default().Host)
	}
	if cfg.DecodeConcurrency != Default().DecodeConcurrency {
		t.Errorf("DecodeConcurrency = %d, want default %d",
			cfg.DecodeConcurrency, Default().DecodeConcurrency)
	}
}

func TestSaveRejectsInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := Default()
	c.Port = -1
	if err := Save(path, c); err == nil {
		t.Fatal("expected Save to reject invalid config")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("invalid config must not be written to disk")
	}
}

func TestLoadCorruptFileReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for corrupt config")
	}
}

func TestDefaultRootHonorsEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GROPIUS_ROOT", dir)

	got, err := DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Errorf("DefaultRoot() = %q, want the GROPIUS_ROOT override %q", got, dir)
	}
}

// A shared directory the account cannot write to must be ignored, not used:
// otherwise every download would fail at the moment it tries to write.
func TestWritableDirRejectsUnwritableAndMissingDirs(t *testing.T) {
	if writableDir("/does/not/exist") {
		t.Error("a missing directory is not writable")
	}
	if writableDir("/System") {
		t.Error("a read-only system directory must not be treated as writable")
	}

	dir := t.TempDir()
	if !writableDir(dir) {
		t.Error("a fresh temp dir should be writable")
	}
}

func TestDefaultRootFallsBackToHomeWhenNoSharedDir(t *testing.T) {
	t.Setenv("GROPIUS_ROOT", "")

	got, err := DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	// On a machine without /Users/Shared/Gropius, this must be the per-user path.
	if !writableDir(SharedRoot) && !strings.Contains(got, "Application Support") {
		t.Errorf("DefaultRoot() = %q, want the per-user Application Support path", got)
	}
}
