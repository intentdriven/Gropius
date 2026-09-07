package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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
	cfg, _, err := Load(filepath.Join(t.TempDir(), "nope.json"))
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
	got, _, err := Load(path)
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
	cfg, _, err := Load(path)
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
	if _, _, err := Load(path); err == nil {
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

// Under a setgid shared root the layout directories are widened to 3775 at
// startup. A symlink planted under one of their names (any local account can
// create an absent name there, and the first launcher owns the real ones and
// can swap them later) would make that chmod land on an arbitrary directory
// the victim owns — group-writable by every account. EnsureDirs must refuse
// anything that is not a real directory, and must not have touched the target.
func TestEnsureDirsRefusesSymlinkedLayoutDirUnderSetgidRoot(t *testing.T) {
	for _, name := range []string{"models", "hf", "logs", "bin"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0o775|os.ModeSetgid|os.ModeSticky); err != nil {
				t.Fatal(err)
			}
			victim := filepath.Join(t.TempDir(), "victim")
			if err := os.Mkdir(victim, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(victim, filepath.Join(root, name)); err != nil {
				t.Fatal(err)
			}
			if err := NewPaths(root).EnsureDirs(); err == nil {
				t.Fatal("EnsureDirs accepted a symlinked layout directory")
			}
			fi, err := os.Stat(victim)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm() != 0o700 || fi.Mode()&os.ModeSetgid != 0 {
				t.Errorf("victim mode = %v, want 0700 untouched", fi.Mode())
			}
			if _, err := os.Stat(filepath.Join(victim, "hub")); !os.IsNotExist(err) {
				t.Error("EnsureDirs created hf/hub inside the victim directory")
			}
		})
	}
}

// /Users/Shared is world-writable on stock macOS, so any unprivileged account
// can pre-create the shared root and own every other account's data. Only a
// directory the installer's `sudo mkdir` produced — root-owned and not
// other-writable — may be adopted; anything else falls back to the per-user
// root. A self-owned directory (what an attacker, or t.TempDir, produces) must
// fail the shape check; the root filesystem is a handy root-owned directory
// that passes it.
func TestSharedRootShapeRequiresRootOwnershipAndNoOtherWrite(t *testing.T) {
	mine := t.TempDir()
	if err := os.Chmod(mine, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := sharedRootShape(mine); err == nil {
		t.Error("a self-owned, other-writable directory passed the shared-root shape check")
	}
	if err := sharedRootShape("/"); err != nil {
		t.Errorf("a root-owned, non-other-writable directory failed the shape check: %v", err)
	}
	if err := sharedRootShape(filepath.Join(mine, "missing")); err == nil {
		t.Error("a missing directory passed the shape check")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink("/", link); err != nil {
		t.Fatal(err)
	}
	if err := sharedRootShape(link); err == nil {
		t.Error("a symlink to a root-owned directory passed the shape check")
	}
}

// A per-user root has no hostile co-tenant, so a layout directory the user
// pointed elsewhere (models on an external disk) keeps working.
func TestEnsureDirsFollowsSymlinkedLayoutDirOnPerUserRoot(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(root, "models")); err != nil {
		t.Fatal(err)
	}
	if err := NewPaths(root).EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs refused a symlinked models directory on a per-user root: %v", err)
	}
}

// Per-model settings survive a save and a load, so a switch the operator set in
// Settings still applies after a restart.
func TestSaveLoadRoundTripKeepsPerModelSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	want := Default()
	want.PerModel = map[string]ModelSettings{
		"mlx-community/Qwen3-8B-4bit": {MergeSystemMessages: true},
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// A per-model entry is settings for one model, so a file written by a newer
// build — one carrying a setting this build does not know — still loads, with
// the settings this build does know intact.
func TestLoadPerModelIgnoresUnknownSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"port":11535,"host":"0.0.0.0","decode_concurrency":4,` +
		`"per_model":{"mlx-community/Qwen3-8B-4bit":{"merge_system_messages":true,"temperature":0.7}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.PerModel["mlx-community/Qwen3-8B-4bit"].MergeSystemMessages {
		t.Error("merge_system_messages was lost next to a setting this build does not know")
	}
}

// The keys of the per-model map name models. A key that is not a well-formed
// repo id names nothing and is refused, so the map cannot fill up with
// entries no request can ever match.
func TestValidatePerModelKeys(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"a repo id", "mlx-community/Qwen3-8B-4bit", false},
		{"no organization", "Qwen3-8B-4bit", true},
		{"a path traversal", "../../etc", true},
		{"an empty key", "", true},
		{"a trailing segment", "mlx-community/Qwen3/extra", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePerModelKeys(map[string]ModelSettings{c.key: {MergeSystemMessages: true}})
			if (err != nil) != c.wantErr {
				t.Errorf("ValidatePerModelKeys(%q) error = %v, want error: %v", c.key, err, c.wantErr)
			}
		})
	}
}

