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

	if err := a.Download("org/repo"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	waitFor(t, "the model to become ready", func() bool {
		m, err := a.Registry.Get("org/repo")
		return err == nil && m.Ready()
	})

	// Enough files that removing the directory takes long enough for a second
	// request to arrive while it is half gone. That gap is the whole of what
	// the serialisation has to cover; a real multi-gigabyte model opens it on
	// its own.
	for i := 0; i < 1500; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("blob-%05d.txt", i)), nil, 0o644); err != nil {
			t.Fatalf("filling the model directory: %v", err)
		}
	}

	deleted := make(chan error, 1)
	go func() { deleted <- a.Delete("org/repo") }()

	// Wait until the removal is demonstrably under way rather than guessing at
	// a sleep, so the test cannot pass by arriving after it finished.
	underWay := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		ents, err := os.ReadDir(dir)
		if err != nil {
			break
		}
		if len(ents) < 1200 {
			underWay = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !underWay {
		t.Fatal("the removal finished before a second request could reach it")
	}

	_ = a.Download("org/repo")
	if err := <-deleted; err != nil {
		t.Fatalf("Delete: %v", err)
	}
	waitFor(t, "the download to settle", func() bool { return len(a.Downloading()) == 0 })

	m, err := a.Registry.Get("org/repo")
	if err != nil {
		if _, serr := os.Stat(dir); !os.IsNotExist(serr) {
			t.Fatal("the model is gone from the registry but its directory is still on disk")
		}
		return
	}
	if !m.Ready() {
		// A refused or failed attempt leaves a record the operator can retry or
		// remove, which is a state they can act on.
		return
	}
	for _, f := range []string{"config.json", "model.safetensors", "tokenizer.json"} {
		if _, serr := os.Stat(filepath.Join(dir, f)); serr != nil {
			t.Errorf("the registry says the model is ready but %s is missing: %v", f, serr)
		}
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
	for i := 0; i < 1500; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("blob-%05d.txt", i)), nil, 0o644); err != nil {
			t.Fatalf("filling the model directory: %v", err)
		}
	}

	deleted := make(chan error, 1)
	go func() { deleted <- a.Delete("org/repo") }()

	underWay := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		ents, err := os.ReadDir(dir)
		if err != nil {
			break
		}
		if len(ents) < 1200 {
			underWay = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !underWay {
		t.Fatal("the removal finished before a load could reach it")
	}

	if _, _, err := src.Resolve("org/repo"); err == nil {
		t.Error("a model whose files are being removed was resolved for a load")
	}
	if err := <-deleted; err != nil {
		t.Fatalf("Delete: %v", err)
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
