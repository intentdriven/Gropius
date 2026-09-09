package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/capability"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// fakeHub serves a minimal model repo.
func fakeHub(t *testing.T) *httptest.Server {
	t.Helper()
	files := map[string][]byte{
		"config.json":       []byte(`{"model_type":"qwen3","max_position_embeddings":40960}`),
		"model.safetensors": make([]byte, 2048),
		"tokenizer.json":    []byte(`{}`),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/org/repo/tree/main", func(w http.ResponseWriter, r *http.Request) {
		type file struct {
			Path string `json:"path"`
			Size int64  `json:"size"`
		}
		var out []file
		for p, b := range files {
			out = append(out, file{Path: p, Size: int64(len(b))})
		}
		json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/org/repo/resolve/main/", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[len("/org/repo/resolve/main/"):]
		b, ok := files[name]
		if !ok {
			http.Error(w, "nope", http.StatusNotFound)
			return
		}
		w.Write(b)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	paths := config.NewPaths(t.TempDir())
	a, err := New(Options{Paths: paths, Config: config.Default()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

// waitFor polls until cond is true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDownloadMarksModelReadyAndWritesFiles(t *testing.T) {
	a := newTestApp(t)
	hub := fakeHub(t)
	a.Hub.BaseURL = hub.URL

	if err := a.Download("org/repo"); err != nil {
		t.Fatalf("Download: %v", err)
	}

	waitFor(t, "the model to become ready", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.Ready()
	})

	m, _ := a.Registry.Get("org/repo")
	if m.Bytes == 0 {
		t.Error("model size was not recorded")
	}
	// The files must actually be on disk where mlx-lm will look for them.
	for _, f := range []string{"config.json", "model.safetensors", "tokenizer.json"} {
		if _, err := os.Stat(filepath.Join(m.Path, f)); err != nil {
			t.Errorf("%s missing from the model directory: %v", f, err)
		}
	}
}

func TestDownloadRejectsDuplicateInFlightDownload(t *testing.T) {
	a := newTestApp(t)
	// Point at a server that never responds, so the first download stays in flight.
	stall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer stall.Close()
	a.Hub.BaseURL = stall.URL

	if err := a.Download("org/repo"); err != nil {
		t.Fatal(err)
	}
	if err := a.Download("org/repo"); err == nil {
		t.Error("a second download of the same model should be refused while the first is running")
	}
}

func TestFailedDownloadIsMarkedFailedWithReason(t *testing.T) {
	a := newTestApp(t)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer broken.Close()
	a.Hub.BaseURL = broken.URL

	if err := a.Download("org/repo"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the download to fail", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.State == registry.StateFailed
	})

	m, _ := a.Registry.Get("org/repo")
	if m.Err == "" {
		t.Error("a failed download must record why, or the user cannot act on it")
	}
}

func TestFailedRedownloadKeepsReadyModelServable(t *testing.T) {
	a := newTestApp(t)
	hub := fakeHub(t)
	a.Hub.BaseURL = hub.URL

	if err := a.Download("org/repo"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	waitFor(t, "the model to become ready", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.Ready()
	})

	// Re-download the same model against a dead hub: the attempt fails before
	// a byte moves, but the files on disk are still the intact ready model.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer broken.Close()
	a.Hub.BaseURL = broken.URL

	// The ready record lands just before the first download deregisters, so a
	// second attempt in that window is refused as already downloading; retry.
	var dlErr error
	waitFor(t, "the re-download to start", func() bool {
		dlErr = a.Download("org/repo")
		return !errors.Is(dlErr, ErrAlreadyDownloading)
	})
	if dlErr != nil {
		t.Fatalf("Download: %v", dlErr)
	}
	waitFor(t, "the failed attempt to settle", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.State != registry.StateDownloading
	})

	m, err := a.Registry.Get("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	if m.State != registry.StateReady {
		t.Errorf("state = %q, want ready: a failed re-download must not demote an intact model", m.State)
	}
	if m.Bytes == 0 {
		t.Error("restored model must keep a non-zero recorded size")
	}
}

func TestFailedRedownloadKeepsOriginalAddedAt(t *testing.T) {
	a := newTestApp(t)
	hub := fakeHub(t)
	a.Hub.BaseURL = hub.URL

	if err := a.Download("org/repo"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	waitFor(t, "the model to become ready", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.Ready()
	})
	original, err := a.Registry.Get("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	if original.AddedAt.IsZero() {
		t.Fatal("AddedAt must be set after the first download")
	}

	// Re-download against a dead hub: the attempt fails before a byte moves,
	// but the files on disk are still the intact ready model. AddedAt must
	// keep recording when the model was first added, not when it was retried.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer broken.Close()
	a.Hub.BaseURL = broken.URL

	// The ready record lands just before the first download deregisters, so a
	// second attempt in that window is refused as already downloading; retry.
	var dlErr error
	waitFor(t, "the re-download to start", func() bool {
		dlErr = a.Download("org/repo")
		return !errors.Is(dlErr, ErrAlreadyDownloading)
	})
	if dlErr != nil {
		t.Fatalf("Download: %v", dlErr)
	}
	waitFor(t, "the failed attempt to settle", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.State != registry.StateDownloading
	})

	m, err := a.Registry.Get("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	if m.State != registry.StateReady {
		t.Fatalf("state = %q, want ready", m.State)
	}
	if !m.AddedAt.Equal(original.AddedAt) {
		t.Errorf("AddedAt = %v, want unchanged %v: a retry must not reset when the model was first added", m.AddedAt, original.AddedAt)
	}
}

