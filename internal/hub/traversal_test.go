package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