// A settings file carrying a per-model key that names no model must still
// load: the panel serves the stored settings into its form and the form posts
// them back, so a key that is refused rather than dropped would come back on
// the next save and refuse it — wedging every settings change there is. It is
// dropped and reported, the way an unusable sampling override beside it is.
func TestLoadDropsAPerModelKeyThatNamesNoModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"port":11535,"host":"0.0.0.0","decode_concurrency":4,"per_model":{` +
		`"../../etc":{"merge_system_messages":true},` +
		`"mlx-community/Qwen3-8B-4bit":{"merge_system_messages":true}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, dropped, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, bad := cfg.PerModel["../../etc"]; bad {
		t.Errorf("a key that names no model survived the load: %+v", cfg.PerModel)
	}
	if !cfg.PerModel["mlx-community/Qwen3-8B-4bit"].MergeSystemMessages {
		t.Errorf("the usable setting beside it was dropped too: %+v", cfg.PerModel)
	}
	if len(dropped) != 1 || !strings.Contains(dropped[0], "../../etc") {
		t.Errorf("dropped = %v, want the one unusable key named", dropped)
	}
	// The whole point: what loaded is a config that can be saved again.
	if err := cfg.Validate(); err != nil {
		t.Errorf("the loaded config does not validate: %v", err)
	}
	if err := ValidatePerModelKeys(cfg.PerModel); err != nil {
		t.Errorf("the loaded config would be refused by the next settings save: %v", err)
	}
}

// Two spellings of one model id would make the effective settings depend on
// map iteration order, so the later one in sorted order is dropped.
func TestLoadDropsADuplicatePerModelSpelling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"port":11535,"host":"0.0.0.0","decode_concurrency":4,"per_model":{` +
		`"MLX-Community/Qwen3-8B-4bit":{"merge_system_messages":true},` +
		`"mlx-community/Qwen3-8B-4bit":{}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, dropped, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.PerModel) != 1 {
		t.Errorf("per-model settings = %+v, want one of the two spellings", cfg.PerModel)
	}
	if len(dropped) != 1 || !strings.Contains(dropped[0], "duplicate") {
		t.Errorf("dropped = %v, want the duplicate spelling named", dropped)
	}
}

// Statistics is off in a fresh install and stays off through a save-and-load
// round trip that never names it: adr-2609061503319212 makes local recording a
// strict opt-in, so a configuration file written by a build that predates the
// switch must come back with it off rather than absent-meaning-on.
func TestStatisticsIsOffByDefaultAndSurvivesARoundTrip(t *testing.T) {
	if Default().Statistics {
		t.Error("a fresh install records request statistics; it must be off until the operator turns it on")
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"host":"127.0.0.1","port":11535,"decode_concurrency":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Statistics {
		t.Error("a configuration file that does not name the switch loaded with statistics on")
	}

	loaded.Statistics = true
	if err := Save(path, loaded); err != nil {
		t.Fatal(err)
	}
	again, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Statistics {
		t.Error("the switch did not survive being saved and read back")
	}
}

// The pinned list is what protects a model from eviction, so it has to survive
// a round trip through the settings file exactly as it was saved.
func TestSaveLoadRoundTripKeepsPinnedModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := Default()
	c.Pinned = []string{"mlx-community/Qwen3-8B-4bit", "org/reviewer"}

	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, dropped, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want nothing dropped", dropped)
	}
	if !reflect.DeepEqual(got.Pinned, c.Pinned) {
		t.Errorf("Pinned = %v, want %v", got.Pinned, c.Pinned)
	}
}