func TestCancelDownload(t *testing.T) {
	a := newTestApp(t)
	stall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second)
	}))
	defer stall.Close()
	a.Hub.BaseURL = stall.URL

	if err := a.Download("org/repo"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the download to register", func() bool {
		return len(a.Downloading()) == 1
	})

	if err := a.CancelDownload("org/repo"); err != nil {
		t.Fatalf("CancelDownload: %v", err)
	}
	waitFor(t, "the download to stop", func() bool {
		return len(a.Downloading()) == 0
	})

	if err := a.CancelDownload("org/repo"); err == nil {
		t.Error("cancelling a download that is not running should error")
	}
}

func TestCancelClearsOrphanedDownloadState(t *testing.T) {
	a := newTestApp(t)
	// A download recorded in the registry with no live goroutine behind it —
	// exactly what a crash or restart leaves behind.
	if err := a.Registry.Put(registry.Model{
		RepoID: "org/orphan",
		Path:   a.Paths.ModelDir("org/orphan"),
		State:  registry.StateDownloading,
	}); err != nil {
		t.Fatal(err)
	}

	if err := a.CancelDownload("org/orphan"); err != nil {
		t.Fatalf("CancelDownload on an orphan should succeed, got %v", err)
	}

	m, err := a.Registry.Get("org/orphan")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.State != registry.StateFailed {
		t.Fatalf("state = %q, want failed so the UI offers Retry/Remove", m.State)
	}
}

func TestDeleteRemovesFilesAndRegistryEntry(t *testing.T) {
	a := newTestApp(t)
	hub := fakeHub(t)
	a.Hub.BaseURL = hub.URL

	a.Download("org/repo")
	waitFor(t, "the model to become ready", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.Ready()
	})
	m, _ := a.Registry.Get("org/repo")

	if err := a.Delete("org/repo"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := a.Registry.Get("org/repo"); err == nil {
		t.Error("model still in the registry after Delete")
	}
	if _, err := os.Stat(m.Path); !os.IsNotExist(err) {
		t.Error("model files still on disk after Delete")
	}
}

// Deleting a model while it is downloading must not leave the directory behind.
// Cancelling only asks the downloader to stop; if Delete does not wait, the
// goroutine keeps writing and the "deleted" model reappears on disk.
func TestDeleteDuringDownloadLeavesNothingBehind(t *testing.T) {
	a := newTestApp(t)

	// A hub that dribbles bytes out, so the download is still running when we
	// delete it.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/models/org/repo/tree/main" {
			fmt.Fprint(w, `[{"path":"config.json","size":2},{"path":"model.safetensors","size":1048576}]`)
			return
		}
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 64; i++ {
			w.Write(make([]byte, 16384))
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(30 * time.Millisecond)
		}
	}))
	defer slow.Close()
	a.Hub.BaseURL = slow.URL

	if err := a.Download("org/repo"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the download to start", func() bool { return len(a.Downloading()) == 1 })
	time.Sleep(100 * time.Millisecond) // let some bytes land

	dir := a.Paths.ModelDir("org/repo")
	if err := a.Delete("org/repo"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Delete returned, so the downloader must already have stopped. Nothing may
	// be written after this point.
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the model directory still exists right after Delete returned: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the model directory reappeared after Delete — the download goroutine was still writing")
	}
	if len(a.Downloading()) != 0 {
		t.Error("the download is still registered as in-flight after Delete")
	}
}

