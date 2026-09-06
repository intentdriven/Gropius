package runtime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// The embedded MLX lock must stay fully hash-pinned: pin mlx-lm at the tested
// version, and carry a hash for every package. A regeneration that dropped
// --generate-hashes, or an accidental empty file, would silently return the
// install to trusting PyPI content — this catches that at test time.
func TestMLXRequirementsAreHashLocked(t *testing.T) {
	s := string(mlxRequirements)
	if len(s) == 0 {
		t.Fatal("embedded mlx-requirements.txt is empty")
	}
	if !strings.Contains(s, "mlx-lm=="+mlxLMVersion) {
		t.Errorf("lock does not pin mlx-lm==%s", mlxLMVersion)
	}

	pkgs := 0
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.Contains(t, "==") {
			pkgs++
		}
	}
	if pkgs < 10 {
		t.Errorf("expected the full transitive tree (>=10 pinned packages), got %d", pkgs)
	}
	hashes := strings.Count(s, "--hash=sha256:")
	if hashes < pkgs {
		t.Errorf("only %d hashes for %d pinned packages — a dependency is unhashed", hashes, pkgs)
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

// writeInterpreter lays down a fake venv interpreter plus the MLX marker so
// installed() sees a complete runtime, with the interpreter at the given mode.
func writeInterpreter(t *testing.T, paths config.Paths, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(paths.VenvPython()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.VenvPython(), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.VenvPython(), mode); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(paths.Venv, ".gropius-mlx-"+mlxLMVersion)
	if err := os.WriteFile(marker, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The interpreter is executed under this account's uid, and in shared-cache
// mode it sits under a setgid staff root where a group-writable file is one
// any local account can rewrite in place. A group- or other-writable
// interpreter must be treated as not installed, and refused before launch.
func TestInstalledRejectsGroupWritableInterpreter(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	writeInterpreter(t, paths, 0o775)
	if NewProvisioner(paths).Installed() {
		t.Error("a group-writable interpreter was reported as installed")
	}
	if err := os.Chmod(paths.VenvPython(), 0o755); err != nil {
		t.Fatal(err)
	}
	if !NewProvisioner(paths).Installed() {
		t.Error("a correctly moded interpreter was not reported as installed")
	}
}

func TestPrecheckRejectsGroupWritableInterpreter(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	writeInterpreter(t, paths, 0o775)
	l := &ExecLauncher{Paths: paths, LogDir: paths.Logs}
	if err := l.Precheck(Spec{RepoID: "org/m", ModelPath: t.TempDir()}); err == nil {
		t.Error("Precheck accepted a group-writable interpreter")
	}
	if err := os.Chmod(paths.VenvPython(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := l.Precheck(Spec{RepoID: "org/m", ModelPath: t.TempDir()}); err != nil {
		t.Errorf("Precheck rejected a correctly moded interpreter: %v", err)
	}
}

// Mode bits are not provenance: the reported plant is an attacker-owned
// venv/bin/python at 0755, which is not group-writable. The interpreter (and
// uv) must be owned by the account about to execute it, or by root.
func TestInstalledRejectsInterpreterNotOwnedByUs(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	writeInterpreter(t, paths, 0o755)
	p := NewProvisioner(paths)
	p.Owner = os.Geteuid() + 1 // simulate a file some other account planted
	if p.Installed() {
		t.Error("an interpreter owned by another account was reported as installed")
	}
}

func TestPrecheckRejectsInterpreterNotOwnedByUs(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	writeInterpreter(t, paths, 0o755)
	l := &ExecLauncher{Paths: paths, LogDir: paths.Logs, Owner: os.Geteuid() + 1}
	if err := l.Precheck(Spec{RepoID: "org/m", ModelPath: t.TempDir()}); err == nil {
		t.Error("Precheck accepted an interpreter owned by another account")
	}
}
