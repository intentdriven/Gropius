// Package instance answers one question about the process on the server port:
// is it this account's Gropius, something else, or nothing identifiable at all.
//
// It is one primitive with two callers. The server's singleton election asks it
// on EADDRINUSE, to decide between deferring to a peer as a client and refusing
// to route this user's model traffic to an impostor; `gropius status` asks it
// from a terminal, to decide between reporting the running server and reporting
// that nothing is serving. A second implementation of the challenge would be a
// second thing to keep right, on a path where being wrong means either handing
// traffic to another account's process or telling an operator their server is
// down while it is up.
package instance

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
)

// Holder classifies whatever is already bound to the server port.
type Holder int

const (
	// HolderNone: nothing identifiable answered — either a predecessor still
	// shutting down, or a process that is not a Gropius server.
	HolderNone Holder = iota
	// HolderOurs: a Gropius that shares this user's data root (a live server to
	// defer to, or our own predecessor mid-restart).
	HolderOurs
	// HolderForeign: something else owns the port and could not prove it is this
	// user's Gropius. Handing local model traffic to it would be a hijack, so we
	// refuse rather than silently become its client.
	HolderForeign
)

// Probe classifies the process on the given port by comparing the instance
// token it serves on the loopback control plane against the token this user's
// server recorded in its data root. A match proves the responder shares our
// root (our own server, a restart-in-progress, or a shared-cache peer). A
// mismatch — or a server that will not identify itself — is treated as foreign.
func Probe(paths config.Paths, port int) Holder {
	name, answer, err := writeChallenge(paths)
	if err != nil {
		// The root cannot be written, so no proof can be constructed. Refuse
		// rather than defer: an unprovable holder is exactly the case this
		// function exists to catch.
		return HolderForeign
	}
	defer removeChallenge(paths, name)

	served, ok := fetchChallengeAnswer(port, name)
	if !ok {
		return HolderNone
	}
	if subtle.ConstantTimeCompare([]byte(served), []byte(answer)) == 1 {
		return HolderOurs
	}
	return HolderForeign
}

// ProbeExisting is Probe for a caller that must not change the Mac it is asking
// about.
//
// Probe creates the data root if it is missing, which is right for the server:
// it is about to write the root either way, and a challenge needs somewhere to
// live. It is wrong for a poll. `gropius status` is run repeatedly, by a person
// and by the menu bar, and a read-only question that creates a directory tree
// as a side effect is a question nobody can ask safely — the more so under
// GROPIUS_ROOT, where the tree would be created wherever the variable happens
// to point.
//
// A root that is not there, or is not a directory, is not a root this account
// is serving from, which is HolderNone rather than an error: nothing is
// serving, which is exactly what the caller asked.
func ProbeExisting(paths config.Paths, port int) Holder {
	fi, err := os.Stat(paths.Root)
	if err != nil || !fi.IsDir() {
		return HolderNone
	}
	return Probe(paths, port)
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
