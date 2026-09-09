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
	// Grace on a LAN-exposed server needs a key: the wait queue is shared out
	// per key, so without one it cannot be shared out at all.
	c.APIKey = "test-key"
	c.EvictionGraceSec = 45
	c.EvictionMaxWaitSec = 90
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, notices, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(notices.All()) != 0 {
		t.Errorf("Load dropped %v from a file it wrote itself", notices.All())
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
	// Grace on a LAN-exposed server needs a key: the wait queue is shared out
	// per key, so without one it cannot be shared out at all.
	c.APIKey = "test-key"
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
	// Grace on a LAN-exposed server needs a key: the wait queue is shared out
	// per key, so without one it cannot be shared out at all.
	c.APIKey = "test-key"
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
		{"a negative grace", func(c *Config) { c.EvictionGraceSec = -1 }},
		{"a grace past the ceiling", func(c *Config) { c.EvictionGraceSec = MaxEvictionWaitSec + 1 }},
		{"a negative maximum wait", func(c *Config) { c.EvictionMaxWaitSec = -1 }},
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
		"eviction_max_wait_sec":-5,"stats_months":6,"stats_max_bytes":209715200}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	got, notices, err := Load(path)
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
	joined := strings.Join(notices.Repaired, " ")
	for _, want := range []string{"eviction_grace_sec", "eviction_max_wait_sec"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Load repaired %s without saying so: %v", want, notices.Repaired)
		}
	}
	// What Load hands back must be loadable: the repair, not the file, is what
	// the rest of the process runs on.
	if err := got.Validate(); err != nil {
		t.Errorf("the repaired config does not validate: %v", err)
	}
}

// A field the operator clears, a file from a build that had no such field, and
// a fresh install all mean the same thing: the default. A zero that refused
// the save would be the wedge this repository has now built twice — a setting
// nobody chose standing between the operator and the API key they came to set.
func TestAClearedIntervalMeansTheDefaultRatherThanARefusal(t *testing.T) {
	c := Default()
	c.EvictionGrace = true
	// Grace on a LAN-exposed server needs a key: the wait queue is shared out
	// per key, so without one it cannot be shared out at all.
	c.APIKey = "test-key"
	c.EvictionGraceSec = 0
	c.EvictionMaxWaitSec = 0

	if err := c.Validate(); err != nil {
		t.Fatalf("Validate refused a cleared interval: %v", err)
	}
	if got := c.GraceSeconds(); got != DefaultEvictionGraceSec {
		t.Errorf("GraceSeconds() = %d, want the default %d", got, DefaultEvictionGraceSec)
	}
	if got := c.MaxWaitSeconds(); got != DefaultEvictionMaxWaitSec {
		t.Errorf("MaxWaitSeconds() = %d, want the default %d", got, DefaultEvictionMaxWaitSec)
	}
	// And the resolved figure is what the idle-timeout rule is judged on, so a
	// cleared grace is refused against a short idle timeout exactly as the
	// figure it stands for would be.
	c.IdleTimeoutSec = 30
	if err := c.Validate(); err == nil {
		t.Error("Validate accepted a cleared grace that resolves to more than the idle timeout")
	}
}

// A maximum wait below the grace does not make the wait shorter, it makes the
// anti-starvation clause unreachable: a waiter may override a model's
// protection once its own age passes the grace, and it is refused once its age
// passes the maximum, so with the maximum the smaller of the two the first can
// never happen. That is the indefinite starvation grace exists to prevent,
// restored by a pair of numbers the Settings form offers side by side.
func TestValidateRefusesAMaximumWaitBelowTheGrace(t *testing.T) {
	c := Default()
	c.EvictionGrace = true
	// Grace on a LAN-exposed server needs a key: the wait queue is shared out
	// per key, so without one it cannot be shared out at all.
	c.APIKey = "test-key"
	c.EvictionGraceSec = 300
	c.EvictionMaxWaitSec = 60

	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted a maximum wait shorter than the grace")
	}
	for _, want := range []string{"300", "60"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %s", err, want)
		}
	}

	// Equal is fine: a waiter is served the instant its own age reaches the
	// grace, which is the same instant its maximum runs out.
	c.EvictionMaxWaitSec = 300
	if err := c.Validate(); err != nil {
		t.Errorf("Validate refused a maximum wait equal to the grace: %v", err)
	}
	// And with the switch off the pair decides nothing.
	c.EvictionGrace = false
	c.EvictionMaxWaitSec = 60
	if err := c.Validate(); err != nil {
		t.Errorf("Validate refused an inert pair: %v", err)
	}
}

// A hand-edited file is raised rather than refused, for the reason every other
// repair here has.
func TestLoadRaisesAMaximumWaitBelowTheGrace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"host":"0.0.0.0","port":11535,"api_key":"secret","decode_concurrency":4,
		"eviction_grace":true,"eviction_grace_sec":300,"eviction_max_wait_sec":60,
		"stats_months":6,"stats_max_bytes":209715200}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	got, notices, err := Load(path)
	if err != nil {
		t.Fatalf("Load refused a file it could repair: %v", err)
	}
	if got.APIKey != "secret" {
		t.Errorf("api_key = %q; the rest of the file must survive the repair", got.APIKey)
	}
	if got.EvictionMaxWaitSec != 300 {
		t.Errorf("EvictionMaxWaitSec = %d, want it raised to the grace (300)", got.EvictionMaxWaitSec)
	}
	if !strings.Contains(strings.Join(notices.Repaired, " "), "eviction_max_wait_sec") {
		t.Errorf("Load repaired the maximum wait without saying so: %v", notices.Repaired)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("the repaired config does not validate: %v", err)
	}
}

// Eviction grace on an open endpoint is a denial-of-service lever: the wait
// queue is shared out per API key, so with no key it cannot be shared out at
// all and one client can hold up model loading for everyone.
func TestGraceNeedsAKeyOnlyWhenExposed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		host    string
		key     string
		wantErr bool
	}{
		{name: "LAN with no key is refused", host: "0.0.0.0", wantErr: true},
		{name: "specific LAN address with no key is refused", host: "192.0.2.5", wantErr: true},
		{name: "LAN with a key is allowed", host: "0.0.0.0", key: "k"},
		{name: "loopback with no key is allowed", host: "127.0.0.1"},
		{name: "localhost with no key is allowed", host: "localhost"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.Host, c.APIKey, c.EvictionGrace = tc.host, tc.key, true
			err := c.Validate()
			if tc.wantErr && err == nil {
				t.Error("expected grace to be refused without a key on an exposed bind")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected refusal: %v", err)
			}
		})
	}
}

// Grace off must never be refused, whatever the bind: the shipping default
// stores the two intervals while the feature is switched off.
func TestGraceOffIsNeverRefused(t *testing.T) {
	c := Default()
	c.Host, c.APIKey, c.EvictionGrace = "0.0.0.0", "", false
	if err := c.Validate(); err != nil {
		t.Errorf("grace off must validate: %v", err)
	}
}
