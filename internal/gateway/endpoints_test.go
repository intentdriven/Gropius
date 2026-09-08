//go:build !prod

// Every test here fixes this machine's interface list through
// netshape.SetEnumerator, which the release build compiles out, so this file
// is compiled out with it. Without the constraint `go vet -tags prod ./...`
// and `go test -tags prod ./...` do not build, and the configuration Gropius
// ships is first exercised by the release job, after the tag is pushed.

package gateway

import (
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/netshape"
)

// The endpoint list is a statement about this machine, so against a real Mac
// none of it was checkable: whether an address was marked depended on whether
// the person running the tests happened to be on a mesh VPN. These tests fix
// the interface list instead, which is the whole reason netshape.Interfaces is
// a variable.

func stubIfaces(t *testing.T, ifaces ...netshape.Interface) {
	t.Helper()
	t.Cleanup(netshape.SetEnumerator(func() ([]netshape.Interface, error) { return ifaces, nil }))
}

func testIface(name string, addrs ...string) netshape.Interface {
	i := netshape.Interface{Name: name, Flags: net.FlagUp}
	for _, a := range addrs {
		i.Addrs = append(i.Addrs, net.ParseIP(a))
	}
	return i
}

// laptopOnAMeshVPN is the machine the intent describes: one Wi-Fi address, one
// tunnel address on the mesh network, and loopback.
func laptopOnAMeshVPN() []netshape.Interface {
	return []netshape.Interface{
		testIface("lo0", "127.0.0.1", "::1"),
		testIface("en0", "192.168.1.5", "fe80::1"),
		testIface("utun4", "100.101.102.103"),
	}
}

// dotLocal is the .local endpoint for this machine, or "" when the machine
// will not say what it is called.
func dotLocal(port int) string {
	h := hostname()
	if h == "" {
		return ""
	}
	return fmt.Sprintf("http://%s.local:%d/v1", h, port)
}

func urls(eps []Endpoint) []string {
	out := make([]string, 0, len(eps))
	for _, e := range eps {
		out = append(out, e.URL)
	}
	return out
}

// Criterion 1: on a mesh VPN the private endpoint is marked and the
// local-network ones are not.
func TestPrivateEndpointIsMarkedAndLANEndpointsAreNot(t *testing.T) {
	stubIfaces(t, laptopOnAMeshVPN()...)
	cfg := config.Default()
	cfg.Port = 11535

	marks := map[string]string{}
	for _, e := range Endpoints(cfg) {
		marks[e.URL] = e.Network
	}

	if got := marks["http://100.101.102.103:11535/v1"]; got != netshape.PrivateNetwork {
		t.Errorf("the mesh address is marked %q, want %q", got, netshape.PrivateNetwork)
	}
	for _, url := range []string{
		"http://192.168.1.5:11535/v1",
		"http://127.0.0.1:11535/v1",
		dotLocal(11535),
	} {
		if url == "" {
			continue
		}
		got, ok := marks[url]
		if !ok {
			t.Errorf("%s is missing from the endpoint list", url)
			continue
		}
		if got != "" {
			t.Errorf("%s is marked %q — only an address on a private network carries a mark", url, got)
		}
	}
}

// Criterion 1, the other half: the mark must not follow the range alone or the
// interface alone, because a wrong mark is worse than no mark.
func TestNoMarkOnHalfASignal(t *testing.T) {
	cases := []struct {
		name   string
		ifaces []netshape.Interface
		url    string
	}{
		{
			"the carrier-grade range on a physical interface",
			[]netshape.Interface{testIface("en0", "100.101.102.103")},
			"http://100.101.102.103:11535/v1",
		},
		{
			"a tunnel carrying an ordinary address",
			[]netshape.Interface{testIface("utun0", "192.168.1.5")},
			"http://192.168.1.5:11535/v1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubIfaces(t, c.ifaces...)
			cfg := config.Default()
			cfg.Port = 11535
			for _, e := range Endpoints(cfg) {
				if e.URL == c.url && e.Network != "" {
					t.Errorf("%s is marked %q on half a signal", e.URL, e.Network)
				}
			}
		})
	}
}

