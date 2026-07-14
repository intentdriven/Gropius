package main

import (
	"net"
	"testing"
	"time"
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
	ln, claimed, err := acquireListener(freeAddr(t), time.Second, func() bool { return false })
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

	ln, claimed, err := acquireListener(occupied.Addr().String(), time.Second, func() bool { return true })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claimed || ln != nil {
		t.Fatalf("claimed=%v, want claimed=false (defer to the live server)", claimed)
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
	ln, claimed, err := acquireListener(addr, 3*time.Second, func() bool { return false })
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

func TestAcquireListenerGivesUpAfterWait(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	ln, claimed, err := acquireListener(occupied.Addr().String(), 300*time.Millisecond, func() bool { return false })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claimed || ln != nil {
		t.Fatalf("claimed=%v, want claimed=false after the wait expires", claimed)
	}
}
