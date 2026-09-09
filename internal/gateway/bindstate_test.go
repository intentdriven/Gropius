//go:build !prod

// Fixes this machine's interface list through netshape.SetEnumerator, which
// the release build compiles out, so this file is compiled out with it.

package gateway

import (
	"reflect"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/app"

	"github.com/intentdriven/Gropius/internal/bind"
	"github.com/intentdriven/Gropius/internal/config"
)

// The amendment's second condition: the choice is always shown
// (adr-2609081118587999). The panel is one of the two surfaces that has to
// show it, so the snapshot carries the address the mode selected — not the
// configuration's Host field, which under this mode is whatever the operator
// last set and is not what the server bound.
func TestThePanelIsToldWhichAddressTheModeSelected(t *testing.T) {
	stubIfaces(t, testIface("lo0", "127.0.0.1"), testIface("utun4", "100.101.102.103")) // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
	cfg := config.Default()
	cfg.BindMode = config.BindModePrivateNetwork

	got := bindState(cfg, bind.Private("100.101.102.103", []string{"100.101.102.103"}, "")) // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
	if got.Mode != config.BindModePrivateNetwork {
		t.Errorf("Mode = %q, want the mode in force", got.Mode)
	}
	if got.Selected != "100.101.102.103" { // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
		t.Errorf("Selected = %q, want the address the mode bound — a selection nobody can see is one nobody can check", got.Selected)
	}
	if got.Refusal != "" {
		t.Errorf("Refusal = %q, want none", got.Refusal)
	}
}

// A mode that fell back to this Mac says so, in the words the resolver used.
// The panel is where an operator finds out that the setting they chose is not
// in force — otherwise the only difference between "serving on the private
// network" and "serving on this Mac because there was nothing to select" is an
// endpoint that is not in the list.
func TestThePanelIsToldWhyTheModeNarrowed(t *testing.T) {
	stubIfaces(t, testIface("lo0", "127.0.0.1"), testIface("en0", "192.0.2.5"))
	cfg := config.Default()
	cfg.BindMode = config.BindModePrivateNetwork

	got := bindState(cfg, bind.Private("", nil, "no address on this Mac is on a private network"))
	if got.Selected != "" {
		t.Errorf("Selected = %q, want none: the mode selected nothing", got.Selected)
	}
	if got.Refusal == "" {
		t.Error("Refusal is empty — the pane would show a mode that is on and not running, with nothing to say why")
	}
}

// What the panel offers is a question about this Mac now, not about the bind
// this process started with: the mode can be chosen only where there is an
// address to choose, and that changes while the server runs. So the candidates
// are read live, and they are read whatever mode is in force — the operator
// deciding whether to switch the mode on is exactly who needs them.
func TestTheCandidatesAreReadLiveAndUnderEveryMode(t *testing.T) {
	cfg := config.Default()
	plan := bind.ForHost("0.0.0.0")

	stubIfaces(t, testIface("lo0", "127.0.0.1"), testIface("en0", "192.0.2.5"))
	if got := bindState(cfg, plan).Candidates; len(got) != 0 {
		t.Errorf("Candidates = %v on a machine with no private network, want none", got)
	}
	stubIfaces(t, testIface("lo0", "127.0.0.1"), testIface("utun4", "100.101.102.103"))                           // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
	if got, want := bindState(cfg, plan).Candidates, []string{"100.101.102.103"}; !reflect.DeepEqual(got, want) { // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
		t.Errorf("Candidates = %v after a tunnel came up, want %v — the pane is stale otherwise", got, want)
	}
}

