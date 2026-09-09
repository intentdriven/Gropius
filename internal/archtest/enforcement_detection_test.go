package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// adr-2609081118587999: detecting a private-network daemon may inform what
// Gropius reports and may never inform what it enforces.
//
// The reason is that the detection is not a fact about Gropius. It is an
// inference about another process's state, and that state changes without
// Gropius being told — the operator logs out, a key expires, an ACL is
// edited, the daemon is stopped — while the bind address stays exactly as it
// was. A check that relaxed on the inference would fail open on every one of
// those transitions, silently, on a machine already reachable by anyone on the
// network.
//
// WHAT THESE TESTS ARE. They catch a maintainer coupling enforcement to the
// app's own classifier BY ACCIDENT. That is their whole value, and it is worth
// having: the accident is the likely failure, and it is the one a reviewer
// skimming a diff is most likely to wave through.
//
// They are not, and cannot become, a barrier against code that means to read
// the classification, for a reason that has nothing to do with how tight the
// scan is: THE CLASSIFICATION IS NOT SECRET. Anything linked into this process
// can call net.Interfaces() and re-derive it in three lines — walk the
// interfaces, look for a name starting "utun" carrying an address in
// 100.64.0.0/10 — without touching internal/netshape, gateway.Endpoint or the
// word "network" at all. No scan over identifiers can prevent that, and none of
// what follows tries to.
//
// So the ADR's closing sentence is exact and stays: the rule is not
// mechanically enforced. It is a design constraint enforced by review, and
// these tests are the accidents review does not have to catch.
//
// WHAT THEY CLOSE. Routes that name the detection in Go source, in the packages
// scanned below:
//
//   - the classifier package, closed by an import rule wherever an import rule
//     can reach, and by name where it cannot;
//   - the field the answer travels on, Network, wherever it is named — read as
//     a selector, written as a composite-literal key, or declared;
//   - the type it travels in, gateway.Endpoint, whose value carries the answer
//     even when the field is never named — comparing an Endpoint against a
//     literal of itself is a read of Network that mentions neither netshape nor
//     Network;
//   - cmd/gropius, which no dependency rule can cover (it imports the gateway,
//     which imports the classifier) and which holds the strongest enforcement
//     decision in the app: the generate-a-key-or-drop-to-loopback branch;
//   - internal/ui, which imports nothing of ours today and is therefore free to
//     import the gateway tomorrow, while cmd/gropius already imports IT. A
//     helper there reading Endpoint.Network and returning a bool is a route
//     into the enforcement path that no rule saw. It is scanned for the same
//     names as the gateway, with nothing allowlisted;
//   - the private-network resolver, which is the one carve-out. The
//     2026-09-08 amendment to adr-2609081118587999 lets the classifier choose
//     the address the private-network bind mode acquires, and
//     adr-2609091123526871 rule 10 puts that in one package. So the resolver's
//     package name is scanned for exactly as the classifier's is, everywhere
//     the classifier's is: reaching the detection THROUGH the carve-out has to
//     be as loud as reaching it directly, or the exception becomes a laundry.
//     Two declarations in cmd/gropius may name it, listed below with what each
//     is for; internal/gateway's list says the same for the panel's side.
//
// WHAT THEY DO NOT CLOSE, in full, because a list that says "and some other
// things" is the overclaim this comment exists to retire. Every one of these
// compiles, reads the classification, and leaves this file green:
//
//  1. a JSON round-trip over an Endpoint, testing the bytes for a needle built
//     at runtime;
//  2. a struct comparison — `e != Endpoint{URL: e.URL}` — whose
//     composite-literal key is an Ident and never a SelectorExpr (closed by
//     name, not by shape: spell the type differently and it is back);
//  3. fmt's %v, encoding/gob, or reflect over the same value;
//  4. anything in a package these rules do not scan;
//  5. a package-level variable written from inside an allowlisted declaration
//     and read from anywhere;
//  6. net.Interfaces(), re-deriving the classification from scratch;
//  7. the endpoint list's own SHAPE — see
//     TestTheDetectionIsCarriedByTheShapeOfTheListItself below, which is the
//     one that cannot be fixed even in principle;
//  8. anything at all, once the scan's own file is edited.
const (
	detectionPkg  = "github.com/intentdriven/Gropius/internal/netshape"
	detectionName = "netshape"
	// detectionField is how the classifier's answer leaves the classifier.
	// Naming it is reading the detection, whatever the reader calls it, and it
	// is matched as a bare identifier rather than as a selector because a
	// composite-literal key is an identifier too: `Endpoint{Network: x}` writes
	// the field without any selector expression existing anywhere.
	detectionField = "Network"
	// detectionType is the type the answer travels in. A value of it carries
	// Network whether or not anything names the field, so the type is closed as
	// well: outside the handful of declarations that build the endpoint list,
	// nothing in internal/gateway or cmd/gropius may name it at all.
	detectionType = "Endpoint"
	// resolverPkg is the carve-out: the one package outside the endpoint list
	// that may read the classifier, because the private-network bind mode has
	// to resolve an address from it (adr-2609081118587999's 2026-09-08
	// amendment, adr-2609091123526871 rule 10).
	//
	// It is scanned for by name everywhere the classifier is, and for the same
	// reason. A helper in the resolver taking an interface list and returning a
	// bool would be the classification with a different spelling, and reaching
	// it from the gateway, the panel or the command has to be exactly as loud
	// as reaching netshape directly — otherwise the carve-out is a laundry and
	// the amendment widened by accident, which is the failure this file exists
	// to make impossible to do quietly.
	resolverPkg  = "github.com/intentdriven/Gropius/internal/bind/private"
	resolverName = "private"
)

