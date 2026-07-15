package main

import (
	"fmt"
	"net"
	"net/http"
	"time"
)

// acquireListener claims the server port, distinguishing a genuinely-running
// peer from a predecessor that is merely still shutting down.
//
// A plain net.Listen is not enough. When Gropius is quit and relaunched right
// away, the old process is often still inside its graceful shutdown with the
// listener open, so the new process gets EADDRINUSE and — with a naive check —
// wrongly concludes "another server owns the port" and drops into client mode.
// The user is left with a menu bar that says "running in another account" and
// nothing actually serving.
//
// So on EADDRINUSE we ask who is there: if `healthy` reports a live server, we
// defer to it immediately as a client (claimed=false). If nothing answers, the
// port is only transiently busy — a predecessor closing down — so we retry until
// it frees, up to `wait`. Only if it never frees do we give up and defer.
//
// Returns (listener, claimed, err): claimed=true means run as the server;
// claimed=false with a nil error means run as a client.
func acquireListener(addr string, wait time.Duration, healthy func() bool) (net.Listener, bool, error) {
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
		// The port is busy. A live server means defer now; anything else means a
		// predecessor is probably still going down, so wait for the port to free.
		if healthy() {
			return nil, false, nil
		}
		if time.Now().After(deadline) {
			return nil, false, nil
		}
		time.Sleep(retry)
	}
}

// serverResponding reports whether a Gropius server is answering /health on the
// loopback interface at the given port. Loopback reaches the server regardless
// of whether it bound 127.0.0.1 or 0.0.0.0.
func serverResponding(port int) bool {
	c := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
