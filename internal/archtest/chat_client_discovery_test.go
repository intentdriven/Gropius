package archtest_test

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/discovery"
)

// The chat client is a second surface onto the same server, and two of its
// values are not its own to choose: the address it opens on, and the mDNS
// service type it browses for. Both are the server's, and a client holding a
// stale copy of either fails in a way the user cannot diagnose -- a first run
// that reaches nothing, or a network search that finds nothing while the
// server is advertising a metre away. Swift is compiled by client/build.sh,
// not by `go test`, so these are read out of the client's own files here.

// serverURLDefault matches the AppStorage declaration that seeds the client's
// stored server address on first launch. The placeholder in SettingsView is a
// separate string and is deliberately not matched: it illustrates the shape of
// an address on another Mac and may name a persona's host.
var serverURLDefault = regexp.MustCompile(
	`@AppStorage\("serverURL"\)\s+var\s+serverURL\s*:\s*String\s*=\s*"([^"]*)"`)

// TestChatClientOpensOnTheServersOwnDefaultAddress holds the client's first-run
// address to the endpoint a freshly installed server listens on.
//
// The documented order installs the server and then the client on the same
// Mac, so loopback on the server's default port is the one address that is
// right before the user has told the client anything. It also has to be an
// address that resolves: the composer is disabled until a server answers, so a
// default that reaches nothing leaves a first-run user with a dead text field.
func TestChatClientOpensOnTheServersOwnDefaultAddress(t *testing.T) {
	root := repoRootDir(t)
	source := readRepoFile(t, root, filepath.Join("client", "GropiusChat", "GropiusChat.swift"))

	m := serverURLDefault.FindStringSubmatch(source)
	if m == nil {
		t.Fatal(`client/GropiusChat/GropiusChat.swift declares no @AppStorage("serverURL") default; ` +
			"the address the client opens on is unchecked")
	}
	want := fmt.Sprintf("http://localhost:%d", config.Default().Port)
	if m[1] != want {
		t.Errorf("the chat client's first-run server address is %q; a default server listens on %q",
			m[1], want)
	}
}

// TestChatClientBrowsesForTheAdvertisedServiceType holds the client's Bonjour
// browse to the service type the server publishes.
//
// Two declarations have to agree with discovery.ServiceType, and each fails
// silently on its own: a browse for another type simply never returns a
// result, and macOS Local Network Privacy hands an undeclared service type an
// empty result set rather than an error. Either way the client reports "no
// servers found" while the server is advertising.
func TestChatClientBrowsesForTheAdvertisedServiceType(t *testing.T) {
	root := repoRootDir(t)

	t.Run("client source", func(t *testing.T) {
		source := readRepoFile(t, root, filepath.Join("client", "GropiusChat", "GropiusChat.swift"))
		if !strings.Contains(source, `"`+discovery.ServiceType+`"`) {
			t.Errorf("client/GropiusChat/GropiusChat.swift names no %q service type; "+
				"the server advertises on it and nothing in the client browses for it",
				discovery.ServiceType)
		}
	})

	t.Run("bundle declaration", func(t *testing.T) {
		declared := plistStringArray(t, filepath.Join(root, "client", "Info.plist"), "NSBonjourServices")
		// macOS accepts the type with or without the trailing dot the DNS-SD
		// wire format uses; both are the same declaration.
		if !slices.Contains(declared, discovery.ServiceType) &&
			!slices.Contains(declared, discovery.ServiceType+".") {
			t.Errorf("client/Info.plist declares NSBonjourServices %v, which does not include %q; "+
				"macOS returns an empty browse rather than an error for an undeclared type",
				declared, discovery.ServiceType)
		}
	})
}