// enforcementPath is every package that decides who may reach this server or
// what it will do for them: the bind address and its exposure test, the
// validation rules, the model pool's admission, the app that wires them
// together, and this Mac's own measurements the budget comes from. None of
// them may have the detection anywhere in its dependency closure.
//
// internal/gateway and cmd/gropius are deliberately absent — the first has to
// see the classifier because the endpoint list lives in it, the second reaches
// it through the first — and both are covered by the source-scoped rules
// below instead.
var enforcementPath = []string{
	"github.com/intentdriven/Gropius/internal/config",
	"github.com/intentdriven/Gropius/internal/bind",
	"github.com/intentdriven/Gropius/internal/runtime",
	"github.com/intentdriven/Gropius/internal/app",
	"github.com/intentdriven/Gropius/internal/capability",
	"github.com/intentdriven/Gropius/internal/hub",
	"github.com/intentdriven/Gropius/internal/registry",
	"github.com/intentdriven/Gropius/internal/discovery",
	"github.com/intentdriven/Gropius/internal/stats",
}

// notEnforcement is every other package in the module, each with the reason it
// is not on the enforcement path. It exists so that
// TestEveryPackageIsOnOneSideOfTheRule can fail on a package that is on
// neither list: enforcementPath is hand-maintained, so without this a package
// added next year is uncovered by default and the rule quietly stops applying
// to it.
var notEnforcement = map[string]string{
	"github.com/intentdriven/Gropius/internal/gateway":  "holds the endpoint list, so it must see the classifier; covered by the source-scoped rule below instead",
	"github.com/intentdriven/Gropius/cmd/gropius":       "reaches the endpoint list through the gateway, so no import rule can cover it; covered by its own source-scoped rule below",
	"github.com/intentdriven/Gropius/internal/netshape": "is the classifier",
	"github.com/intentdriven/Gropius/internal/ui":       "serves the control panel's assets and decides nothing about who may reach the server; presentation is what rule 1 allows. It imports nothing of ours and so could import the gateway, while cmd/gropius already imports it — a helper here reading Endpoint.Network is a route into the enforcement path, which is why TestThePanelPackageDoesNotReadTheDetectionEither scans it with nothing allowlisted",
	"github.com/intentdriven/Gropius/internal/archtest": "is these rules",
	"github.com/intentdriven/Gropius/internal/mlxtest":  "test helpers; nothing ships in the binary",
	"github.com/intentdriven/Gropius/internal/sitetest": "test helpers for the landing-page renderer",
	"github.com/intentdriven/Gropius/cmd/gropius-site":  "renders the landing page offline and serves nothing",
}

