package bind

import (
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const port = 11535

// The invariant the whole record rests on: whatever else a bind names,
// loopback is in it, first (adr-2609091123526871 rule 1). First, because it is
// the address every instance is guaranteed to take and therefore the one the
// singleton can contend on, and because the port-ownership challenge contacts
// it and nothing else.
func TestEveryPlanAcquiresLoopbackFirst(t *testing.T) {
	plans := map[string]Plan{
		"the wildcard":                       ForHost("0.0.0.0"),
		"loopback":                           ForHost("127.0.0.1"),
		"loopback by name":                   ForHost("localhost"),
		"a specific address":                 ForHost("192.0.2.5"),
		"a name":                             ForHost("alices-mac.local"),
		"an IPv6 wildcard":                   ForHost("[::]"),
		"an IPv6 literal":                    ForHost("[2001:db8::1]"),
		"a host that cannot bind":            ForHost("not an ip"),
		"the private mode":                   Private("100.101.102.103", []string{"100.101.102.103"}, ""), // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
		"the private mode with no candidate": Private("", nil, "nothing matched"),
	}
	for what, p := range plans {
		if p.Loopback != LoopbackAddr {
			t.Errorf("%s: plan.Loopback = %q, want %q", what, p.Loopback, LoopbackAddr)
		}
		got := p.Addrs(port)
		if len(got) == 0 || got[0] != "127.0.0.1:11535" {
			t.Errorf("%s: Addrs() = %v, want the loopback address first", what, got)
		}
	}
}

// What each mode acquires beyond loopback. The second listener is the whole of
// the narrowing, so the table is the specification of it.
func TestWhatEachBindAcquiresBesidesLoopback(t *testing.T) {
	cases := []struct {
		host string
		want []string
		why  string
	}{
		{"0.0.0.0", []string{"127.0.0.1:11535", "0.0.0.0:11535"}, "the wildcard is acquired alongside loopback, not instead of it"},
		{"192.0.2.5", []string{"127.0.0.1:11535", "192.0.2.5:11535"}, "a specific bind keeps this Mac's own access (iss-7)"},
		{"alices-mac.local", []string{"127.0.0.1:11535", "alices-mac.local:11535"}, "a name is a specific bind too"},
		{"127.0.0.1", []string{"127.0.0.1:11535"}, "loopback alone: a second listener on the same address is refused by the kernel"},
		{"localhost", []string{"127.0.0.1:11535"}, "the name resolves to the address already held"},
		{"LOCALHOST.", []string{"127.0.0.1:11535"}, "and every spelling of it"},
		{"127.0.0.53", []string{"127.0.0.1:11535", "127.0.0.53:11535"}, "a different loopback address is a different socket, and a client may be pointed at it"},
		{"[::1]", []string{"127.0.0.1:11535", "[::1]:11535"}, "IPv6 loopback binds beside IPv4 loopback, bracketed by JoinHostPort"},
		{"::1", []string{"127.0.0.1:11535", "[::1]:11535"}, "and the unbracketed spelling means the same bind"},
		{"[::]", []string{"127.0.0.1:11535", "[::]:11535"}, "the IPv6 wildcard"},
		{"[fe80::1%en0]", []string{"127.0.0.1:11535", "[fe80::1%en0]:11535"}, "a zone survives into the listen address"},
	}
	for _, c := range cases {
		got := ForHost(c.host).Addrs(port)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ForHost(%q).Addrs(%d) = %v, want %v — %s", c.host, port, got, c.want, c.why)
		}
	}
}

// Every address in a plan is one net.Listen actually takes. The fault this
// closes is iss-7's second one: the address was built with fmt.Sprintf, so an
// IPv6 literal came out as "::1:11535" and the process exited with "too many
// colons in address" before it served anything.
//
// Measured rather than asserted, which is the same standard config's own bind
// table is held to. A port of 0 is used so the test cannot collide with a
// running server or with itself.
func TestEveryAddressAPlanNamesIsOneAListenerTakes(t *testing.T) {
	// 127.0.0.53 is deliberately absent: macOS assigns lo0 only 127.0.0.1, so
	// a listener on the rest of 127/8 fails with "can't assign requested
	// address" on this platform. It stays in the table above, which is about
	// what the plan names rather than about what this Mac happens to hold.
	for _, host := range []string{"0.0.0.0", "127.0.0.1", "localhost", "::1", "[::1]", "[::]"} {
		for _, addr := range ForHost(host).Addrs(0) {
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				t.Errorf("ForHost(%q) names %q, which does not listen: %v", host, addr, err)
				continue
			}
			ln.Close()
		}
	}
}

