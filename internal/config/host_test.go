package config

import "testing"

// Host is the one setting an operator can only reach by editing config.json by
// hand (the panel offers the wildcard and loopback and nothing else, iss-7), so
// it is the one setting that arrives unchecked. Until now the only thing asked
// of it was that it was not empty: a value that cannot be bound at all was
// accepted, the listener failed, and the process exited — and before it did,
// the same value went into the endpoint list as a base URL, control characters
// and all.
//
// What is accepted is what the listener accepts, no less: cmd/gropius builds
// the address as "<host>:<port>", so a bracketed IPv6 literal binds and its
// unbracketed form does not, and both spellings have to stay legal here or a
// working install stops starting.
func TestValidBindHostAcceptsWhatCanBeBoundAndRefusesWhatCannot(t *testing.T) {
	cases := []struct {
		host string
		want bool
		why  string
	}{
		{"0.0.0.0", true, "the shipping default"},
		{"127.0.0.1", true, "the loopback bind"},
		{"localhost", true, "the loopback bind by name"},
		{"192.168.1.5", true, "a specific LAN address"},
		{"[::]", true, "the IPv6 wildcard in the form that actually binds"},
		{"[::1]", true, "IPv6 loopback in the form that actually binds"},
		{"[fd00::1]", true, "a specific IPv6 address in the form that actually binds"},
		{"[fe80::1%en0]", true, "a link-local bind carries a zone and binds"},
		{"alices-mac.local", true, "a name is a bind too"},
		{"alices-mac", true, "an unqualified name"},

		{"", false, "empty was already refused and stays refused"},
		{"192.168.1.5:8080", false, "a host and a port is not a host"},
		{"[fd00::1]:8080", false, "nor bracketed with a port"},
		{"not an ip", false, "spaces are in neither an address nor a name"},
		{"10.0.0.1\r\nX-Injected: yes", false, "CR and LF must never reach a URL"},
		{"10.0.0.1\n", false, "a trailing newline is the same fault"},
		{"[::1", false, "an unclosed bracket"},
		{"::1]", false, "an unopened bracket"},
		{"[]", false, "brackets around nothing"},
		{"-leading-dash.local", false, "not a legal label"},
		{"trailing-dash-.local", false, "nor is this one"},
		{"a..b", false, "an empty label"},
		{"::", false, "the IPv6 wildcard unbracketed: \"::\" + \":11535\" is not an address"},
		{"::1", false, "nor is IPv6 loopback unbracketed — measured, the listen fails"},
		{"fd00::1", false, "nor a routable one"},
		{"fe80::1%en0", false, "nor a zoned one"},
	}
	for _, c := range cases {
		if got := ValidBindHost(c.host); got != c.want {
			t.Errorf("ValidBindHost(%q) = %v, want %v — %s", c.host, got, c.want, c.why)
		}
	}
}

// Validate is where a hand-edited config.json is caught. A Host it refuses
// makes Load refuse the file, which main already treats as "lock down to
// loopback" — the failure is closed, and it is the same one a corrupt file
// gets.
func TestValidateRefusesAHostThatCannotBeBound(t *testing.T) {
	for _, host := range []string{"192.168.1.5:8080", "not an ip", "10.0.0.1\r\nX-Injected: yes"} {
		c := Default()
		c.Host = host
		if err := c.Validate(); err == nil {
			t.Errorf("Validate() accepted Host %q — it cannot be bound, and it reaches the endpoint list as a base URL", host)
		}
	}
	for _, host := range []string{"0.0.0.0", "127.0.0.1", "localhost", "[::1]", "[fd00::1]", "alices-mac.local"} {
		c := Default()
		c.Host = host
		if err := c.Validate(); err != nil {
			t.Errorf("Validate() refused Host %q: %v — this one binds, so refusing it stops a working install from starting", host, err)
		}
	}
}

