package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/stats"
)

func statsApp(t *testing.T, c config.Config) (*App, config.Paths) {
	t.Helper()
	paths := config.NewPaths(t.TempDir())
	a, err := New(Options{Paths: paths, Config: c})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a, paths
}

// The store is under the same switch as the live view, and under no other:
// nothing is on disk until the operator turns recording on, and what is on
// disk when they turn it off stays there.
func TestTheStoreFollowsTheStatisticsSwitch(t *testing.T) {
	a, paths := statsApp(t, config.Default())
	if _, err := os.Stat(paths.Stats); !os.IsNotExist(err) {
		t.Fatalf("a fresh install has a statistics store directory (%v)", err)
	}

	on := config.Default()
	on.Statistics = true
	if err := a.SetConfig(on); err != nil {
		t.Fatal(err)
	}
	a.Stats.Add(stats.Record{Model: "org/a", At: 100, Class: stats.ClassOK, CompletionTokens: 7})
	if err := a.StatsStore.Flush(); err != nil {
		t.Fatal(err)
	}
	files, err := a.StatsStore.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("recording wrote %d files, want one", len(files))
	}

	before, err := os.ReadFile(filepath.Join(paths.Stats, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	off := config.Default()
	if err := a.SetConfig(off); err != nil {
		t.Fatal(err)
	}
	a.Stats.Add(stats.Record{Model: "org/a", At: 200, Class: stats.ClassOK})
	after, err := os.ReadFile(filepath.Join(paths.Stats, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("the store changed after recording was switched off:\n before %q\n after  %q", before, after)
	}
	if a.StatsStore.Enabled() {
		t.Error("the store is still recording after the switch went off")
	}
}

// Every startup with recording on writes down what Gropius is actually
// serving under, so a reader of the store can tell a change in the figures
// from a change in the settings.
func TestTheSettingsInForceAreRecordedWhenRecordingStarts(t *testing.T) {
	c := config.Default()
	c.Statistics = true
	c.DecodeConcurrency = 3
	c.IdleTimeoutSec = 90
	c.StatsMonths = 4
	a, _ := statsApp(t, c)

	var got []stats.Settings
	if err := a.StatsStore.Latest(0, func(l stats.Line) bool {
		if l.Kind == stats.KindSettings {
			got = append(got, l.Settings)
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("the store holds %d settings records at startup, want one", len(got))
	}
	set := got[0]
	if set.DecodeConcurrency != 3 || set.IdleTimeoutSec != 90 || set.Months != 4 {
		t.Errorf("the settings record says %+v, not what the server is running with", set)
	}
	if set.BudgetBytes <= 0 {
		t.Errorf("the settings record carries no memory budget (%d)", set.BudgetBytes)
	}
	if set.At < time.Now().Add(-time.Hour).UTC().Unix() {
		t.Errorf("the settings record is stamped %d, which is not when it was written", set.At)
	}

	// A save that changes nothing about them writes no second record.
	if err := a.SetConfig(c); err != nil {
		t.Fatal(err)
	}
	got = got[:0]
	if err := a.StatsStore.Latest(0, func(l stats.Line) bool {
		if l.Kind == stats.KindSettings {
			got = append(got, l.Settings)
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("a save that changed nothing wrote %d settings records", len(got))
	}
}

// Clear empties both halves of what recording holds — the files and the live
// view — and leaves recording on.
func TestClearEmptiesTheStoreAndTheView(t *testing.T) {
	c := config.Default()
	c.Statistics = true
	a, _ := statsApp(t, c)
	for i := range 5 {
		a.Stats.Add(stats.Record{Model: "org/a", At: int64(i + 1), Class: stats.ClassOK})
	}
	if err := a.StatsStore.Flush(); err != nil {
		t.Fatal(err)
	}

	if err := a.ClearStats(); err != nil {
		t.Fatal(err)
	}
	if files, err := a.StatsStore.Files(); err != nil {
		t.Fatal(err)
	} else if len(files) != 0 {
		t.Errorf("the store still holds %v after Clear", files)
	}
	if v := a.Stats.View(); len(v.Requests) != 0 || !v.Enabled {
		t.Errorf("after Clear the live view holds %d requests and enabled=%v", len(v.Requests), v.Enabled)
	}

	// Recording carries on, and the first thing written is the settings in
	// force again: a record made after a Clear must still be readable against
	// the settings that produced it.
	a.Stats.Add(stats.Record{Model: "org/a", At: 99, Class: stats.ClassOK})
	if err := a.StatsStore.Flush(); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	if err := a.StatsStore.Latest(0, func(l stats.Line) bool {
		kinds = append(kinds, l.Kind)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if len(kinds) != 2 || kinds[0] != stats.KindRequest || kinds[1] != stats.KindSettings {
		t.Errorf("after Clear the store holds %v, want the settings in force and then the request", kinds)
	}
}

// Clear removes the request records and nothing else. The per-model logs are
// a different feature with their own rule about when they are emptied, and
// they hold what a model server itself wrote — so a button about statistics
// must not reach them.
func TestClearLeavesThePerModelLogsAlone(t *testing.T) {
	c := config.Default()
	c.Statistics = true
	a, paths := statsApp(t, c)
	logs := map[string][]byte{
		"org_a.log":                []byte("loading weights" + "\n"),
		"stats-20260907-001.jsonl": []byte("a file named like a record, in the wrong directory" + "\n"),
	}
	for name, body := range logs {
		if err := os.WriteFile(filepath.Join(paths.Logs, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a.Stats.Add(stats.Record{Model: "org/a", At: 1788696030, Class: stats.ClassOK})
	if err := a.StatsStore.Flush(); err != nil {
		t.Fatal(err)
	}

	if err := a.ClearStats(); err != nil {
		t.Fatal(err)
	}
	for name, want := range logs {
		got, err := os.ReadFile(filepath.Join(paths.Logs, name))
		if err != nil {
			t.Errorf("Clear removed %s from the log directory: %v", name, err)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("%s reads %q after Clear, want %q", name, got, want)
		}
	}
}

// A store that cannot be created is not a reason to refuse to serve. The
// records stay in memory and the refusal is said once.
func TestAStoreThatCannotBeCreatedLeavesTheServerServing(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatal(err)
	}
	paths := config.NewPaths(root)
	c := config.Default()
	c.Statistics = true
	a, err := New(Options{Paths: paths, Config: c})
	if err != nil {
		t.Fatalf("the server refused to start because its statistics store could not be opened: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	if a.StatsStore.Enabled() {
		t.Error("the store opened in a directory every account on the Mac can write")
	}
	if !a.Stats.Enabled() {
		t.Error("the live view was switched off because the store could not be opened")
	}
	a.Stats.Add(stats.Record{Model: "org/a", At: 1, Class: stats.ClassOK})
	if v := a.Stats.View(); len(v.Requests) != 1 {
		t.Errorf("the live view holds %d requests, want the one recorded", len(v.Requests))
	}
	if _, err := os.Stat(paths.Stats); !os.IsNotExist(err) {
		t.Errorf("the store created its directory in a place it refused (%v)", err)
	}
	// And the panel is told, rather than being shown an empty store: an
	// operator who believes records are accumulating finds out otherwise only
	// when they go looking for them.
	if st := a.StatsStore.Status(); !st.Refused {
		t.Errorf("the store reports itself as %+v, not as refused", st)
	}
}