// carveOut is the amendment, written down. It is deliberately not part of
// notEnforcement: the resolver DOES decide who may reach this server — it picks
// the address the private-network mode binds — and filing it as "not
// enforcement" would be the quiet widening the amendment forbade in the same
// paragraph that granted it. It is enforcement, it reads the detection, and it
// is the only thing in the module of which both are true.
var carveOut = map[string]string{
	resolverPkg: "resolves the address the private-network bind mode acquires, which is enforcement reading a detection — permitted by adr-2609081118587999's 2026-09-08 amendment, on its two conditions: ambiguity is refused rather than resolved, and the selection is always shown. Nothing else here is opened, and the package is scanned for by name wherever the classifier is",
}

func TestTheEnforcementPathCannotSeeThePrivateNetworkDetection(t *testing.T) {
	for _, pkg := range enforcementPath {
		t.Run(pkg, func(t *testing.T) {
			out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
			if err != nil {
				t.Fatalf("go list -deps %s: %v\n%s", pkg, err, out)
			}
			for _, dep := range strings.Fields(string(out)) {
				if dep == detectionPkg {
					t.Errorf("%s depends on %s — detecting a private network may inform what Gropius reports, never what it enforces (adr-2609081118587999 rule 2)", pkg, detectionPkg)
				}
			}
		})
	}
}

// enforcementPath is a hand-maintained list, which on its own means a package
// added later is covered by nothing and passes in silence. This is what makes
// that loud: every package in the module is on the enforcement path or is
// recorded as not being on it, with the reason, and a package on neither list
// fails until somebody decides which it is.
func TestEveryPackageIsOnOneSideOfTheRule(t *testing.T) {
	// The module pattern rather than ./... : the test runs with this package's
	// directory as its working directory, where ./... is this package alone.
	const all = "github.com/intentdriven/Gropius/..."
	out, err := exec.Command("go", "list", all).CombinedOutput()
	if err != nil {
		t.Fatalf("go list %s: %v\n%s", all, err, out)
	}
	onPath := map[string]bool{}
	for _, pkg := range enforcementPath {
		onPath[pkg] = true
	}
	seen := map[string]bool{}
	for _, pkg := range strings.Fields(string(out)) {
		seen[pkg] = true
		if onPath[pkg] || notEnforcement[pkg] != "" || carveOut[pkg] != "" {
			continue
		}
		t.Errorf("%s is on neither enforcementPath nor notEnforcement, so no rule here covers it and it would pass in silence — decide which it is: if anything in it decides who may reach this server or what it will do for them, add it to enforcementPath; otherwise add it to notEnforcement with the reason (adr-2609081118587999 rule 2)", pkg)
	}
	// The other direction: a stale entry is a rule asserting nothing about a
	// package that no longer exists, which reads as coverage and is not.
	for pkg := range onPath {
		if !seen[pkg] {
			t.Errorf("enforcementPath names %s, which is not a package in this module any more — remove it rather than leaving a rule that matches nothing", pkg)
		}
	}
	for pkg := range notEnforcement {
		if !seen[pkg] {
			t.Errorf("notEnforcement names %s, which is not a package in this module any more — remove it", pkg)
		}
	}
	for pkg := range carveOut {
		if !seen[pkg] {
			t.Errorf("carveOut names %s, which is not a package in this module any more — remove it, and with it the exception the amendment granted", pkg)
		}
	}
}

