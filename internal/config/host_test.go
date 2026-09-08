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
		{"::", true, "the IPv6 wildcard"},
		{"::1", true, "IPv6 loopback"},
		{"[::1]", true, "IPv6 loopback in the form that actually binds"},
		{"[fd00::1]", true, "a specific IPv6 address in the form that actually binds"},
		{"fd00::1", true, "and unbracketed, which config.json may still carry"},
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
