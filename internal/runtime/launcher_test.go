package runtime

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
)

// Distinct repo ids must map to distinct log files. ValidRepoID admits
// underscores in both components, so a separator the id itself can contain
// collapses ids like these into one path — and Launch opens the log O_TRUNC,
// so the collision truncates a live log, not just a stale one.
func TestLogFileNamesDoNotCollideAcrossDistinctRepoIDs(t *testing.T) {
	a, b := logFileName("a/b_c"), logFileName("a_b/c")
	if a == b {
		t.Fatalf("logFileName maps distinct repo ids to one file %q — launching the second model truncates the first model's live log", a)
	}
}

// The per-model log has a predictable name in the logs directory, which in
// shared-cache mode is group-writable: another local account can plant a
// symlink there and the truncating open would land on any file this account
// can write. Launch must refuse to open anything but a regular file — and must
// not block on a planted FIFO either.
func TestLaunchRefusesSymlinkedLogFile(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	if err := os.MkdirAll(filepath.Dir(paths.VenvPython()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.VenvPython(), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Logs, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(paths.Logs, logFileName("org/name"))); err != nil {
		t.Fatal(err)
	}

	l := &ExecLauncher{Paths: paths, LogDir: paths.Logs}
	p, err := l.Launch(context.Background(), Spec{RepoID: "org/name", ModelPath: t.TempDir(), Port: 1})
	if p != nil {
		<-p.Done()
	}
	if err == nil {
		t.Fatal("Launch opened a symlinked log file")
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Fatalf("victim file was truncated through the symlink: %q", b)
	}
}

func TestLaunchDoesNotBlockOnFIFOLogFile(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	if err := os.MkdirAll(filepath.Dir(paths.VenvPython()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.VenvPython(), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Logs, 0o755); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(paths.Logs, logFileName("org/name"))
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if fd, err := syscall.Open(fifo, syscall.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
			syscall.Close(fd)
		}
	})
	done := make(chan error, 1)
	go func() {
		p, err := (&ExecLauncher{Paths: paths, LogDir: paths.Logs}).Launch(context.Background(),
			Spec{RepoID: "org/name", ModelPath: t.TempDir(), Port: 1})
		if p != nil {
			<-p.Done()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Launch accepted a FIFO as its log file")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Launch blocked opening a FIFO planted as the log file")
	}
}
