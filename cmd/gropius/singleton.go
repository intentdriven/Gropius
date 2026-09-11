package main

import (
	"fmt"
	"net"
	"time"

	"github.com/intentdriven/Gropius/internal/instance"
)

// portHolder is internal/instance's Holder under the name this command has
// always called it. The classification itself lives there because `gropius
// status` asks the same question from a terminal, and two implementations of
// the challenge would be two things to keep right on a path where being wrong
// means handing this account's model traffic to another account's process.
type portHolder = instance.Holder

const (
	holderNone    = instance.HolderNone
	holderOurs    = instance.HolderOurs
	holderForeign = instance.HolderForeign
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
