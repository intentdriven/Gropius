package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The store holds one account's own records at 0600, so it can never live in
// the shared root: that directory is group-writable by design (see the
// Makefile's install-shared), and a per-account record file has no business
// in a directory every other account can write.
func TestTheStoreLivesUnderTheAccountsOwnRootNotTheSharedOne(t *testing.T) {
	if got, want := NewPaths("/root").Stats, "/root/stats"; got != want {
		t.Errorf("Stats = %q, want %q", got, want)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	got := NewPaths(SharedRoot).Stats
	if strings.HasPrefix(got, SharedRoot) {
		t.Errorf("the store is at %q, inside the group-writable shared root", got)
	}
	if want := filepath.Join(home, "Library", "Application Support", "Gropius", "stats"); got != want {
		t.Errorf("Stats = %q, want the account's own %q", got, want)
	}
}

// Retention is two figures the operator sets, and both are usable the moment
// the switch goes on rather than waiting for anyone to choose a number.
func TestRetentionHasDefaultsAndSurvivesARoundTrip(t *testing.T) {
	d := Default()
	if d.StatsMonths != DefaultStatsMonths || d.StatsMaxBytes != DefaultStatsMaxBytes {
		t.Errorf("a fresh install keeps %d months / %d bytes, want %d / %d",
			d.StatsMonths, d.StatsMaxBytes, DefaultStatsMonths, DefaultStatsMaxBytes)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	c := Default()
	c.Statistics = true
	c.StatsMonths = 3
	c.StatsMaxBytes = 50 << 20
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	again, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.StatsMonths != 3 || again.StatsMaxBytes != 50<<20 {
		t.Errorf("read back %d months / %d bytes, want 3 / %d", again.StatsMonths, again.StatsMaxBytes, 50<<20)
	}
}

// A configuration written by a build that did not know these fields still
// loads, and loads with the defaults rather than with a cap of zero — which
// would be a store that can hold nothing.
func TestAConfigurationWithoutRetentionLoadsWithTheDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"host":"127.0.0.1","port":11535,"decode_concurrency":1,"statistics":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.StatsMonths != DefaultStatsMonths || c.StatsMaxBytes != DefaultStatsMaxBytes {
		t.Errorf("loaded %d months / %d bytes, want the defaults %d / %d",
			c.StatsMonths, c.StatsMaxBytes, DefaultStatsMonths, DefaultStatsMaxBytes)
	}
}

// Saving a figure that bounds nothing is refused at the moment the operator is
// there to read why.
func TestSavingRetentionThatBoundsNothingIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"no months at all", func(c *Config) { c.StatsMonths = 0 }},
		{"a negative horizon", func(c *Config) { c.StatsMonths = -1 }},
		{"more months than a Mac lives", func(c *Config) { c.StatsMonths = MaxStatsMonths + 1 }},
		{"a cap below one file", func(c *Config) { c.StatsMaxBytes = MinStatsMaxBytes - 1 }},
		{"a cap larger than any disk", func(c *Config) { c.StatsMaxBytes = MaxStatsMaxBytes + 1 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := Default()
			tt.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Error("the settings were accepted; a retention figure that bounds nothing must be refused")
			}
			if err := Save(filepath.Join(t.TempDir(), "config.json"), c); err == nil {
				t.Error("the settings were written; a retention figure that bounds nothing must be refused")
			}
		})
	}
}

// A hand-edited file is repaired rather than refused, though. Refusing it
// sends the next start into its fail-closed loopback-only branch — a
// machine-wide outage over a figure that decides how long a statistics file is
// kept — which is the same bargain the sampling preferences beside it strike.
func TestLoadRepairsRetentionRatherThanRefusingTheWholeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(
		`{"host":"127.0.0.1","port":11535,"decode_concurrency":1,"stats_months":-3,"stats_max_bytes":12}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, dropped, err := Load(path)
	if err != nil {
		t.Fatalf("the file was refused over a retention figure: %v", err)
	}
	if c.StatsMonths != DefaultStatsMonths || c.StatsMaxBytes != DefaultStatsMaxBytes {
		t.Errorf("loaded %d months / %d bytes, want them repaired to %d / %d",
			c.StatsMonths, c.StatsMaxBytes, DefaultStatsMonths, DefaultStatsMaxBytes)
	}
	if len(dropped) != 2 {
		t.Errorf("the load reported %v, want both repaired figures named", dropped)
	}
	if c.Host != "127.0.0.1" {
		t.Errorf("the rest of the file was discarded: host = %q", c.Host)
	}
}

// The shared root is recognised however it is spelled. A root that names the
// same directory by another spelling — a trailing slash, a dot segment, a
// doubled separator — must not slip past the exception and put a per-account
// record file inside the group-writable shared root.
func TestTheSharedRootIsRecognisedHoweverItIsSpelled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, "Library", "Application Support", "Gropius", "stats")
	for _, spelling := range []string{
		SharedRoot,
		SharedRoot + "/",
		SharedRoot + "/.",
		"/Users/Shared/./Gropius",
		"//Users/Shared/Gropius",
		"/Users/Shared/Gropius/../Gropius",
	} {
		if got := StatsDir(spelling); got != want {
			t.Errorf("StatsDir(%q) = %q, want the account's own %q", spelling, got, want)
		}
	}
}