// Criterion 2: the mark says which network the address is on, names no vendor,
// and claims nothing about encryption, reachability or who else can connect.
// This is the criterion that keeps the record honest, so it is enforced here
// rather than left to review.
func TestTheMarkNamesNoVendorAndPromisesNothing(t *testing.T) {
	stubIfaces(t, laptopOnAMeshVPN()...)
	cfg := config.Default()
	cfg.Port = 11535

	// The whole vocabulary of the mark. A new kind of network may be added to
	// this list; a claim about what a network is worth may not.
	allowed := map[string]bool{"": true, netshape.PrivateNetwork: true}
	// Vendor names, and the words an operator reads as a statement about their
	// exposure rather than about which network an address is on.
	forbidden := []string{
		"tailscale", "tailnet", "headscale", "zerotier", "wireguard", "nebula",
		"encrypt", "secure", "safe", "only", "vpn", "private and",
	}

	for _, e := range Endpoints(cfg) {
		if !allowed[e.Network] {
			t.Errorf("endpoint %s carries the mark %q, which is not one of the names the panel may show", e.URL, e.Network)
		}
		lower := strings.ToLower(e.Network)
		for _, word := range forbidden {
			if strings.Contains(lower, word) {
				t.Errorf("the mark %q contains %q — it states which network the address is on and nothing more", e.Network, word)
			}
		}
	}
}

// Criterion 3: with no private network, nothing about the list changes. The
// injected interface list is what makes "nothing" checkable — against a real
// machine it never was.
func TestWithNoPrivateNetworkTheListIsUnchanged(t *testing.T) {
	stubIfaces(t,
		testIface("lo0", "127.0.0.1", "::1"),
		testIface("en0", "192.168.1.5", "fe80::1"),
		testIface("en1", "10.0.0.9"),
	)
	cfg := config.Default()
	cfg.Port = 11535

	var want []Endpoint
	if d := dotLocal(11535); d != "" {
		want = append(want, Endpoint{URL: d})
	}
	want = append(want,
		Endpoint{URL: "http://192.168.1.5:11535/v1"},
		Endpoint{URL: "http://10.0.0.9:11535/v1"},
		Endpoint{URL: "http://127.0.0.1:11535/v1"},
	)

	if got := Endpoints(cfg); !reflect.DeepEqual(got, want) {
		t.Errorf("Endpoints() = %#v, want %#v", got, want)
	}
}

