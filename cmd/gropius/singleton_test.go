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

// A challenge file sits in the data root, which in shared mode is
// group-writable and where a peer can plant a FIFO under a name the prober is
// about to use. The read runs inside acquireListener's holder probe; a blocking
// open would turn a fast, logged "foreign holder" refusal into a silent hang
// past the probe's deadline.
func TestChallengeReadDoesNotBlockOnFIFO(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	name := "0123456789abcdef0123456789abcdef"
	path := config.ChallengePath(paths.Root, name)
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			syscall.Close(fd)
		}
	})
	done := make(chan struct{})
	go func() {
		_, _ = config.ReadRegular(path, config.MaxChallengeBytes)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the challenge read blocked on a planted FIFO")
	}
}

// A symlinked challenge is never something writeChallenge produced (it renames
// a regular temp into place); following it would answer with a file the peer
// chose, which is how a squatter would forge a proof it cannot construct.
func TestChallengeReadDoesNotFollowSymlink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "planted")
	if err := os.WriteFile(target, []byte("deadbeef"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := config.NewPaths(t.TempDir())
	name := "0123456789abcdef0123456789abcdef"
	if err := os.Symlink(target, config.ChallengePath(paths.Root, name)); err != nil {
		t.Fatal(err)
	}
	if _, err := config.ReadRegular(config.ChallengePath(paths.Root, name), config.MaxChallengeBytes); err == nil {
		t.Error("the challenge read followed a symlink")
	}
}

// The prober must be able to read back what it wrote, and the spent nonce must
// not survive the probe: a challenge that persisted would be replayable, which
// is the defect the identity token had.
func TestChallengeRoundTripAndCleanup(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	name, answer, err := writeChallenge(paths)
	if err != nil {
		t.Fatal(err)
	}
	if !config.ValidChallengeName(name) {
		t.Fatalf("writeChallenge produced a name the guard refuses: %q", name)
	}
	b, err := config.ReadRegular(config.ChallengePath(paths.Root, name), config.MaxChallengeBytes)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != answer {
		t.Errorf("round trip = %q, want %q", b, answer)
	}
	removeChallenge(paths, name)
	if _, err := os.Lstat(config.ChallengePath(paths.Root, name)); !os.IsNotExist(err) {
		t.Error("a spent challenge must not survive the probe")
	}
}

// Two probes must never share a nonce or an answer, or one probe's observed
// answer would authenticate the next.
func TestChallengesAreSingleUse(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	n1, a1, err := writeChallenge(paths)
	if err != nil {
		t.Fatal(err)
	}
	n2, a2, err := writeChallenge(paths)
	if err != nil {
		t.Fatal(err)
	}
	if n1 == n2 || a1 == a2 {
		t.Error("challenges must be unique per probe")
	}
}

// The challenge name is caller-supplied and becomes a path the server READS, so
// anything that could escape the root must be refused before it is joined.
func TestChallengeNameRefusesTraversal(t *testing.T) {
	for _, bad := range []string{
		"", "..", "../../../../etc/passwd", "0123456789abcdef0123456789abcde",
		"0123456789abcdef0123456789abcdeff", "0123456789ABCDEF0123456789abcdef",
		"0123456789abcdef0123456789abcde/", ".gropius-challenge-x",
	} {
		if config.ValidChallengeName(bad) {
			t.Errorf("accepted a malformed challenge name: %q", bad)
		}
		if config.ChallengePath("/tmp", bad) != "" {
			t.Errorf("built a path from a malformed name: %q", bad)
		}
	}
}
