package instance

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
)

// A challenge file sits in the data root, which in shared mode is
// group-writable and where a peer can plant a FIFO under a name the prober is
// about to use. The read runs inside the holder probe; a blocking open would
// turn a fast, logged "foreign holder" refusal into a silent hang past the
// probe's deadline.
func TestChallengeReadDoesNotBlockOnFIFO(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	name := strings.Repeat("ab12", 8)
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
	name := strings.Repeat("ab12", 8)
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
	// Built rather than written out: a 32-character hex literal beside a path
	// like "/etc/passwd" reads as a credential to a secret scanner, and a test
	// fixture is not worth a false positive on every branch in the repository.
	good := strings.Repeat("ab12", 8)
	for _, bad := range []string{
		"", "..", "../../../../etc/passwd",
		good[:len(good)-1],               // one short
		good + "f",                       // one long
		strings.ToUpper(good),            // wrong case
		good[:len(good)-1] + "/",         // carries a separator
		".gropius-challenge-" + good[:1], // the prefix is not part of the name
	} {
		if config.ValidChallengeName(bad) {
			t.Errorf("accepted a malformed challenge name: %q", bad)
		}
		if config.ChallengePath("/tmp", bad) != "" {
			t.Errorf("built a path from a malformed name: %q", bad)
		}
	}
}

// The three classifications, against a stand-in for the control plane: one that
// reads the root and echoes the answer back (ours), one that answers with
// something else (foreign), and no listener at all (none).
func TestProbeClassifiesTheHolder(t *testing.T) {
	for _, tc := range []struct {
		name string
		// answer builds the body the stand-in returns for a challenge name;
		// nil means serve nothing at all.
		answer func(root, challenge string) (string, bool)
		want   Holder
	}{
		{
			name: "a holder that can read this root is ours",
			answer: func(root, challenge string) (string, bool) {
				b, err := config.ReadRegular(config.ChallengePath(root, challenge), config.MaxChallengeBytes)
				return string(b), err == nil
			},
			want: HolderOurs,
		},
		{
			name: "a holder that answers with something else is foreign",
			answer: func(string, string) (string, bool) {
				return strings.Repeat("00", 32), true
			},
			want: HolderForeign,
		},
		{
			name: "a holder that will not identify itself is none",
			answer: func(string, string) (string, bool) {
				return "", false
			},
			want: HolderNone,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := config.NewPaths(t.TempDir())
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				answer, ok := tc.answer(paths.Root, r.URL.Query().Get("challenge"))
				if !ok {
					http.Error(w, `{"error":"no such challenge"}`, http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"answer":"` + answer + `"}`))
			}))
			// The probe only ever talks to loopback, so the stand-in has to be
			// there; httptest's default listener already is, but the port is
			// what the probe takes, so it is read back from the listener.
			srv.Listener = mustListenLoopback(t)
			srv.Start()
			defer srv.Close()

			if got := Probe(paths, portOf(t, srv.Listener)); got != tc.want {
				t.Errorf("Probe = %v, want %v", got, tc.want)
			}
		})
	}
}

// A port nothing is listening on is HolderNone: the caller waits for a
// predecessor rather than concluding a server is there.
func TestProbeReportsNoneWhenNothingListens(t *testing.T) {
	ln := mustListenLoopback(t)
	port := portOf(t, ln)
	ln.Close()

	if got := Probe(config.NewPaths(t.TempDir()), port); got != HolderNone {
		t.Errorf("Probe on a free port = %v, want %v", got, HolderNone)
	}
}

func mustListenLoopback(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

func portOf(t *testing.T, ln net.Listener) int {
	t.Helper()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split %q: %v", ln.Addr(), err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("port %q: %v", port, err)
	}
	return n
}
