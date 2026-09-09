//go:build !prod

// The tests here fix the interface list through netshape.SetEnumerator, which
// the release build compiles out, so they are compiled out with it — the same
// constraint, for the same reason, as internal/netshape's own table tests.

package private

import (
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/netshape"
)

// stubInterfaces makes the classifier read the given interface list instead of
// this machine's. Every case here drives a fixed list: the mode's whole
// behaviour is a function of what the machine holds, and against a real Mac
// none of it is checkable.
func stubInterfaces(t *testing.T, ifaces ...netshape.Interface) {
	t.Helper()
	t.Cleanup(netshape.SetEnumerator(func() ([]netshape.Interface, error) { return ifaces, nil }))
}

func iface(name string, addrs ...string) netshape.Interface {
	i := netshape.Interface{Name: name, Flags: net.FlagUp}
	for _, a := range addrs {
		i.Addrs = append(i.Addrs, net.ParseIP(a))
	}
	return i
}

func privateMode() config.Config {
	c := config.Default()
	c.BindMode = config.BindModePrivateNetwork
	return c
}

// Criterion 1: with the mode chosen and exactly one address carrying the
// shape, the set of addresses served is exactly that address and this Mac's
// loopback address.
func TestOneCandidateIsBoundBesideLoopback(t *testing.T) {
	stubInterfaces(t,
		iface("utun4", "100.101.102.103"), // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
		iface("en0", "192.0.2.5"),
		iface("lo0", "127.0.0.1"),
	)
	p := Resolve(privateMode())
	if got, want := p.Addrs(11535), []string{"127.0.0.1:11535", "100.101.102.103:11535"}; !reflect.DeepEqual(got, want) { // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
		t.Errorf("Addrs() = %v, want %v — the mode binds the private-network address and this Mac, and nothing else", got, want)
	}
	if p.Refusal != "" {
		t.Errorf("Refusal = %q, want none: one candidate is not an ambiguity", p.Refusal)
	}
	if !reflect.DeepEqual(p.Candidates, []string{"100.101.102.103"}) { // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
		t.Errorf("Candidates = %v, want the address it selected — the selection is always shown", p.Candidates)
	}
}

// Criterion 3: more than one candidate refuses the mode, names them, and
// serves this Mac. It never picks one — this classifier cannot tell one
// product on that address range from another, so an arbitrary pick would bind
// the operator to a network they did not mean while they believed they had
// narrowed to their own.
func TestSeveralCandidatesRefuseTheModeAndNameThem(t *testing.T) {
	stubInterfaces(t,
		iface("utun4", "100.101.102.103"), // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
		iface("utun7", "100.64.7.7"),      // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
		iface("en0", "192.0.2.5"),
	)
	p := Resolve(privateMode())
	if got, want := p.Addrs(11535), []string{"127.0.0.1:11535"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Addrs() = %v, want %v — it serves this Mac rather than choosing between the candidates", got, want)
	}
	if !reflect.DeepEqual(p.Candidates, []string{"100.101.102.103", "100.64.7.7"}) { // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
		t.Errorf("Candidates = %v, want both — a refusal names what it refused to choose between", p.Candidates)
	}
	if p.Refusal == "" {
		t.Error("no Refusal — the operator is left with a mode that silently did nothing")
	}
}

// Criterion 4: no candidate serves this Mac and never a wider set. A mode that
// fell back to the wildcard when it could not find its address would be the
// fail-open the whole record exists to prevent.
func TestNoCandidateServesThisMacAndNeverWider(t *testing.T) {
	for _, name := range []string{"an ordinary machine", "a tunnel that is not on the range", "the range on a physical interface"} {
		var ifaces []netshape.Interface
		switch name {
		case "an ordinary machine":
			ifaces = []netshape.Interface{iface("en0", "192.0.2.5")}
		case "a tunnel that is not on the range":
			ifaces = []netshape.Interface{iface("utun0", "192.0.2.9")}
		case "the range on a physical interface":
			ifaces = []netshape.Interface{iface("en0", "100.101.102.103")} // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
		}
		stubInterfaces(t, ifaces...)
		p := Resolve(privateMode())
		if got, want := p.Addrs(11535), []string{"127.0.0.1:11535"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: Addrs() = %v, want %v", name, got, want)
		}
		if p.Refusal == "" {
			t.Errorf("%s: no Refusal — the mode was chosen and did not happen, and nothing says so", name)
		}
	}
}