// The carve-out is one package wide, and this is what holds it there: the
// classifier is imported by the endpoint list's package, by the resolver, and
// by nothing else in the module. A second importer is a second place the
// detection reaches enforcement, and it fails here rather than being noticed
// in review.
func TestOnlyTheEndpointListAndTheResolverImportTheClassifier(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", "{{.ImportPath}} {{join .Imports \" \"}}", "github.com/intentdriven/Gropius/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	allowed := map[string]bool{
		"github.com/intentdriven/Gropius/internal/gateway": true,
		resolverPkg: true,
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pkg := fields[0]
		for _, imp := range fields[1:] {
			if imp == detectionPkg && !allowed[pkg] {
				t.Errorf("%s imports %s — the classifier is read by the endpoint list and by the private-network resolver, and by nothing else (adr-2609081118587999 rule 2 and its amendment)", pkg, detectionPkg)
			}
		}
	}
}

// Inside internal/gateway the import rule runs out: Endpoints, withAuth and
// loopbackOnly are one package. So the rule is scoped to the declaration
// instead — the detection is readable from the endpoint list and from nothing
// else, which is what keeps it out of the bearer-token check, the loopback
// guards and every warning's firing condition.
//
// The allowlist is keyed on the file AND the receiver as well as the name.
// Keying on the bare name was defeatable twice over: a method called
// `Endpoints` on any type borrowed the allowance of the function `Endpoints`,
// and a second file in the package could declare a function by an allowed name
// and inherit it. A site here is one exact declaration in one exact file.
var gatewayMayReadTheDetection = map[string]bool{
	// The type itself, and the state field that carries it to the panel.
	"control.go::type Endpoint": true,
	"control.go::type State":    true,
	// The endpoint list and the three helpers it is built from. Adding a name
	// to this map is the decision the ADR governs, and it should be as hard to
	// do quietly as changing the ADR. Anything that decides who may reach this
	// server belongs nowhere near it.
	"control.go::func Endpoints":           true,
	"control.go::func networkOf":           true,
	"control.go::func hasLocalNetworkAddr": true,
	"control.go::func appendLocalName":     true,
	"control.go::func appendEndpoint":      true,
}

// cmd/gropius names the resolver in exactly two declarations, and this is the
// list of them. It is separate from the gateway's, keyed the same way — file
// and declaration — and it exists rather than the command being scanned with
// nothing allowlisted, because the carve-out has to be consumed somewhere: the
// mode resolves an address, and the process that acquires listeners is what
// acquires it.
//
// What the command may do with the resolver is bounded by what these two
// declarations are. One asks for the plan and hands it on; the other prints
// what the mode selected, which is the amendment's second condition — the
// choice is always shown — on the surface an operator running headless reads.
// Neither decides anything: no key requirement, no admission, no warning's
// firing condition reads any of it, and adding a third name here is the
// decision adr-2609081118587999 governs, not a refactor.
var commandMayNameTheResolver = map[string]bool{
	"main.go::func resolveBind":  true,
	"main.go::func announceBind": true,
}

func TestOnlyTheEndpointListReadsTheDetection(t *testing.T) {
	for _, r := range scanForDetection(t, filepath.Join("..", "gateway")) {
		if gatewayMayReadTheDetection[r.site()] {
			continue
		}
		t.Errorf("%s: %s reads the detection (%s) — it is readable from the endpoint list and nothing else, or an enforcement decision can come to depend on another process's state (adr-2609081118587999 rule 2)",
			r.file, r.where, r.what)
	}
}

// internal/ui is scanned for the same names, with nothing allowlisted.
//
// It was scanned by nothing, and it is the one package in the module that is
// blessed as pure presentation, imports nothing of ours, and is imported by
// cmd/gropius. That combination is a laundry: internal/ui may import the
// gateway without a cycle, and a helper here taking a gateway.Endpoint and
// returning a bool would put the classification in cmd/gropius's hands with no
// rule anywhere objecting. It renders the mark from a JSON field in the
// browser and needs none of these names in Go.
//
// This closes the route that NAMES the field. It does not stop internal/ui
// calling net.Interfaces() itself, and nothing can: see the header.
func TestThePanelPackageDoesNotReadTheDetectionEither(t *testing.T) {
	for _, r := range scanForDetection(t, filepath.Join("..", "ui")) {
		t.Errorf("%s: %s reads the detection (%s) — internal/ui serves the panel's assets, is imported by cmd/gropius, and imports nothing of ours; a helper here reading the classification hands it to the enforcement path with no import rule in the way (adr-2609081118587999 rule 2)",
			r.file, r.where, r.what)
	}
	for file, name := range detectionImports(t, filepath.Join("..", "ui")) {
		t.Errorf("%s imports %s (as %q) — the panel renders the mark from a JSON field in the browser and has no business with the classifier in Go", file, detectionPkg, name)
	}
}

// cmd/gropius is where the app decides, before serving anything, whether an
// exposed bind may run at all: it generates and persists an API key or drops
// to loopback. That is enforcement in its purest form and it is the one place
// no dependency rule reaches, because cmd/gropius imports the gateway and the
// gateway imports the classifier.
//
// Nothing here may read the detection by any route the source can name — not
// the classifier, not the field, and not the Endpoint type, whose value
// carries the field. The one exception is the resolver, in the two
// declarations commandMayNameTheResolver lists: the private-network mode
// resolves an address, and this is the process that acquires listeners, so the
// carve-out has to be consumed here or it cannot be consumed at all. What
// arrives is a set of addresses; why they were chosen stays in the resolver.
//
// What cmd/gropius legitimately needs is the URL of the first entry, for the
// menu-bar title and the clipboard, and reading `.URL` off a value it never
// names is untouched by any of this. That is NOT the same as `.URL` being safe,
// and an earlier version of this comment said it was. The presence of the
// .local entry is a function of the classification — it is suppressed on a
// machine holding no local-network address, which is exactly the machine whose
// only address is on a private network — so eps[0].URL and len(eps) both carry
// the answer. See TestTheDetectionIsCarriedByTheShapeOfTheListItself.
func TestTheCommandCannotSeeTheDetection(t *testing.T) {
	dir := filepath.Join("..", "..", "cmd", "gropius")
	for _, r := range scanForDetection(t, dir) {
		if commandMayNameTheResolver[r.site()] && strings.HasPrefix(r.what, resolverName+".") {
			continue
		}
		t.Errorf("%s: %s reads the detection (%s) — cmd/gropius decides whether an exposed bind may run at all, and that decision may not rest on another process's state (adr-2609081118587999 rule 2). The menu bar needs the first entry's .URL and nothing else",
			r.file, r.where, r.what)
	}
	// The scan finds the package by the name it is called by, so the import
	// itself is refused outright here: cmd/gropius has no legitimate use for
	// the classifier, and an import with no reference today is a reference
	// tomorrow.
	for file, name := range detectionImports(t, dir) {
		t.Errorf("%s imports %s (as %q) — cmd/gropius reaches the endpoint list through the gateway and has no other business with the classifier", file, detectionPkg, name)
	}
}

// detectionSpelling is the JSON tag the answer is serialised under, and the
// mark's own text. Neither belongs in a string literal outside the
// declarations that build the endpoint list.
//
// This one is a spelling check and not a structural rule, and it is here for
// exactly one reason: the shortest route to the detection that names nothing
// is to serialise or format the whole Endpoint value and look in the bytes —
// `bytes.Contains(json.Marshal(eps), []byte("\"network\":"))`. That route
// cannot be closed structurally (see
// TestTheDetectionCanStillBeReadThroughTheWholeValue), so what is closed is
// its spelling: the needle has to be written down somewhere, and writing it
// down here fails. Building the needle at runtime walks past this, which is
// why it is described as a speed bump rather than as a rule.
var detectionSpelling = []string{`"network"`, "private network"}

func TestTheDetectionsSpellingIsNotWrittenDownOutsideTheEndpointList(t *testing.T) {
	for dir, allowed := range map[string]map[string]bool{
		filepath.Join("..", "gateway"):              gatewayMayReadTheDetection,
		filepath.Join("..", "..", "cmd", "gropius"): commandMayNameTheResolver,
	} {
		for _, u := range declUnits(t, dir) {
			if allowed[u.file+"::"+u.name] {
				continue
			}
			ast.Inspect(u.node, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				for _, needle := range detectionSpelling {
					if strings.Contains(strings.ToLower(s), needle) {
						t.Errorf("%s: %s writes down %q in the string %q — the detection's answer is read from the endpoint list, not looked for in the bytes of a serialised or formatted Endpoint (adr-2609081118587999 rule 2)",
							u.file, u.name, needle, s)
					}
				}
				return true
			})
		}
	}
}

