package runtime

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// refusalLogAt drives a no-room refusal through a pool logging at the given
// level, and returns what reached the log.
//
// The shape is the grace tests': a budget that holds one model, a model
// already resident and protected by the grace, and a second model asked for.
func refusalLogAt(t *testing.T, level slog.Level) string {
	t.Helper()
	var logged safeBuffer
	lv := new(slog.LevelVar)
	lv.Set(level)
	p := newTestPool(t, newFakeLauncher(), graceModels(), PoolOptions{
		MaxResidentBytes: graceBudget,
		EvictionGrace:    10 * time.Second,
		MaxEvictionWait:  30 * time.Second,
		MaxLoadWaiters:   1,
		Log:              slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: lv})),
	})

	warm(t, p, "org/a")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if _, release, err := p.Acquire(ctx, "org/b"); err == nil {
			release()
		}
	}()
	waitUntil(t, 2*time.Second, func() bool { return p.Waiting() == 1 },
		"the first request never joined the queue")

	if _, _, err := p.Acquire(context.Background(), "org/c"); err == nil {
		t.Fatal("a request past the load-waiter cap was served")
	}
	return logged.String()
}

// Sparse says which refusal happened and for which model, and nothing about
// this Mac's size.
//
// The figures are what an operator asks for when they are diagnosing, and they
// are also the facts a refusal to a network client is deliberately stripped
// of: the memory budget in bytes is roughly how much memory this Mac has, and
// the queue depth is how busy it is. Writing them on every refusal at the
// default level means a log a client can drive is a log that describes the
// machine in detail nobody chose.
func TestASparseRefusalNamesTheModelAndNotTheFigures(t *testing.T) {
	got := refusalLogAt(t, slog.LevelInfo)

	if !strings.Contains(got, "already waiting for memory") {
		t.Errorf("the sparse log does not say which refusal it was:\n%s", got)
	}
	if !strings.Contains(got, "model=org/c") {
		t.Errorf("the sparse log does not name the model that was refused:\n%s", got)
	}
	for _, figure := range []string{"limit=", "waiting=", "protected=", "waited="} {
		if strings.Contains(got, figure) {
			t.Errorf("the sparse log carries the figure %q that detailed is for:\n%s", figure, got)
		}
	}
}

// Detailed adds them, on the same event, without taking the sparse line away.
func TestADetailedRefusalCarriesTheFigures(t *testing.T) {
	got := refusalLogAt(t, slog.LevelDebug)

	if !strings.Contains(got, "already waiting for memory") {
		t.Errorf("the detailed log dropped the sparse line:\n%s", got)
	}
	for _, figure := range []string{"model=org/c", "limit=", "waiting="} {
		if !strings.Contains(got, figure) {
			t.Errorf("the detailed log is missing %q:\n%s", figure, got)
		}
	}
}