// An interface that is down holds its addresses and routes nothing to them, so
// it is not a candidate. The case is a tunnel whose daemon was killed: the
// utun is left behind still carrying its address, and binding it would serve
// an address nothing reaches while reporting the mode as running.
func TestAnInterfaceThatIsDownIsNotACandidate(t *testing.T) {
	down := iface("utun4", "100.101.102.103") // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
	down.Flags = 0
	stubInterfaces(t, down)
	if p := Resolve(privateMode()); !p.LoopbackOnly() {
		t.Errorf("plan = %+v, want this Mac only — the tunnel is down", p)
	}
}

// Every other mode is decided by Host and goes through the same path, so that
// there is one answer to what a configuration binds. This is also criterion 6's
// half that this package owns: the mode does not route around acquisition, it
// produces a plan like every other mode.
func TestEveryOtherModeIsTheHostField(t *testing.T) {
	stubInterfaces(t, iface("utun4", "100.101.102.103")) // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
	cases := map[string][]string{
		"0.0.0.0":   {"127.0.0.1:11535", "0.0.0.0:11535"},
		"127.0.0.1": {"127.0.0.1:11535"},
		"192.0.2.5": {"127.0.0.1:11535", "192.0.2.5:11535"},
	}
	for host, want := range cases {
		c := config.Default()
		c.Host = host
		if got := Resolve(c).Addrs(11535); !reflect.DeepEqual(got, want) {
			t.Errorf("Host %q: Addrs() = %v, want %v — a private network being present changes nothing for a mode that did not ask for it", host, got, want)
		}
	}
}

// A refusal is read by an operator, in the log and in the panel, and it is
// held to the same vocabulary the mark is (adr-2609081118587999 rule 1): it
// says which address and why, and nothing about what a network is worth. The
// vendor is refused for a second reason — this classifier cannot tell one
// product on the range from another, so naming one would be a guess.
func TestARefusalSaysWhatHappenedAndPromisesNothing(t *testing.T) {
	forbidden := []string{
		"tailscale", "tailnet", "headscale", "zerotier", "wireguard", "nebula",
		"encrypt", "secure", "safe", "only", "vpn", "private and",
	}
	stubInterfaces(t, iface("en0", "192.0.2.5"))
	none := Resolve(privateMode()).Refusal
	stubInterfaces(t, iface("utun4", "100.101.102.103"), iface("utun7", "100.64.7.7")) // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
	several := Resolve(privateMode()).Refusal
	for _, text := range []string{none, several} {
		for _, word := range forbidden {
			if strings.Contains(strings.ToLower(text), word) {
				t.Errorf("a refusal contains %q: %q", word, text)
			}
		}
	}
}

// Candidates is what the panel asks on every snapshot, so that the mode shows
// as available or unavailable as the network comes and goes. It reads the
// interfaces on every call and holds nothing between them.
func TestCandidatesFollowsTheMachineRatherThanCachingIt(t *testing.T) {
	stubInterfaces(t, iface("en0", "192.0.2.5"))
	if got := Candidates(); len(got) != 0 {
		t.Errorf("Candidates() = %v, want none", got)
	}
	stubInterfaces(t, iface("en0", "192.0.2.5"), iface("utun4", "100.101.102.103")) // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
	if got := Candidates(); !reflect.DeepEqual(got, []string{"100.101.102.103"}) {  // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
		t.Errorf("Candidates() = %v, want the tunnel that just came up", got)
	}
}
