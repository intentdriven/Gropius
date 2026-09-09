package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/registry"
)

// manyFileHub serves a repo of many small files, each answered slowly, so a
// download stays in flight long enough for another goroutine to change the
// settings underneath it — and keeps building requests (and so keeps reading
// the access token) for the whole of that time.
func manyFileHub(t *testing.T, files int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/org/repo/tree/main", func(w http.ResponseWriter, r *http.Request) {
		type file struct {
			Path string `json:"path"`
			Size int64  `json:"size"`
		}
		out := []file{
			{Path: "config.json", Size: 54},
			{Path: "tokenizer.json", Size: 2},
		}
		for i := 0; i < files; i++ {
			out = append(out, file{Path: fmt.Sprintf("w-%03d.safetensors", i), Size: 64})
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/org/repo/resolve/main/", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[len("/org/repo/resolve/main/"):]
		time.Sleep(15 * time.Millisecond)
		switch name {
		case "config.json":
			_, _ = w.Write([]byte(`{"model_type":"qwen3","max_position_embeddings":40960}`))
		case "tokenizer.json":
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write(make([]byte, 64))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// A settings save writes the HuggingFace access token while a download is
// reading it to build its next request. Nothing orders those two accesses, so
// -race reports the write against the read; the token must be behind a lock,
// and a download that started under one token must go on using it rather than
// picking up a new one halfway through a repo.
func TestSavingSettingsDoesNotRaceALiveDownloadReadingTheToken(t *testing.T) {
	a := newTestApp(t)
	a.Hub.BaseURL = manyFileHub(t, 80).URL

	if err := a.Download("org/repo"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	waitFor(t, "the download to start", func() bool { return len(a.Downloading()) == 1 })

	for i := 0; i < 8; i++ {
		c := a.Config()
		c.HFToken = fmt.Sprintf("hf_%020d", i)
		if err := a.SetConfig(c); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := a.CancelDownload("org/repo"); err != nil {
		t.Fatalf("CancelDownload: %v", err)
	}
	waitFor(t, "the download to stop", func() bool { return len(a.Downloading()) == 0 })
}

// Close cancels the downloads it can see and then waits for them. A Download
// accepted after that snapshot is a goroutine writing into the model directory
// of an app that has already finished shutting down — and its wait-group
// registration lands after Close began waiting on a zero counter. It must be
// refused instead.
func TestDownloadAfterCloseIsRefused(t *testing.T) {
	a := newTestApp(t)
	a.Hub.BaseURL = fakeHub(t).URL

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := a.Download("org/repo"); err == nil {
		t.Fatal("Download was accepted after Close; its goroutine would write files nothing is waiting for")
	}
	if n := len(a.Downloading()); n != 0 {
		t.Errorf("a download was registered after Close: %d in flight", n)
	}
}

// plantBlobs fills a model directory with enough entries that removing it takes
// long enough for another request to arrive mid-removal.
func plantBlobs(t *testing.T, dir string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("blob-%06d.txt", i)), nil, 0o644); err != nil {
			t.Fatalf("filling the model directory: %v", err)
		}
	}
}

// removalUnderWay waits until dir is demonstrably being unlinked — fewer entries
// than were planted in it — and reports whether it caught it.
//
// A directory that has already gone is reported rather than failed. How fast a
// disk unlinks n files is a fact about the machine, not about the code under
// test, and a test that fails on a fast one is reporting the wrong thing; the
// callers retry with more to remove instead, and assert their invariant either
// way, so a round that misses the window still checks something true.
func removalUnderWay(dir string, planted int) bool {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		if len(ents) < planted*3/4 {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// blobCounts is what the two tests below plant, in order, until one of them is
// slow enough to remove that the window can be observed. 1500 files take
// hundreds of milliseconds to unlink on an ordinary Mac; the larger figures are
// there so that a much faster disk does not turn a real property into an
// unobservable one.
var blobCounts = []int{1500, 8000, 30000}

// A Delete and a Download of the same model must settle on one of two outcomes:
// the model is registered and every file it needs is on disk, or it is gone
// from the registry and its directory is gone with it. Deleting a model is not
// one step — the index entry goes first and the files afterwards — so a
// download that starts inside that gap writes into a directory that is still
// being unlinked, and finishes by recording as ready a model whose files the
// removal then carries away.
func TestADownloadStartedWhileAModelIsBeingDeletedDoesNotResurrectIt(t *testing.T) {
	a := newTestApp(t)
	a.Hub.BaseURL = fakeHub(t).URL
	dir := a.Paths.ModelDir("org/repo")

	caught := false
	for _, planted := range blobCounts {
		if err := a.Download("org/repo"); err != nil {
			t.Fatalf("Download: %v", err)
		}
		waitFor(t, "the model to become ready", func() bool {
			m, err := a.Registry.Get("org/repo")
			return err == nil && m.Ready()
		})
		// Enough files that removing the directory takes long enough for a
		// second request to arrive while it is half gone. That gap is the whole
		// of what the serialisation has to cover; a real multi-gigabyte model
		// opens it on its own.
		plantBlobs(t, dir, planted)

		deleted := make(chan error, 1)
		go func() { deleted <- a.Delete("org/repo") }()

		// Observed rather than slept for, so a round that reports the window as
		// caught really did reach it.
		caught = removalUnderWay(dir, planted)
		_ = a.Download("org/repo")
		if err := <-deleted; err != nil {
			t.Fatalf("Delete: %v", err)
		}
		waitFor(t, "the download to settle", func() bool { return len(a.Downloading()) == 0 })

		// Asserted on every round, caught or not: the two outcomes are the only
		// two whether or not this round managed to land inside the removal.
		m, err := a.Registry.Get("org/repo")
		switch {
		case err != nil:
			if _, serr := os.Stat(dir); !os.IsNotExist(serr) {
				t.Fatal("the model is gone from the registry but its directory is still on disk")
			}
		case m.Ready():
			for _, f := range []string{"config.json", "model.safetensors", "tokenizer.json"} {
				if _, serr := os.Stat(filepath.Join(dir, f)); serr != nil {
					t.Errorf("the registry says the model is ready but %s is missing: %v", f, serr)
				}
			}
		default:
			// A refused or failed attempt leaves a record the operator can
			// retry or remove, which is a state they can act on.
		}
		if caught {
			break
		}
		t.Logf("this disk unlinked %d files before the removal could be observed; retrying with more", planted)
		_ = os.RemoveAll(dir)
	}
	if !caught {
		t.Log("WARNING: the removal was never observed in progress on this machine, so this run did not exercise the window it exists for")
	}
}

// The removal has to cover the load path as well as the download path. The pool
// decides to launch a model server inside Resolve, under its own lock, and a
// launch that got the directory a moment before it was unlinked would serve
// from files nothing on this Mac lists any more.
func TestAModelBeingDeletedCannotBeResolvedForALoad(t *testing.T) {
	a := newTestApp(t)
	a.Hub.BaseURL = fakeHub(t).URL
	src := modelSource{a}

	if err := a.Download("org/repo"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	waitFor(t, "the model to become ready", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.Ready()
	})
	if _, _, err := src.Resolve("org/repo"); err != nil {
		t.Fatalf("a ready model must be resolvable: %v", err)
	}

	dir := a.Paths.ModelDir("org/repo")
	caught := false
	for _, planted := range blobCounts {
		plantBlobs(t, dir, planted)

		deleted := make(chan error, 1)
		go func() { deleted <- a.Delete("org/repo") }()
		caught = removalUnderWay(dir, planted)

		// Asserted whether or not the window was reached. Inside it this is the
		// guard doing its work; past it the model is simply gone, and either way
		// nothing may resolve it for a load.
		if _, _, err := src.Resolve("org/repo"); err == nil {
			t.Error("a model whose files are being removed was resolved for a load")
		}
		if err := <-deleted; err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if caught {
			break
		}
		t.Logf("this disk unlinked %d files before the removal could be observed; retrying with more", planted)
		if err := a.Download("org/repo"); err != nil {
			t.Fatalf("Download: %v", err)
		}
		waitFor(t, "the model to become ready again", func() bool {
			m, err := a.Registry.Get("org/repo")
			return err == nil && m.Ready()
		})
	}
	if !caught {
		t.Log("WARNING: the removal was never observed in progress on this machine, so this run did not exercise the window the guard exists for")
	}
}

// The pool waits on dlMu for every load — modelSource.Resolve takes it while the
// pool holds p.mu — so a finishing download must not do its filesystem work
// there. Sizing the directory that has just arrived is that work, and on a real
// model it is a walk of a multi-gigabyte tree; done under the lock, it would let
// the size of one model set how long every other model's load waits.
//
// Two rounds of the same download, differing only in how much there is to size,
// rather than one round against an absolute bound. Something does legitimately
// stay under this lock — the registry write, which has to be one step with the
// deregistration — and on a loaded machine an fsync is tens of milliseconds, so
// an absolute bound measures that and the scheduler rather than the property.
// What the property predicts is a difference: with the sizing under the lock the
// worst delay grows by the cost of the walk, and without it, it does not.
func TestAFinishingDownloadDoesNotHoldUpALoadOfAnotherModel(t *testing.T) {
	a := newTestApp(t)
	a.Hub.BaseURL = fakeHub(t).URL
	src := modelSource{a}

	// A second, unrelated model that a request could ask for at any moment.
	if err := a.Registry.Put(registry.Model{
		RepoID: "org/other",
		Path:   a.Paths.ModelDir("org/other"),
		Bytes:  100,
		State:  registry.StateReady,
	}); err != nil {
		t.Fatalf("registering the second model: %v", err)
	}

	// worstLoadDelayDuringDownload runs a download to completion while asking
	// for the other model as fast as it can, and reports the longest any one of
	// those asks took.
	worstLoadDelayDuringDownload := func() time.Duration {
		stop := make(chan struct{})
		worst := make(chan time.Duration, 1)
		go func() {
			var longest time.Duration
			for {
				select {
				case <-stop:
					worst <- longest
					return
				default:
				}
				began := time.Now()
				if _, _, err := src.Resolve("org/other"); err != nil {
					t.Errorf("resolving the second model: %v", err)
				}
				if took := time.Since(began); took > longest {
					longest = took
				}
			}
		}()
		if err := a.Download("org/repo"); err != nil {
			t.Fatalf("Download: %v", err)
		}
		waitFor(t, "the model to become ready", func() bool {
			m, err := a.Registry.Get("org/repo")
			return err == nil && m.Ready()
		})
		close(stop)
		return <-worst
	}

	// The control: the model's directory holds only the model.
	small := worstLoadDelayDuringDownload()

	// The same download again, with a great deal more to size. A re-download
	// keeps the directory, so the blobs are still there when it finishes.
	dir := a.Paths.ModelDir("org/repo")
	plantBlobs(t, dir, 6000)

	// What sizing it now costs, measured warm on this machine, so the margin
	// below is a figure from this run rather than a guess about the hardware.
	_ = dirSize(dir)
	start := time.Now()
	_ = dirSize(dir)
	sizing := time.Since(start)

	big := worstLoadDelayDuringDownload()

	if big > small+sizing/2 {
		t.Errorf("the worst load delay went from %v to %v when the finishing download's directory grew by %v of sizing — the sizing is happening under the lock the pool waits on",
			small, big, sizing)
	}
}

// Download publishes its handle — with the channel a Delete waits on — before
// it writes the model's first registry record. When that write fails the
// function returns, and nothing else will ever close that channel, so a Delete
// holding the handle waits for a goroutine that was never started.
func TestADeleteDoesNotHangWhenTheDownloadRecordCannotBeWritten(t *testing.T) {
	a := newTestApp(t)
	a.Hub.BaseURL = fakeHub(t).URL

	// Make every registry write fail: the index is saved by renaming a temp
	// file onto this path, and a directory cannot be renamed over.
	_ = os.Remove(a.Paths.State)
	if err := os.Mkdir(a.Paths.State, 0o755); err != nil {
		t.Fatalf("blocking the registry file: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = a.Download("org/repo")
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = a.Delete("org/repo")
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	time.Sleep(2 * time.Second)
	close(stop)
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("a Delete is still waiting on a download that never started: the handle's done channel was never closed")
	}
}
