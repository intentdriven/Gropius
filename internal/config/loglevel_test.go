package config_test

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// writeConfig writes a settings file and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Sparse by default, and a file written before this field existed means what a
// fresh install means. The empty value is the default rather than a value of
// its own, so nothing has to be written to config.json to get it.
func TestAFreshInstallLogsSparsely(t *testing.T) {
	if got := config.Default().EffectiveLogLevel(); got != config.LogLevelSparse {
		t.Errorf("a fresh install logs at %q, want %q", got, config.LogLevelSparse)
	}
	if got := config.Default().LogLevel; got != "" {
		t.Errorf("Default() stores log_level=%q; the default is the empty value", got)
	}

	cfg, notices, err := config.Load(writeConfig(t, `{"host":"127.0.0.1","port":11535,"decode_concurrency":4}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.EffectiveLogLevel(); got != config.LogLevelSparse {
		t.Errorf("a settings file with no log_level loads as %q, want %q", got, config.LogLevelSparse)
	}
	if !notices.Empty() {
		t.Errorf("a settings file with no log_level produced notices: %v", notices.All())
	}
}

// The two levels are the two the panel and the docs name, and both survive a
// round trip through the file.
func TestBothLogLevelsSurviveTheFile(t *testing.T) {
	for _, level := range []string{config.LogLevelSparse, config.LogLevelDetailed} {
		t.Run(level, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			cfg := config.Default()
			cfg.LogLevel = level
			if err := config.Save(path, cfg); err != nil {
				t.Fatalf("Save: %v", err)
			}
			back, notices, err := config.Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if back.EffectiveLogLevel() != level {
				t.Errorf("log_level = %q after a round trip, want %q", back.EffectiveLogLevel(), level)
			}
			if !notices.Empty() {
				t.Errorf("a valid log_level produced notices: %v", notices.All())
			}
		})
	}
}

// A hand-edited or restored file with a level this build cannot use is
// REPAIRED, not refused. A refused config.json takes the whole install down to
// a loopback bind with the shipping defaults, which is far too much to pay for
// a word that decides how much the log says.
func TestLoadRepairsAnUnusableLogLevel(t *testing.T) {
	for _, written := range []string{`"verbose"`, `"DETAILED"`, `""`, `"  "`} {
		t.Run(written, func(t *testing.T) {
			cfg, notices, err := config.Load(writeConfig(t,
				`{"host":"127.0.0.1","port":11535,"decode_concurrency":4,"log_level":`+written+`}`))
			if err != nil {
				t.Fatalf("Load refused the file instead of repairing it: %v", err)
			}
			if got := cfg.EffectiveLogLevel(); got != config.LogLevelSparse {
				t.Errorf("log_level in force = %q, want the default %q", got, config.LogLevelSparse)
			}
			if written == `""` {
				// An absent value is the default, not a repair: there is
				// nothing for the operator to go and look at.
				if !notices.Empty() {
					t.Errorf("an empty log_level was reported as repaired: %v", notices.All())
				}
				return
			}
			named := slices.ContainsFunc(notices.Repaired, func(s string) bool {
				return len(s) >= len("log_level=") && s[:len("log_level=")] == "log_level="
			})
			if !named {
				t.Errorf("Repaired = %v, want it to name log_level", notices.Repaired)
			}
			if slices.ContainsFunc(notices.Ignored, func(s string) bool {
				return len(s) >= len("log_level=") && s[:len("log_level=")] == "log_level="
			}) {
				t.Error("log_level was reported as ignored; it IS in force, in a changed form")
			}
		})
	}
}

// A save is the moment the operator is there to read why, so it is refused
// rather than repaired — the rule sanitizeStats states and every other
// repairable setting follows.
func TestASaveRefusesAnUnusableLogLevel(t *testing.T) {
	cfg := config.Default()
	cfg.LogLevel = "verbose"
	if err := cfg.Validate(); err == nil {
		t.Error("Validate accepted log_level=verbose")
	}
	if err := config.Save(filepath.Join(t.TempDir(), "config.json"), cfg); err == nil {
		t.Error("Save wrote a settings file with log_level=verbose")
	}
}

// The two levels are what the handler reads. The mapping lives with the
// setting, so "detailed" cannot come to mean one thing in the panel and
// another in the process.
func TestTheLevelNamesTheLevelTheHandlerReads(t *testing.T) {
	for name, want := range map[string]slog.Level{
		config.LogLevelSparse:   slog.LevelInfo,
		config.LogLevelDetailed: slog.LevelDebug,
		"":                      slog.LevelInfo,
	} {
		cfg := config.Default()
		cfg.LogLevel = name
		if got := cfg.SlogLevel(); got != want {
			t.Errorf("log_level %q reads as %v, want %v", name, got, want)
		}
	}
}

// The setting is omitted from the file when it is the default, so a fresh
// install's config.json does not grow a field nobody set.
func TestTheDefaultLevelIsNotWrittenToTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, config.Default()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["log_level"]; ok {
		t.Errorf("a fresh settings file carries log_level:\n%s", b)
	}
}