// The honest statement of where the mechanical part stops. There are two
// limits, and the second is the one that cannot be fixed.
//
// FIRST: the routes that name nothing. An Endpoint value carries Network in its
// bytes, so anything holding one can recover the answer through encoding/json,
// fmt's %v, encoding/gob, reflect, or a comparison against a value built
// without the type — and none of those mentions netshape, Network or Endpoint
// anywhere in the source. Dropping `omitempty` does not help: the empty and
// non-empty encodings still differ. Closing that would need type-directed taint
// analysis over every value derived from Endpoints, which a
// package-name-and-identifier scan is not and cannot become.
//
// SECOND, and this one survives every fix to the first: THE LIST'S OWN SHAPE
// CARRIES THE ANSWER. Endpoints suppresses this Mac's .local name when the
// machine holds no local-network address — because the name resolves over the
// local network and nowhere else, so on a machine whose only address is on a
// private network it resolves to nothing, and listing it would offer an address
// the server does not answer on. That suppression IS the classification.
// `len(eps)` and `eps[0].URL` differ between a machine on a private network and
// the same machine on Wi-Fi, and reading either names nothing at all.
//
// It cannot be removed while the list stays truthful. The intent's fourth
// criterion is that the list never offers an address the server does not answer
// on; the list is therefore a function of the machine's network state; the
// classification is a function of the same state. A list that did not vary
// would either offer a dead .local name or withhold a working one. Truthfulness
// and non-disclosure are in direct conflict here, and truthfulness wins: this
// is a list an operator copies an address out of, and a list that lies to keep
// a secret from code in its own process is the wrong trade in both directions.
//
// Which is the whole point. Anything linked into this process can call
// net.Interfaces() and re-derive the classification without touching any of
// this, so there is no secret to keep. What these rules are worth is catching
// the accidental coupling, and what they are not is a barrier.
//
// This test asserts nothing and cannot fail. It is here so that the next person
// to read these rules learns their limit from the rules themselves rather than
// from a reviewer who got past them, and so that the ADR's closing sentence —
// the rule is not mechanically enforced — stays true of this file.
func TestTheDetectionIsCarriedByTheShapeOfTheListItself(t *testing.T) {
	t.Log("the routes that name the detection are closed; the routes that read the whole Endpoint value through json, fmt, gob or reflect are not, and cannot be closed by a scan over identifiers; and the endpoint list's own shape carries the classification, which no scan can change because a truthful list has to vary with the network state the classification reads")
}

