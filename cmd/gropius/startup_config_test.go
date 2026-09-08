package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// A config.json that reads and parses and then fails Validate is not the same
// thing as one that cannot be read, and treating it as one threw away
// everything the operator had set. A hand-edited Host that will not bind —
// which is the only way Host can be edited at all, since the panel offers the
// wildcard and loopback and nothing else (iss-7) — replaced the API key, the
// port, the pinned models, the memory budget and the statistics retention with
// the shipping defaults, and said so in a log line reading "config.json could
// not be read" about a file that read perfectly.
//
// The lockdown itself is right and stays: an invalid configuration binds
// loopback and does not advertise. What changes is that everything Validate
// did not object to survives it.
func TestAnInvalidConfigKeepsEverythingButTheBind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	write(t, path, `{
	  "host": "not an ip",
	  "port": 12345,
	  "api_key": "a-key-the-operator-chose",
	  "advertise": true,
	  "pinned": ["mlx-community/Qwen3-8B-4bit"],
	  "max_resident_bytes": 8589934592,
	  "stats_months": 7
	}`)

	got := loadStartupConfig(path)

	if got.Config.Host != loopbackBind {
		t.Errorf("Host = %q, want %q — an invalid configuration must not bind wider than loopback", got.Config.Host, loopbackBind)
	}
	if got.Config.Advertise {
		t.Error("Advertise stayed on — a locked-down server must not announce itself over Bonjour")
	}
	if got.Config.APIKey != "a-key-the-operator-chose" {
		t.Errorf("APIKey = %q — the operator's key was discarded because an unrelated field would not validate", got.Config.APIKey)
	}
	if got.Config.Port != 12345 {
		t.Errorf("Port = %d, want 12345 — the port was discarded too", got.Config.Port)
	}
	if len(got.Config.Pinned) != 1 || got.Config.Pinned[0] != "mlx-community/Qwen3-8B-4bit" {
		t.Errorf("Pinned = %#v — the pins were discarded", got.Config.Pinned)
	}
	if got.Config.MaxResidentBytes != 8589934592 {
		t.Errorf("MaxResidentBytes = %d — the memory budget was discarded", got.Config.MaxResidentBytes)
	}
	if got.Config.StatsMonths != 7 {
		t.Errorf("StatsMonths = %d, want 7 — the retention period was discarded", got.Config.StatsMonths)
	}
	if err := got.Config.Validate(); err != nil {
		t.Errorf("the configuration handed to the process does not validate: %v", err)
	}
	if strings.Contains(got.Problem, "could not be read") {
		t.Errorf("the log says %q about a file that read and parsed — the operator goes looking for a disk fault instead of the setting they mistyped", got.Problem)
	}
	if !strings.Contains(got.Problem, "loopback") {
		t.Errorf("the log says %q, which does not tell the operator the bind was narrowed", got.Problem)
	}
}

// The same lockdown when the bind is a name that is secretly an address:
// `{"host":"0"}` resolved to the unspecified address and bound every
// interface. Validate refuses it now, and this is what the process does with
// that refusal.
func TestAHostThatBindsWiderThanItNamesLocksDown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	write(t, path, `{"host": "0", "api_key": "kept"}`)

	got := loadStartupConfig(path)
	if got.Config.Host != loopbackBind {
		t.Errorf("Host = %q, want %q", got.Config.Host, loopbackBind)
	}
	if got.Config.APIKey != "kept" {
		t.Errorf("APIKey = %q, want %q", got.Config.APIKey, "kept")
	}
	if got.Config.ExposedToLAN() {
		t.Error("the locked-down configuration still reads as LAN-exposed")
	}
}

// A file that cannot be parsed says nothing about what the operator wanted, so
// there is nothing to keep: the shipping defaults, locked down, and a message
// that says the file could not be read — because this time it could not.
func TestAnUnparseableConfigFallsBackToLockedDownDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	write(t, path, `{"host": "127.0.0.1", "api_key": "trunc`)

	got := loadStartupConfig(path)
	if got.Config.Host != loopbackBind {
		t.Errorf("Host = %q, want %q", got.Config.Host, loopbackBind)
	}
	if got.Config.Advertise {
		t.Error("Advertise stayed on")
	}
	if got.Config.APIKey != "" {
		t.Errorf("APIKey = %q — nothing in an unparseable file is trusted", got.Config.APIKey)
	}
	if !strings.Contains(got.Problem, "could not be read") {
		t.Errorf("the log says %q, which does not say the file could not be read", got.Problem)
	}
}

// An invalid configuration that is still invalid once the bind is narrowed had
// an objection to something other than the bind. There is then nothing to
// build on and the defaults are what is left — locked down, and said so
// without claiming the file could not be read.
func TestAConfigInvalidForAnotherReasonFallsBackToDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	write(t, path, `{"host": "0.0.0.0", "port": 0, "api_key": "kept"}`)

	got := loadStartupConfig(path)
	if err := got.Config.Validate(); err != nil {
		t.Fatalf("the configuration handed to the process does not validate: %v", err)
	}
	if got.Config.Host != loopbackBind || got.Config.Advertise {
		t.Errorf("Host = %q, Advertise = %v — want a locked-down bind", got.Config.Host, got.Config.Advertise)
	}
	if got.Config.APIKey != "" {
		t.Errorf("APIKey = %q — a configuration that cannot be repaired is not partly adopted", got.Config.APIKey)
	}
	if strings.Contains(got.Problem, "could not be read") {
		t.Errorf("the log says %q about a file that read and parsed", got.Problem)
	}
}

// A machine with no config.json is a fresh install, not a fault.
func TestAMissingConfigIsNotAProblem(t *testing.T) {
	got := loadStartupConfig(filepath.Join(t.TempDir(), "config.json"))
	if got.Problem != "" {
		t.Errorf("a missing config.json logged %q", got.Problem)
	}
	if got.Config.Host != config.Default().Host {
		t.Errorf("Host = %q, want the shipping default %q", got.Config.Host, config.Default().Host)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