// The private-network mode's three outcomes. Ambiguity is refused rather than
// resolved and zero candidates serve this Mac only — never a wider set, which
// is the fail-open the amendment's conditions exist to prevent
// (adr-2609081118587999, amendment). The candidates travel with the plan in
// every case, because the selection is always shown.
func TestThePrivateModeSelectsRefusesOrFallsBackToThisMac(t *testing.T) {
	one := Private("100.101.102.103", []string{"100.101.102.103"}, "") // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
	if one.Extra != "100.101.102.103" || one.LoopbackOnly() {          // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
		t.Errorf("one candidate: plan = %+v, want it acquired beside loopback", one)
	}
	if one.Refusal != "" {
		t.Errorf("one candidate: Refusal = %q, want none", one.Refusal)
	}

	several := Private("", []string{"100.101.102.103", "100.64.7.7"}, "more than one address matched") // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
	if !several.LoopbackOnly() {
		t.Errorf("several candidates: plan = %+v, want this Mac only — it never picks one", several)
	}
	if several.Refusal == "" {
		t.Error("several candidates: no Refusal — a refusal that says nothing is one the operator cannot act on")
	}
	if !reflect.DeepEqual(several.Candidates, []string{"100.101.102.103", "100.64.7.7"}) { // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
		t.Errorf("several candidates: Candidates = %v, want both — a refusal names what it refused to choose between", several.Candidates)
	}

	none := Private("", nil, "no address matched")
	if !none.LoopbackOnly() || none.Refusal == "" {
		t.Errorf("no candidate: plan = %+v, want this Mac only with a reason", none)
	}
}

// A Host no listener takes is loopback with a reason, not an error and not a
// wider bind. config.Validate refuses such a value first, so this is the
// belt-and-braces path; the direction it errs in is the point.
func TestAHostThatCannotBindNarrowsRatherThanWidens(t *testing.T) {
	for _, host := range []string{"not an ip", "192.0.2.5:8080", "", "192.0.2.5\r\nX-Injected: yes"} {
		p := ForHost(host)
		if !p.LoopbackOnly() {
			t.Errorf("ForHost(%q) = %+v, want this Mac only", host, p)
		}
		if p.Refusal == "" {
			t.Errorf("ForHost(%q) narrowed silently — the operator has to be told which address was dropped", host)
		}
	}
}

// A refusal is read by an operator, in the log and in the panel. It names the
// address it is about, and it never claims the network is safe or private:
// this package's strings are held to the same honesty rule as the mark's
// (adr-2609081118587999 rule 1).
func TestARefusalNamesTheAddressAndPromisesNothing(t *testing.T) {
	p := ForHost("not an ip")
	if !strings.Contains(p.Refusal, "not an ip") {
		t.Errorf("Refusal = %q, want it to name the address it is about", p.Refusal)
	}
	for _, word := range []string{"encrypt", "secure", "safe", "vpn", "tailscale", "tailnet"} {
		for _, s := range []string{p.Refusal, Private("", nil, "no address matched").Refusal} {
			if strings.Contains(strings.ToLower(s), word) {
				t.Errorf("a refusal contains %q: %q — it says which address and why, and nothing about what a network is worth", word, s)
			}
		}
	}
}

// The measurement adr-2609091123526871 rests on, kept as a test so that a
// platform or a Go release that changes it fails here rather than in the
// field. Two processes cannot be run from a unit test, but the kernel's answer
// does not depend on that: what is asserted is that an exact duplicate bind is
// refused — which is what makes loopback a contention point — while a
// differing bind on the same port is not, which is why loopback has to be the
// address every mode takes.
func TestLoopbackIsAContentionPointAndADifferingBindIsNot(t *testing.T) {
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback listener available: %v", err)
	}
	p := free.Addr().(*net.TCPAddr).Port
	free.Close()
	// The kernel holds a closed listener's address briefly; retry rather than
	// flake on it.
	var first net.Listener
	for i := 0; i < 20; i++ {
		if first, err = net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p))); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if first == nil {
		t.Skipf("could not retake the loopback port: %v", err)
	}
	defer first.Close()

	if second, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p))); err == nil {
		second.Close()
		t.Error("a second listener took the address the first one holds — the singleton's whole signal is that this is refused, so an instance would no longer detect a peer")
	}
	if second, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(p))); err != nil {
		t.Errorf("the wildcard was refused beside a loopback listener (%v) — the wildcard bind acquires both, so this would stop the default install from starting", err)
	} else {
		second.Close()
	}
}

// A plan that could not take the address it named narrows and says why, and
// there is no way for it to end up wider than it started: WithoutExtra is the
// only change a plan undergoes after it is built.
func TestWithoutExtraNarrowsAndRecordsWhy(t *testing.T) {
	p := ForHost("0.0.0.0").WithoutExtra("could not listen on 0.0.0.0")
	if !p.LoopbackOnly() {
		t.Errorf("plan = %+v, want this Mac only", p)
	}
	if p.Loopback != LoopbackAddr {
		t.Errorf("Loopback = %q — narrowing must never take this Mac's own access with it", p.Loopback)
	}
	if p.Refusal == "" {
		t.Error("no Refusal — a bind that quietly serves less than it was asked to is the fault this whole record is about")
	}
}