// The gateway's own import must stay unaliased: these rules find the
// classifier by its package name, and an alias would walk straight past them.
// There is no reason to rename this import, and a rule that can be defeated by
// renaming is not a rule.
func TestTheGatewayImportsTheDetectionUnderItsOwnName(t *testing.T) {
	imports := detectionImports(t, filepath.Join("..", "gateway"))
	if len(imports) == 0 {
		t.Fatal("internal/gateway no longer imports the classifier — these rules are then asserting nothing; if the endpoint list stopped classifying addresses, delete them rather than leaving them green")
	}
	for file, under := range imports {
		if under != detectionName {
			t.Errorf("%s imports %s under the name %q — these rules find the detection by its package name, so an alias would hide it", file, detectionPkg, under)
		}
	}
}

// declUnit is one top-level declaration: a function, a method, or a single spec
// of a var/const/type/import block. It is the unit the allowlist is keyed on.
type declUnit struct {
	file string // base name, so the rule is per file as well as per declaration
	name string // "func Endpoints", "method (*Gateway).withAuth", "type Endpoint"
	node ast.Node
}

// detectionRef is one place the detection is named, and the declaration it was
// named in.
type detectionRef struct {
	file  string
	where string
	what  string
}

func (r detectionRef) site() string { return r.file + "::" + r.where }

