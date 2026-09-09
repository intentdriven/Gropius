// Package private turns a configuration into the set of addresses to acquire,
// resolving the private-network mode's address from this Mac's interfaces.
//
// It is the one place outside the endpoint list that may read the
// private-network classifier, and it may do so under a named carve-out rather
// than by being overlooked. adr-2609081118587999 rule 2 closes every
// enforcement decision to the detection; its 2026-09-08 amendment opens
// exactly one — the address the private-network mode binds — on two
// conditions, both of which live here:
//
//   - ambiguity is refused, never resolved. More than one address carrying the
//     shape means the mode does not start: this classifier cannot tell one
//     product on that range from another, so choosing would bind the operator
//     to a network they did not mean while they believed they had narrowed to
//     their own;
//   - the choice is always shown. The plan carries the address it selected and
//     every candidate it saw, and both surfaces render them.
//
// Everything else stays closed. Nothing here decides whether a key is
// required, who is admitted, or what a warning says; it decides which address
// is acquired, and cmd/gropius acquires what it is handed without knowing why.
// internal/archtest/enforcement_detection_test.go names this package
// explicitly, so reaching the detection *through* it is as loud as reaching it
// directly.
package private

import (
	"github.com/intentdriven/Gropius/internal/bind"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/netshape"
)

// Resolve is the plan this configuration acquires.
//
// Every mode but one is decided by the Host field and needs nothing from this
// package; they are delegated rather than duplicated, so that there is one
// answer to "what does this configuration bind" and one place it comes from.
func Resolve(cfg config.Config) bind.Plan {
	if cfg.BindMode != config.BindModePrivateNetwork {
		return bind.ForHost(cfg.Host)
	}
	found := Candidates()
	switch len(found) {
	case 0:
		// Never a wider set. A mode that fell back to the wildcard when it
		// could not find its address would be the fail-open the amendment's
		// conditions exist to prevent.
		return bind.Private("", nil, "no address on this Mac is on a private network — serving this Mac and nothing else")
	case 1:
		return bind.Private(found[0], found, "")
	default:
		return bind.Private("", found, "more than one address on this Mac is on a private network, and Gropius does not choose between them — serving this Mac and nothing else")
	}
}

// Candidates lists every address on this Mac that carries the private-network
// shape, in the order the classifier reports them.
//
// It reads the interfaces on every call and holds nothing between them, which
// is what lets the panel show the mode as available or unavailable as the
// network comes and goes. What the running server acquired is a separate
// question, answered by the plan it started with: nothing re-binds
// (adr-2609091123526871 rule 9).
func Candidates() []string {
	var out []string
	for _, a := range netshape.Addrs() {
		if a.Network == netshape.PrivateNetwork {
			out = append(out, a.IP)
		}
	}
	return out
}
