package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
)

// portHolder classifies whatever is already bound to the server port.
type portHolder int

const (
	// holderNone: nothing identifiable answered — either a predecessor still
	// shutting down, or a process that is not a Gropius server.
	holderNone portHolder = iota
	// holderOurs: a Gropius that shares this user's data root (a live server to
	// defer to, or our own predecessor mid-restart).
	holderOurs
	// holderForeign: something else owns the port and could not prove it is this
	// user's Gropius. Handing local model traffic to it would be a hijack, so we
	// refuse rather than silently become its client.
	holderForeign
)

// acquireListener claims the server port, distinguishing a genuinely-running
// peer from a predecessor that is merely still shutting down — and refusing to
// defer to a process that cannot prove it is this user's Gropius.
//
// A plain net.Listen is not enough. When Gropius is quit and relaunched right
// away, the old process is often still inside its graceful shutdown with the
// listener open, so the new process gets EADDRINUSE. It must wait for the port to
// free rather than wrongly concluding "another server owns the port" and dropping
// into client mode.
//
// On EADDRINUSE we ask who is there via holder():
//   - holderOurs   → a Gropius sharing our data root; defer to it as a client.
//   - holderForeign → an unidentified process; refuse with an error rather than
//     route this user's model traffic to a possible impostor.
//   - holderNone   → nothing answered; a predecessor is probably still going down,
//     so wait for the port to free, up to `wait`, then give up with an error.
//
// Returns (listener, claimed, err): claimed=true means run as the server;
// claimed=false with a nil error means run as a client.
func acquireListener(addr string, wait time.Duration, holder func() portHolder) (net.Listener, bool, error) {
	deadline := time.Now().Add(wait)
	const retry = 150 * time.Millisecond
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, true, nil
		}
		if !isAddrInUse(err) {
			return nil, false, err
		}
		switch holder() {
		case holderOurs:
			return nil, false, nil // defer to the trusted server as a client
		case holderForeign:
			return nil, false, fmt.Errorf(
				"port %s is held by a process that is not this user's Gropius; "+
					"refusing to route local model traffic to it (another account may be impersonating the server)", addr)
		default: // holderNone
			if time.Now().After(deadline) {
				return nil, false, fmt.Errorf(
					"port %s is busy but no Gropius server is responding on it", addr)
			}
			time.Sleep(retry)
		}
	}
}

// probePortHolder classifies the process on the given port by comparing the
// instance token it serves on the loopback control plane against the token this
// user's server recorded in its data root. A match proves the responder shares
// our root (our own server, a restart-in-progress, or a shared-cache peer). A
// mismatch — or a server that will not identify itself — is treated as foreign.
func probePortHolder(paths config.Paths, port int) portHolder {
	served, ok := fetchInstanceToken(port)
	if !ok {
		return holderNone
	}
	ours := readInstanceToken(paths)
	if ours != "" && served != "" &&
		subtle.ConstantTimeCompare([]byte(served), []byte(ours)) == 1 {
		return holderOurs
	}
	return holderForeign
}

// fetchInstanceToken reads the token a running Gropius publishes on its
// loopback-only control plane. ok=false means nothing that looks like a Gropius
// answered (connection refused, non-200, or unparseable).
func fetchInstanceToken(port int) (token string, ok bool) {
	c := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/instance", port))
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", false
	}
	return body.Token, true
}

// instanceTokenPath is where a server records its per-run identity token.
func instanceTokenPath(paths config.Paths) string {
	return filepath.Join(paths.Root, "instance.token")
}

// newInstanceToken returns a fresh random identity token for this server run.
func newInstanceToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// writeInstanceToken records the token 0600 in the data root. 0600 is the crux of
// the anti-impersonation check: in a per-user root another account cannot read it,
// so it cannot forge a matching token when it tries to hold the port.
//
// The write goes through a random O_EXCL temp then rename, not straight to the
// predictable instance.token path. In shared mode the data root is
// group-writable, so a direct write could follow a symlink another account
// pre-planted at instance.token and truncate a file the server user owns. A
// rename replaces the final component without following a link there.
func writeInstanceToken(paths config.Paths, token string) error {
	if err := os.MkdirAll(paths.Root, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(paths.Root, ".instance-*.token")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(token); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, instanceTokenPath(paths))
}

// readInstanceToken returns the recorded token, or "" if none.
func readInstanceToken(paths config.Paths) string {
	b, err := os.ReadFile(instanceTokenPath(paths))
	if err != nil {
		return ""
	}
	return string(b)
}
