package main

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
)

// freeAddr returns an address that was momentarily bound and then released, so
// it is very likely free for the next Listen.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestAcquireListenerClaimsFreePort(t *testing.T) {
	ln, claimed, err := acquireListener(freeAddr(t), time.Second, func() portHolder { return holderNone })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !claimed || ln == nil {
		t.Fatalf("claimed=%v ln=%v, want claimed=true with a listener", claimed, ln)
	}
	ln.Close()
}

func TestAcquireListenerDefersToLiveServer(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	ln, claimed, err := acquireListener(occupied.Addr().String(), time.Second, func() portHolder { return holderOurs })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claimed || ln != nil {
		t.Fatalf("claimed=%v, want claimed=false (defer to the live server)", claimed)
	}
}

// The hijack defense: an unidentified process holds the port. acquireListener
// must refuse with an error rather than silently become its client and route
// this user's model traffic to a possible impostor.
func TestAcquireListenerRefusesForeignHolder(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	ln, claimed, err := acquireListener(occupied.Addr().String(), time.Second, func() portHolder { return holderForeign })
	if err == nil {
		t.Fatal("expected an error when a foreign process holds the port")
	}
	if claimed || ln != nil {
		t.Fatalf("claimed=%v ln=%v, want no listener for a foreign holder", claimed, ln)
	}
}

// This is the regression test for the restart race: the port is busy but nothing
// answers health (a predecessor is shutting down). acquireListener must wait for
// the port to free rather than immediately dropping into client mode.
func TestAcquireListenerWaitsForShutdownThenClaims(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := occupied.Addr().String()

	// Free the port shortly, simulating the predecessor finishing shutdown.
	go func() {
		time.Sleep(400 * time.Millisecond)
		occupied.Close()
	}()

	start := time.Now()
	ln, claimed, err := acquireListener(addr, 3*time.Second, func() portHolder { return holderNone })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !claimed || ln == nil {
		t.Fatalf("claimed=%v, want claimed=true once the port frees", claimed)
	}
	if time.Since(start) < 300*time.Millisecond {
		t.Fatalf("claimed too fast (%s); it should have waited for the port", time.Since(start))
	}
	ln.Close()
}

// A port that stays busy with nothing identifiable answering is no longer
// silently treated as a server to client-mode into: acquireListener gives up
// with an error so the user learns the port is stuck rather than trusting it.
func TestAcquireListenerGivesUpAfterWait(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	ln, claimed, err := acquireListener(occupied.Addr().String(), 300*time.Millisecond, func() portHolder { return holderNone })
	if err == nil {
		t.Fatal("expected an error after the wait expires with the port still held")
	}
	if claimed || ln != nil {
		t.Fatalf("claimed=%v ln=%v, want no listener after the wait expires", claimed, ln)
	}
}

// instance.token sits in the data root, which in shared mode is group-writable
// and where a peer can plant a FIFO (or replace its own 0600 token with one).
// readInstanceToken runs inside acquireListener's holder probe; a blocking
// open would turn a fast, logged "foreign holder" refusal into a silent hang
// past the probe's deadline.
func TestReadInstanceTokenDoesNotBlockOnFIFO(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	path := instanceTokenPath(paths)
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			syscall.Close(fd)
		}
	})
	done := make(chan string, 1)
	go func() { done <- readInstanceToken(paths) }()
	select {
	case got := <-done:
		if got != "" {
			t.Errorf("a FIFO token must read as no token, got %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readInstanceToken blocked on a FIFO planted as instance.token")
	}
}

// A symlinked token is never something writeInstanceToken produced (it renames
// a regular 0600 temp into place); following it would compare against a file
// the peer chose.
func TestReadInstanceTokenDoesNotFollowSymlink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "planted.token")
	if err := os.WriteFile(target, []byte("deadbeef"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := config.NewPaths(t.TempDir())
	if err := os.Symlink(target, instanceTokenPath(paths)); err != nil {
		t.Fatal(err)
	}
	if got := readInstanceToken(paths); got != "" {
		t.Errorf("readInstanceToken followed a symlink: %q", got)
	}
}

// The hardened read must still round-trip the token the server writes.
func TestInstanceTokenRoundTrip(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	tok, err := newInstanceToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeInstanceToken(paths, tok); err != nil {
		t.Fatal(err)
	}
	if got := readInstanceToken(paths); got != tok {
		t.Errorf("round trip = %q, want %q", got, tok)
	}
}