// URLHost is the other half: the spelling a URL needs rather than the spelling
// a bind needs. The brackets come off exactly once, and a host a URL cannot
// carry at all — a zone, which is legal in a bind and not in an address
// literal — is refused rather than pasted in and hoped over.
func TestURLHostStripsTheBracketsABindNeeds(t *testing.T) {
	cases := []struct {
		host string
		want string
		ok   bool
	}{
		{"[::1]", "::1", true},
		{"[fd00::1]", "fd00::1", true},
		{"fd00::1", "fd00::1", true},
		{"192.168.1.5", "192.168.1.5", true},
		{"alices-mac.local", "alices-mac.local", true},
		{"[fe80::1%en0]", "", false},
		{"fe80::1%en0", "", false},
		{"192.168.1.5:8080", "", false},
		{"not an ip", "", false},
		{"10.0.0.1\r\nX-Injected: yes", "", false},
	}
	for _, c := range cases {
		got, ok := URLHost(c.host)
		if got != c.want || ok != c.ok {
			t.Errorf("URLHost(%q) = %q, %v; want %q, %v", c.host, got, ok, c.want, c.ok)
		}
	}
}

// ExposedToLAN is read by everything that decides how open this server is: the
// generate-a-key-or-drop-to-loopback branch in cmd/gropius, the eviction-grace
// key requirement, the panel's warning, whether Bonjour advertises the service
// at all, and whether the endpoint list enumerates this machine's addresses.
// It string-compared Host, so it never saw the bracketed spelling of an IPv6
// address — the spelling that is the only one a listener actually takes.
//
// A "[::1]" bind is loopback. It was read as LAN-exposed: an API key was
// generated and persisted for a server nothing off this Mac can reach, the log
// told the operator "this server binds a LAN address", and Bonjour advertised a
// service no machine on the LAN could connect to. It errs closed, and telling
// an operator on a loopback bind that they are exposed is a false statement
// about their exposure — exactly the class adr-2609081118587999 exists to
// refuse, on the surface where it does the most damage.
//
// The matrix below is not exhaustive, and calling it that was itself a claim
// that did not hold: it said so through three reviews, and each of the three
// found a spelling it did not contain — first "[::1]", then "0", then
// "LOCALHOST", "LocalHost" and "localhost.", every one of them a loopback bind
// reported as a LAN one. What it is, instead, is a regression table: every
// spelling any review has found is kept here, on both sides of the answer, so
// that none of them comes back. A spelling not listed has not been ruled out;
// it has not been looked at.
func TestExposedToLANReadsEverySpellingOfALoopbackBind(t *testing.T) {
	cases := []struct {
		host string
		want bool
		why  string
	}{
		// Not exposed: loopback, in every spelling that binds.
		{"127.0.0.1", false, "the loopback bind"},
		{"127.0.0.53", false, "the whole 127/8 is loopback"},
		{"localhost", false, "loopback by name"},
		{"::1", false, "IPv6 loopback unbracketed, which config.json may carry"},
		{"[::1]", false, "IPv6 loopback in the form that actually binds"},
		{"[::1%lo0]", false, "and with the zone a link-local spelling carries"},
		{"[localhost]", false, "a bracketed name is the same bind as the bare one"},
		{"LOCALHOST", false, "the resolver is case-insensitive and this binds 127.0.0.1 only"},
		{"LocalHost", false, "so is this one"},
		{"localhost.", false, "a fully qualified name is the same name"},
		{"LOCALHOST.", false, "and both at once"},
		{"[LocalHost.]", false, "and bracketed, which is how an operator writes a bind"},

		// Exposed: everything else, including everything malformed.
		{"", true, "empty is the wildcard"},
		{"0.0.0.0", true, "the shipping default"},
		{"::", true, "the IPv6 wildcard"},
		{"[::]", true, "and bracketed"},
		{"192.168.1.10", true, "a specific LAN address is as reachable as the wildcard"},
		{"10.0.0.5", true, "so is this one"},
		{"fe80::1", true, "link-local is not loopback"},
		{"[fe80::1]", true, "nor bracketed"},
		{"[fe80::1%en0]", true, "nor with a zone"},
		{"[fd00::1]", true, "a routable IPv6 address"},
		{"mac-studio.local", true, "a name resolves to who knows what: fail closed"},
		{"0", true, "the resolver reads this as the unspecified address, so the listener takes every interface"},
		{"192.168.1.5:8080", true, "not bindable at all, and must not read as safe"},
		{"[::1", true, "an unclosed bracket is not a loopback bind"},
		{"::1]", true, "nor an unopened one"},
		{"[]", true, "nor brackets around nothing"},
		{"127.0.0.1\r\nX-Injected: yes", true, "loopback with a payload stapled to it is not loopback"},
		{"localhost.evil.example", true, "a name that merely starts with localhost is not loopback"},
	}
	for _, c := range cases {
		cfg := Default()
		cfg.Host = c.host
		if got := cfg.ExposedToLAN(); got != c.want {
			t.Errorf("Host %q: ExposedToLAN() = %v, want %v — %s", c.host, got, c.want, c.why)
		}
	}
}

