// Package netshape says which kind of network one of this machine's addresses
// sits on.
//
// It exists so the control panel can tell an operator that the address they
// are about to hand out is the one on their mesh network rather than the one
// on the café's Wi-Fi. It answers that with an observation and never with a
// promise: the network behind a tunnel can be published to the internet or
// shared with machines the operator does not own, and neither transition
// touches the interface or the address this package reads. A name that says
// where the address lives survives both; a name that says the address is
// encrypted, or reachable by the operator's devices and no others, does not.
//
// Nothing here may be read by anything that enforces. adr-2609081118587999
// makes that a rule: this is an inference about another process's state, that
// state changes without Gropius being told, and a check that relaxed on it
// would fail open on every one of those changes. internal/archtest holds the
// import rule that keeps the enforcement path clear of this package, which is
// why nothing here imports internal/gateway.
package netshape

import (
	"net"
	"strings"
	"sync"
)

// PrivateNetwork names the kind of network a mesh VPN puts an address on. It
// is the whole vocabulary of the mark: which network, and nothing about what
// that network is worth.
const PrivateNetwork = "private network"

// Interface is one of this machine's network interfaces: its name, the
// addresses configured on it, and its flags. The name is needed because it is
// half the classification signal, which is why this is not the flat
// net.InterfaceAddrs() list; the flags are needed because an address on an
// interface that is down is an address nothing reaches.
type Interface struct {
	Name  string
	Addrs []net.IP
	Flags net.Flags
}

// The interface enumeration is a variable for the reason gateway.historySource
// is: the answers this package gives depend entirely on what the machine
// happens to be running, and a test that could not fix the interface list
// could assert nothing about them.
//
// It is guarded rather than exported bare. Addrs is read from the control
// panel's snapshot on every event and from the menu bar's ticker, both on
// their own goroutines, while the tests that fix the list live in other
// packages; an exported bare variable would put a write from one package's
// test against reads from those goroutines, and only the order the tests
// happen to run in would keep it quiet. The write side is SetEnumerator, in
// inject.go, which the release build compiles out.
var (
	enumerateMu sync.RWMutex
	enumerate   = realInterfaces
)

// interfaces is the enumeration in force. The function is copied out from
// under the lock and called outside it, so a slow enumeration never holds a
// test's restore.
func interfaces() ([]Interface, error) {
	enumerateMu.RLock()
	fn := enumerate
	enumerateMu.RUnlock()
	return fn()
}

// realInterfaces reads this machine's interfaces.
func realInterfaces() ([]Interface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]Interface, 0, len(ifaces))
	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			// One interface refusing to describe itself is not a reason to
			// report nothing about the rest: an endpoint list missing an
			// address is a worse answer than an unmarked one.
			continue
		}
		e := Interface{Name: i.Name, Flags: i.Flags}
		for _, a := range addrs {
			switch v := a.(type) {
			case *net.IPNet:
				e.Addrs = append(e.Addrs, v.IP)
			case *net.IPAddr:
				e.Addrs = append(e.Addrs, v.IP)
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// Addr is one of this machine's addresses and the kind of network it sits on.
// Network is empty for an ordinary address, which is every address on a
// machine with no private network at all.
type Addr struct {
	IP      string
	Network string
}

// Addrs lists this machine's non-loopback IPv4 addresses on interfaces that
// are up, each with the kind of network it is on. It reads the interfaces on
// every call and holds nothing between them: a tunnel that came up a second
// ago is in the next answer, and one that went away — or went down — is not.
func Addrs() []Addr {
	ifaces, err := interfaces()
	if err != nil {
		return nil
	}
	var out []Addr
	for _, iface := range ifaces {
		// An interface that is down holds its addresses and routes nothing to
		// them. The case this is here for is a tunnel whose daemon was killed:
		// the utun is left behind still carrying its 100.64.0.0/10 address, so
		// both halves of the classification are true of it and neither is
		// worth anything. Listing it would offer an address the server does not
		// answer on, and marking it would put a statement about the network on
		// an address that is not on it — the two failures this package exists
		// to avoid, at once.
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		for _, ip := range iface.Addrs {
			ip4 := ip.To4()
			if ip4 == nil || ip.IsLoopback() {
				continue
			}
			out = append(out, Addr{IP: ip4.String(), Network: networkOf(iface.Name, ip4)})
		}
	}
	return out
}

// carrierGrade is 100.64.0.0/10. A mesh VPN hands its nodes an address out of
// it; so does an ISP doing carrier-grade NAT, which is why the range alone
// decides nothing.
var carrierGrade = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// networkOf is the classification, and it is the conjunction of two facts
// because neither is safe alone.
//
// The range on its own is the shared carrier-grade NAT range: an ISP handing
// it out would have every one of its customers' addresses marked, and the mark
// would be a plain falsehood. The interface on its own is worse — utun is
// taken by several VPN clients, by iCloud Private Relay, and by tunnels macOS
// brings up for itself, none of which is the network the operator means.
//
// What the conjunction leaves is every tunnel carrying an address out of that
// range, which is where mesh VPNs put their nodes but is not theirs alone —
// other VPN clients take utun too, and some hand out addresses from the same
// range. So the reach of this signal is "a tunnel, on the range mesh networks
// use", and the name is written to be true of all of them: it says which kind
// of network the address is on and names no product, because this cannot tell
// one product from another. What it does not establish is that the operator's
// own devices are the ones on the other end, which is why no wording here may
// imply it.
//
// The cost of the conjunction is a miss — an unusual or self-hosted network
// goes unmarked — and unmarked is the designed failure.
func networkOf(ifaceName string, ip net.IP) string {
	if !isTunnel(ifaceName) {
		return ""
	}
	ip4 := ip.To4()
	if ip4 == nil || !carrierGrade.Contains(ip4) {
		return ""
	}
	return PrivateNetwork
}

// isTunnel reports whether the interface name is one of macOS's tunnel
// interfaces. Gropius is an Apple-silicon-only app, so utun is the whole list.
func isTunnel(name string) bool {
	return strings.HasPrefix(name, "utun")
}
