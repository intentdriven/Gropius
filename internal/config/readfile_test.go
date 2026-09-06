package config

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// unwedgeFIFO opens the FIFO for writing at cleanup so a reader left blocked
// in open(2) by a missing fix is released instead of leaking the goroutine.
func unwedgeFIFO(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(func() {
		if fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			syscall.Close(fd)
		}
	})
}

// In shared-cache mode config.json sits in a group-writable root and is created
// lazily, so another local account can plant a FIFO under that name before the
// first save. Load runs before the port is claimed; a blocking open would hang
// startup with no error and no way to recover from the app.
func TestLoadDoesNotBlockOnFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatal(err)
	}
	unwedgeFIFO(t, path)

	done := make(chan error, 1)
	go func() { _, _, err := Load(path); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Load of a FIFO must fail so main fails closed, not return defaults silently")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Load blocked on a FIFO planted as config.json")
	}
}

// A symlinked config.json is never something Save wrote; following it would
// apply a file from outside the root with this account's privileges.
func TestLoadDoesNotFollowSymlink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(target, []byte(`{"host":"0.0.0.0","port":11535,"decode_concurrency":4}`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path); err == nil {
		t.Fatal("Load followed a symlinked config.json")
	}
}

// os.ReadFile has no cap: a link to an endless device never reaches EOF, and
// a huge regular file balloons memory before the parse. The read is bounded.
func TestLoadRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	big := `{"host":"127.0.0.1","port":11535,"decode_concurrency":4,"hf_token":"` +
		strings.Repeat("x", int(MaxConfigBytes)) + `"}`
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path); err == nil {
		t.Fatal("Load accepted a config.json larger than the cap")
	}
}

func TestReadRegularReadsOrdinaryFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := ReadRegular(path, 5)
	if err != nil || string(b) != "hello" {
		t.Fatalf("ReadRegular = %q, %v; want hello, nil", b, err)
	}
	if _, err := ReadRegular(path, 4); err == nil {
		t.Fatal("ReadRegular accepted a file one byte over the cap")
	}
	if _, err := ReadRegular(filepath.Join(t.TempDir(), "missing"), 5); !os.IsNotExist(err) {
		t.Fatalf("missing file must surface as not-exist, got %v", err)
	}
}
