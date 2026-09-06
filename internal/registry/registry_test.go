package registry

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
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

	if err := r.Remove("org/m", modelDir); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := r.Get("org/m"); !errors.Is(err, ErrNotFound) {
		t.Error("model still in the index after Remove")
	}
	if _, err := os.Stat(modelDir); !os.IsNotExist(err) {
		t.Error("model files still on disk after Remove — the disk space was not reclaimed")
	}
}

// Remove already deleted the in-memory entry by the time saveLocked can fail
// (a disk-full, EPERM, or — in shared-cache mode — sticky-bit-blocked write),
// so subscribers must still hear about the change; every other mutator in this
// file broadcasts unconditionally for the same reason.
func TestRemoveBroadcastsEvenWhenSaveFails(t *testing.T) {
	regParent := t.TempDir()
	regPath := filepath.Join(regParent, "sub", "registry.json")
	r, err := Open(regPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ch, unsub := r.Subscribe()
	defer unsub()

	if err := r.Put(Model{RepoID: "org/m", Path: t.TempDir(), State: StateReady}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	<-ch // drain the Put broadcast

	// Break saveLocked deterministically, without relying on permissions (the
	// test may run as root): replace the registry's directory with a regular
	// file, so os.MkdirAll fails with ENOTDIR instead of writing anything.
	regDir := filepath.Dir(regPath)
	if err := os.RemoveAll(regDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regDir, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := r.Remove("org/m", ""); err == nil {
		t.Fatal("Remove should have failed to persist the registry")
	}

	select {
	case snap := <-ch:
		if len(snap) != 0 {
			t.Errorf("snapshot after Remove = %v, want empty", snap)
		}
	default:
		t.Fatal("Remove must broadcast the in-memory deletion even when saveLocked fails")
	}
}

// writeModelDir creates a directory that looks like a complete model.
func writeModelDir(t *testing.T, root, org, name string, weightBytes int) string {
	t.Helper()
	dir := filepath.Join(root, org, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"model_type":"test"}`), 0o644); err != nil {
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
	// config.json (21 bytes) + weights (1024)
	if m.Bytes != 1045 {
		t.Errorf("Bytes = %d, want 1045", m.Bytes)
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
	// config.json (21) + weights (1024) + nested tokenizer.json (100)
	if m.Bytes != 1145 {
		t.Errorf("Bytes = %d, want 1145 (nested files must be counted)", m.Bytes)
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

// A stat failure that is not "the directory does not exist" — a permission
// hiccup, a transient I/O error — is not proof a model was deleted, and must
// not be treated as one: only a confirmed fs.ErrNotExist means "gone".
func TestRescanKeepsModelWhenStatFailsForReasonOtherThanNotExist(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}
	// A regular file standing in for a directory component makes any os.Stat of
	// a path beneath it fail with ENOTDIR, not ENOENT — a stat failure that is
	// unambiguously not "the directory was deleted".
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	unstatable := filepath.Join(blocker, "org", "model")
	r.Put(Model{RepoID: "org/model", Path: unstatable, State: StateReady})

	if err := r.Rescan(models); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("org/model"); err != nil {
		t.Errorf("a model whose directory could not be statted for a reason other than ENOENT must not be dropped: %v", err)
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

// writeShardedModelDir creates a model directory whose
// model.safetensors.index.json names two weight shards, with only the given
// shards actually present — the on-disk shape a partially downloaded or
// partially failed sharded model leaves behind.
func writeShardedModelDir(t *testing.T, root, org, name string, shards ...string) string {
	t.Helper()
	dir := filepath.Join(root, org, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"config.json": `{"model_type":"test"}`,
		"model.safetensors.index.json": `{"weight_map":{` +
			`"a":"model-00001-of-00002.safetensors",` +
			`"b":"model-00002-of-00002.safetensors"}}`,
	}
	for _, s := range shards {
		files[s] = "weights"
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A verification failure removes the failed file's .part, so a sharded model
// missing a weight shard can sit on disk with no .part marker at all. Rescan
// must not promote its failed record to ready: the shard manifest proves the
// directory incomplete, and the recorded error is the user's only diagnostic.
func TestRescanDoesNotPromoteFailedShardIncompleteModel(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	md := writeShardedModelDir(t, models, "org", "half", "model-00001-of-00002.safetensors")
	r.Put(Model{
		RepoID: "org/half",
		Path:   md,
		State:  StateFailed,
		Err:    "download model-00002-of-00002.safetensors: content hash mismatch",
	})

	if err := r.Rescan(models); err != nil {
		t.Fatal(err)
	}
	m, err := r.Get("org/half")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.State != StateFailed {
		t.Errorf("Rescan promoted a shard-incomplete failed model to %q", m.State)
	}
	if m.Err == "" {
		t.Error("Rescan wiped the failure diagnostic from a still-broken model")
	}
}

func TestRescanDoesNotAdoptShardIncompleteDir(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	writeShardedModelDir(t, models, "org", "half", "model-00001-of-00002.safetensors")

	if err := r.Rescan(models); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("org/half"); !errors.Is(err, ErrNotFound) {
		t.Error("Rescan adopted a directory missing a weight shard named by its manifest")
	}
}

// A byte-complete download can still fail structural validation (an HTML error
// page saved as config.json, say); the app records it failed with zero .part
// files on disk. Rescan must not overrule that verdict.
func TestRescanDoesNotPromoteJunkConfigModel(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	md := filepath.Join(models, "org", "junk")
	if err := os.MkdirAll(md, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(md, "config.json"), []byte(`<!DOCTYPE html><html>404</html>`), 0o644)
	os.WriteFile(filepath.Join(md, "model.safetensors"), []byte("weights"), 0o644)
	r.Put(Model{
		RepoID: "org/junk",
		Path:   md,
		State:  StateFailed,
		Err:    "downloaded but not a usable MLX model",
	})

	if err := r.Rescan(models); err != nil {
		t.Fatal(err)
	}
	m, err := r.Get("org/junk")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.State != StateFailed || m.Err == "" {
		t.Errorf("Rescan promoted a structurally invalid model: state=%q err=%q", m.State, m.Err)
	}
}

// The flip side, pinning the shared-cache promise: once the directory really is
// complete (another account finished the download), a failed record must be
// promoted to ready.
func TestRescanPromotesFailedModelOnceDirComplete(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	md := writeShardedModelDir(t, models, "org", "whole",
		"model-00001-of-00002.safetensors", "model-00002-of-00002.safetensors")
	r.Put(Model{RepoID: "org/whole", Path: md, State: StateFailed, Err: "content hash mismatch"})

	if err := r.Rescan(models); err != nil {
		t.Fatal(err)
	}
	m, err := r.Get("org/whole")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.State != StateReady {
		t.Errorf("a genuinely complete directory should promote the failed record, got %q", m.State)
	}
	if m.Err != "" {
		t.Errorf("promotion should clear the stale error, got %q", m.Err)
	}
}

// In the shared cache another account can plant a FIFO (or a symlink to one)
// under a manifest name. Opening it for the completeness check would block
// until a writer appears — never, for a hostile plant — wedging the startup
// rescan for every account. Rescan must skip it and finish.
func TestRescanDoesNotBlockOnFIFOManifest(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	md := filepath.Join(models, "org", "hostile")
	if err := os.MkdirAll(md, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(md, "config.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(md, "model.safetensors"), []byte("weights"), 0o644)

	done := make(chan error, 1)
	go func() { done <- r.Rescan(models) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Rescan: %v", err)
		}
	case <-timeoutAfterSeconds(5):
		t.Fatal("Rescan blocked on a FIFO planted as config.json")
	}
	if _, err := r.Get("org/hostile"); !errors.Is(err, ErrNotFound) {
		t.Error("a directory whose config.json is not a regular file must not be adopted")
	}
}

// A symlinked manifest is never something the downloader wrote; following it
// would probe files outside the model directory with this account's
// privileges. The completeness check must refuse it.
func TestRescanDoesNotFollowSymlinkedManifest(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	md := filepath.Join(models, "org", "linked")
	if err := os.MkdirAll(md, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "outside-config.json")
	os.WriteFile(outside, []byte(`{"model_type":"test"}`), 0o644)
	if err := os.Symlink(outside, filepath.Join(md, "config.json")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(md, "model.safetensors"), []byte("weights"), 0o644)

	if err := r.Rescan(models); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("org/linked"); !errors.Is(err, ErrNotFound) {
		t.Error("a directory whose config.json is a symlink must not be adopted")
	}
}

// A symlinked weight file is never something the downloader wrote. Adopting
// it would serve weights from outside the model directory while the walk's
// lstat-based size charges the memory budget the symlink's own few dozen
// bytes instead of the gigabytes behind it, so the pool would admit the model
// without ever evicting to make room. Rescan must skip the directory, the
// same fail-safe treatment a symlinked model directory already gets.
func TestRescanDoesNotAdoptSymlinkedWeights(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	md := filepath.Join(models, "org", "linked-weights")
	if err := os.MkdirAll(md, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(md, "config.json"), []byte(`{"model_type":"test"}`), 0o644)
	outside := filepath.Join(t.TempDir(), "real.safetensors")
	if err := os.WriteFile(outside, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(md, "model.safetensors")); err != nil {
		t.Fatal(err)
	}

	if err := r.Rescan(models); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("org/linked-weights"); !errors.Is(err, ErrNotFound) {
		t.Error("Rescan adopted a model whose weights are a symlink")
	}
}

// A shard named by the index must be a regular file, not a symlink to one: a
// following stat would attest the link while the downloader only ever writes
// regular files, and a shard name without the .safetensors suffix would slip
// past the top-level weights check entirely.
func TestRescanDoesNotAdoptSymlinkedShard(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	md := filepath.Join(models, "org", "linked-shard")
	if err := os.MkdirAll(md, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"config.json": `{"model_type":"test"}`,
		"model.safetensors.index.json": `{"weight_map":{` +
			`"a":"model-00001-of-00002.safetensors",` +
			`"b":"shard-00002.bin"}}`,
		"model-00001-of-00002.safetensors": "weights",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(md, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "shard-00002.bin")
	if err := os.WriteFile(outside, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(md, "shard-00002.bin")); err != nil {
		t.Fatal(err)
	}

	if err := r.Rescan(models); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("org/linked-shard"); !errors.Is(err, ErrNotFound) {
		t.Error("Rescan adopted a model whose index names a symlinked shard")
	}
}

func timeoutAfterSeconds(n int) <-chan time.Time {
	return time.After(time.Duration(n) * time.Second)
}

// registry.json sits in the data root, which in shared mode is group-writable
// and where the file is created lazily; another local account can plant a
// FIFO under its name and a blocking open would wedge startup after the port
// is claimed. Open must not block, and must not silently adopt the plant.
func TestOpenDoesNotBlockOnFIFOState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			syscall.Close(fd)
		}
	})
	done := make(chan error, 1)
	go func() { _, err := Open(path); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Open of a FIFO must fail loudly rather than pretend the index is empty")
		}
	case <-timeoutAfterSeconds(5):
		t.Fatal("Open blocked on a FIFO planted as registry.json")
	}
}

// A symlinked registry.json is never something saveLocked wrote; following it
// would load an index from outside the root — whose `path` fields later feed
// os.RemoveAll — with this account's privileges.
func TestOpenDoesNotFollowSymlinkedState(t *testing.T) {
	target := filepath.Join(t.TempDir(), "planted.json")
	if err := os.WriteFile(target, []byte(`[{"repo_id":"org/planted","path":"/nonexistent","state":"ready"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err == nil {
		if _, gerr := r.Get("org/planted"); gerr == nil {
			t.Fatal("Open followed a symlinked registry.json and loaded its entries")
		}
		t.Fatal("Open of a symlinked registry.json must fail loudly")
	}
}

// Open must apply the same ValidRepoID gate Rescan does: every write path
// gates on it, so an invalid id in registry.json was planted (a shared root)
// or hand-edited, and loading it would advertise an entry Delete refuses.
func TestOpenSkipsEntriesWithInvalidRepoIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	body := `[{"repo_id":"","state":"ready"},{"repo_id":"../../../../etc","path":"/","state":"ready"},{"repo_id":"noslash","state":"ready"},{"repo_id":"org/m","state":"ready"}]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.List(); len(got) != 1 || got[0].RepoID != "org/m" {
		t.Errorf("List = %v, want only org/m", got)
	}
	if got := r.Ready(); len(got) != 1 {
		t.Errorf("Ready = %v, want only org/m", got)
	}
}

// Repo ids are case-insensitive on the Hub and map onto case-insensitive APFS
// paths, so two spellings are one model: the index must fold them onto one
// entry that keeps the first-seen spelling.
func TestGetAndPutFoldRepoIDCase(t *testing.T) {
	r, _ := newTestRegistry(t)
	if err := r.Put(Model{RepoID: "mlx-community/Qwen3-8B-4bit", State: StateReady}); err != nil {
		t.Fatal(err)
	}
	m, err := r.Get("MLX-Community/qwen3-8b-4bit")
	if err != nil || m.RepoID != "mlx-community/Qwen3-8B-4bit" {
		t.Fatalf("Get by case variant = %+v, %v; want the canonical entry", m, err)
	}
	if err := r.Put(Model{RepoID: "MLX-Community/Qwen3-8B-4bit", State: StateDownloading}); err != nil {
		t.Fatal(err)
	}
	got := r.List()
	if len(got) != 1 || got[0].RepoID != "mlx-community/Qwen3-8B-4bit" || got[0].State != StateDownloading {
		t.Errorf("List after re-cased Put = %+v; want one entry, first-seen spelling, state updated", got)
	}
}

func TestOpenCollapsesCaseVariantEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	body := `[{"repo_id":"Org/repo","state":"failed"},{"repo_id":"org/repo","state":"ready"}]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := r.List()
	if len(got) != 1 || got[0].RepoID != "org/repo" || got[0].State != StateReady {
		t.Errorf("List = %+v; want the single ready entry", got)
	}
}

func TestRemoveByCaseVariantRemovesThatOneEntry(t *testing.T) {
	r, dir := newTestRegistry(t)
	modelDir := filepath.Join(dir, "models", "org", "repo")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(Model{RepoID: "org/repo", Path: modelDir, State: StateReady}); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove("ORG/repo", modelDir); err != nil {
		t.Fatalf("Remove by case variant: %v", err)
	}
	if got := r.List(); len(got) != 0 {
		t.Errorf("List after Remove = %+v, want empty", got)
	}
	if _, err := os.Stat(modelDir); !os.IsNotExist(err) {
		t.Error("model directory still present after Remove")
	}
}

// Remove recomputes the directory from the id, but os.RemoveAll by path still
// follows a symlinked ancestor: an org symlink planted in the shared models
// tree plus a planted "ready" entry would make the victim's Remove click
// delete an arbitrary subdirectory of the link's target. The removal must go
// through the models root and refuse a non-directory org.
func TestRemoveRefusesSymlinkedOrgDir(t *testing.T) {
	r, dir := newTestRegistry(t)
	models := filepath.Join(dir, "models")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := t.TempDir()
	keep := filepath.Join(victim, "name", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(keep), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(models, "org")); err != nil {
		t.Fatal(err)
	}
	modelDir := filepath.Join(models, "org", "name")
	if err := r.Put(Model{RepoID: "org/name", Path: modelDir, State: StateReady}); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove("org/name", modelDir); err == nil {
		t.Error("Remove followed a symlinked org directory")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("Remove deleted through the planted org symlink: %v", err)
	}
}
