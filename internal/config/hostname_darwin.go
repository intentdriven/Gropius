package config

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

// LocalHostName returns the name this Mac answers to on the network, i.e. the
// "<name>.local" that Bonjour publishes.
//
// This is NOT os.Hostname(). The two genuinely differ: os.Hostname() returns the
// DNS/BSD hostname (often something short like "Mac"), while Bonjour uses the
// *LocalHostName* from System Settings (e.g. "AlicesMac"). Advertising the wrong
// one produces a URL that quietly resolves to loopback on this machine — so it
// looks fine while testing — and fails to resolve from every other machine on
// the network, which is exactly the case that matters.
//
// The result is memoized: resolving it forks a `scutil` subprocess, and this is
// called from the state snapshot that the SSE stream re-renders on every registry
// change — up to ~10x/sec during a download, per open dashboard. That is 10+
// fork+execs a second competing with token generation on the same Mac, all for a
// value that does not change during a process's lifetime.
var (
	hostnameOnce sync.Once
	hostnameVal  string
)

func LocalHostName() string {
	hostnameOnce.Do(func() { hostnameVal = resolveLocalHostName() })
	return hostnameVal
}

func resolveLocalHostName() string {
	// scutil is the authoritative source; it is what System Settings edits and
	// what mDNSResponder publishes.
	//
	// The absolute path, not the name, as internal/capability does for sysctl.
	// This runs with no user gesture at all — memoized behind the sync.Once
	// above, reached from the endpoint list the menu bar builds and the control
	// panel's snapshot re-renders — so it runs once early in every account on
	// this Mac that launches Gropius. Gropius is built for a Mac shared by
	// several accounts, where a group-writable directory ahead of /usr/sbin on
	// this account's PATH is another account's way into this process; and even
	// with nobody hostile, a bare name is no proof of which tool answered.
	if out, err := exec.Command("/usr/sbin/scutil", "--get", "LocalHostName").Output(); err == nil {
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
