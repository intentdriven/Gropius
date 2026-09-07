package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two intervals are stored in whole seconds beside the idle timeout they
// are bound to, and they survive a round trip through the file.
func TestSaveLoadRoundTripKeepsTheEvictionGraceSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	c := Default()
	c.IdleTimeoutSec = 600
	c.EvictionGrace = true
	c.EvictionGraceSec = 45
	c.EvictionMaxWaitSec = 90
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, dropped, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("Load dropped %v from a file it wrote itself", dropped)
	}
	if !got.EvictionGrace || got.EvictionGraceSec != 45 || got.EvictionMaxWaitSec != 90 {
		t.Errorf("round trip gave grace=%v %ds/%ds, want true 45s/90s",
			got.EvictionGrace, got.EvictionGraceSec, got.EvictionMaxWaitSec)
	}
}

// Off, with the two intervals carrying the defaults the record settles: a Mac
// that never touches this setting is served exactly as it is today.
func TestAFreshInstallHasEvictionGraceOffWithTheDefaultIntervals(t *testing.T) {
	c := Default()
	if c.EvictionGrace {
		t.Error("a fresh install has eviction grace switched on")
	}
	if c.EvictionGraceSec != DefaultEvictionGraceSec {
		t.Errorf("EvictionGraceSec = %d, want %d", c.EvictionGraceSec, DefaultEvictionGraceSec)
	}
	if c.EvictionMaxWaitSec != DefaultEvictionMaxWaitSec {
		t.Errorf("EvictionMaxWaitSec = %d, want %d", c.EvictionMaxWaitSec, DefaultEvictionMaxWaitSec)
	}
	if DefaultEvictionGraceSec != 120 || DefaultEvictionMaxWaitSec != 300 {
		t.Errorf("the defaults are %d s and %d s, want 120 s and 300 s",
			DefaultEvictionGraceSec, DefaultEvictionMaxWaitSec)
	}
}

// The idle reaper unloads an idle model after the idle timeout, so a grace
// longer than that timeout protects a model the reaper is about to take: the
// promise would be false. A save is the moment the operator is there to read
// why, so it is refused there, naming both figures.
func TestValidateRefusesAGraceLongerThanTheIdleTimeout(t *testing.T) {
	c := Default()
	c.EvictionGrace = true
	c.IdleTimeoutSec = 60
	c.EvictionGraceSec = 120

	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted a grace longer than the idle timeout")
	}
	for _, want := range []string{"60", "120"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %s", err, want)
		}
	}

	// The same figures with the switch off are inert, and with no idle timeout
	// there is no reaper to defeat.
	c.EvictionGrace = false
	if err := c.Validate(); err != nil {
		t.Errorf("Validate refused an inert grace: %v", err)
	}
	c.EvictionGrace = true
	c.IdleTimeoutSec = 0
	if err := c.Validate(); err != nil {
		t.Errorf("Validate refused a grace on a Mac with no idle timeout: %v", err)
	}
}

func TestValidateRefusesAnIntervalOutOfRange(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*Config)
	}{
		{"a grace of zero", func(c *Config) { c.EvictionGraceSec = 0 }},
		{"a negative grace", func(c *Config) { c.EvictionGraceSec = -1 }},
		{"a grace past the ceiling", func(c *Config) { c.EvictionGraceSec = MaxEvictionWaitSec + 1 }},
		{"a maximum wait of zero", func(c *Config) { c.EvictionMaxWaitSec = 0 }},
		{"a maximum wait past the ceiling", func(c *Config) { c.EvictionMaxWaitSec = MaxEvictionWaitSec + 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.set(&c)
			if err := c.Validate(); err == nil {
				t.Errorf("Validate accepted %s", tc.name)
			}
		})
	}
}

// A hand-edited file is repaired rather than refused, for the reason every
// other repair here has: a refused config.json takes the whole install down to
// the loopback-only defaults, which is far too much to pay for one interval.
func TestLoadRepairsAnUnusableEvictionGrace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"host":"0.0.0.0","port":11535,"api_key":"secret","decode_concurrency":4,
		"idle_timeout_sec":60,"eviction_grace":true,"eviction_grace_sec":900,
		"eviction_max_wait_sec":0,"stats_months":6,"stats_max_bytes":209715200}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	got, dropped, err := Load(path)
	if err != nil {
		t.Fatalf("Load refused a file it could repair: %v", err)
	}
	if got.APIKey != "secret" {
		t.Errorf("api_key = %q; the rest of the file must survive the repair", got.APIKey)
	}
	// The grace is clamped to the idle timeout, not dropped: the operator asked
	// for a grace, and the longest one the reaper leaves intact is the timeout.
	if got.EvictionGraceSec != 60 {
		t.Errorf("EvictionGraceSec = %d, want it clamped to the idle timeout (60)", got.EvictionGraceSec)
	}
	if got.EvictionMaxWaitSec != DefaultEvictionMaxWaitSec {
		t.Errorf("EvictionMaxWaitSec = %d, want the default", got.EvictionMaxWaitSec)
	}
	joined := strings.Join(dropped, " ")
	for _, want := range []string{"eviction_grace_sec", "eviction_max_wait_sec"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Load repaired %s without saying so: %v", want, dropped)
		}
	}
	// What Load hands back must be loadable: the repair, not the file, is what
	// the rest of the process runs on.
	if err := got.Validate(); err != nil {
		t.Errorf("the repaired config does not validate: %v", err)
	}
}
