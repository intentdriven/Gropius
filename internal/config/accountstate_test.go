package config

import (
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"testing"
)

// The shared root holds models, which every account may read, and nothing that
// belongs to one account alone. config.json carries the API key and the
// HuggingFace token, and registry.json decides what the gateway serves, so both
// resolve to the account's own directory — the rule ExecRoot and StatsDir
// already follow, for the same reason.
func TestSharedModeStateIsPerAccount(t *testing.T) {
	homeA := t.TempDir()
	t.Setenv("HOME", homeA)
	a := NewPaths(SharedRoot)

	homeB := t.TempDir()
	t.Setenv("HOME", homeB)
	b := NewPaths(SharedRoot)

	for name, got := range map[string]string{"Config": a.Config, "State": a.State} {
		if dir := filepath.Dir(got); dir != filepath.Join(homeA, "Library", "Application Support", "Gropius") {
			t.Errorf("%s = %q, want it in this account's own directory under %q", name, got, homeA)
		}
	}
	if a.Config == b.Config || a.State == b.State {
		t.Errorf("two accounts share one state file: config %q, registry %q — every account must keep its own", a.Config, a.State)
	}
	// The models stay shared: that is what the shared root exists for.
	if b.Models != filepath.Join(SharedRoot, "models") {
		t.Errorf("Models = %q, want them to stay in the shared root", b.Models)
	}
}

// Two accounts, one shared root. Account B must be able to read its own
// settings and write its own after account A has run, and A's secrets must
// never reach B.
//
// The shared root itself is the real one: nothing here writes to it, because
// every state path this test touches resolves into the account's own temporary
// home. The sticky-bit half of the fault — account B's os.Rename over a file
// account A owns failing EPERM — cannot be reproduced in a unit test, which
// runs under a single uid; what is reproduced is the layout rule that keeps the
// two accounts off one file in the first place.
func TestTwoAccountsKeepTheirOwnSharedModeSettings(t *testing.T) {
	homeA, homeB := t.TempDir(), t.TempDir()

	t.Setenv("HOME", homeA)
	a := NewPaths(SharedRoot)
	alice := Default()
	alice.APIKey = "key-belonging-to-the-first-account"
	alice.Port = 12001
	if err := Save(a.Config, alice); err != nil {
		t.Fatalf("the first account could not save its settings: %v", err)
	}

	t.Setenv("HOME", homeB)
	b := NewPaths(SharedRoot)
	got, _, err := Load(b.Config)
	if err != nil {
		t.Fatalf("the second account could not load its settings after the first had run: %v", err)
	}
	if got.APIKey != "" {
		t.Errorf("the second account read %q as its API key — no secret may cross accounts", got.APIKey)
	}
	if got.Port != Default().Port {
		t.Errorf("Port = %d, want the shipping default %d for an account that has never saved settings", got.Port, Default().Port)
	}
	bob := Default()
	bob.APIKey = "key-belonging-to-the-second-account"
	if err := Save(b.Config, bob); err != nil {
		t.Fatalf("the second account could not save its own settings: %v", err)
	}

	t.Setenv("HOME", homeA)
	back, _, err := Load(NewPaths(SharedRoot).Config)
	if err != nil {
		t.Fatalf("reload the first account's settings: %v", err)
	}
	if back.APIKey != alice.APIKey || back.Port != alice.Port {
		t.Errorf("the second account overwrote the first account's settings: %+v", back)
	}
}

// A per-user install is one folder, and this change must not have moved
// anything in it: config.json and registry.json stay in the root, EnsureDirs
// creates the same entries it always did, and the settings file is still 0600.
func TestSingleUserLayoutIsUnchanged(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)
	if p.Config != filepath.Join(root, "config.json") {
		t.Errorf("Config = %q, want %q", p.Config, filepath.Join(root, "config.json"))
	}
	if p.State != filepath.Join(root, "registry.json") {
		t.Errorf("State = %q, want %q", p.State, filepath.Join(root, "registry.json"))
	}
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := Save(p.Config, Default()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	want := []string{"bin", "config.json", "hf", "logs", "models", "python", "venv"}
	if len(names) != len(want) {
		t.Fatalf("the per-user root holds %v, want exactly %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("the per-user root holds %v, want exactly %v", names, want)
		}
	}
	fi, err := os.Stat(p.Config)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("config.json mode = %v, want 0600", fi.Mode().Perm())
	}
	// Nothing is adopted from anywhere in a per-user install: the state files
	// are already in the root.
	if adopted, err := p.AdoptSharedConfig(); adopted || err != nil {
		t.Errorf("AdoptSharedConfig = (%v, %v), want (false, nil) in a per-user install", adopted, err)
	}
}

