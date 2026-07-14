package hub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A cancelled download is INCOMPLETE. It must report an error, never nil — a nil
// return would let the caller mark a half-downloaded model as ready to serve.
func TestCancelledDownloadReturnsError(t *testing.T) {
	repo := standardRepo()
	// Serve the tree, but stall the weights so we can cancel mid-flight.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/org/repo/tree/main", func(w http.ResponseWriter, r *http.Request) {
		for p, b := range repo {
			_ = p
			_ = b
		}
		w.Write([]byte(`[{"path":"config.json","size":22},{"path":"model.safetensors","size":1048576}]`))
	})
	mux.HandleFunc("/org/repo/resolve/main/", func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 100; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			w.Write(make([]byte, 8192))
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(80 * time.Millisecond); cancel() }()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	err := c.Download(ctx, DownloadRequest{RepoID: "org/repo", Dest: t.TempDir()})
	if err == nil {
		t.Fatal("a cancelled (incomplete) download returned nil — it would be marked ready")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
