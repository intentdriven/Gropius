package config

import (
	"os"
	"os/exec"
	"strings"
)

// localHostName returns the name this Mac answers to on the network, i.e. the
// "<name>.local" that Bonjour publishes.
//
// This is NOT os.Hostname(). The two genuinely differ: os.Hostname() returns the
// DNS/BSD hostname (often something short like "Mac"), while Bonjour uses the
// *LocalHostName* from System Settings (e.g. "AlicesMac"). Advertising the wrong
// one produces a URL that quietly resolves to loopback on this machine — so it
// looks fine while testing — and fails to resolve from every other machine on
// the network, which is exactly the case that matters.
func LocalHostName() string {
	// scutil is the authoritative source; it is what System Settings edits and
	// what mDNSResponder publishes.
	if out, err := exec.Command("scutil", "--get", "LocalHostName").Output(); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return name
		}
	}
	// Fall back to the BSD hostname. Better a possibly-wrong name than none.
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(h, ".local")
}
