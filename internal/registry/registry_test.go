package registry

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestRegistry(t *testing.T) (*Registry, string) {
	t.Helper()
	dir := t.TempDir()
	r, err := Open(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return r, dir
}

func TestReconcileInterruptedMarksOrphanedDownloadsFailed(t *testing.T) {
	r, _ := newTestRegistry(t)
	r.Put(Model{RepoID: "org/downloading", State: StateDownloading, Progress: 21})
	r.Put(Model{RepoID: "org/ready", State: StateReady})
	r.Put(Model{RepoID: "org/failed", State: StateFailed, Err: "boom"})

	changed := r.ReconcileInterrupted()
	if len(changed) != 1 || changed[0] != "org/downloading" {
		t.Fatalf("changed = %v, want [org/downloading]", changed)
	}

	got, err := r.Get("org/downloading")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateFailed {
		t.Fatalf("state = %q, want failed", got.State)
	}
	if got.Err == "" {
		t.Fatal("failed model should carry an explanation")
	}
	// A ready model must be left untouched.
	if ready, _ := r.Get("org/ready"); ready.State != StateReady {
		t.Fatalf("ready model was altered: %q", ready.State)
	}
}

func TestReconcileInterruptedPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	r1, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	r1.Put(Model{RepoID: "org/dl", State: StateDownloading})
	r1.ReconcileInterrupted()

	r2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if m, _ := r2.Get("org/dl"); m.State != StateFailed {
		t.Fatalf("state after reopen = %q, want failed", m.State)
	}
}

func TestReconcileInterruptedNoOpWhenNothingDownloading(t *testing.T) {
	r, _ := newTestRegistry(t)
	r.Put(Model{RepoID: "org/ready", State: StateReady})
	if changed := r.ReconcileInterrupted(); changed != nil {
		t.Fatalf("changed = %v, want nil", changed)
	}
}

func TestPutGetList(t *testing.T) {
	r, _ := newTestRegistry(t)

	if err := r.Put(Model{RepoID: "mlx-community/B", State: StateReady}); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(Model{RepoID: "mlx-community/A", State: StateReady}); err != nil {
		t.Fatal(err)
	}

	m, err := r.Get("mlx-community/A")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.RepoID != "mlx-community/A" {
		t.Errorf("RepoID = %q", m.RepoID)
	}

	list := r.List()
	if len(list) != 2 {
		t.Fatalf("got %d models, want 2", len(list))
	}
	// Sorted for a stable UI.
	if list[0].RepoID != "mlx-community/A" {
		t.Errorf("list not sorted: %v", list)
	}
}

func TestGetMissingReturnsErrNotFound(t *testing.T) {
	r, _ := newTestRegistry(t)
	_, err := r.Get("nope/nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestPutRequiresRepoID(t *testing.T) {
	r, _ := newTestRegistry(t)
	if err := r.Put(Model{}); err == nil {
		t.Error("expected an error for an empty RepoID")
	}
}

func TestReadyFiltersOutIncompleteModels(t *testing.T) {
	r, _ := newTestRegistry(t)
	r.Put(Model{RepoID: "org/ready", State: StateReady})
	r.Put(Model{RepoID: "org/downloading", State: StateDownloading})
	r.Put(Model{RepoID: "org/failed", State: StateFailed})

	ready := r.Ready()
	if len(ready) != 1 || ready[0].RepoID != "org/ready" {
		t.Errorf("Ready() = %v, want only org/ready", ready)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")

	r1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r1.Put(Model{RepoID: "org/model", State: StateReady, Bytes: 4096}); err != nil {
		t.Fatal(err)
	}

	r2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := r2.Get("org/model")
	if err != nil {
		t.Fatalf("model did not survive a reopen: %v", err)
	}
	if m.Bytes != 4096 {
		t.Errorf("Bytes = %d, want 4096", m.Bytes)
	}
}

// A corrupt index must not brick the app — the model directories are the real
// data and Rescan can rebuild from them.
func TestOpenCorruptFileStartsEmptyRatherThanFailing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte("{{{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open should tolerate a corrupt index, got: %v", err)
	}
	if len(r.List()) != 0 {
		t.Error("expected an empty registry")
	}
}

