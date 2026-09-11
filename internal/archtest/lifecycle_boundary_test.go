package archtest_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The lifecycle verbs are not reachable from the HTTP control plane, and the
// dependency runs one way: from cmd/gropius down.
//
// WHY IT IS A RULE AND NOT A PREFERENCE. internal/lifecycle is where a verb
// removes files, raises an authorization panel and replaces the running
// application. A route that could reach it — through a handler, through a
// helper the handler already calls, through a package the gateway imports for
// something else — would be a way for an HTTP request arriving on this Mac to
// drive a self-replacement or an elevation. No amount of care in the handlers
// closes that; not being on the path does.
//
// It carries a second rule at the same time. adr-2609111126115848 lets a
// diagnostic a person invokes report an observed signal about state Gropius
// does not own — the firewall entry — on three conditions, and the third is
// that it gates nothing, armed rather than asserted. This is that arming: the
// enforcement path cannot see the package that reads the signal, so no
// admission, authentication or validation decision can be made from it even by
// accident.
//
// WHAT IT CANNOT DO, stated so a green run is not over-read. Like every rule in
// this directory it catches the accident, which is what a reviewer skimming a
// diff is most likely to wave through. Nothing here stops code that means to
// re-derive a signal for itself: anything linked into this process can run the
// firewall query in three lines without naming this package. The superseded
// record says exactly that about its own guards, and it stays true here.
const lifecyclePkg = "github.com/intentdriven/Gropius/internal/lifecycle"

// controlPlaneRoots are the packages the spec names as the control plane's
// path. Their dependency closures are computed rather than listed, so a package
// the gateway starts importing next year is on the path the day it is imported
// and needs no edit here — internal/app and internal/config are already on it
// that way.
//
// internal/ui is a root of its own because nothing imports it but the command:
// it serves the panel's assets, and a helper there that reached a lifecycle
// verb would put the verbs behind the same surface by another door.
var controlPlaneRoots = []string{
	"github.com/intentdriven/Gropius/internal/gateway",
	"github.com/intentdriven/Gropius/internal/ui",
}

// offTheControlPlane is every package in the module that is not on that path,
// each with the reason. It exists so TestEveryPackageIsJudgedByTheBoundary can
// fail on a package that is on neither side: the control-plane side is computed
// and therefore never stale, but a package nothing imports yet would otherwise
// be uncovered by default and the rule would quietly stop applying to it.
var offTheControlPlane = map[string]string{
	lifecyclePkg: "is the package the rule is about",
	"github.com/intentdriven/Gropius/cmd/gropius":        "is the one place both sides meet, by design: it dispatches a verb typed in a terminal and it starts the server. Nothing imports it, so it is a leaf and not a path",
	"github.com/intentdriven/Gropius/cmd/gropius-site":   "renders the landing page offline and serves nothing",
	"github.com/intentdriven/Gropius/internal/archtest":  "is these rules",
	"github.com/intentdriven/Gropius/internal/mlxtest":   "test helpers; nothing ships in the binary",
	"github.com/intentdriven/Gropius/internal/sitetest":  "test helpers for the landing-page renderer",
	"github.com/intentdriven/Gropius/internal/discovery": "advertises the server over mDNS. Only the command imports it, and it answers the network rather than being asked anything by a route; a lifecycle verb reaching it would be advertising from a terminal command, which is why it is written down rather than left uncovered",
	"github.com/intentdriven/Gropius/internal/applog":    "builds the process's own log and imports nothing of ours",
	"github.com/intentdriven/Gropius/internal/instance":  "classifies the process on the server port. The command and the lifecycle verbs both ask it; it imports only the paths, and a route cannot reach a verb through it",
}

// No package on the control plane's path may see internal/lifecycle, at any
// depth.
func TestTheControlPlaneCannotSeeTheLifecycleVerbs(t *testing.T) {
	for _, root := range controlPlaneRoots {
		t.Run(root, func(t *testing.T) {
			for _, dep := range depsOf(t, root) {
				if dep == lifecyclePkg {
					t.Errorf("%s depends on %s — no route may reach a verb that removes files, elevates or replaces the running application (spc-2609111029315861 criterion 25, adr-2609111126115848 condition 3)", root, lifecyclePkg)
				}
			}
		})
	}
}

