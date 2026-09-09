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

	"github.com/intentdriven/Gropius/internal/bind"
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
	for _, e := range Endpoints(cfg, bind.ForHost(cfg.Host)) {
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
			for _, e := range Endpoints(cfg, bind.ForHost(cfg.Host)) {
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

	for _, e := range Endpoints(cfg, bind.ForHost(cfg.Host)) {
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

	if got := Endpoints(cfg, bind.ForHost(cfg.Host)); !reflect.DeepEqual(got, want) {
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
			// An empty host is not a bind. It used to reach a listener as
			// ":11535" and take every interface, which is why this list read
			// it as the wildcard; config.Validate has refused it since, so no
			// running server carries one, and the bind plan narrows what it
			// cannot bind to this Mac rather than guessing wide. Every other
			// unbindable Host in this file takes the same closed direction.
			name:   "an empty host is not a bind, and narrows to this Mac",
			host:   "",
			ifaces: laptopOnAMeshVPN(),
			want:   []Endpoint{{URL: "http://127.0.0.1:11535/v1"}},
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
			got := Endpoints(cfg, bind.ForHost(cfg.Host))
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

	before := Endpoints(cfg, bind.ForHost(cfg.Host))
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
	after := Endpoints(cfg, bind.ForHost(cfg.Host))
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
	for _, e := range Endpoints(cfg, bind.ForHost(cfg.Host)) {
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

// The fourth criterion, true by construction rather than by exception:
// loopback is listed under every bind because every bind answers on it
// (adr-2609091123526871 rule 1). This test used to record the opposite — that
// loopback was listed under a bind that refused it — as a known divergence
// handed to iss-7. iss-7 was resolved at its cause instead: the bind acquires
// loopback as well, so the entry the panel has always shown is now one the
// server actually answers on.
//
// It stays last in the list, and unmarked. The menu bar hands out the first
// entry, which is the one another machine should use; loopback is the entry
// this Mac uses, and it is on no network worth naming.
func TestLoopbackIsListedUnderEveryBindBecauseEveryBindAnswersOnIt(t *testing.T) {
	stubIfaces(t, laptopOnAMeshVPN()...)
	for _, host := range []string{"0.0.0.0", "192.168.1.5", "100.101.102.103", "127.0.0.1"} {
		cfg := config.Default()
		cfg.Host = host
		cfg.Port = 11535
		eps := Endpoints(cfg, bind.ForHost(host))
		last := eps[len(eps)-1]
		if last.URL != "http://127.0.0.1:11535/v1" {
			t.Errorf("Endpoints() with Host %q ends with %q, want the loopback URL last", host, last.URL)
		}
		if last.Network != "" {
			t.Errorf("the loopback entry is marked %q — a mark belongs on an address that is on a network, and loopback is on none", last.Network)
		}
	}
}

// The list is what the server acquired, not what the configuration asked for.
// A bind that narrowed — the private-network mode with no address to select,
// an address that has gone away — offers loopback and nothing else, because
// that is what answers. Offering the address the mode was chosen for would be
// the dead address this list exists to stop handing out, and it would say the
// mode is running when it is not.
func TestABindThatNarrowedListsOnlyWhatItAnswersOn(t *testing.T) {
	stubIfaces(t, laptopOnAMeshVPN()...)
	cfg := config.Default()
	cfg.Host = "0.0.0.0"
	cfg.BindMode = config.BindModePrivateNetwork
	cfg.Port = 11535

	narrowed := bind.Private("", nil, "no address on this Mac is on a private network")
	if got, want := urls(Endpoints(cfg, narrowed)), []string{"http://127.0.0.1:11535/v1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Endpoints() = %#v, want %#v — the mode fell back to this Mac and the list has to say so", got, want)
	}

	selected := bind.Private("100.101.102.103", []string{"100.101.102.103"}, "")
	want := []Endpoint{
		{URL: "http://100.101.102.103:11535/v1", Network: netshape.PrivateNetwork},
		{URL: "http://127.0.0.1:11535/v1"},
	}
	if got := Endpoints(cfg, selected); !reflect.DeepEqual(got, want) {
		t.Errorf("Endpoints() = %#v, want %#v — the mode binds the selected address and this Mac", got, want)
	}
}

// Criterion 7 of the private-network intent, and the other half of the
// dead-address rule: nothing re-binds, so a listener outlives the address it
// was taken on. The served set never widens, and the list stops offering an
// address this Mac no longer holds rather than going on naming it.
func TestAnAcquiredAddressThatWentAwayIsNoLongerOffered(t *testing.T) {
	cfg := config.Default()
	cfg.Host = "100.101.102.103"
	cfg.Port = 11535
	plan := bind.ForHost("100.101.102.103")

	stubIfaces(t, testIface("lo0", "127.0.0.1"), testIface("utun4", "100.101.102.103"))
	if got, want := len(Endpoints(cfg, plan)), 2; got != want {
		t.Fatalf("with the tunnel up the list has %d entries, want %d", got, want)
	}
	stubIfaces(t, testIface("lo0", "127.0.0.1"), testIface("en0", "192.168.1.5"))
	if got, want := urls(Endpoints(cfg, plan)), []string{"http://127.0.0.1:11535/v1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Endpoints() = %#v after the tunnel went away, want %#v — the socket is still open and nothing arrives on it", got, want)
	}
}

// A bind host is not a URL host, and the two differ in exactly the case an
// operator hits: the bind spelling of an IPv6 address may be bracketed and the
// URL spelling must be, so a value pasted through unchanged is bracketed twice
// or not at all. The first entry of this list is the menu-bar title, the
// clipboard, and the panel's curl and Python base URL, so a mangled one is
// handed out everywhere at once.
//
// Both spellings of an IPv6 literal appear below, and both are binds now: the
// listen address is built with net.JoinHostPort, which brackets what needs it
// (adr-2609091123526871 rule 5). Before that they were not equivalent —
// "::1:11535" came back from net.SplitHostPort as "too many colons in address"
// and the process exited — which is iss-7's second fault.
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
			// A "[::1]" bind is loopback, and it answers on both loopback
			// addresses: 127.0.0.1 because every bind acquires it, and ::1
			// because that is what this bind names. Listing one and refusing
			// the other was the dead-address fault in both directions at once.
			name:   "a bracketed IPv6 loopback bind lists both loopback addresses",
			host:   "[::1]",
			ifaces: []netshape.Interface{testIface("lo0", "127.0.0.1", "::1"), testIface("en0", "192.168.1.5")},
			want:   []string{"http://[::1]:11535/v1", "http://127.0.0.1:11535/v1"},
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
			// and is left off. What is left is 127.0.0.1, which this bind does
			// answer on: every bind acquires it, zone or no zone.
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
			got := urls(Endpoints(cfg, bind.ForHost(cfg.Host)))
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