func TestSetState(t *testing.T) {
	r, _ := newTestRegistry(t)
	r.Put(Model{RepoID: "org/m", State: StateDownloading})

	if err := r.SetState("org/m", StateReady, 100, ""); err != nil {
		t.Fatal(err)
	}
	m, _ := r.Get("org/m")
	if m.State != StateReady || m.Progress != 100 {
		t.Errorf("got state=%s progress=%v", m.State, m.Progress)
	}

	if err := r.SetState("org/missing", StateReady, 0, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetState on a missing model: want ErrNotFound, got %v", err)
	}
}

func TestRemoveDeletesFilesFromDisk(t *testing.T) {
	r, dir := newTestRegistry(t)
	modelDir := filepath.Join(dir, "models", "org", "m")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.safetensors"), []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.Put(Model{RepoID: "org/m", Path: modelDir, State: StateReady})

	if err := r.Remove("org/m"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := r.Get("org/m"); !errors.Is(err, ErrNotFound) {
		t.Error("model still in the index after Remove")
	}
	if _, err := os.Stat(modelDir); !os.IsNotExist(err) {
		t.Error("model files still on disk after Remove — the disk space was not reclaimed")
	}
}

// writeModelDir creates a directory that looks like a complete model.
func writeModelDir(t *testing.T, root, org, name string, weightBytes int) string {
	t.Helper()
	dir := filepath.Join(root, org, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), make([]byte, weightBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Rescan is what lets a second macOS user account pick up models the first
// account downloaded into the shared cache.
func TestRescanAdoptsExistingModelDirectories(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	want := writeModelDir(t, models, "mlx-community", "Qwen3-8B-4bit", 1024)

	if err := r.Rescan(models); err != nil {
		t.Fatalf("Rescan: %v", err)
	}

	m, err := r.Get("mlx-community/Qwen3-8B-4bit")
	if err != nil {
		t.Fatalf("Rescan did not adopt the model directory: %v", err)
	}
	if m.Path != want {
		t.Errorf("Path = %q, want %q", m.Path, want)
	}
	if !m.Ready() {
		t.Errorf("adopted model should be ready, got %s", m.State)
	}
	// config.json (2 bytes) + weights (1024)
	if m.Bytes != 1026 {
		t.Errorf("Bytes = %d, want 1026", m.Bytes)
	}
}

func TestRescanIgnoresDirectoriesWithoutWeights(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	// config.json but no safetensors — not loadable.
	junk := filepath.Join(models, "org", "not-a-model")
	if err := os.MkdirAll(junk, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(junk, "config.json"), []byte("{}"), 0o644)

	if err := r.Rescan(models); err != nil {
		t.Fatal(err)
	}
	if len(r.List()) != 0 {
		t.Errorf("Rescan adopted a directory with no weights: %v", r.List())
	}
}

// A half-downloaded directory must not be adopted as ready, or the app would
// try to serve a truncated model.
func TestRescanSkipsPartialDownloads(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	md := writeModelDir(t, models, "org", "half", 512)
	// A leftover .part marks the download as incomplete.
	if err := os.WriteFile(filepath.Join(md, "extra.safetensors.gropius-part"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := r.Rescan(models); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("org/half"); !errors.Is(err, ErrNotFound) {
		t.Error("Rescan adopted a partially-downloaded model as ready")
	}
}

// Downloads can create nested files (a repo manifest may include
// subdirectories), so the rescan must walk the whole tree: nested bytes count
// toward the size that feeds the memory budget, and a nested .part still
// marks the download as incomplete.
func TestRescanCountsNestedFiles(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	md := writeModelDir(t, models, "org", "nested", 1024)
	sub := filepath.Join(md, "processor")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "tokenizer.json"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := r.Rescan(models); err != nil {
		t.Fatal(err)
	}
	m, err := r.Get("org/nested")
	if err != nil {
		t.Fatalf("Rescan did not adopt the model: %v", err)
	}
	// config.json (2) + weights (1024) + nested tokenizer.json (100)
	if m.Bytes != 1126 {
		t.Errorf("Bytes = %d, want 1126 (nested files must be counted)", m.Bytes)
	}
}

func TestRescanSkipsNestedPartialDownloads(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	md := writeModelDir(t, models, "org", "half-nested", 512)
	sub := filepath.Join(md, "processor")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "weights.safetensors.gropius-part"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := r.Rescan(models); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("org/half-nested"); !errors.Is(err, ErrNotFound) {
		t.Error("Rescan adopted a model whose nested download is incomplete")
	}
}

func TestRescanDoesNotClobberInFlightDownload(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	r.Put(Model{RepoID: "org/m", State: StateDownloading, Progress: 42})

	if err := r.Rescan(models); err != nil {
		t.Fatal(err)
	}
	m, err := r.Get("org/m")
	if err != nil {
		t.Fatalf("in-flight download was dropped by Rescan: %v", err)
	}
	if m.State != StateDownloading || m.Progress != 42 {
		t.Errorf("Rescan clobbered an in-flight download: %+v", m)
	}
}

func TestRescanDropsModelsDeletedOutsideTheApp(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	// The models root exists and holds one real model; the other entry's
	// directory has been deleted behind the app's back.
	writeModelDir(t, models, "org", "present", 128)
	r.Put(Model{RepoID: "org/present", Path: filepath.Join(models, "org", "present"), State: StateReady})
	r.Put(Model{RepoID: "org/vanished", Path: filepath.Join(models, "org", "vanished"), State: StateReady})

	if err := r.Rescan(models); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("org/vanished"); !errors.Is(err, ErrNotFound) {
		t.Error("a model whose directory no longer exists should be dropped from the index")
	}
	if _, err := r.Get("org/present"); err != nil {
		t.Errorf("the surviving model was dropped: %v", err)
	}
}

// If the models root itself is unreachable — an unmounted external volume, a
// shared directory that is not available to this account — Rescan must leave the
// index alone. Treating "root missing" as "everything was deleted" would wipe
// the registry for a transient condition.
func TestRescanLeavesIndexAloneWhenModelsRootIsMissing(t *testing.T) {
	r, dir := newTestRegistry(t)
	r.Put(Model{RepoID: "org/m", Path: "/some/where", State: StateReady})

	if err := r.Rescan(filepath.Join(dir, "does-not-exist")); err != nil {
		t.Errorf("Rescan of a missing root should be a no-op, got: %v", err)
	}
	if _, err := r.Get("org/m"); err != nil {
		t.Error("an unreachable models root must not wipe the registry")
	}
}

func TestSubscribeReceivesUpdates(t *testing.T) {
	r, _ := newTestRegistry(t)
	ch, unsub := r.Subscribe()
	defer unsub()

	if err := r.Put(Model{RepoID: "org/m", State: StateReady}); err != nil {
		t.Fatal(err)
	}

	select {
	case snap := <-ch:
		if len(snap) != 1 || snap[0].RepoID != "org/m" {
			t.Errorf("snapshot = %v", snap)
		}
	default:
		t.Fatal("expected a snapshot on the subscription channel")
	}
}

// A UI that stops reading must never wedge a download.
func TestSlowSubscriberDoesNotBlockWriters(t *testing.T) {
	r, _ := newTestRegistry(t)
	_, unsub := r.Subscribe() // never drained
	defer unsub()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			r.Put(Model{RepoID: "org/m", State: StateDownloading, Progress: float64(i)})
		}
	}()

	select {
	case <-done:
	case <-timeoutAfterSeconds(5):
		t.Fatal("writers blocked on a subscriber that never reads")
	}
}

func TestUnsubscribeIsIdempotent(t *testing.T) {
	r, _ := newTestRegistry(t)
	_, unsub := r.Subscribe()
	unsub()
	unsub() // must not panic on a double close
}

func TestConcurrentAccessIsSafe(t *testing.T) {
	r, _ := newTestRegistry(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.Put(Model{RepoID: "org/m", State: StateDownloading, Progress: float64(i)})
			r.List()
			r.Ready()
			r.Get("org/m")
		}(i)
	}
	wg.Wait()
}

func timeoutAfterSeconds(n int) <-chan time.Time {
	return time.After(time.Duration(n) * time.Second)
}
