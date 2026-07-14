package hub

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// weights makes a deterministic blob of n bytes.
func weights(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

func standardRepo() map[string][]byte {
	return map[string][]byte{
		"config.json":       []byte(`{"model_type":"qwen3"}`),
		"model.safetensors": weights(4096),
		"tokenizer.json":    []byte(`{"version":"1.0"}`),
	}
}

func TestDownloadWritesAllFiles(t *testing.T) {
	fh := newFakeHub(standardRepo())
	srv := fh.server(t)
	dest := t.TempDir()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	err := c.Download(context.Background(), DownloadRequest{
		RepoID: "org/repo", Dest: dest,
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	for name, want := range fh.files {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: content mismatch (%d bytes, want %d)", name, len(got), len(want))
		}
	}
}

// A partial download must never be left under the final filename, or the next
// run would treat a truncated file as complete.
func TestDownloadLeavesNoPartFilesOnSuccess(t *testing.T) {
	fh := newFakeHub(standardRepo())
	srv := fh.server(t)
	dest := t.TempDir()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if err := c.Download(context.Background(), DownloadRequest{RepoID: "org/repo", Dest: dest}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), partSuffix) {
			t.Errorf("leftover part file: %s", e.Name())
		}
	}
}

func TestDownloadResumesFromPartialFile(t *testing.T) {
	repo := standardRepo()
	fh := newFakeHub(repo)
	srv := fh.server(t)
	dest := t.TempDir()

	// Simulate an interrupted run: the first 1000 bytes are already on disk.
	full := repo["model.safetensors"]
	part := filepath.Join(dest, "model.safetensors"+partSuffix)
	if err := os.WriteFile(part, full[:1000], 0o644); err != nil {
		t.Fatal(err)
	}

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if err := c.Download(context.Background(), DownloadRequest{RepoID: "org/repo", Dest: dest}); err != nil {
		t.Fatalf("Download: %v", err)
	}

	// It must have asked to resume, not refetched from zero.
	if got := fh.rangeFor("model.safetensors"); got != "bytes=1000-" {
		t.Errorf("Range header = %q, want %q (download did not resume)", got, "bytes=1000-")
	}
	got, err := os.ReadFile(filepath.Join(dest, "model.safetensors"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, full) {
		t.Errorf("resumed file is corrupt: got %d bytes, want %d", len(got), len(full))
	}
}

// If the server ignores our Range and sends the whole body (some CDNs do), the
// result must still be correct rather than the body appended onto the partial.
func TestDownloadHandlesServerIgnoringRange(t *testing.T) {
	repo := standardRepo()
	fh := newFakeHub(repo)
	fh.ignoreRange = true
	srv := fh.server(t)
	dest := t.TempDir()

	full := repo["model.safetensors"]
	part := filepath.Join(dest, "model.safetensors"+partSuffix)
	if err := os.WriteFile(part, full[:1000], 0o644); err != nil {
		t.Fatal(err)
	}

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if err := c.Download(context.Background(), DownloadRequest{RepoID: "org/repo", Dest: dest}); err != nil {
		t.Fatalf("Download: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "model.safetensors"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, full) {
		t.Errorf("file corrupt when server ignored Range: got %d bytes, want %d", len(got), len(full))
	}
}

func TestDownloadSkipsAlreadyCompleteFiles(t *testing.T) {
	repo := standardRepo()
	fh := newFakeHub(repo)
	srv := fh.server(t)
	dest := t.TempDir()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if err := c.Download(context.Background(), DownloadRequest{RepoID: "org/repo", Dest: dest}); err != nil {
		t.Fatal(err)
	}
	before := fh.hitsFor("model.safetensors")

	// Second run over a complete directory must not refetch anything.
	if err := c.Download(context.Background(), DownloadRequest{RepoID: "org/repo", Dest: dest}); err != nil {
		t.Fatal(err)
	}
	if after := fh.hitsFor("model.safetensors"); after != before {
		t.Errorf("weights refetched on second run (hits %d -> %d); complete files must be skipped", before, after)
	}
}

// A file whose size does not match the manifest is corrupt and must be refetched.
func TestDownloadRefetchesTruncatedFile(t *testing.T) {
	repo := standardRepo()
	fh := newFakeHub(repo)
	srv := fh.server(t)
	dest := t.TempDir()

	// A truncated file sitting under the *final* name (e.g. from an older,
	// buggier writer, or a half-copied directory).
	if err := os.WriteFile(filepath.Join(dest, "model.safetensors"), []byte("truncated"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if err := c.Download(context.Background(), DownloadRequest{RepoID: "org/repo", Dest: dest}); err != nil {
		t.Fatalf("Download: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "model.safetensors"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, repo["model.safetensors"]) {
		t.Error("truncated file was not repaired")
	}
}

func TestDownloadReportsProgressReachingOneHundredPercent(t *testing.T) {
	fh := newFakeHub(standardRepo())
	srv := fh.server(t)
	dest := t.TempDir()

	var mu sync.Mutex
	var last Progress
	var sawRepo string
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	err := c.Download(context.Background(), DownloadRequest{
		RepoID: "org/repo", Dest: dest,
		OnProgress: func(p Progress) {
			mu.Lock()
			defer mu.Unlock()
			sawRepo = p.RepoID
			if p.Completed > last.Completed {
				last = p
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if sawRepo != "org/repo" {
		t.Errorf("progress RepoID = %q", sawRepo)
	}
	wantTotal := int64(len(fh.files["config.json"]) + len(fh.files["model.safetensors"]) + len(fh.files["tokenizer.json"]))
	if last.Total != wantTotal {
		t.Errorf("progress Total = %d, want %d", last.Total, wantTotal)
	}
	if last.Completed != wantTotal {
		t.Errorf("final progress Completed = %d, want %d (progress must reach 100%%)", last.Completed, wantTotal)
	}
	if last.FilesTotal != 3 {
		t.Errorf("FilesTotal = %d, want 3", last.FilesTotal)
	}
}

// Resuming must not double-count the bytes already on disk, or the progress bar
// would exceed 100%.
func TestProgressAccountsForPreexistingBytes(t *testing.T) {
	repo := standardRepo()
	fh := newFakeHub(repo)
	srv := fh.server(t)
	dest := t.TempDir()

	full := repo["model.safetensors"]
	part := filepath.Join(dest, "model.safetensors"+partSuffix)
	if err := os.WriteFile(part, full[:1000], 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var maxCompleted int64
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	err := c.Download(context.Background(), DownloadRequest{
		RepoID: "org/repo", Dest: dest,
		OnProgress: func(p Progress) {
			mu.Lock()
			defer mu.Unlock()
			if p.Completed > maxCompleted {
				maxCompleted = p.Completed
			}
			if p.Percent() > 100.001 {
				t.Errorf("progress exceeded 100%%: %.2f%% (%d/%d)", p.Percent(), p.Completed, p.Total)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	total := TotalSize([]File{
		{Size: int64(len(repo["config.json"]))},
		{Size: int64(len(repo["model.safetensors"]))},
		{Size: int64(len(repo["tokenizer.json"]))},
	})
	if maxCompleted != total {
		t.Errorf("final Completed = %d, want %d", maxCompleted, total)
	}
}

// Files download concurrently, but the progress callback must be serialised:
// callers naturally write callbacks that touch shared state (a progress bar, a
// slice) without a lock. Run under -race, this fails if emission is unguarded.
func TestProgressCallbackIsNeverCalledConcurrently(t *testing.T) {
	files := map[string][]byte{"model.safetensors": weights(1 << 16)}
	for i := 0; i < 8; i++ {
		files[fmt.Sprintf("shard-%d.safetensors", i)] = weights(1 << 16)
	}
	fh := newFakeHub(files)
	srv := fh.server(t)

	var inCallback int32
	var updates int // deliberately unguarded: -race proves serialisation
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	err := c.Download(context.Background(), DownloadRequest{
		RepoID: "org/repo", Dest: t.TempDir(), Concurrency: 8,
		OnProgress: func(p Progress) {
			if !atomic.CompareAndSwapInt32(&inCallback, 0, 1) {
				t.Error("OnProgress was called concurrently from two goroutines")
				return
			}
			updates++
			atomic.StoreInt32(&inCallback, 0)
		},
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if updates == 0 {
		t.Error("expected at least one progress update")
	}
}

func TestDownloadRejectsRepoWithoutSafetensors(t *testing.T) {
	fh := newFakeHub(map[string][]byte{
		"config.json":       []byte("{}"),
		"pytorch_model.bin": weights(64),
	})
	srv := fh.server(t)

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	err := c.Download(context.Background(), DownloadRequest{RepoID: "org/repo", Dest: t.TempDir()})
	if err == nil {
		t.Fatal("expected an error for a repo with no MLX weights")
	}
	if !strings.Contains(err.Error(), "safetensors") {
		t.Errorf("error should explain the repo is not MLX-loadable, got: %v", err)
	}
}

func TestDownloadCancellation(t *testing.T) {
	fh := newFakeHub(standardRepo())
	srv := fh.server(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before we start

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	err := c.Download(ctx, DownloadRequest{RepoID: "org/repo", Dest: t.TempDir()})
	if err == nil {
		t.Fatal("expected an error when the context is already cancelled")
	}
}

func TestDownloadValidatesRequest(t *testing.T) {
	c := New()
	if err := c.Download(context.Background(), DownloadRequest{Dest: "/tmp"}); err == nil {
		t.Error("expected error when RepoID is empty")
	}
	if err := c.Download(context.Background(), DownloadRequest{RepoID: "org/repo"}); err == nil {
		t.Error("expected error when Dest is empty")
	}
}

func TestDownloadCreatesNestedDirectories(t *testing.T) {
	fh := newFakeHub(map[string][]byte{
		"config.json":            []byte("{}"),
		"model.safetensors":      weights(128),
		"subdir/extra_file.json": []byte(`{"nested":true}`),
	})
	srv := fh.server(t)
	dest := t.TempDir()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if err := c.Download(context.Background(), DownloadRequest{RepoID: "org/repo", Dest: dest}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "subdir", "extra_file.json")); err != nil {
		t.Errorf("nested file was not written: %v", err)
	}
}