// A pin is matched against the id a request resolves to, so an entry of any
// other shape names nothing and would sit in the settings file looking
// effective while protecting no model. Refusing it at the point of saving is
// the only moment the operator is there to see it.
func TestValidateRefusesPinsThatNameNoModel(t *testing.T) {
	cases := []struct {
		name   string
		pinned []string
		want   string
	}{
		{"not a repo id", []string{"../../etc"}, "not a model id"},
		{"two spellings of one model", []string{"Org/M", "org/m"}, "name the same model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.Pinned = tc.pinned
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate accepted pinned %v", tc.pinned)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// Everything saved lands in config.json, which Load refuses above
// MaxConfigBytes — and a config.json that cannot be read sends the next start
// into its fail-closed loopback-only branch. A bounded list keeps this field
// from being the lever for that.
func TestValidateRefusesMorePinsThanTheCeiling(t *testing.T) {
	c := Default()
	for i := 0; i <= MaxPinned; i++ {
		c.Pinned = append(c.Pinned, fmt.Sprintf("org/m%d", i))
	}
	err := c.Validate()
	if err == nil {
		t.Fatalf("Validate accepted %d pins, over the %d ceiling", len(c.Pinned), MaxPinned)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(MaxPinned)) {
		t.Errorf("Validate error = %q, want it to give the ceiling", err)
	}
}

// The file path, not the settings path: a hand-edited pin that names nothing
// is dropped and reported rather than refusing the whole file, which would
// send the next start into its fail-closed loopback-only mode over one entry.
func TestLoadDropsAPinThatNamesNoModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"port":11535,"host":"0.0.0.0","decode_concurrency":4,` +
		`"pinned":["../../etc","org/keeper","ORG/keeper"]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, dropped, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(cfg.Pinned, []string{"org/keeper"}) {
		t.Errorf("Pinned = %v, want only the one usable entry", cfg.Pinned)
	}
	if len(dropped) != 2 {
		t.Fatalf("dropped = %v, want the bad id and the duplicate spelling named", dropped)
	}
	if !strings.Contains(dropped[0], "../../etc") {
		t.Errorf("dropped = %v, want the entry that names no model named", dropped)
	}
	if !strings.Contains(dropped[1], "duplicate") {
		t.Errorf("dropped = %v, want the duplicate spelling named", dropped)
	}
	// The whole point: what loaded is a config that can be saved again.
	if err := cfg.Validate(); err != nil {
		t.Errorf("the loaded config does not validate: %v", err)
	}
}

// Clone exists so a posted body cannot reach the live configuration before
// Validate has looked at it. A shared backing array would defeat that for the
// pinned list exactly as it would for the preload list beside it.
func TestClonePinnedSharesNoStorage(t *testing.T) {
	c := Default()
	c.Pinned = []string{"org/a"}
	clone := c.Clone()
	clone.Pinned[0] = "org/b"
	if c.Pinned[0] != "org/a" {
		t.Errorf("the clone wrote through to the original: %v", c.Pinned)
	}
}

// The two bounds together, for the pinned list as for the override map beside
// it: a full list, at the longest ids allowed, must still round-trip through
// the file rather than write one the next start cannot read.
func TestAFullPinnedListStillFitsTheConfigFile(t *testing.T) {
	c := Default()
	for i := range MaxPinned {
		c.Pinned = append(c.Pinned, fmt.Sprintf("%s%03d/%s",
			strings.Repeat("o", MaxRepoComponent-3), i, strings.Repeat("n", MaxRepoComponent)))
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v — a legal pinned list does not fit the file", err)
	}
	loaded, dropped, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped %v from a config within every limit", dropped)
	}
	if len(loaded.Pinned) != MaxPinned {
		t.Errorf("loaded %d pins, want %d", len(loaded.Pinned), MaxPinned)
	}
}

// The budget is typed in gigabytes and stored in bytes: one unit in the file,
// whatever the panel puts in front of the operator.
func TestSaveLoadRoundTripKeepsTheMemoryBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := Default()
	c.MaxResidentBytes = 96 << 30
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.MaxResidentBytes != c.MaxResidentBytes {
		t.Errorf("MaxResidentBytes = %d, want %d", got.MaxResidentBytes, c.MaxResidentBytes)
	}
}

// A fresh install stores no explicit budget: zero means "the default share of
// this Mac's memory", and omitempty keeps the key out of the file, so a config
// written on one Mac does not carry another one's figure.
func TestADefaultConfigStoresNoMemoryBudget(t *testing.T) {
	if got := Default().MaxResidentBytes; got != 0 {
		t.Errorf("Default().MaxResidentBytes = %d, want 0 — the default is resolved, not stored", got)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Save(path, Default()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "max_resident_bytes") {
		t.Errorf("a default config wrote a memory budget:\n%s", b)
	}
}

// Validate is the settings path, where a human is waiting for an answer. It
// stays machine-independent — the ceiling is checked where the machine's size
// is known — so the only rule here is the one that holds on every Mac.
func TestValidateRefusesANegativeMemoryBudget(t *testing.T) {
	c := Default()
	c.MaxResidentBytes = -1
	if err := c.Validate(); err == nil {
		t.Fatal("Validate accepted a negative memory budget")
	}
	c.MaxResidentBytes = 64 << 30
	if err := c.Validate(); err != nil {
		t.Errorf("Validate refused a budget larger than this Mac's memory: %v", err)
	}
}

// A hand-edited file with an unusable budget must still load. Refusing it
// sends main into its fail-closed loopback-only branch, so the API key and the
// bind address a user set go unused over one number that can simply fall back
// to the default.
func TestLoadDropsAnUnusableMemoryBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"port":11535,"host":"0.0.0.0","decode_concurrency":4,` +
		`"api_key":"bh_keep","max_resident_bytes":-5}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, dropped, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MaxResidentBytes != 0 {
		t.Errorf("MaxResidentBytes = %d, want 0 — an unusable figure falls back to the default",
			cfg.MaxResidentBytes)
	}
	if cfg.APIKey != "bh_keep" {
		t.Errorf("APIKey = %q, want the file's key — one bad figure must not reset the install", cfg.APIKey)
	}
	if len(dropped) != 1 || !strings.Contains(dropped[0], "max_resident_bytes") {
		t.Errorf("dropped = %v, want the memory budget named", dropped)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("the loaded config does not validate: %v", err)
	}
}