func TestSetConfigPersists(t *testing.T) {
	a := newTestApp(t)

	c := a.Config()
	c.Port = 12321
	c.HFToken = "hf_token"
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	if a.Config().Port != 12321 {
		t.Error("config not updated in memory")
	}
	// The HF token must reach the hub client, or gated downloads keep failing.
	if a.Hub.Token() != "hf_token" {
		t.Errorf("Hub token = %q, want the newly-saved token", a.Hub.Token())
	}

	reloaded, _, err := config.Load(a.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Port != 12321 {
		t.Error("config was not written to disk")
	}
}

func TestSetConfigRejectsInvalid(t *testing.T) {
	a := newTestApp(t)
	c := a.Config()
	c.Port = 0
	if err := a.SetConfig(c); err == nil {
		t.Error("expected an invalid config to be rejected")
	}
}

// A restart must pick up models already on disk rather than re-downloading them.
// This is also what lets a second macOS account use a shared cache.
func TestNewAdoptsModelsAlreadyOnDisk(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)

	dir := paths.ModelDir("mlx-community/Existing-4bit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"model_type":"test"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "model.safetensors"), make([]byte, 512), 0o644)

	a, err := New(Options{Paths: paths, Config: config.Default()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	m, err := a.Registry.Get("mlx-community/Existing-4bit")
	if err != nil {
		t.Fatalf("a model already on disk was not adopted: %v", err)
	}
	if !m.Ready() {
		t.Errorf("adopted model state = %s, want ready", m.State)
	}
}

func TestDownloadRequiresRepoID(t *testing.T) {
	a := newTestApp(t)
	if err := a.Download(""); err == nil {
		t.Error("expected an error for an empty model id")
	}
}

func TestResolveOnlyReturnsReadyModels(t *testing.T) {
	a := newTestApp(t)
	src := modelSource{a}

	a.Registry.Put(registry.Model{
		RepoID: "org/half", Path: "/tmp/x", State: registry.StateDownloading,
	})
	if _, err := src.Resolve("org/half"); err == nil {
		t.Error("a still-downloading model must not be servable")
	}
	if _, err := src.Resolve("org/absent"); err == nil {
		t.Error("an unknown model must not be servable")
	}

	a.Registry.Put(registry.Model{
		RepoID: "org/ok", Path: "/models/org/ok", Bytes: 100, State: registry.StateReady,
	})
	got, err := src.Resolve("org/ok")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Path != "/models/org/ok" || got.Bytes != 100 {
		t.Errorf("Resolve = (%q, %d)", got.Path, got.Bytes)
	}
}

func TestProgressReachesTheRegistry(t *testing.T) {
	a := newTestApp(t)
	hub := fakeHub(t)
	a.Hub.BaseURL = hub.URL

	updates, unsub := a.Registry.Subscribe()
	defer unsub()

	if err := a.Download("org/repo"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case snap := <-updates:
			for _, m := range snap {
				if m.RepoID == "org/repo" && m.Ready() && m.Progress == 100 {
					return // the UI would see this
				}
			}
		case <-deadline:
			t.Fatal("the registry never reported the download as complete")
		}
	}
}

func TestHumanReadableErrorForMissingModel(t *testing.T) {
	a := newTestApp(t)
	src := modelSource{a}
	_, err := src.Resolve("org/nope")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := err.Error(); got != fmt.Sprintf("%s is not downloaded", "org/nope") {
		t.Errorf("error = %q, want a plain-English message", got)
	}
}

// In shared-cache mode registry.json is created lazily in a group-writable
// root, so another local account can plant one whose `path` names a directory
// this account owns. Delete must never hand a stored path to os.RemoveAll; the
// directory to remove is recomputed from the validated repo id.
func TestDeleteNeverRemovesPathTakenFromPlantedRegistry(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	victim := t.TempDir()
	keep := filepath.Join(victim, "keep.txt")
	if err := os.WriteFile(keep, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	planted := fmt.Sprintf(`[{"repo_id":"org/victim","path":%q,"state":"ready","bytes":1}]`, victim)
	if err := os.WriteFile(paths.State, []byte(planted), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{Paths: paths, Config: config.Default()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	if err := a.Delete("org/victim"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("Delete followed the planted path and removed the victim directory: %v", err)
	}
	if _, err := a.Registry.Get("org/victim"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("entry still present after Delete: %v", err)
	}
}

// A re-cased id names the same model (the Hub redirects case variants and
// APFS aliases the directory), so a download of it must reuse the existing
// entry rather than mint a second row over the same directory — whose later
// removal would delete the original model's weights.
func TestDownloadOfReCasedIDReusesExistingEntry(t *testing.T) {
	a := newTestApp(t)
	hub := fakeHub(t)
	a.Hub.BaseURL = hub.URL
	if err := a.Download("org/repo"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	waitFor(t, "the model to become ready", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.Ready()
	})

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer broken.Close()
	a.Hub.BaseURL = broken.URL

	// Straight through, with no retry loop around it: the first download has
	// been seen finished, and a download that has published its final state is
	// no longer registered as in flight.
	if err := a.Download("Org/Repo"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	waitFor(t, "the attempt to settle", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.State != registry.StateDownloading
	})

	got := a.Registry.List()
	if len(got) != 1 || got[0].RepoID != "org/repo" || got[0].State != registry.StateReady {
		t.Errorf("registry after re-cased download = %+v; want the one canonical ready entry", got)
	}
}

// The in-flight downloads map folds case too, so a case variant of a running
// download is refused as already downloading and is reported under the
// canonical spelling.
func TestDownloadRejectsCaseVariantOfInFlightDownload(t *testing.T) {
	a := newTestApp(t)
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer slow.Close()
	defer close(release)
	a.Hub.BaseURL = slow.URL

	if err := a.Download("org/repo"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if err := a.Download("ORG/REPO"); !errors.Is(err, ErrAlreadyDownloading) {
		t.Errorf("case variant of an in-flight download returned %v, want ErrAlreadyDownloading", err)
	}
	if ids := a.Downloading(); len(ids) != 1 || ids[0] != "org/repo" {
		t.Errorf("Downloading() = %v, want [org/repo]", ids)
	}
}

// A model must carry its context length the moment the download completes,
// not only after the next startup rescan.
func TestDownloadRecordsContextLength(t *testing.T) {
	a := newTestApp(t)
	hub := fakeHub(t)
	a.Hub.BaseURL = hub.URL

	if err := a.Download("org/repo"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	waitFor(t, "the model to become ready", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.Ready()
	})

	m, _ := a.Registry.Get("org/repo")
	if m.ContextLength != 40960 {
		t.Errorf("ContextLength = %d, want 40960", m.ContextLength)
	}
}

// Restoring a ready model after a failed re-download must not strip the
// figure the record already carried.
func TestRestoredReadyModelKeepsItsContextLength(t *testing.T) {
	a := newTestApp(t)
	hub := fakeHub(t)
	a.Hub.BaseURL = hub.URL

	if err := a.Download("org/repo"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	waitFor(t, "the first download to finish", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.Ready()
	})

	// A second attempt against a dead hub fails, and the ready model on disk
	// is restored rather than demoted.
	a.Hub.BaseURL = "http://127.0.0.1:1"
	if err := a.Download("org/repo"); err != nil {
		t.Fatalf("re-download: %v", err)
	}
	waitFor(t, "the failed attempt to settle", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.Ready() && m.Progress == 100
	})

	m, _ := a.Registry.Get("org/repo")
	if m.ContextLength != 40960 {
		t.Errorf("ContextLength = %d, want 40960 — the restored record lost the figure", m.ContextLength)
	}
}

// restoreReady deliberately restores the record the model had before the
// failed attempt — its size and its first-download time — rather than
// re-measuring a directory the attempt has been writing into. The context
// length must come from the same place: a re-download that got as far as
// writing the new revision's config.json before failing on a weight shard
// would otherwise restore a record whose size is the old revision's and whose
// context figure is the new one's.
func TestRestoredReadyModelKeepsTheRecordedContextLengthNotTheNewConfig(t *testing.T) {
	a := newTestApp(t)
	hub := fakeHub(t)
	a.Hub.BaseURL = hub.URL

	if err := a.Download("org/repo"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	waitFor(t, "the first download to finish", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.Ready()
	})
	m, _ := a.Registry.Get("org/repo")

	// A failed attempt that already replaced config.json with a revision
	// declaring a different range.
	if err := os.WriteFile(filepath.Join(m.Path, "config.json"),
		[]byte(`{"model_type":"qwen3","max_position_embeddings":131072}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a.Hub.BaseURL = "http://127.0.0.1:1"
	if err := a.Download("org/repo"); err != nil {
		t.Fatalf("re-download: %v", err)
	}
	waitFor(t, "the failed attempt to settle", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.Ready() && m.Progress == 100
	})

	restored, _ := a.Registry.Get("org/repo")
	if restored.ContextLength != 40960 {
		t.Errorf("ContextLength = %d, want 40960 — the restored record took the figure from the failed attempt's config.json", restored.ContextLength)
	}
}

// A per-model setting is keyed by the id a request resolves to. The registry
// matches an id case-insensitively and answers under one canonical spelling,
// so a key typed in another case must be rewritten to that spelling on the way
// in — otherwise the setting is stored under a key no request ever matches.
func TestSetConfigCanonicalizesPerModelKeys(t *testing.T) {
	a := newTestApp(t)
	if err := a.Registry.Put(registry.Model{
		RepoID: "org/Repo",
		Path:   filepath.Join(a.Paths.Models, "org", "Repo"),
		State:  registry.StateReady,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	c := a.Config()
	c.PerModel = map[string]config.ModelSettings{
		"ORG/repo": {MergeSystemMessages: true},
	}
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	got := a.Config().PerModel
	if len(got) != 1 {
		t.Fatalf("PerModel = %+v, want one entry", got)
	}
	if !got["org/Repo"].MergeSystemMessages {
		t.Errorf("PerModel = %+v, want the setting under the registry's spelling %q", got, "org/Repo")
	}
}

// A key that names no model is refused, and the refusal leaves the settings
// file exactly as it was — a rejected save must not half-apply.
func TestSetConfigRejectsInvalidPerModelKeyAndLeavesTheFileAlone(t *testing.T) {
	a := newTestApp(t)
	if err := a.SetConfig(a.Config()); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	before, err := os.ReadFile(a.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}

	c := a.Config()
	c.PerModel = map[string]config.ModelSettings{"../../etc": {MergeSystemMessages: true}}
	if err := a.SetConfig(c); err == nil {
		t.Fatal("expected a per-model key that is not a model id to be refused")
	}

	after, err := os.ReadFile(a.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("the refused save rewrote the settings file:\n before %s\n after  %s", before, after)
	}
	if len(a.Config().PerModel) != 0 {
		t.Errorf("the refused save reached the live config: %+v", a.Config().PerModel)
	}
}

// A per-model setting is matched against the id a request resolves to, and the
// settings file can be edited by hand or written by another build. Keys are
// therefore folded to the registry's spelling when they are read, not only
// when they are saved — otherwise a key differing only in case sits in the
// file looking effective, shows as off in the panel, and matches no request.
func TestNewCanonicalizesPerModelKeysFromDisk(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)

	dir := paths.ModelDir("mlx-community/Existing-4bit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"model_type":"test"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "model.safetensors"), make([]byte, 512), 0o644)

	cfg := config.Default()
	cfg.PerModel = map[string]config.ModelSettings{
		"MLX-Community/existing-4bit": {MergeSystemMessages: true},
	}
	a, err := New(Options{Paths: paths, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	got := a.Config().PerModel
	if !got["mlx-community/Existing-4bit"].MergeSystemMessages {
		t.Errorf("per-model settings = %+v, want the setting under the registry's spelling", got)
	}
}

// A settings file carrying a key that names no model must not wedge the panel:
// the key is echoed to the form, comes back on the next save, and every save
// of any setting would be refused. Startup drops it and says so, the way an
// unusable preload entry is dropped and logged.
func TestNewDropsAPerModelKeyThatNamesNoModel(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	cfg := config.Default()
	cfg.PerModel = map[string]config.ModelSettings{
		"../../etc":                   {MergeSystemMessages: true},
		"mlx-community/Qwen3-8B-4bit": {MergeSystemMessages: true},
	}
	a, err := New(Options{Paths: paths, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	got := a.Config().PerModel
	if _, bad := got["../../etc"]; bad {
		t.Errorf("a key that names no model survived startup: %+v", got)
	}
	if !got["mlx-community/Qwen3-8B-4bit"].MergeSystemMessages {
		t.Errorf("the usable setting beside it was dropped too: %+v", got)
	}
	// The whole point: a save now goes through instead of being refused.
	if err := a.SetConfig(a.Config()); err != nil {
		t.Errorf("saving settings after startup dropped the bad key: %v", err)
	}
}

// A settings map with more than one problem in it must report the same one
// every time. Map iteration is randomised, so two independent collisions is
// the shape that catches an unordered scan: without a stable order the refusal
// names one pair on one run and the other pair on the next, and an operator
// fixing what they were told sees a different complaint appear.
func TestSetConfigReportsAFoldedDuplicateDeterministically(t *testing.T) {
	a := newTestApp(t)
	for _, id := range []string{"org/Repo", "org/Other"} {
		if err := a.Registry.Put(registry.Model{
			RepoID: id,
			Path:   filepath.Join(a.Paths.Models, filepath.FromSlash(id)),
			State:  registry.StateReady,
		}); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	c := a.Config()
	c.PerModel = map[string]config.ModelSettings{
		"ORG/repo":  {MergeSystemMessages: true},
		"org/repo":  {},
		"ORG/other": {MergeSystemMessages: true},
		"org/other": {},
	}
	first := ""
	for i := 0; i < 50; i++ {
		err := a.SetConfig(c)
		if err == nil {
			t.Fatal("expected two keys that name the same model to be refused")
		}
		if first == "" {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("the refusal names a different model from run to run:\n %s\n %s", first, err)
		}
	}
}

// putReady records a model as downloaded and ready, at the given size, so the
// pin-fit check has something to measure.
func putReady(t *testing.T, a *App, repoID string, bytes int64) {
	t.Helper()
	if err := a.Registry.Put(registry.Model{
		RepoID: repoID, Path: a.Paths.ModelDir(repoID), Bytes: bytes,
		State: registry.StateReady, Progress: 100,
	}); err != nil {
		t.Fatalf("Put(%s): %v", repoID, err)
	}
}

// A pinned set larger than the memory budget makes the machine refuse work it
// would otherwise have served by swapping, and the operator would only find
// out at the first refused request. The save is refused instead, naming both
// figures, and nothing is written.
func TestSetConfigRefusesPinsThatDoNotFitTheMemoryBudget(t *testing.T) {
	a := newTestApp(t)
	// Far beyond any Mac's memory budget, so the check is the same on every
	// machine this test runs on.
	const each = int64(1) << 50
	putReady(t, a, "org/writer", each)
	putReady(t, a, "org/reviewer", each)

	base := a.Config()
	base.APIKey = "bh_before"
	if err := a.SetConfig(base); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	before, err := os.ReadFile(a.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}

	overshooting := base.Clone()
	overshooting.Pinned = []string{"org/writer", "org/reviewer"}
	err = a.SetConfig(overshooting)
	if err == nil {
		t.Fatal("SetConfig accepted a pinned set larger than the whole memory budget")
	}
	sum := runtime.HumanBytes(2 * capability.LoadCost(each))
	budget := runtime.HumanBytes(a.Pool.MemoryBudget())
	if !strings.Contains(err.Error(), sum) || !strings.Contains(err.Error(), budget) {
		t.Errorf("error = %q, want it to give the pinned sum %s and the budget %s", err, sum, budget)
	}

	after, err := os.ReadFile(a.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a refused save rewrote config.json")
	}
	if got := a.Config().Pinned; len(got) != 0 {
		t.Errorf("the refused pins reached the live configuration: %v", got)
	}
	if got := a.Pool.Pinned(); len(got) != 0 {
		t.Errorf("the refused pins reached the pool: %v", got)
	}
}

// A pinned set that fits is saved, and one that only fits because a model is
// not downloaded yet is accepted: it cannot be sized, and it protects nothing
// until something loads it.
func TestSetConfigAcceptsPinsThatFitAndOnesNotYetDownloaded(t *testing.T) {
	a := newTestApp(t)
	putReady(t, a, "org/small", 1<<20)

	c := a.Config()
	c.Pinned = []string{"org/small", "org/not-downloaded"}
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if got := a.Config().Pinned; !reflect.DeepEqual(got, c.Pinned) {
		t.Errorf("Pinned = %v, want %v", got, c.Pinned)
	}
}

// The registry matches an id case-insensitively but answers under one
// spelling, and that spelling is what the operator sees everywhere else. A pin
// typed in another case is folded onto it on the way in, where the operator is
// present; a pin for a model this machine does not have is kept as typed.
func TestSetConfigCanonicalizesPinnedModelIDs(t *testing.T) {
	a := newTestApp(t)
	putReady(t, a, "org/Writer", 1<<20)

	c := a.Config()
	c.Pinned = []string{"ORG/writer", "org/not-downloaded"}
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	want := []string{"org/Writer", "org/not-downloaded"}
	if got := a.Config().Pinned; !reflect.DeepEqual(got, want) {
		t.Errorf("Pinned = %v, want %v", got, want)
	}
	if got := a.Pool.Pinned(); !reflect.DeepEqual(got, want) {
		t.Errorf("the pool holds %v, want %v", got, want)
	}
}

// Pins apply the moment Settings is saved: the pool is told, so a model
// already in memory is protected from the next eviction without a restart.
func TestSetConfigAppliesPinsToThePoolWithoutARestart(t *testing.T) {
	a := newTestApp(t)
	putReady(t, a, "org/keeper", 1<<20)

	c := a.Config()
	c.Pinned = []string{"org/keeper"}
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if got := a.Pool.Pinned(); !reflect.DeepEqual(got, []string{"org/keeper"}) {
		t.Errorf("the pool holds %v, want the model just pinned", got)
	}

	// And clearing the list unprotects it, again without a restart.
	c.Pinned = nil
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if got := a.Pool.Pinned(); len(got) != 0 {
		t.Errorf("the pool still holds %v after the pins were cleared", got)
	}
}

// A pin only bites if the pool knows about it at startup too, not only after
// the operator saves Settings again.
func TestPinsFromTheSettingsFileReachThePoolAtStartup(t *testing.T) {
	cfg := config.Default()
	cfg.Pinned = []string{"org/keeper"}
	a, err := New(Options{Paths: config.NewPaths(t.TempDir()), Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	if got := a.Pool.Pinned(); !reflect.DeepEqual(got, []string{"org/keeper"}) {
		t.Errorf("the pool holds %v at startup, want the pinned model from the settings file", got)
	}
}

// A pin read from the settings file has not been through SetConfig's checks:
// the file can be hand-edited, restored from a backup, or written by another
// build. Folded onto the registry's spelling here, or the panel draws an
// unticked box for a model that is in fact protected — and ticking it posts
// both spellings, which Validate refuses, wedging every settings change there
// is, the API key included.
func TestPinsFromTheSettingsFileAreFoldedOntoTheRegistrySpelling(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	seedReadyModel(t, paths, "org/Writer", 1<<20)

	cfg := config.Default()
	cfg.Pinned = []string{"ORG/writer", "org/not-downloaded"}
	a, err := New(Options{Paths: paths, Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	want := []string{"org/Writer", "org/not-downloaded"}
	if got := a.Config().Pinned; !reflect.DeepEqual(got, want) {
		t.Errorf("Pinned = %v after startup, want %v", got, want)
	}
	if got := a.Pool.Pinned(); !reflect.DeepEqual(got, want) {
		t.Errorf("the pool holds %v, want %v", got, want)
	}
	// The whole point: what loaded is a configuration the next save accepts.
	if err := a.SetConfig(a.Config()); err != nil {
		t.Errorf("the next settings save was refused: %v", err)
	}
}

// Two spellings of one model in the settings file are one model. The later one
// is dropped and named, rather than refused, because refusing at startup is
// the wedge above by another route.
func TestDuplicatePinsFromTheSettingsFileAreDropped(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	seedReadyModel(t, paths, "org/writer", 1<<20)

	cfg := config.Default()
	cfg.Pinned = []string{"org/writer", "ORG/Writer"}
	a, err := New(Options{Paths: paths, Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	if got := a.Config().Pinned; !reflect.DeepEqual(got, []string{"org/writer"}) {
		t.Errorf("Pinned = %v, want one spelling of the one model", got)
	}
}

// A model that is still downloading has no size on disk yet, but it declares
// one from the first byte. Charging it zero is how a pinned pair that cannot
// possibly fit gets accepted: tick both boxes while they download and the
// machine is over budget the moment they land, permanently, with every
// unpinned request refused and no hint of why.
func TestSetConfigChargesAModelThatIsStillDownloading(t *testing.T) {
	a := newTestApp(t)
	if err := a.Registry.Put(registry.Model{
		RepoID: "org/incoming", Path: a.Paths.ModelDir("org/incoming"),
		SizeBytes: 1 << 50, State: registry.StateDownloading, Progress: 3,
	}); err != nil {
		t.Fatal(err)
	}

	c := a.Config()
	c.Pinned = []string{"org/incoming"}
	err := a.SetConfig(c)
	if err == nil {
		t.Fatal("SetConfig accepted a pin on a download far larger than the whole budget")
	}
	if want := runtime.HumanBytes(capability.LoadCost(1 << 50)); !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to charge the declared download size %s", err, want)
	}
}

// The fit check cannot run when the settings file is read — a hand-edited file
// can pin anything — so an over-budget set reaches the pool. Say so in the log
// at startup, where the operator can act on it, rather than leaving it to be
// discovered as a refusal of every unpinned request.
func TestStartupWarnsWhenThePinnedSetCannotFit(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	seedReadyModel(t, paths, "org/enormous", 1<<50)

	var logged bytes.Buffer
	cfg := config.Default()
	cfg.Pinned = []string{"org/enormous"}
	a, err := New(Options{
		Paths:  paths,
		Config: cfg,
		Log:    slog.New(slog.NewTextHandler(&logged, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	if !strings.Contains(logged.String(), "pinned") {
		t.Errorf("startup logged %q, want a warning that the pinned set does not fit", logged.String())
	}
	// Warned, not refused: refusing here would take the whole install down
	// over a setting, and the pins still protect what they name.
	if got := a.Pool.Pinned(); len(got) != 1 {
		t.Errorf("the pool holds %v, want the pin applied anyway", got)
	}
}

// seedReadyModel writes a registry file recording one ready model, as a
// previous run would have left it, so New reads a registry that already knows
// the model a pin names.
func seedReadyModel(t *testing.T, paths config.Paths, repoID string, size int64) {
	t.Helper()
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	// The directory has to exist or the startup rescan reads its absence as a
	// model deleted outside the app and drops the record; it is left empty so
	// the rescan leaves the recorded size alone rather than re-deriving it.
	if err := os.MkdirAll(paths.ModelDir(repoID), 0o755); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Open(paths.State)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Put(registry.Model{
		RepoID: repoID, Path: paths.ModelDir(repoID), Bytes: size,
		State: registry.StateReady, Progress: 100,
	}); err != nil {
		t.Fatal(err)
	}
}

// A settings file carried from a larger Mac names a pinned set this one cannot
// hold. The operator did not choose that here, and must not have to notice it
// before they can change the API key: the fit check judges what this save
// makes worse, not what it inherited. Refusing the whole save over a setting
// the form did not touch is the wedge this branch already closed once.
func TestAnInheritedOverBudgetPinnedSetDoesNotBlockAnUnrelatedSave(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	seedReadyModel(t, paths, "org/enormous", 1<<50)

	cfg := config.Default()
	cfg.Pinned = []string{"org/enormous"}
	a, err := New(Options{Paths: paths, Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	c := a.Config()
	c.APIKey = "bh_secret"
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("an unrelated save was refused over a pinned set the operator did not touch: %v", err)
	}
	if got := a.Config().APIKey; got != "bh_secret" {
		t.Errorf("APIKey = %q, want the save to have gone through", got)
	}

	// But making it worse is still refused: this is the moment the operator is
	// choosing it.
	seedReadyModel(t, paths, "org/second", 1<<50)
	if err := a.Registry.Rescan(paths.Models); err != nil {
		t.Fatal(err)
	}
	c = a.Config()
	c.Pinned = append(append([]string(nil), c.Pinned...), "org/second")
	if err := a.SetConfig(c); err == nil {
		t.Error("adding a pin to a set that already does not fit was accepted")
	}

	// And so is shedding one, which leaves the set no worse than it was.
	c = a.Config()
	c.Pinned = nil
	if err := a.SetConfig(c); err != nil {
		t.Errorf("unpinning was refused: %v", err)
	}
}

// A failed download can never be loaded — modelSource.Resolve refuses anything
// that is not ready — so it can never occupy a byte of the budget. Its declared
// size survives the failure in the registry, though, so charging by size alone
// refuses a save over memory a pin cannot take.
func TestAPinOnAFailedDownloadIsChargedNothing(t *testing.T) {
	a := newTestApp(t)
	if err := a.Registry.Put(registry.Model{
		RepoID: "org/broken", Path: a.Paths.ModelDir("org/broken"),
		SizeBytes: 1 << 50, State: registry.StateFailed, Err: "no usable weights",
	}); err != nil {
		t.Fatal(err)
	}

	c := a.Config()
	c.Pinned = []string{"org/broken"}
	if err := a.SetConfig(c); err != nil {
		t.Errorf("a pin on a model that can never load was charged the memory it never takes: %v", err)
	}
}

// A model the registry holds with no size at all cannot be measured against
// the budget, so pinning it would make the fit check a promise it cannot keep.
// Say so at the moment the pin is added rather than accept it silently.
func TestAPinOnAModelOfUnknownSizeIsRefusedWhenItIsAdded(t *testing.T) {
	a := newTestApp(t)
	if err := a.Registry.Put(registry.Model{
		RepoID: "org/sizeless", Path: a.Paths.ModelDir("org/sizeless"),
		State: registry.StateReady, Progress: 100,
	}); err != nil {
		t.Fatal(err)
	}

	c := a.Config()
	c.Pinned = []string{"org/sizeless"}
	err := a.SetConfig(c)
	if err == nil {
		t.Fatal("a pin on a model of unknown size was accepted")
	}
	if !strings.Contains(err.Error(), "org/sizeless") {
		t.Errorf("error = %q, want it to name the model it cannot measure", err)
	}
}

// The gate is "no greater than the budget", so a set that exactly fills it is
// allowed and one byte more is not. Without this the boundary could move a
// byte in either direction and every other test would stay green.
func TestThePinnedFitCheckIsInclusiveOfTheBudget(t *testing.T) {
	a := newTestApp(t)
	budget := a.Pool.MemoryBudget()
	// LoadCost is size + size/5, so a size of 5k is charged exactly 6k: pick
	// the largest such size that fits, and the next one that cannot.
	fits := 5 * (budget / 6)
	over := fits + 6

	putReady(t, a, "org/exact", fits)
	c := a.Config()
	c.Pinned = []string{"org/exact"}
	if err := a.SetConfig(c); err != nil {
		t.Errorf("a pinned model charged %s against a budget of %s was refused: %v",
			runtime.HumanBytes(capability.LoadCost(fits)), runtime.HumanBytes(budget), err)
	}

	putReady(t, a, "org/exact", over)
	c = a.Config()
	c.Pinned = nil
	if err := a.SetConfig(c); err != nil {
		t.Fatal(err)
	}
	c.Pinned = []string{"org/exact"}
	if err := a.SetConfig(c); err == nil {
		t.Errorf("a pinned model charged %s against a budget of %s was accepted",
			runtime.HumanBytes(capability.LoadCost(over)), runtime.HumanBytes(budget))
	}
}

// SetConfig writes the file, swaps the live configuration and tells the pool as
// three steps. Overlapping saves interleaving across those steps leave the pool
// enforcing a pin that the settings, the panel and /v1/models all say does not
// exist — a divergence between what is enforced and what every surface reports,
// lasting until the next save or a restart.
func TestOverlappingSavesLeaveThePoolAgreeingWithTheSettings(t *testing.T) {
	a := newTestApp(t)
	putReady(t, a, "org/one", 1<<20)
	putReady(t, a, "org/two", 1<<20)

	var wg sync.WaitGroup
	for i := range 24 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := a.Config()
			if i%2 == 0 {
				c.Pinned = []string{"org/one"}
			} else {
				c.Pinned = []string{"org/two"}
			}
			_ = a.SetConfig(c)
		}(i)
	}
	wg.Wait()

	if got, want := a.Pool.Pinned(), a.Config().Pinned; !reflect.DeepEqual(got, want) {
		t.Errorf("the pool enforces %v while the settings say %v", got, want)
	}
	stored, _, err := config.Load(a.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := stored.Pinned, a.Config().Pinned; !reflect.DeepEqual(got, want) {
		t.Errorf("config.json holds %v while the running settings say %v", got, want)
	}
}

// A pin can be set before its model is downloaded, and is kept as it was typed
// because there is nothing yet to fold it onto. When the model arrives it has a
// spelling of its own, and every surface that joins on the id — the panel's
// boxes, its card marker — joins on that one. Fold the pin then, or the panel
// shows the model unpinned while the pool protects it.
func TestAPinTakesTheRegistrySpellingWhenItsModelArrives(t *testing.T) {
	a := newTestApp(t)
	hub := fakeHub(t)
	a.Hub.BaseURL = hub.URL

	c := a.Config()
	c.Pinned = []string{"ORG/Repo"}
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	if err := a.Download("org/repo"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	waitFor(t, "the download to finish", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.State == registry.StateReady
	})
	waitFor(t, "the pin to take the registry's spelling", func() bool {
		return reflect.DeepEqual(a.Config().Pinned, []string{"org/repo"})
	})
	if got := a.Pool.Pinned(); !reflect.DeepEqual(got, []string{"org/repo"}) {
		t.Errorf("the pool holds %v, want the registry's spelling", got)
	}
}

// Deleting a pinned model leaves its pin, charged nothing; downloading the
// model again brings the charge back, and the set that fitted a moment ago may
// not any more. No save happens on that path, so nothing refuses it — say so
// where the operator can see it.
func TestAPinnedSetThatStopsFittingIsReportedToTheOperator(t *testing.T) {
	a := newTestApp(t)
	putReady(t, a, "org/small", 1<<20)

	c := a.Config()
	c.Pinned = []string{"org/small", "org/enormous"}
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if w := a.PinnedFitWarning(); w != "" {
		t.Fatalf("PinnedFitWarning = %q while the set fits", w)
	}

	// The second pin's model arrives, and it is far too large.
	putReady(t, a, "org/enormous", 1<<50)
	w := a.PinnedFitWarning()
	if w == "" {
		t.Fatal("PinnedFitWarning is empty although the pinned set no longer fits")
	}
	if !strings.Contains(w, runtime.HumanBytes(a.Pool.MemoryBudget())) {
		t.Errorf("PinnedFitWarning = %q, want it to give the budget", w)
	}
}
