package app

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/runtime"
	"github.com/intentdriven/Gropius/internal/stats"
)

// The pool names the seven ways a model can leave it, and the recorder names
// them again. Two vocabularies in two packages, joined here — so this is where
// they have to be shown to agree. A cast would have gone on agreeing forever
// after one side changed its spelling, and the only figure that reads a reason
// is the eviction count, which would then quietly stop rising.
func TestEveryReasonThePoolGivesHasAStatisticsName(t *testing.T) {
	want := map[runtime.StopReason]string{
		runtime.StopEvicted:    stats.ReasonEvicted,
		runtime.StopIdle:       stats.ReasonIdle,
		runtime.StopUnloaded:   stats.ReasonUnloaded,
		runtime.StopAbandoned:  stats.ReasonAbandoned,
		runtime.StopLoadFailed: stats.ReasonLoadFailed,
		runtime.StopCrashed:    stats.ReasonCrashed,
		runtime.StopShutdown:   stats.ReasonShutdown,
	}
	for reason, name := range want {
		got, ok := stopReasons[reason]
		if !ok {
			t.Errorf("the pool can report %q and the statistics have no name for it", reason)
			continue
		}
		if got != name {
			t.Errorf("%q is recorded as %q, want %q", reason, got, name)
		}
	}
	if len(stopReasons) != len(want) {
		t.Errorf("the mapping holds %d reasons and the pool gives %d", len(stopReasons), len(want))
	}
}

// And the join actually joins: the pool's own word for an eviction, put
// through this adapter, is what makes a model's eviction count rise — and none
// of the other six does.
func TestOnlyThePoolsEvictionIncrementsTheEvictionCount(t *testing.T) {
	rec := stats.New(stats.Options{})
	rec.SetEnabled(true)
	obs := poolObserver{rec: rec, log: slog.New(slog.DiscardHandler)}

	obs.LoadStarted("org/a")
	obs.LoadFinished("org/a", 2*time.Second, nil)
	for reason := range stopReasons {
		obs.EntryStopped("org/a", reason)
	}

	got := rec.Summary()[0]
	if got.Evictions != 1 {
		t.Errorf("all seven removals produced %d evictions, want 1", got.Evictions)
	}
	if got.Loads != 1 || got.LastLoadMS != 2000 {
		t.Errorf("the load was recorded as %+v, want one load of 2000 ms", got)
	}
	obs.LoadFinished("org/b", time.Second, errors.New("did not become ready"))
	for _, m := range rec.Summary() {
		if m.Model == "org/b" && m.FailedLoads != 1 {
			t.Errorf("a load that failed was recorded as %+v, want one failed load", m)
		}
	}
}

// A reason nobody mapped is recorded under its own name and said out loud,
// rather than counted as something it is not.
func TestAnUnmappedReasonIsReported(t *testing.T) {
	var logged bytes.Buffer
	rec := stats.New(stats.Options{})
	rec.SetEnabled(true)
	obs := poolObserver{rec: rec, log: slog.New(slog.NewTextHandler(&logged, nil))}

	obs.EntryStopped("org/a", runtime.StopReason("something-new"))

	if !bytes.Contains(logged.Bytes(), []byte("something-new")) {
		t.Errorf("an unmapped reason was swallowed; the log says:\n%s", logged.String())
	}
	if got := rec.Summary()[0]; got.Evictions != 0 {
		t.Errorf("an unmapped reason was counted as an eviction: %+v", got)
	}
}