// And the dependency may not run the other way either. A lifecycle verb that
// imported the gateway would put the whole control plane in the closure of a
// command a person types, and the next person to add a route would have no way
// to see that the rule above had stopped meaning anything.
func TestTheLifecycleVerbsCannotSeeTheControlPlane(t *testing.T) {
	for _, dep := range depsOf(t, lifecyclePkg) {
		for _, root := range controlPlaneRoots {
			if dep == root {
				t.Errorf("%s depends on %s — the verbs read the control plane over loopback like any other client, and decode what they need structurally", lifecyclePkg, root)
			}
		}
	}
}

// Every package in the module is on the control plane's path or is recorded as
// not being on it, with the reason. Without this a package added later is
// covered by nothing and passes in silence.
func TestEveryPackageIsJudgedByTheBoundary(t *testing.T) {
	onPath := map[string]bool{}
	for _, root := range controlPlaneRoots {
		for _, dep := range depsOf(t, root) {
			if strings.HasPrefix(dep, "github.com/intentdriven/Gropius/") {
				onPath[dep] = true
			}
		}
	}

	const all = "github.com/intentdriven/Gropius/..."
	out, err := exec.Command("go", "list", all).CombinedOutput()
	if err != nil {
		t.Fatalf("go list %s: %v\n%s", all, err, out)
	}
	seen := map[string]bool{}
	for _, pkg := range strings.Fields(string(out)) {
		seen[pkg] = true
		if onPath[pkg] || offTheControlPlane[pkg] != "" {
			continue
		}
		t.Errorf("%s is neither on the control plane's path nor recorded as off it, so no rule here covers it — decide which it is: if a route can reach it, it belongs in a control-plane closure; otherwise add it to offTheControlPlane with the reason (spc-2609111029315861 criterion 25)", pkg)
	}
	// The other direction: a stale entry reads as coverage and is not.
	for pkg := range offTheControlPlane {
		if !seen[pkg] {
			t.Errorf("offTheControlPlane names %s, which is not a package in this module any more — remove it rather than leaving a rule that matches nothing", pkg)
		}
	}
	for _, root := range controlPlaneRoots {
		if !seen[root] {
			t.Errorf("controlPlaneRoots names %s, which is not a package in this module any more", root)
		}
	}
	// A package recorded as off the path that has since been pulled onto it is
	// the failure this file exists for, and it must not read as a pair of
	// contradictory lists.
	for pkg := range offTheControlPlane {
		if onPath[pkg] {
			t.Errorf("%s is recorded as off the control plane's path and something on that path now imports it — the record and the code disagree, and the code is what runs", pkg)
		}
	}
}

// depsOf is the closure of one package, the module's own and everything under
// it, as the toolchain resolves it.
func depsOf(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", pkg, err, out)
	}
	return strings.Fields(string(out))
}

// No verb in internal/lifecycle writes the settings file.
//
// config.json is single-writer state (iss-2609062045106963): the control plane
// writes it, and a save there is serialized behind one lock. A terminal verb
// that wrote it would be a second writer with no lock between them, and the
// two would race on a file that decides who can reach this server. `gropius
// config show` therefore reads and answers, which is a decision the record
// took rather than a stage on the way to a writing verb
// (itd-2609081259493890) — and this is what keeps the decision from being
// undone by a diff nobody read closely.
//
// A scan for the one function that writes it, which is honest about being one:
// anything in this package could open the path and write bytes without naming
// config.Save. What it catches is the ordinary way the second writer arrives —
// somebody reaching for the save the panel already uses — and it makes adding
// one a line in a diff somebody reviews.
func TestNoLifecycleVerbWritesTheSettingsFile(t *testing.T) {
	root := repoRootDir(t)
	dir := filepath.Join(root, "internal", "lifecycle")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		seen++
		src := readRepoFile(t, root, filepath.Join("internal", "lifecycle", name))
		// Two spellings of "save a setting". REMOVING the file is not one of
		// them and is not caught here: uninstall lists it among the things an
		// installation put on this Mac, which is the whole of what uninstall
		// does and is a deliberate act with its own criteria. The rule is
		// about a second writer of settings, not about a verb that takes the
		// installation away.
		for _, writer := range []string{"config.Save(", "WriteFile(env.Paths.Config"} {
			if strings.Contains(src, writer) {
				t.Errorf("internal/lifecycle/%s names %s, so a verb can write the settings file — the "+
					"control plane is its only writer, and a second one races it on the file that decides "+
					"who can reach this server (iss-2609062045106963)", name, writer)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no source files found in internal/lifecycle, so this scan is reading the wrong directory")
	}
}