// A value that no DNS name can be is a legacy IPv4 spelling, and the resolver
// treats it as one. Measured: net.Listen("tcp", "0:0") returns a listener on
// "[::]" — every interface — while boundAddr read "0" as a name, so the panel
// offered "http://0:11535/v1" and listed nothing else: a wildcard bind
// under-reported, which is the inverse of the dead-address fault this branch
// exists to close. "127.1", "2130706433" and "0x7f.1" all resolve to
// 127.0.0.1, where the error runs the other way: a loopback bind that
// ExposedToLAN reads as a name and reports as LAN-exposed.
//
// RFC 1123 has the answer already: the top label of a host name is alphabetic.
// A value that does not satisfy that is not a name, and if it is not an address
// either it is refused — which fails closed, since Load then refuses the file
// and cmd/gropius locks the bind down to loopback.
func TestABindHostThatIsSecretlyAnAddressIsRefused(t *testing.T) {
	for _, host := range []string{
		"0", "127.1", "2130706433", "0x7f.1", "0177.0.0.1", "10.1",
		// Hex, which "the top label carries a letter" read as a name because
		// "x", "a"-"f" are letters. Measured: every one of these binds, and
		// the first five bind "[::]" — every interface on the Mac — while
		// nothing downstream enumerates a single address of it.
		"0x0", "0X0", "0x00000000", "0x0.0x0.0x0.0x0", "0.0.0.0x0",
		"0x7f000001", "0x7f.0x0.0x0.0x1", "127.0.0.0x1",
	} {
		if ValidBindHost(host) {
			t.Errorf("ValidBindHost(%q) accepted it — getaddrinfo resolves it as an IPv4 literal, so the listener binds an address this value does not name and the panel reports the name instead", host)
		}
		c := Default()
		c.Host = host
		if err := c.Validate(); err == nil {
			t.Errorf("Validate() accepted Host %q — the bind and the endpoint list then disagree about which addresses answer", host)
		}
	}
	// Names whose top label carries a letter are names, and stay bindable.
	for _, host := range []string{"alices-mac", "alices-mac.local", "mac-0", "0a", "x0.y1.local", "srv01"} {
		if !ValidBindHost(host) {
			t.Errorf("ValidBindHost(%q) refused it — this is a host name, and refusing it stops a working install from starting", host)
		}
	}
	// And addresses are still addresses, whatever their labels look like.
	// (An IPv6 literal appears here only bracketed: unbracketed it is not a
	// bind at all — see the table above.)
	for _, host := range []string{"0.0.0.0", "127.0.0.1", "192.168.1.5", "[::1]", "[fd00::1]"} {
		if !ValidBindHost(host) {
			t.Errorf("ValidBindHost(%q) refused an address", host)
		}
	}
}