// Criterion 4: a specific bind lists only what the server answers on, and the
// mark never lands on an address outside the bind.
func TestASpecificBindListsOnlyWhatItAnswersOn(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		ifaces []netshape.Interface
		want   []Endpoint
		local  bool // whether the .local name belongs in the list
	}{
		{
			name:   "the wildcard is unchanged: every address, the .local name, loopback",
			host:   "0.0.0.0",
			ifaces: laptopOnAMeshVPN(),
			local:  true,
			want: []Endpoint{
				{URL: "http://192.168.1.5:11535/v1"},
				{URL: "http://100.101.102.103:11535/v1", Network: netshape.PrivateNetwork},
				{URL: "http://127.0.0.1:11535/v1"},
			},
		},
		{
			name:   "an empty host is the wildcard too",
			host:   "",
			ifaces: laptopOnAMeshVPN(),
			local:  true,
			want: []Endpoint{
				{URL: "http://192.168.1.5:11535/v1"},
				{URL: "http://100.101.102.103:11535/v1", Network: netshape.PrivateNetwork},
				{URL: "http://127.0.0.1:11535/v1"},
			},
		},
		{
			name:   "a specific LAN address: that address and loopback, and no mesh address",
			host:   "192.168.1.5",
			ifaces: laptopOnAMeshVPN(),
			want: []Endpoint{
				{URL: "http://192.168.1.5:11535/v1"},
				{URL: "http://127.0.0.1:11535/v1"},
			},
		},
		{
			name:   "a specific mesh address: that address, marked, and loopback",
			host:   "100.101.102.103",
			ifaces: laptopOnAMeshVPN(),
			want: []Endpoint{
				{URL: "http://100.101.102.103:11535/v1", Network: netshape.PrivateNetwork},
				{URL: "http://127.0.0.1:11535/v1"},
			},
		},
		{
			name:   "loopback: loopback alone, as today",
			host:   "127.0.0.1",
			ifaces: laptopOnAMeshVPN(),
			want:   []Endpoint{{URL: "http://127.0.0.1:11535/v1"}},
		},
		{
			name:   "localhost: loopback alone, as today",
			host:   "localhost",
			ifaces: laptopOnAMeshVPN(),
			want:   []Endpoint{{URL: "http://127.0.0.1:11535/v1"}},
		},
		{
			// The .local name resolves to the addresses this machine holds on
			// the local network. When the bind covers the only one of those,
			// the name answers; when the machine has others, it may resolve to
			// one of them and the name is dropped rather than guessed at.
			name:   "a specific bind on a machine with one address keeps the .local name",
			host:   "192.168.1.5",
			ifaces: []netshape.Interface{testIface("lo0", "127.0.0.1"), testIface("en0", "192.168.1.5")},
			local:  true,
			want: []Endpoint{
				{URL: "http://192.168.1.5:11535/v1"},
				{URL: "http://127.0.0.1:11535/v1"},
			},
		},
		{
			// The same machine under the wildcard. The .local name is answered
			// over the local network, and this machine holds no address on
			// one, so the name resolves to nothing and belongs in the list no
			// more than it does under the bind below. This is the intent's own
			// user story with the Wi-Fi off.
			name:   "a wildcard on a machine whose only address is the private one gets no .local name",
			host:   "0.0.0.0",
			ifaces: []netshape.Interface{testIface("lo0", "127.0.0.1"), testIface("utun4", "100.101.102.103")},
			want: []Endpoint{
				{URL: "http://100.101.102.103:11535/v1", Network: netshape.PrivateNetwork},
				{URL: "http://127.0.0.1:11535/v1"},
			},
		},
		{
			// The .local name is answered over the local network, not over a
			// tunnel, so a machine whose sole address is the private one does
			// not get it either — the sole-address test is about which
			// addresses the name could resolve to, and a tunnel address is not
			// one of them.
			name:   "a machine whose only address is the private one gets no .local name",
			host:   "100.101.102.103",
			ifaces: []netshape.Interface{testIface("lo0", "127.0.0.1"), testIface("utun4", "100.101.102.103")},
			want: []Endpoint{
				{URL: "http://100.101.102.103:11535/v1", Network: netshape.PrivateNetwork},
				{URL: "http://127.0.0.1:11535/v1"},
			},
		},
		{
			name:   "an IPv6 bind is bracketed rather than mangled",
			host:   "fd00::1",
			ifaces: []netshape.Interface{testIface("lo0", "127.0.0.1"), testIface("en0", "fd00::1")},
			want: []Endpoint{
				{URL: "http://[fd00::1]:11535/v1"},
				{URL: "http://127.0.0.1:11535/v1"},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubIfaces(t, c.ifaces...)
			cfg := config.Default()
			cfg.Host = c.host
			cfg.Port = 11535

			want := c.want
			if c.local {
				if d := dotLocal(11535); d != "" {
					want = append([]Endpoint{{URL: d}}, want...)
				}
			}
			got := Endpoints(cfg)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Endpoints() with Host %q = %#v, want %#v", c.host, got, want)
			}
			// Stated separately because it is the failure the intent names:
			// a mark that promotes an address the server refuses. Under a
			// wildcard every address answers, so there is nothing to check.
			if c.host == "" || c.host == "0.0.0.0" {
				return
			}
			for _, e := range got {
				if e.Network != "" && !strings.Contains(e.URL, c.host) {
					t.Errorf("%s is marked %q but the server is bound to %s", e.URL, e.Network, c.host)
				}
			}
		})
	}
}

