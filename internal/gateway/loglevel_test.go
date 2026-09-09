package gateway

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/mlxtest"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// levelBuffer collects the gateway's log while the handler goroutine writes it.
type levelBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *levelBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *levelBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// launchFailureLogAt drives a model server that will not start through the
// gateway, logging at the given level, and returns what reached the log.
func launchFailureLogAt(t *testing.T, level slog.Level) string {
	t.Helper()
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	var logged levelBuffer
	lv := new(slog.LevelVar)
	lv.Set(level)

	models := &stubModels{models: []registry.Model{{RepoID: "org/m", State: registry.StateReady}}}
	leaky := fmt.Errorf("python runtime is not installed (/Users/carol/Library/Application Support/Gropius/venv/bin/python): file does not exist")
	pool := &stubPool{srv: fake, acquireErr: fmt.Errorf("start model server for org/m: %w", &runtime.LaunchError{Err: leaky})}
	g := New(Options{
		Config: config.Default(), Pool: pool, Models: models,
		Log: slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: lv})),
	})
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()

	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":    "org/m",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	return logged.String()
}

// A model server that would not start is an event that mattered, so the sparse
// level says so and names the model. What it does not say is what the child
// process said: that error carries absolute paths out of the serving account's
// own home directory, and it is a figure — the operator asks for it by
// switching to detailed.
func TestASparseLaunchFailureNamesTheModelAndNotTheError(t *testing.T) {
	got := launchFailureLogAt(t, slog.LevelInfo)

	if !strings.Contains(got, "model launch failed") || !strings.Contains(got, "model=org/m") {
		t.Errorf("the sparse log does not report the failed launch:\n%s", got)
	}
	if strings.Contains(got, "/Users/") || strings.Contains(got, "carol") {
		t.Errorf("the sparse log carries the wrapped launch error:\n%s", got)
	}
}

// Detailed carries it, on the same event, without taking the sparse line away.
func TestADetailedLaunchFailureCarriesTheWrappedError(t *testing.T) {
	got := launchFailureLogAt(t, slog.LevelDebug)

	if !strings.Contains(got, "model launch failed") {
		t.Errorf("the detailed log dropped the sparse line:\n%s", got)
	}
	if !strings.Contains(got, "python runtime is not installed") {
		t.Errorf("the detailed log does not carry the error behind the failed launch:\n%s", got)
	}
}
