package main

import (
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/intentdriven/Gropius/internal/app"
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

		// A name resolving to the address the loopback listener already holds
		// is this Mac, not a contended port. internal/bind folds away the
		// loopback spellings it can read, but a name is whatever the resolver
		// says it is — an alias for 127.0.0.1 in a hosts file is an ordinary
		// developer-machine state — and taking it for a peer meant colliding
		// with our own socket, releasing loopback, probing a port nothing was
		// listening on any more, and exiting: the iss-7 trap, reached from a
		// new direction.
		//
		// It narrows, and it says so, because the operator did not ask for
		// this: a name is a bind Gropius treats as exposed and generates a key
		// for, and what turned it into loopback was the resolver — a stale
		// hosts entry, split-horizon DNS, or a resolver another account on
		// this Mac controls. Serving this Mac while the operator believes they
		// serve the network is the narrowing least likely to be intended, so
		// it is the last one that may happen quietly.
		if to, ok := resolvedAddr(addrs[1]); ok && to.String() == ln.Addr().String() {
			return []net.Listener{ln}, plan.WithoutExtra(fmt.Sprintf(
				"the bind address %q resolves to %s, which is the address this Mac already answers on — serving this Mac and nothing else",
				plan.Extra, to.IP)), true, nil
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

// resolvedAddr is what a listen address resolves to on this Mac right now, and
// whether it resolves at all. A value that will not resolve is not this
// listener's address: the listen below is where that is reported, with the
// resolver's own message.
func resolvedAddr(addr string) (*net.TCPAddr, bool) {
	a, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, false
	}
	return a, true
}

// secureExposedBind is the fail-closed rule for a bind that reaches other
// machines with no API key set (iss-1), asked of the addresses this process
// ACQUIRED rather than of the ones its configuration asked for.
//
// A LAN-bound listener with no key is reachable, unauthenticated, by everyone
// on the network, and a warning is not a control: the operator running
// headless never reads it, and the window between first launch and setting a
// key is exactly when the machine is undefended. So a key is generated,
// persisted so it survives the restart, and announced loudly enough to be
// used.
//
// What is new is what it does NOT do. A private-network mode that found no
// address to bind serves this Mac and nothing else, and a key for that is
// friction with no exposure behind it — nobody off this Mac can reach the
// server, and a loopback connection is exempt from the bearer check in any
// case. The maintainer declined the conservative reading on 2026-09-09 for
// that reason, and adr-2609091123526871 rule 7 records the adopted one.
//
// A save that fails is fatal to the exposure, not to the process: the second
// listener is CLOSED rather than left serving, because a key held only in
// memory would vanish on the next start and reopen the endpoint. It runs after
// acquisition and before anything is served, so no request is ever answered
// under the empty key.
// The third return value reports whether config.json was rewritten, which the
// caller needs for a reason of its own: that save writes the settings in force,
// repairs and all, so a repair notice loaded from the old file is no longer
// true of the file.
func secureExposedBind(paths config.Paths, cfg *config.Config, lns []net.Listener, plan bind.Plan, log *slog.Logger) ([]net.Listener, bind.Plan, bool) {
	if !plan.ReachesOtherMachines() || cfg.APIKey != "" {
		return lns, plan, false
	}
	lockDown := func(msg string, args ...any) ([]net.Listener, bind.Plan, bool) {
		log.Error(msg, args...)
		cfg.APIKey = ""
		return closeExtra(lns), plan.WithoutExtra(
			"no API key could be saved for a bind other machines reach — serving this Mac and nothing else"), false
	}
	key, err := config.GenerateAPIKey()
	if err != nil {
		return lockDown("could not generate an API key for a bind other machines reach — narrowing to this Mac so the endpoint is not left open", "err", err)
	}
	cfg.APIKey = key
	if err := config.Save(paths.Config, *cfg); err != nil {
		return lockDown("could not save the generated API key — narrowing to this Mac so the endpoint is not left open", "path", paths.Config, "err", err)
	}
	log.Warn("SECURITY: this server answers on an address other machines reach, so an API key was generated and saved; clients must send it as \"Authorization: Bearer <key>\". Change or clear it in Settings.",
		"api_key", key)
	return lns, plan, true
}

// closeExtra drops every listener but the loopback one, which is always first.
// Closed rather than merely unreported: an open socket serves whatever the
// server is mounted on, whatever the plan says about it.
func closeExtra(lns []net.Listener) []net.Listener {
	for _, ln := range lns[1:] {
		ln.Close()
	}
	return lns[:1]
}

// advertises reports whether this process advertises itself over Bonjour. The
// rule lives in internal/app as Advertises, where the control plane reads the
// same decision for the posture page; this is that rule, applied to the
// configuration and the plan the process is starting under.
func advertises(cfg config.Config, plan bind.Plan) bool {
	return app.Advertises(cfg, plan)
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
