//go:build !prod

// The tests here fix the interface list through SetEnumerator, which the
// release build compiles out, so they are compiled out with it: without this
// constraint `go vet -tags prod ./...` and `go test -tags prod ./...` do not
// build at all, and the configuration Gropius ships is first exercised by the
// release job, after the tag is pushed. What needs no seam — the scan over
// this package's own string literals — is in honesty_test.go and runs in every
// configuration.

package netshape

import (
	"net"
	"reflect"
	"testing"
)

// stubInterfaces makes Addrs read the given interface list instead of this
// machine's, and restores the real enumeration afterwards. Every test
// here drives a fixed list: the whole point of the injection is that the
// classifier's answers are checkable, which against a real Mac they never
// were.
func stubInterfaces(t *testing.T, ifaces ...Interface) {
	t.Helper()
	t.Cleanup(SetEnumerator(func() ([]Interface, error) { return ifaces, nil }))
}

func iface(name string, addrs ...string) Interface {
	i := Interface{Name: name, Flags: net.FlagUp}
	for _, a := range addrs {
		i.Addrs = append(i.Addrs, net.ParseIP(a))
	}
	return i
}

// The signal is the conjunction of the range and the interface, and neither
// half alone. The range on its own is the shared CGNAT range, which some ISPs
// hand out over a physical interface; the interface on its own is taken by
// several unrelated tunnels. Marking on either half alone would put a
// statement about the network on an address that is not on it.
func TestPrivateNetworkNeedsBothTheRangeAndATunnel(t *testing.T) {
	cases := []struct {
		name   string
		ifaces []Interface
		ip     string
		want   string
	}{
		{
			name:   "both halves",
			ifaces: []Interface{iface("utun4", "100.101.102.103")},
			ip:     "100.101.102.103",
			want:   PrivateNetwork,
		},
		{
			name:   "the range on a physical interface is ISP CGNAT",
			ifaces: []Interface{iface("en0", "100.101.102.103")},
			ip:     "100.101.102.103",
			want:   "",
		},
		{
			name:   "a tunnel carrying an ordinary address is some other tunnel",
			ifaces: []Interface{iface("utun0", "192.168.1.5")},
			ip:     "192.168.1.5",
			want:   "",
		},
		{
			name:   "the LAN address beside a tunnel is not marked",
			ifaces: []Interface{iface("utun4", "100.101.102.103"), iface("en0", "192.168.1.5")},
			ip:     "192.168.1.5",
			want:   "",
		},
		{
			name:   "an address this machine does not hold",
			ifaces: []Interface{iface("en0", "192.168.1.5")},
			ip:     "10.0.0.9",
			want:   "",
		},
		{
			name:   "the bottom of the range",
			ifaces: []Interface{iface("utun4", "100.64.0.0")},
			ip:     "100.64.0.0",
			want:   PrivateNetwork,
		},
		{
			name:   "the top of the range",
			ifaces: []Interface{iface("utun4", "100.127.255.255")},
			ip:     "100.127.255.255",
			want:   PrivateNetwork,
		},
		{
			name:   "just below the range",
			ifaces: []Interface{iface("utun4", "100.63.255.255")},
			ip:     "100.63.255.255",
			want:   "",
		},
		{
			name:   "just above the range",
			ifaces: []Interface{iface("utun4", "100.128.0.0")},
			ip:     "100.128.0.0",
			want:   "",
		},
		{
			// The prefix is the rule, and nothing pinned it: strings.HasPrefix
			// mutated to strings.Contains left every test green, and a
			// classification that fires on a name merely containing "utun"
			// marks addresses on interfaces that are not tunnels at all.
			name:   "a name that merely contains utun is not a tunnel",
			ifaces: []Interface{iface("en-utun0", "100.101.102.103")},
			ip:     "100.101.102.103",
			want:   "",
		},
		{
			name:   "nor is one that ends with it",
			ifaces: []Interface{iface("bridge-utun", "100.101.102.103")},
			ip:     "100.101.102.103",
			want:   "",
		},
		{
			name:   "not an address at all",
			ifaces: []Interface{iface("utun4", "100.101.102.103")},
			ip:     "not-an-address",
			want:   "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubInterfaces(t, c.ifaces...)
			var got string
			var found bool
			for _, a := range Addrs() {
				if a.IP == c.ip {
					got, found = a.Network, true
				}
			}
			if !found && c.want != "" {
				t.Fatalf("Addrs() does not list %s at all, so it cannot mark it", c.ip)
			}
			if got != c.want {
				t.Errorf("Addrs() marks %s %q, want %q", c.ip, got, c.want)
			}
		})
	}
}

// Addrs is what replaces the flat net.InterfaceAddrs() walk the endpoint list
// used to do, so it must select the same addresses: non-loopback IPv4 and
// nothing else. Anything wider changes the list on machines with no private
// network at all.
func TestAddrsSelectsNonLoopbackIPv4AndCarriesTheNetwork(t *testing.T) {
	stubInterfaces(t,
		iface("lo0", "127.0.0.1", "::1"),
		iface("en0", "192.168.1.5", "fe80::1"),
		iface("utun4", "100.101.102.103"),
	)
	want := []Addr{
		{IP: "192.168.1.5"},
		{IP: "100.101.102.103", Network: PrivateNetwork},
	}
	if got := Addrs(); !reflect.DeepEqual(got, want) {
		t.Errorf("Addrs() = %#v, want %#v", got, want)
	}
}

// Nothing is memoized: the private network can appear, disappear or change
// address while Gropius runs, and the next call must say so.
func TestAddrsReflectsTheCurrentInterfaceList(t *testing.T) {
	var ifaces []Interface
	t.Cleanup(SetEnumerator(func() ([]Interface, error) { return ifaces, nil }))

	ifaces = []Interface{iface("en0", "192.168.1.5")}
	if got := Addrs(); len(got) != 1 || got[0].Network != "" {
		t.Fatalf("Addrs() = %#v, want the LAN address alone and unmarked", got)
	}
	ifaces = append(ifaces, iface("utun4", "100.101.102.103"))
	got := Addrs()
	if len(got) != 2 || got[1].Network != PrivateNetwork {
		t.Errorf("Addrs() = %#v after the tunnel appeared, want it listed and marked", got)
	}
	ifaces = ifaces[:1]
	if got := Addrs(); len(got) != 1 || got[0].IP != "192.168.1.5" {
		t.Errorf("Addrs() = %#v after the private network went away — it is still listed, so something is cached", got)
	}
}

// An interface that is down is an interface nothing routes to, and its
// addresses are still configured on it. A tunnel whose daemon was killed is
// the case that matters: the utun stays behind holding its 100.64.0.0/10
// address, both halves of the signal are still true of it, and marking it
// would put "private network" on an address this server does not answer on —
// which is the one failure the mark exists to avoid.
func TestAnInterfaceThatIsDownIsNotListedAtAll(t *testing.T) {
	up := iface("utun4", "100.101.102.103")
	down := iface("utun6", "100.64.9.9")
	down.Flags = 0
	downLAN := iface("en1", "10.0.0.9")
	downLAN.Flags = 0
	stubInterfaces(t, up, down, downLAN, iface("en0", "192.168.1.5"))

	want := []Addr{
		{IP: "100.101.102.103", Network: PrivateNetwork},
		{IP: "192.168.1.5"},
	}
	if got := Addrs(); !reflect.DeepEqual(got, want) {
		t.Errorf("Addrs() = %#v, want %#v — an address on an interface that is down is not one this machine answers on", got, want)
	}
}
