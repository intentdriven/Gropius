package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
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
	name, answer, err := writeChallenge(paths)
	if err != nil {
		// The root cannot be written, so no proof can be constructed. Refuse
		// rather than defer: an unprovable holder is exactly the case this
		// function exists to catch.
		return holderForeign
	}
	defer removeChallenge(paths, name)

	served, ok := fetchChallengeAnswer(port, name)
	if !ok {
		return holderNone
	}
	if subtle.ConstantTimeCompare([]byte(served), []byte(answer)) == 1 {
		return holderOurs
	}
	return holderForeign
}

// writeChallenge drops a single-use nonce file in the data root and returns its
// name and the answer a holder must echo back.
//
// The file is written through a random O_EXCL temp and renamed, for the reason
// the token write did: in shared mode the root is group-writable, so a direct
// write to a predictable name could follow a symlink a peer pre-planted and
// truncate a file this account owns. A rename replaces the final component
// without following a link there.
//
// Mode 0640 rather than 0600 on purpose. The point of the probe is to let a
// process that can read this ROOT prove it, and under a shared root that is a
// peer account in the same group — the case a 0600 token could never serve,
// which is why cross-account client mode never worked. In a per-user root the
// group cannot traverse the directory, so 0640 grants nothing there.
func writeChallenge(paths config.Paths) (name, answer string, err error) {
	if err := os.MkdirAll(paths.Root, 0o755); err != nil {
		return "", "", err
	}
	n := make([]byte, 16)
	if _, err := rand.Read(n); err != nil {
		return "", "", err
	}
	a := make([]byte, 32)
	if _, err := rand.Read(a); err != nil {
		return "", "", err
	}
	name, answer = hex.EncodeToString(n), hex.EncodeToString(a)

	tmp, err := os.CreateTemp(paths.Root, ".challenge-*.tmp")
	if err != nil {
		return "", "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		return "", "", err
	}
	if _, err := tmp.WriteString(answer); err != nil {
		tmp.Close()
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		return "", "", err
	}
	if err := os.Rename(tmpName, config.ChallengePath(paths.Root, name)); err != nil {
		return "", "", err
	}
	return name, answer, nil
}

// removeChallenge deletes a spent nonce, tolerating the failure.
//
// A shared root carries the sticky bit, so a delete can fail with EPERM on a
// file another account owns — and the previous scheme's stale-token cleanup
// failing that way is what left one account's server permanently misread as
// foreign. A leftover challenge is harmless: it is single-use, its answer is
// never reused, and nothing consults it again.
func removeChallenge(paths config.Paths, name string) {
	if p := config.ChallengePath(paths.Root, name); p != "" {
		_ = os.Remove(p)
	}
}

// fetchChallengeAnswer asks the process on the port to read back the nonce.
// ok=false means nothing that looks like a Gropius answered (connection
// refused, non-200, or unparseable).
func fetchChallengeAnswer(port int, name string) (answer string, ok bool) {
	c := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/instance?challenge=%s", port, url.QueryEscape(name)))
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var body struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, config.MaxChallengeBytes)).Decode(&body); err != nil {
		return "", false
	}
	return body.Answer, true
}
