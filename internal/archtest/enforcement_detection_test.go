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
// The ADR records that the rule is NOT mechanically enforced, and that remains
// true: these tests are the mechanical part of a rule whose whole cannot be
// mechanised, and the honest statement of where they stop is in
// TestTheDetectionCanStillBeReadThroughTheWholeValue below. What they do close
// is every route that names the detection in the source — which is every route
// a maintainer takes by accident, and most of the routes one takes on purpose:
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
//     decision in the app: the generate-a-key-or-drop-to-loopback branch.
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
	"github.com/intentdriven/Gropius/internal/ui":       "serves the control panel's assets and decides nothing about who may reach the server; presentation is what rule 1 allows",
	"github.com/intentdriven/Gropius/internal/archtest": "is these rules",
	"github.com/intentdriven/Gropius/internal/mlxtest":  "test helpers; nothing ships in the binary",
	"github.com/intentdriven/Gropius/internal/sitetest": "test helpers for the landing-page renderer",
	"github.com/intentdriven/Gropius/cmd/gropius-site":  "renders the landing page offline and serves nothing",
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
		if onPath[pkg] || notEnforcement[pkg] != "" {
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

func TestOnlyTheEndpointListReadsTheDetection(t *testing.T) {
	for _, r := range scanForDetection(t, filepath.Join("..", "gateway")) {
		if gatewayMayReadTheDetection[r.site()] {
			continue
		}
		t.Errorf("%s: %s reads the detection (%s) — it is readable from the endpoint list and nothing else, or an enforcement decision can come to depend on another process's state (adr-2609081118587999 rule 2)",
			r.file, r.where, r.what)
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
// carries the field. What cmd/gropius legitimately needs is the URL of the
// first entry, for the menu-bar title and the clipboard, and reading `.URL` off
// a value it never names is untouched by any of this.
func TestTheCommandCannotSeeTheDetection(t *testing.T) {
	dir := filepath.Join("..", "..", "cmd", "gropius")
	for _, r := range scanForDetection(t, dir) {
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
	for _, dir := range []string{
		filepath.Join("..", "gateway"),
		filepath.Join("..", "..", "cmd", "gropius"),
	} {
		for _, u := range declUnits(t, dir) {
			if gatewayMayReadTheDetection[u.file+"::"+u.name] {
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

// The honest statement of where the mechanical part stops.
//
// Everything above closes the routes that NAME the detection: the package, the
// field, the type, and the two spellings the answer is serialised under. None
// of them closes the route that names nothing. An Endpoint value carries
// Network in its bytes, so anything holding one can recover the answer through
// encoding/json, fmt's %v, encoding/gob, reflect, or a comparison against a
// value built without the type — and none of those mentions netshape, Network
// or Endpoint anywhere in the source. Dropping `omitempty` does not help: the
// empty and non-empty encodings still differ.
//
// Closing that would need type-directed taint analysis over every value
// derived from Endpoints, which a package-name-and-identifier scan is not and
// cannot become. The only structural close available is to stop handing the
// enforcement side an Endpoint at all — to give cmd/gropius a []string of URLs
// instead — which is a change to the gateway's exported API and a decision for
// whoever owns it, not something to slip in under a test.
//
// This test asserts nothing and cannot fail. It is here so that the next
// person to read these rules learns their limit from the rules themselves
// rather than from a reviewer who got past them, and so that the ADR's closing
// sentence — the rule is not mechanically enforced — stays true of this file.
func TestTheDetectionCanStillBeReadThroughTheWholeValue(t *testing.T) {
	t.Log("the routes that name the detection are closed; the routes that read the whole Endpoint value through json, fmt, gob or reflect are not, and cannot be closed by a scan over identifiers")
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
