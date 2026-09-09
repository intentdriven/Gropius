package main

import (
	"fmt"
	"net"
	"time"

	"github.com/intentdriven/Gropius/internal/bind"
	"github.com/intentdriven/Gropius/internal/config"
)

// acquireBind takes the listeners a plan names, and reports the plan as it
// actually turned out.
//
// The order is the decision (adr-2609091123526871 rules 1-3). Loopback is
// taken first, by every mode, which is what gives the singleton a contention
// point: measured on this hardware, two processes binding different addresses
// on one port both succeed, so EADDRINUSE — the only signal acquireListener
// has — exists only where every instance binds the same address. It is also
// the address the port-ownership challenge contacts, so the winner is
// guaranteed to be answering exactly where the next instance will ask.
//
// The second listener is where the two failures live, and they are not the
// same failure:
//
//   - the address is not on this Mac (a departed private network, a
//     hand-edited address, an interface that went away). The bind narrows to
//     loopback, the returned plan says which address was dropped and why, and
//     the process serves. It does not exit: exiting is what left the operator
//     with no panel, no app and a file to hand-edit, which is iss-7.
//   - something else holds the port on that address. It cannot be a peer of
//     this build, because that peer would have taken loopback first — so it is
//     a foreign process or an older, single-listener Gropius. Loopback is
//     released and the port is classified exactly as it was before there were
//     two listeners: defer to a holder that proves it shares our root, refuse
//     one that cannot, wait out a predecessor still shutting down. Holding
//     loopback while an older build serves would break its control plane and
//     load a second copy of every model.
//
// Returns (listeners, the plan as acquired, claimed, err). claimed=false with a
// nil error means another Gropius owns the port and this instance is a client.
func acquireBind(plan bind.Plan, port int, wait time.Duration, holder func() portHolder) ([]net.Listener, bind.Plan, bool, error) {
	const retry = 150 * time.Millisecond
	deadline := time.Now().Add(wait)
	for {
		addrs := plan.Addrs(port)
		ln, claimed, err := acquireListener(addrs[0], time.Until(deadline), holder)
		if err != nil || !claimed {
			return nil, plan, claimed, err
		}
		if len(addrs) == 1 {
			return []net.Listener{ln}, plan, true, nil
		}

		second, err := net.Listen("tcp", addrs[1])
		if err == nil {
			return []net.Listener{ln, second}, plan, true, nil
		}
		if !isAddrInUse(err) {
			// Fail closed and serve: this Mac keeps its own access, and the
			// reason travels with the plan to the log and to the panel.
			return []net.Listener{ln}, plan.WithoutExtra(fmt.Sprintf(
				"could not listen on %s (%v) — serving this Mac and nothing else", addrs[1], err)), true, nil
		}

		ln.Close()
		switch holder() {
		case holderOurs:
			return nil, plan, false, nil // defer to the trusted server as a client
		case holderForeign:
			return nil, plan, false, fmt.Errorf(
				"port %s is held by a process that is not this user's Gropius; "+
					"refusing to route local model traffic to it (another account may be impersonating the server)", addrs[1])
		default: // holderNone: a predecessor is probably still going down
			if time.Now().After(deadline) {
				return nil, plan, false, fmt.Errorf(
					"port %s is busy but no Gropius server is responding on it", addrs[1])
			}
			time.Sleep(retry)
		}
	}
}

// advertises reports whether this server announces itself over Bonjour.
//
// Three conditions, and the last two are about what the bind turned out to be
// rather than what it asked for (adr-2609091123526871 rule 8). The advert is
// mDNS on the local link: under the private-network mode, and on any bind that
// narrowed to this Mac, every advert would name an address its recipients
// cannot reach — while disclosing this Mac's hostname, the port, the model
// count and whether a key is required to exactly the network the bind
// excludes.
//
// It reads the configured mode and the plan, never the classifier. Both are
// state Gropius owns end to end, which is what keeps this out of
// adr-2609081118587999 rule 2.
func advertises(cfg config.Config, plan bind.Plan) bool {
	if !cfg.Advertise || !cfg.ExposedToLAN() {
		return false
	}
	if cfg.BindMode == config.BindModePrivateNetwork {
		return false
	}
	return !plan.LoopbackOnly()
}

// serveAll starts the server on every listener and returns a function that
// stops them all.
//
// One http.Server across both sockets, deliberately: the two listeners are one
// server reachable at two addresses, and giving them separate servers would
// give them separate shutdown, separate timeouts and two places for a
// behaviour to drift apart.
func serveAll(srv interface{ Serve(net.Listener) error }, lns []net.Listener, onErr func(error)) {
	for _, ln := range lns {
		go func(ln net.Listener) {
			if err := srv.Serve(ln); err != nil {
				onErr(err)
			}
		}(ln)
	}
}
