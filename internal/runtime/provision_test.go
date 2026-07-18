package runtime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// uvTarball builds a release-shaped tar.gz: a top-level directory containing
// uvx and uv, matching astral-sh's real artifact layout.
func uvTarball(t *testing.T, uvBody []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := []struct {
		name string
		body []byte
	}{
		{"uv-aarch64-apple-darwin/uvx", []byte("not the one")},
		{"uv-aarch64-apple-darwin/uv", uvBody},
	}
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     f.name,
			Typeflag: tar.TypeReg,
			Mode:     0o755,
			Size:     int64(len(f.body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractUV(t *testing.T) {
	want := []byte("#!uv binary bytes")
	got, err := extractUV(uvTarball(t, want))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("extracted %q, want %q", got, want)
	}
}

func TestExtractUVMissingBinary(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.Close()
	gz.Close()
	if _, err := extractUV(buf.Bytes()); err == nil {
		t.Fatal("want error for archive without a uv binary")
	}
}

// TestEnsureUVRejectsTamperedDownload proves the SHA-256 pin is enforced: a
// well-formed tarball whose digest does not match the pinned hash must never
// be installed.
func TestEnsureUVRejectsTamperedDownload(t *testing.T) {
	tampered := uvTarball(t, []byte("evil"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tampered)
	}))
	defer srv.Close()

	old := uvBaseURL
	uvBaseURL = srv.URL
	defer func() { uvBaseURL = old }()

	paths := config.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	p := NewProvisioner(paths)
	err := p.ensureUV(context.Background())
	if err == nil {
		t.Fatal("tampered uv archive was accepted")
	}
	if !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("want a SHA-256 verification error, got: %v", err)
	}
	if _, statErr := os.Stat(paths.UV()); statErr == nil {
		t.Fatal("tampered download must not leave a uv binary installed")
	}
}
