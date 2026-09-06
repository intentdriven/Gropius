package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A malicious or compromised repo must not be able to write outside the model
// directory. File paths in the tree listing come from a third party.
func TestDownloadRejectsPathTraversal(t *testing.T) {
	// Enough "../" to climb out of any plausible temp dir, plus a variant that
	// hides the traversal in the middle so a naive prefix check misses it.
	evilPaths := []string{
		"../../../../../../../../../../../../tmp/gropius-pwned",
		"weights/../../../../../../../../../../../../tmp/gropius-pwned2",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/org/evil/tree/main", func(w http.ResponseWriter, r *http.Request) {
		files := []File{
			{Path: "config.json", Size: 2},
			{Path: "model.safetensors", Size: 5},
		}
		for _, p := range evilPaths {
			files = append(files, File{Path: p, Size: 5})
		}
		json.NewEncoder(w).Encode(files)
	})
	mux.HandleFunc("/org/evil/resolve/main/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "PWNED")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	victims := []string{"/tmp/gropius-pwned", "/tmp/gropius-pwned2"}
	for _, v := range victims {
		os.Remove(v)
		t.Cleanup(func() { os.Remove(v) })
	}

	dest := t.TempDir()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	// The download may error (that's fine) — what matters is nothing escaped.
	_ = c.Download(context.Background(), DownloadRequest{RepoID: "org/evil", Dest: dest})

	for _, v := range victims {
		if _, err := os.Stat(v); err == nil {
			t.Fatalf("PATH TRAVERSAL: a repo file escaped the model directory and wrote %s", v)
		}
	}
	// Nothing at all may exist above dest with our marker name.
	if entries, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), "gropius-pwned*")); len(entries) > 0 {
		t.Fatalf("PATH TRAVERSAL: wrote files outside the model dir: %v", entries)
	}
}

// A symlinked parent directory planted inside the model dir (possible in the
// shared, group-writable cache) must not let a download write outside it. The
// O_NOFOLLOW-on-final-component guard alone misses this; os.Root closes it.
func TestDownloadRefusesSymlinkedParentDir(t *testing.T) {
	outside := t.TempDir()
	dest := t.TempDir()
	// Attacker pre-plants dest/weights -> outside before the download runs.
	if err := os.Symlink(outside, filepath.Join(dest, "weights")); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/org/evil/tree/main", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]File{
			{Path: "weights/model.safetensors", Size: 5},
		})
	})
	mux.HandleFunc("/org/evil/resolve/main/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "PWNED")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	// The download must error rather than write through the symlink.
	if err := c.Download(context.Background(), DownloadRequest{RepoID: "org/evil", Dest: dest}); err == nil {
		t.Fatal("download through a symlinked parent dir succeeded; it must be refused")
	}

	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > 0 {
		t.Fatalf("SYMLINK ESCAPE: download wrote through a planted parent symlink: %v", entries)
	}
}

func TestSafeJoinContainsPaths(t *testing.T) {
	dest := "/data/models/org/repo"
	ok := []string{"config.json", "sub/dir/file.json", "model.safetensors"}
	for _, p := range ok {
		if _, err := safeJoin(dest, p); err != nil {
			t.Errorf("safeJoin(%q) errored on a legit path: %v", p, err)
		}
	}
	bad := []string{
		"../escape",
		"a/../../escape",
		"weights/../../../../../../etc/passwd",
		"/absolute/path",
	}
	for _, p := range bad {
		if _, err := safeJoin(dest, p); err == nil {
			t.Errorf("safeJoin(%q) allowed a path that escapes %q", p, dest)
		}
	}
}

// The org and name directories under the models root are created by whichever
// account downloads first, and in the shared cache any account can create an
// absent name there. A symlink planted at models/<org> would make every write
// — and the registry's later delete — land under the attacker's target, since
// os.Root confines only what is below the directory it was opened at, not the
// path used to reach it. Creating and opening Dest relative to the models root
// refuses the link.
func TestDownloadRefusesSymlinkedOrgDir(t *testing.T) {
	models := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(models, "org")); err != nil {
		t.Fatal(err)
	}
	srv := newFakeHub(map[string][]byte{
		"config.json":       []byte(`{"model_type":"x"}`),
		"model.safetensors": []byte("weights"),
	}).server(t)
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	err := c.Download(context.Background(), DownloadRequest{
		RepoID:    "org/repo",
		ModelsDir: models,
		Dest:      filepath.Join(models, "org", "repo"),
	})
	if err == nil {
		t.Fatal("download through a symlinked org directory succeeded; it must be refused")
	}
	if _, err := os.Stat(filepath.Join(outside, "repo")); !os.IsNotExist(err) {
		t.Fatalf("SYMLINK ESCAPE: download wrote through the planted org symlink (stat err %v)", err)
	}
}

// Two repo files whose names differ only by case cannot both exist on macOS's
// case-insensitive default volume: at best two goroutines race over one
// .gropius-part and the download aborts, at worst a non-LFS file finalizes
// with interleaved content. Refuse such a repo up front, naming the pair.
func TestDownloadRefusesCaseCollidingFiles(t *testing.T) {
	dest := t.TempDir()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/org/twins/tree/main", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]File{
			{Path: "tokenizer.json", Size: 5},
			{Path: "Tokenizer.json", Size: 5},
			{Path: "model.safetensors", Size: 5},
		})
	})
	mux.HandleFunc("/org/twins/resolve/main/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "12345")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	err := c.Download(context.Background(), DownloadRequest{RepoID: "org/twins", Dest: dest})
	if err == nil {
		t.Fatal("a repo with case-colliding file names was accepted")
	}
	if !strings.Contains(err.Error(), "differ only by case") ||
		!strings.Contains(err.Error(), "tokenizer.json") || !strings.Contains(err.Error(), "Tokenizer.json") {
		t.Fatalf("error must name the colliding pair, got: %v", err)
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Errorf("refusal must happen before any file is written, found %v", entries)
	}
}
