// Package bind says which addresses one instance of the server acquires.
//
// A bind is a set of listeners rather than an address (adr-2609091123526871).
// Loopback is in every set, first, because two things rest on it: the control
// plane is loopback-only, so a bind that took loopback away stranded the
// operator's own panel (iss-7); and the port-ownership challenge in
// cmd/gropius contacts loopback and nothing else, so loopback is the one
// address every instance can be made to collide on. Measured on this hardware:
// two processes binding *different* addresses on one port both succeed, while
// an exact duplicate bind is refused — so EADDRINUSE, the only signal the
// singleton has, exists only where every instance binds the same address.
//
// Nothing here reads the private-network classifier. The plan for the
// private-network mode is built by internal/bind/private, which may read it
// under the 2026-09-08 amendment to adr-2609081118587999; this package holds
// the value that mode produces, so that internal/app and internal/gateway can
// name a plan without the classifier entering their dependency closure.
package bind

import (
	"net"
	"strconv"
	"strings"

	"github.com/intentdriven/Gropius/internal/config"
)

// LoopbackAddr is the address every plan acquires first: this Mac, and no
// other machine, on any network, under any bind.
const LoopbackAddr = "127.0.0.1"

// Plan is the set of addresses one instance acquires, in the order it acquires
// them. It is a value rather than an address built inline where the listener
// is taken, so that what a mode decided is testable without a listener and
// without a second machine.
type Plan struct {
	// Loopback is always LoopbackAddr. It is a field rather than a constant
	// read at the point of use so that a plan carries its whole answer.
	Loopback string
	// Extra is the second address to acquire, bare — unbracketed, so that
	// every listen address is built through net.JoinHostPort. Empty means
	// this bind is loopback and nothing else.
	Extra string
	// Refusal says why Extra is empty although the configuration asked for a
	// wider bind. Empty when nothing was refused, which includes a bind the
	// operator asked to be loopback-only.
	Refusal string
	// Mode is the bind mode that produced this plan: config.BindModeHost for a
	// plan built from the Host field, config.BindModePrivateNetwork for one the
	// private-network mode resolved.
	//
	// It travels with the plan because the plan is what the process is running
	// and the configuration is what is stored, and the two diverge the moment a
	// save changes the mode: reading the mode from the configuration reported a
	// wildcard bind as the private-network mode's selection, on the surface
	// that selection has to be checkable from.
	Mode string
	// Candidates is every address the private-network mode found carrying the
	// shape it looks for. It is reported whether the mode selected one or
	// refused to choose between several, because the selection is always
	// shown (adr-2609081118587999, amendment condition 2).
	Candidates []string
}

// ForHost is the plan for a configuration whose bind is its Host field: the
// wildcard, loopback, a specific address, or a name.
//
// Extra is dropped only when it would duplicate the loopback listener — the
// case where the second net.Listen would take the same address and be refused
// by the kernel, turning an ordinary loopback-only install into a startup
// error. A loopback address that is not 127.0.0.1 is kept: "::1" and
// "127.0.0.53" are separate addresses, they bind alongside it, and dropping
// them would stop answering somewhere a client is already pointed.
//
// A Host that cannot be bound at all yields loopback with a refusal rather
// than an error. config.Validate refuses such a value long before this is
// reached — Load turns that into a locked-down bind — so arriving here means
// the configuration did not come through Load, and the closed answer is to
// serve this Mac and say why.
func ForHost(host string) Plan {
	p := Plan{Loopback: LoopbackAddr, Mode: config.BindModeHost}
	bare, ok := bareHost(host)
	switch {
	case !ok:
		p.Refusal = "the configured bind address " + strconv.Quote(host) + " is not one this Mac can listen on"
	case duplicatesLoopback(host, bare):
		// Nothing to add: the operator asked for this Mac only, and this Mac
		// only is what the loopback listener already is.
	default:
		p.Extra = bare
	}
	return p
}

// Private is the plan for the private-network mode: the address it selected
// out of the candidates it saw, or loopback alone with the reason when it
// selected none.
//
// The mode can only ever narrow, so an empty selection is loopback and never a
// wider set. Candidates travel either way: a selection has to be shown to be
// checked, and a refusal has to name what it refused to choose between.
func Private(selected string, candidates []string, refusal string) Plan {
	p := Plan{Loopback: LoopbackAddr, Mode: config.BindModePrivateNetwork, Candidates: candidates}
	if selected == "" {
		p.Refusal = refusal
		return p
	}
	p.Extra = selected
	return p
}

// WithoutExtra is this plan with the second address dropped and the reason
// recorded: what a bind turned out to be, when the address it named could not
// be taken.
//
// The narrowing can only ever narrow, which is why this is the only way a plan
// changes after it is built. A fallback that widened — to the wildcard, or to
// the address the mode was chosen instead of — would be the fail-open every
// other rule here exists to prevent.
func (p Plan) WithoutExtra(reason string) Plan {
	p.Extra = ""
	p.Refusal = reason
	return p
}

// ReachesOtherMachines reports whether this bind answers anywhere a machine
// other than this one can connect to.
//
// It is a question about the sockets, so it is asked of the plan and never of
// the stored configuration: the two diverge as soon as a bind is saved and not
// yet in force, and everything that tells an operator how open their server is
// has to answer from what is actually bound. The loopback listener is this Mac
// by definition, and the second address is measured with the same
// spelling-aware test the configuration uses, so "::1" and "127.0.0.53" are
// this Mac too.
func (p Plan) ReachesOtherMachines() bool {
	if p.Extra == "" {
		return false
	}
	return config.Config{Host: p.Extra}.ExposedToLAN()
}

// LoopbackOnly reports whether this plan serves this Mac and nothing else.
func (p Plan) LoopbackOnly() bool { return p.Extra == "" }

// Addrs is the listen addresses, in acquisition order, spelled the way
// net.Listen wants them.
//
// Every one goes through net.JoinHostPort. Built with fmt.Sprintf, as this was,
// an IPv6 literal produced "::1:11535" and the process exited with "too many
// colons in address" (iss-7): a hand-edited configuration that could never
// start, on a value config.Validate accepted.
func (p Plan) Addrs(port int) []string {
	out := []string{net.JoinHostPort(p.Loopback, strconv.Itoa(port))}
	if p.Extra != "" {
		out = append(out, net.JoinHostPort(p.Extra, strconv.Itoa(port)))
	}
	return out
}

// bareHost is the host without the brackets a bind is written with, and
// reports whether it is something a listener takes at all.
//
// config.ValidBindHost is the fence — it is held to the listener by a table
// that was watched to bind — and config.URLHost is the same unbracketing,
// except that it also refuses a zone, which a listener accepts and a URL
// cannot carry. So the brackets come off here rather than there.
func bareHost(host string) (string, bool) {
	if !config.ValidBindHost(host) {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"), true
}

// duplicatesLoopback reports whether binding this host a second time would
// take the address the loopback listener already holds.
//
// Two spellings do. The literal 127.0.0.1 is one. A name that resolves to
// loopback is the other, and it is the reason this is not a string comparison:
// "localhost" is a loopback bind in config.ExposedToLAN's terms, the resolver
// hands it 127.0.0.1, and a second listener on it would be refused.
// 127.0.0.53 and ::1 are loopback too and are not this address, so they are
// acquired alongside it.
func duplicatesLoopback(host, bare string) bool {
	if ip := net.ParseIP(bare); ip != nil {
		return ip.Equal(net.ParseIP(LoopbackAddr))
	}
	return !config.Config{Host: host}.ExposedToLAN()
}
