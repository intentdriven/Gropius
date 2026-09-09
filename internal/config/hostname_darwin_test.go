package config

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The endpoint Gropius hands to other machines must use the Bonjour name
// (LocalHostName), not the BSD hostname. On this machine they differ: the BSD
// hostname is "Mac" while Bonjour publishes "AlicesMac". "Mac.local" resolves to
// loopback locally — so the bug is invisible when testing on the same Mac — and
// fails to resolve from every other machine on the network.
func TestLocalHostNameMatchesBonjourNotBSDHostname(t *testing.T) {
	// The same absolute path the code under test uses: an oracle resolved
	// through PATH could answer from a different binary than the one Gropius
	// asks, and this test would then be comparing two machines' answers.
	out, err := exec.Command("/usr/sbin/scutil", "--get", "LocalHostName").Output()
	if err != nil {
		t.Skip("scutil unavailable")
	}
	want := strings.TrimSpace(string(out))
	if want == "" {
		t.Skip("no LocalHostName set on this machine")
	}

	got := LocalHostName()
	if got != want {
		t.Errorf("LocalHostName() = %q, want %q (the name Bonjour publishes)", got, want)
	}

	// Guard the specific mistake: falling back to os.Hostname() when the two
	// disagree would produce a URL that other machines cannot resolve.
	if bsd, err := os.Hostname(); err == nil {
		bsd = strings.TrimSuffix(bsd, ".local")
		if bsd != want && got == bsd {
			t.Errorf("LocalHostName() returned the BSD hostname %q instead of the Bonjour name %q; "+
				"the advertised endpoint would not resolve from other machines", bsd, want)
		}
	}
}

func TestLocalHostNameIsNotEmpty(t *testing.T) {
	if LocalHostName() == "" {
		t.Error("LocalHostName() returned empty; the Connect tab would show a broken URL")
	}
}
