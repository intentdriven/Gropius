package app

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/runtime"
	"github.com/intentdriven/Gropius/internal/stats"
)

// syncBuf collects log lines written from the observer's own goroutine while
// the test reads them.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(b)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// levelledLog is a logger whose level a test can move, exactly as Settings
// moves the process's.
func levelledLog(at slog.Level) (*slog.Logger, *slog.LevelVar, *syncBuf) {
	var buf syncBuf
	level := new(slog.LevelVar)
	level.Set(at)
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level})), level, &buf
}

// appWithLevel builds an app whose log level is a variable the test holds.
func appWithLevel(t *testing.T, cfg config.Config) (*App, *slog.LevelVar, *syncBuf) {
	t.Helper()
	log, level, buf := levelledLog(cfg.SlogLevel())
	a, err := New(Options{
		Paths:    config.NewPaths(t.TempDir()),
		Config:   cfg,
		Log:      log,
		LogLevel: level,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a, level, buf
}

// The level is applied live: an operator who switches to detailed while
// chasing a problem gets the next line at the new level, not the first line
// after a restart they would have to schedule.
func TestTheLogLevelChangesWithoutARestart(t *testing.T) {
	a, level, _ := appWithLevel(t, config.Default())

	if got := level.Level(); got != slog.LevelInfo {
		t.Fatalf("a fresh install starts at %v, want %v", got, slog.LevelInfo)
	}

	detailed := a.Config()
	detailed.LogLevel = config.LogLevelDetailed
	if err := a.SetConfig(detailed); err != nil {
		t.Fatalf("SetConfig(detailed): %v", err)
	}
	if got := level.Level(); got != slog.LevelDebug {
		t.Errorf("after saving detailed the level is %v, want %v", got, slog.LevelDebug)
	}

	sparse := a.Config()
	sparse.LogLevel = config.LogLevelSparse
	if err := a.SetConfig(sparse); err != nil {
		t.Fatalf("SetConfig(sparse): %v", err)
	}
	if got := level.Level(); got != slog.LevelInfo {
		t.Errorf("after saving sparse again the level is %v, want %v", got, slog.LevelInfo)
	}
}

// A start reads the level from the file it loaded, so a Mac restarted at
// detailed comes back detailed. Start-up and a save go through one function,
// which is what stops them drifting.
func TestAStartAppliesTheStoredLevel(t *testing.T) {
	cfg := config.Default()
	cfg.LogLevel = config.LogLevelDetailed
	_, level, _ := appWithLevel(t, cfg)

	if got := level.Level(); got != slog.LevelDebug {
		t.Errorf("a start with log_level=detailed is at %v, want %v", got, slog.LevelDebug)
	}
}

// A save is an event that mattered: on a Mac several people can reach the
// panel from, the settings changing under a running server is exactly the
// thing an operator later needs to have been told about.
func TestASaveIsOneSparseLine(t *testing.T) {
	a, _, buf := appWithLevel(t, config.Default())

	c := a.Config()
	c.IdleTimeoutSec = 600
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	line := buf.String()
	if !strings.Contains(line, "settings changed") {
		t.Errorf("a save wrote no sparse line:\n%s", line)
	}
	if !strings.Contains(line, "log_level=sparse") {
		t.Errorf("the save line does not name the level in force:\n%s", line)
	}
	for _, secret := range []string{"api_key", "hf_token"} {
		if strings.Contains(line, secret) {
			t.Errorf("the save line names %q:\n%s", secret, line)
		}
	}
}

// A model arriving in memory and leaving it are the two events an operator
// reads the log to reconstruct, so each is one line at the sparse level.
func TestALoadAndAnUnloadAreOneSparseLineEach(t *testing.T) {
	log, _, buf := levelledLog(slog.LevelInfo)
	obs := poolObserver{rec: stats.New(stats.Options{}), log: log}

	obs.LoadStarted("org/repo")
	obs.LoadFinished("org/repo", 4*time.Second, nil)
	obs.EntryStopped("org/repo", runtime.StopEvicted)

	got := buf.String()
	for _, want := range []string{"model loaded", "model unloaded", "org/repo", "reason=evicted"} {
		if !strings.Contains(got, want) {
			t.Errorf("the sparse log is missing %q:\n%s", want, got)
		}
	}
	// How long it took is a figure, and figures are what detailed adds.
	if strings.Contains(got, "took=") {
		t.Errorf("the sparse load line carries the figure detailed is for:\n%s", got)
	}
}

// A model that never became ready is a failure the operator needs at the
// sparse level, and the error behind it is a figure detailed adds — it can
// carry absolute paths out of this account's own home directory.
func TestALoadThatFailedIsSparseAndItsErrorIsDetailed(t *testing.T) {
	t.Run("sparse", func(t *testing.T) {
		log, _, buf := levelledLog(slog.LevelInfo)
		obs := poolObserver{rec: stats.New(stats.Options{}), log: log}
		obs.LoadFinished("org/repo", time.Second, errors.New("no such file or directory"))

		got := buf.String()
		if !strings.Contains(got, "model failed to load") || !strings.Contains(got, "org/repo") {
			t.Errorf("the sparse log does not report the failed load:\n%s", got)
		}
		if strings.Contains(got, "no such file or directory") {
			t.Errorf("the sparse line carries the wrapped error:\n%s", got)
		}
	})
	t.Run("detailed", func(t *testing.T) {
		log, _, buf := levelledLog(slog.LevelDebug)
		obs := poolObserver{rec: stats.New(stats.Options{}), log: log}
		obs.LoadFinished("org/repo", time.Second, errors.New("no such file or directory"))

		got := buf.String()
		if !strings.Contains(got, "no such file or directory") {
			t.Errorf("the detailed log does not carry the error behind the failed load:\n%s", got)
		}
		if !strings.Contains(got, "took=") {
			t.Errorf("the detailed log does not carry how long the load took:\n%s", got)
		}
	})
}

// Detailed adds the figures and nothing else: the sparse line is still there,
// so an operator who turns detailed on does not lose the summary they were
// reading.
func TestDetailedAddsToTheSparseLineRatherThanReplacingIt(t *testing.T) {
	log, _, buf := levelledLog(slog.LevelDebug)
	obs := poolObserver{rec: stats.New(stats.Options{}), log: log}

	obs.LoadFinished("org/repo", 4*time.Second, nil)

	got := buf.String()
	if !strings.Contains(got, "model loaded") {
		t.Errorf("the detailed log dropped the sparse line:\n%s", got)
	}
	if !strings.Contains(got, "took=") {
		t.Errorf("the detailed log does not carry how long the load took:\n%s", got)
	}
}

// A nil level variable is the shape every existing test builds an app with, so
// it has to stay harmless: an app assembled without one must not panic at a
// save.
func TestAnAppWithNoLevelVariableStillSaves(t *testing.T) {
	a := newTestApp(t)
	c := a.Config()
	c.LogLevel = config.LogLevelDetailed
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("SetConfig on an app with no level variable: %v", err)
	}
}