// Criterion 6: nothing about the private network is cached for the life of the
// process. The interface list changes between the two calls and the second
// says so.
func TestEndpointsReflectTheCurrentInterfaceList(t *testing.T) {
	ifaces := []netshape.Interface{testIface("en0", "192.168.1.5")}
	t.Cleanup(netshape.SetEnumerator(func() ([]netshape.Interface, error) { return ifaces, nil }))

	cfg := config.Default()
	cfg.Port = 11535

	before := Endpoints(cfg)
	for _, e := range before {
		if e.Network != "" {
			t.Fatalf("%s is marked %q before any tunnel exists", e.URL, e.Network)
		}
	}
	if contains(urls(before), "http://100.101.102.103:11535/v1") {
		t.Fatal("the mesh address is listed before the tunnel exists")
	}

	// The private network comes up.
	ifaces = append(ifaces, testIface("utun4", "100.101.102.103"))
	after := Endpoints(cfg)
	var marked bool
	for _, e := range after {
		if e.URL == "http://100.101.102.103:11535/v1" && e.Network == netshape.PrivateNetwork {
			marked = true
		}
	}
	if !marked {
		t.Errorf("Endpoints() = %#v after the private network came up — it is not reflected, so something is cached", after)
	}

	// And goes away again.
	ifaces = ifaces[:1]
	for _, e := range Endpoints(cfg) {
		if e.Network != "" {
			t.Errorf("%s is still marked %q after the private network went away — something is cached", e.URL, e.Network)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// Loopback is listed under every bind, including a specific non-loopback one
// where this server does not in fact answer on it — spc-2609081222104376 says
// "loopback, which is always listed and always answers", and the second half
// of that is not true of a bind to one address.
//
// It is left as the spec has it, and stated here rather than buried in a
// table's expectations, because it is a known divergence from the intent's
// fourth criterion and not an oversight. Two things hold it in place: today a
// specific bind leaves the control panel unreachable from anywhere (iss-7), so
// nobody reads this list under that bind at all; and the entry is unmarked, so
// it is a dead address and never a dead address wearing a mark, which is the
// failure the intent actually names. Whoever resolves iss-7 should decide it —
// dropping loopback under a specific non-loopback bind is a two-line change
// and this test is what will fail.
func TestLoopbackIsListedUnderEveryBindIncludingOneItDoesNotAnswerOn(t *testing.T) {
	stubIfaces(t, laptopOnAMeshVPN()...)
	for _, host := range []string{"0.0.0.0", "192.168.1.5", "100.101.102.103", "127.0.0.1"} {
		cfg := config.Default()
		cfg.Host = host
		cfg.Port = 11535
		eps := Endpoints(cfg)
		last := eps[len(eps)-1]
		if last.URL != "http://127.0.0.1:11535/v1" {
			t.Errorf("Endpoints() with Host %q ends with %q, want the loopback URL last", host, last.URL)
		}
		if last.Network != "" {
			t.Errorf("the loopback entry is marked %q — a mark on an address this server may not answer on is the failure the intent names", last.Network)
		}
	}
}

// A bind host is not a URL host, and the two differ in exactly the case an
// operator hits: cmd/gropius builds the listener as "<host>:<port>", so an
// IPv6 address only binds when the configuration carries it bracketed —
// "[::1]:11535" listens and "::1:11535" does not — and iss-7 pushes operators
// towards writing exactly that into config.json by hand. net.JoinHostPort then
// brackets it a second time, and the first entry of this list is the menu-bar
// title, the clipboard, and the panel's curl and Python base URL.
//
// Only the bracketed spellings appear below. This table used to carry a case
// for Host "::1" asserting "http://[::1]:11535/v1", which was an assertion
// about a bind that cannot exist: measured, net.Listen("tcp", "::1:11535")
// fails with "too many colons in address", so no running server ever has that
// Host, and config.ValidBindHost now refuses it before Load will keep it.
//
// The rest of the table is the same fault seen from the other side: boundAddr
// passed anything net.ParseIP refused through verbatim, so a host that cannot
// be bound at all still became a URL — including one carrying CR or LF, which
// is a header-injection primitive in whichever client pastes it.
func TestABindHostBecomesAWellFormedURLOrNoURLAtAll(t *testing.T) {
	loopback := "http://127.0.0.1:11535/v1"
	cases := []struct {
		name   string
		host   string
		ifaces []netshape.Interface
		want   []string
	}{
		{
			// A "[::1]" bind is loopback, and the loopback it answers on is
			// ::1 — not 127.0.0.1, which it refuses. ExposedToLAN read the
			// bracketed spelling as LAN-exposed, so the machine's addresses
			// were enumerated for a server nothing off this Mac can reach.
			name:   "a bracketed IPv6 loopback bind lists the loopback it answers on",
			host:   "[::1]",
			ifaces: []netshape.Interface{testIface("lo0", "127.0.0.1", "::1"), testIface("en0", "192.168.1.5")},
			want:   []string{"http://[::1]:11535/v1"},
		},
		{
			// The name resolves to both loopback addresses for a client on
			// this Mac, so the IPv4 spelling is the one to hand out.
			name:   "the loopback bind by name stays 127.0.0.1",
			host:   "localhost",
			ifaces: laptopOnAMeshVPN(),
			want:   []string{loopback},
		},
		{
			// URLHost refuses a zone — "%" introduces an escape in a URL — so
			// the bound address is one this list cannot describe truthfully
			// and is left off. What is left is the same divergence
			// TestLoopbackIsListedUnderEveryBindIncludingOneItDoesNotAnswerOn
			// records: loopback listed in an IPv4 spelling the bind refuses.
			name:   "a zoned loopback bind is loopback, and its address is not listable",
			host:   "[::1%lo0]",
			ifaces: []netshape.Interface{testIface("lo0", "127.0.0.1", "::1"), testIface("en0", "192.168.1.5")},
			want:   []string{loopback},
		},
		{
			name:   "a bracketed IPv6 address bind is not bracketed twice",
			host:   "[fd00::1]",
			ifaces: []netshape.Interface{testIface("lo0", "127.0.0.1"), testIface("en0", "fd00::1")},
			want:   []string{"http://[fd00::1]:11535/v1", loopback},
		},
		{
			name:   "the unbracketed form of the same address is bracketed once",
			host:   "fd00::1",
			ifaces: []netshape.Interface{testIface("lo0", "127.0.0.1"), testIface("en0", "fd00::1")},
			want:   []string{"http://[fd00::1]:11535/v1", loopback},
		},
		{
			name:   "the bracketed wildcard is still a wildcard",
			host:   "[::]",
			ifaces: []netshape.Interface{testIface("lo0", "127.0.0.1"), testIface("en0", "192.168.1.5")},
			want:   []string{dotLocal(11535), "http://192.168.1.5:11535/v1", loopback},
		},
		{
			name:   "a host that is really a host and a port is listed as nothing",
			host:   "192.168.1.5:8080",
			ifaces: laptopOnAMeshVPN(),
			want:   []string{loopback},
		},
		{
			name:   "a host that is not an address or a name is listed as nothing",
			host:   "not an ip",
			ifaces: laptopOnAMeshVPN(),
			want:   []string{loopback},
		},
		{
			// getaddrinfo reads "0" as 0.0.0.0, so this bound every interface
			// while the panel offered "http://0:11535/v1" and nothing else — a
			// wildcard under-reported. config.ValidBindHost now refuses a name
			// whose top label carries no letter, so it never reaches a
			// listener; here it is the host no listener would take.
			name:   "a name that is really an address is listed as nothing",
			host:   "0",
			ifaces: laptopOnAMeshVPN(),
			want:   []string{loopback},
		},
		{
			name:   "a host carrying CR and LF never reaches a URL",
			host:   "10.0.0.1\r\nX-Injected: yes",
			ifaces: laptopOnAMeshVPN(),
			want:   []string{loopback},
		},
		{
			// The bind that is a name still works: it is a specific bind, and
			// the name is what a client is pointed at.
			name:   "a name is a specific bind and stays a name",
			host:   "alices-mac.local",
			ifaces: laptopOnAMeshVPN(),
			want:   []string{"http://alices-mac.local:11535/v1", loopback},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubIfaces(t, c.ifaces...)
			cfg := config.Default()
			cfg.Host = c.host
			cfg.Port = 11535

			want := make([]string, 0, len(c.want))
			for _, u := range c.want {
				if u != "" { // dotLocal is empty when this Mac will not say its name
					want = append(want, u)
				}
			}
			got := urls(Endpoints(cfg))
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Endpoints() with Host %q = %#v, want %#v", c.host, got, want)
			}
			for _, u := range got {
				if strings.ContainsAny(u, "\r\n") || strings.Contains(u, "[[") || strings.Contains(u, " ") {
					t.Errorf("Endpoints() with Host %q emitted %q, which is not a URL any client can use", c.host, u)
				}
			}
		})
	}
}