// The keyless-exposure warning is about who can reach this server, so it goes
// quiet when the answer is nobody off this Mac — including under a mode that
// asked for more and found nothing to bind. It softens on the sockets this
// process holds, which is state Gropius owns; it never softens on the presence
// of a private network, which is state it does not (adr-2609081118587999 rules
// 2 and 4).
func TestTheKeylessWarningIsAboutWhatWasActuallyBound(t *testing.T) {
	stubIfaces(t, testIface("lo0", "127.0.0.1"), testIface("en0", "192.0.2.5"))
	cfg := config.Default()
	cfg.BindMode = config.BindModePrivateNetwork
	cfg.APIKey = ""

	narrowed := newTestControlAppWithBind(t, cfg, bind.Private("", nil, "no address on this Mac is on a private network"))
	if w := warningsOf(narrowed.snapshot()); containsSubstring(w, "reachable by anyone on your network") {
		t.Errorf("warnings = %v — the mode fell back to this Mac, so nobody on the network can reach it", w)
	}

	bound := newTestControlAppWithBind(t, cfg, bind.Private("100.101.102.103", []string{"100.101.102.103"}, "")) // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
	if w := warningsOf(bound.snapshot()); !containsSubstring(w, "reachable by anyone on your network") {
		t.Errorf("warnings = %v — the mode bound an address other machines reach with no key set", w)
	}
}

// newTestControlAppWithBind is newTestControlApp's sibling for a bind that is
// not simply the Host field: the plan a process acquired is what the panel
// reports, and a mode that narrowed is exactly the case that differs.
func newTestControlAppWithBind(t *testing.T, cfg config.Config, plan bind.Plan) *Control {
	t.Helper()
	a, err := app.New(app.Options{Paths: config.NewPaths(t.TempDir()), Config: cfg, Bind: plan})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return &Control{App: a}
}

func warningsOf(st State) []string { return st.Warnings }

func containsSubstring(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

// The warning and the endpoint list beside it must read the same source, and
// that source is the sockets this process holds.
//
// The gap is reachable without a restart: the operator clears the API key and
// switches the bind to this Mac, the stored configuration goes quiet, and the
// listeners do not move — so the panel would go on handing out LAN URLs for an
// unauthenticated server with nothing said about it. What is bound is what
// decides, in both places.
func TestTheKeylessWarningFollowsTheSocketsAndNotTheStoredBind(t *testing.T) {
	stubIfaces(t, testIface("lo0", "127.0.0.1"), testIface("en0", "192.0.2.5"))
	cfg := config.Default()
	cfg.Host = "127.0.0.1" // saved, and not yet in force
	cfg.APIKey = ""

	ctrl := newTestControlAppWithBind(t, cfg, bind.ForHost("0.0.0.0"))
	st := ctrl.snapshot()
	if !containsSubstring(warningsOf(st), "reachable by anyone on your network") {
		t.Errorf("warnings = %v — the process is still bound to the wildcard, and the list beside this says so", warningsOf(st))
	}
	if len(st.Endpoints) < 2 {
		t.Fatalf("endpoints = %v — this test is only meaningful while the list offers a LAN address", urls(st.Endpoints))
	}
}

// BindState says what the RUNNING bind is, so the mode that produced the plan
// has to come from the plan. Reading it from the live configuration let a
// wildcard bind be reported as the private-network mode's selection —
// "the mode selected 0.0.0.0" — on the surface the amendment's second
// condition rests on, and a hand-edited file or a save is enough to reach it.
func TestASelectionIsOnlyEverReportedForTheModeThatProducedIt(t *testing.T) {
	stubIfaces(t, testIface("lo0", "127.0.0.1"), testIface("utun4", "100.101.102.103")) // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
	cfg := config.Default()
	cfg.Host = "0.0.0.0"
	cfg.BindMode = config.BindModePrivateNetwork // saved, and not yet in force

	got := bindState(cfg, bind.ForHost("0.0.0.0"))
	if got.Selected != "" {
		t.Errorf("Selected = %q — the running bind came from the Host field, so the mode selected nothing", got.Selected)
	}
	if got.Mode != config.BindModePrivateNetwork {
		t.Errorf("Mode = %q, want the mode the pane has to show as chosen", got.Mode)
	}
}