// scanForDetection reports every place a package's non-test source names the
// detection: the classifier by package name, the field its answer travels on,
// and the type its answer travels in. The field and the type are matched as
// bare identifiers rather than as selector expressions, because a selector is
// only one of the ways to touch either — `Endpoint{Network: x}` names both and
// contains no selector at all.
func scanForDetection(t *testing.T, dir string) []detectionRef {
	t.Helper()
	var out []detectionRef
	for _, u := range declUnits(t, dir) {
		add := func(what string) {
			out = append(out, detectionRef{file: u.file, where: u.name, what: what})
		}
		ast.Inspect(u.node, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.SelectorExpr:
				if id, ok := v.X.(*ast.Ident); ok && id.Name == detectionName {
					add(detectionName + "." + v.Sel.Name)
				}
				if id, ok := v.X.(*ast.Ident); ok && id.Name == resolverName {
					add(resolverName + "." + v.Sel.Name)
				}
			case *ast.Ident:
				switch v.Name {
				case detectionField:
					add("the field " + detectionField)
				case detectionType:
					add("the type " + detectionType)
				}
			}
			return true
		})
	}
	return out
}

// declUnits splits a package's non-test source into the declarations the rules
// are scoped to. A method carries its receiver in its name, so a method cannot
// borrow the allowance of a function that happens to share its name; the file
// is carried alongside, so a second file cannot borrow it either.
func declUnits(t *testing.T, dir string) []declUnit {
	t.Helper()
	var out []declUnit
	for path, file := range parsePkgFiles(t, dir) {
		base := filepath.Base(path)
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				out = append(out, declUnit{file: base, name: funcName(d), node: d})
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					out = append(out, declUnit{file: base, name: specName(spec), node: spec})
				}
			}
		}
	}
	return out
}

func funcName(f *ast.FuncDecl) string {
	if f.Recv == nil || len(f.Recv.List) == 0 {
		return "func " + f.Name.Name
	}
	return "method (" + receiverType(f.Recv.List[0].Type) + ")." + f.Name.Name
}

// receiverType renders a receiver's type the way it is written, pointer star
// and all, so `(*Gateway).Endpoints` and `(Gateway).Endpoints` are different
// sites and neither is the function `Endpoints`.
func receiverType(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + receiverType(t.X)
	case *ast.IndexExpr: // a generic receiver, Foo[T]
		return receiverType(t.X)
	case *ast.IndexListExpr:
		return receiverType(t.X)
	}
	return "?"
}

func specName(s ast.Spec) string {
	switch v := s.(type) {
	case *ast.TypeSpec:
		return "type " + v.Name.Name
	case *ast.ValueSpec:
		names := make([]string, 0, len(v.Names))
		for _, n := range v.Names {
			names = append(names, n.Name)
		}
		return "var " + strings.Join(names, ", ")
	case *ast.ImportSpec:
		return "import " + v.Path.Value
	}
	return "a declaration"
}

// detectionImports maps each file importing the classifier to the name it
// imports it under.
func detectionImports(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for name, file := range parsePkgFiles(t, dir) {
		for _, imp := range file.Imports {
			if imp.Path.Value != strconv.Quote(detectionPkg) {
				continue
			}
			under := detectionName
			if imp.Name != nil {
				under = imp.Name.Name
			}
			out[filepath.Base(name)] = under
		}
	}
	return out
}

func parsePkgFiles(t *testing.T, dir string) map[string]*ast.File {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}
	out := map[string]*ast.File{}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			out[name] = file
		}
	}
	if len(out) == 0 {
		t.Fatalf("no non-test Go source found in %s — the scan is asserting nothing", dir)
	}
	return out
}