// EnsureDirs creates this account's own state directory, and closes it: it
// holds the API key and the HuggingFace token, and under the shared root it is
// the one directory that must never be readable by a co-tenant account.
func TestEnsureDirsCreatesThisAccountsStateDirectoryClosed(t *testing.T) {
	root := t.TempDir()
	acct := filepath.Join(t.TempDir(), "Gropius")
	p := NewPaths(root)
	p.Config = filepath.Join(acct, "config.json")
	p.State = filepath.Join(acct, "registry.json")
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	fi, err := os.Stat(acct)
	if err != nil {
		t.Fatalf("EnsureDirs did not create this account's state directory: %v", err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("%s mode = %v, want 0700 — it holds this account's API key and token", acct, fi.Mode().Perm())
	}
}

// An account that ran a shared install before this change kept its settings in
// the shared root. They are its own, and they are not derivable from anything
// else, so the first start after the change copies them into the account's own
// directory rather than starting from the shipping defaults.
func TestSharedRootSettingsAreAdoptedByTheAccountThatOwnsThem(t *testing.T) {
	root := t.TempDir()
	acct := filepath.Join(t.TempDir(), "Gropius")
	p := NewPaths(root)
	p.Config = filepath.Join(acct, "config.json")
	p.State = filepath.Join(acct, "registry.json")

	legacy := Default()
	legacy.APIKey = "the-key-this-account-set-before-the-upgrade"
	legacy.Port = 12002
	if err := Save(filepath.Join(root, "config.json"), legacy); err != nil {
		t.Fatal(err)
	}

	adopted, err := p.AdoptSharedConfig()
	if err != nil {
		t.Fatalf("AdoptSharedConfig: %v", err)
	}
	if !adopted {
		t.Fatal("the settings this account kept in the shared root were not adopted — the upgrade would silently reset them")
	}
	got, _, err := Load(p.Config)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.APIKey != legacy.APIKey || got.Port != legacy.Port {
		t.Errorf("adopted %+v, want the account's own settings back", got)
	}
	if fi, err := os.Stat(p.Config); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the adopted copy is %v (err=%v), want mode 0600", fi.Mode().Perm(), err)
	}
	// The original is left where it was. A downgrade to the previous build must
	// still find these settings, and under a sticky shared root this account
	// could not reliably remove the file anyway.
	if _, err := os.Stat(filepath.Join(root, "config.json")); err != nil {
		t.Errorf("the original settings file was not left in place: %v", err)
	}

	// Adoption happens once. A later start must not overwrite what the account
	// has since saved.
	current := Default()
	current.APIKey = "set-after-the-upgrade"
	if err := Save(p.Config, current); err != nil {
		t.Fatal(err)
	}
	if adopted, err := p.AdoptSharedConfig(); adopted || err != nil {
		t.Errorf("AdoptSharedConfig = (%v, %v) on a second start, want (false, nil)", adopted, err)
	}
	again, _, err := Load(p.Config)
	if err != nil {
		t.Fatal(err)
	}
	if again.APIKey != current.APIKey {
		t.Errorf("APIKey = %q, want the settings saved since the upgrade to survive", again.APIKey)
	}
}

// The shared root is group-writable, so the file adoption reads is a file
// another account can create. It is read through the same hardened open every
// other state file uses: anything that is not a regular file is refused rather
// than followed or blocked on.
func TestSharedRootSettingsAdoptionRefusesAPlantedFile(t *testing.T) {
	root := t.TempDir()
	acct := filepath.Join(t.TempDir(), "Gropius")
	p := NewPaths(root)
	p.Config = filepath.Join(acct, "config.json")
	p.State = filepath.Join(acct, "registry.json")

	elsewhere := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := Save(elsewhere, Default()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}
	adopted, err := p.AdoptSharedConfig()
	if adopted {
		t.Error("adopted a symlink planted under config.json in the shared root")
	}
	if err == nil {
		t.Error("a planted config.json was accepted silently, want the refusal reported")
	}
	if _, statErr := os.Stat(p.Config); statErr == nil {
		t.Error("a file was written into this account's directory from a planted link")
	}
}

// Ownership is what separates "the settings this account left behind" from
// "another account's settings". A file this account does not own is never
// adopted, whatever its mode: adopting one would copy another account's API key
// and HuggingFace token across the account boundary.
//
// A test running under one uid cannot create a file owned by another, so what
// is checked here is the rule the code applies, against the one uid available.
func TestSharedRootSettingsAdoptionRequiresOwnership(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "config.json")
	if err := Save(legacy, Default()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(legacy)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no stat information on this platform")
	}
	if ownedByThisAccount(fi) != (int(st.Uid) == os.Getuid()) {
		t.Errorf("ownedByThisAccount disagrees with the file's uid %d (this account is %d)", st.Uid, os.Getuid())
	}
	if !ownedByThisAccount(fi) {
		t.Error("a file this account just wrote is not recognized as its own")
	}
}
