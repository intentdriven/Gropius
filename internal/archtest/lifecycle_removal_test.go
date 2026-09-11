package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The three rules the removing criteria turn on, armed over internal/lifecycle
// itself: exactly one elevation, no external removal command, and no read of
// standard input anywhere.
//
// WHAT THESE CANNOT DO, so a green run is not over-read. They are text and
// syntax over one package. Code that means to elevate can spell osascript in
// three lines without naming this package's helper, and code that means to read
// standard input can do it through a file descriptor. Like every rule in this
// directory they refuse the ACCIDENT — the second panel added beside the first
// because it seemed easier, the `rm -rf` reached for because a directory would
// not go — which is what a reviewer skimming a diff is most likely to wave
// through.

const lifecycleDir = "internal/lifecycle"

// The system tools a lifecycle verb may start, and why each is there. A
// program not on this list is a finding, whatever it is: the interesting case
// is not a hostile command but an ordinary one — `rm`, `ditto`, `chmod` —
// reached for because it was quicker than doing the work in process.
var lifecycleTools = map[string]string{
	"/usr/bin/osascript": "draws the system authorisation panel, and quits a running copy",
	"/usr/bin/open":      "launches the installed bundle",
	"/usr/libexec/ApplicationFirewall/socketfilterfw": "reads the firewall's answer for the doctor's observed line",

	// The four the update verb adds. Each is the tool the BOOTSTRAP already
	// uses for the same step, and that is the argument for every one of them:
	// the script and the verb must agree on what a verified download is, so
	// there is one fetch path and one verification path rather than two that
	// can drift.
	"/usr/bin/curl":   "downloads the current release's archive and the checksums published beside it",
	"/usr/bin/shasum": "IS the verification — the only integrity control in the update path, and the same invocation the bootstrap is verified against",
	"/usr/bin/ditto":  "unpacks the verified archive. It extracts, it does not remove",
	"/usr/bin/xattr":  "clears the quarantine attribute on a bundle that has already been verified",
}

// Every subprocess the lifecycle verbs start is one of the three named above.
//
// This is the arming of "never invokes an external removal command". Every
// removal is os.RemoveAll in this process, acting with this account's own
// rights: a path this account cannot delete is REPORTED, never handed to
// something that might delete more than was meant. `sudo /bin/rm -rf …` appears
// in this package as TEXT — the one deliberate command that removes a shared
// root, printed for a person to decide about — and a scan that read text rather
// than call sites could not tell that from running it.
func TestTheLifecycleVerbsStartOnlyTheToolsTheyDeclare(t *testing.T) {
	forEachLifecycleFile(t, func(rel string, fset *token.FileSet, file *ast.File) {
		pkgName, imported := execImportName(t, rel, file)
		if !imported {
			return
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); !ok || id.Name != pkgName {
				return true
			}
			var program ast.Expr
			switch sel.Sel.Name {
			case "Command", "LookPath":
				if len(call.Args) > 0 {
					program = call.Args[0]
				}
			case "CommandContext":
				if len(call.Args) > 1 {
					program = call.Args[1]
				}
			}
			lit, ok := program.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			spelled, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if _, allowed := lifecycleTools[spelled]; !allowed {
				t.Errorf("%s:%d starts %q. The lifecycle verbs start the tools they declare above and no others: "+
					"every removal is in process, with this account's own rights. If this is a new tool the "+
					"verbs genuinely need, add it to lifecycleTools with the reason; if it removes files, it "+
					"does not belong here at all",
					rel, fset.Position(lit.Pos()).Line, spelled)
			}
			return true
		})
	})
}

// There is exactly ONE place in the package that asks for administrator rights.
//
// The criteria allow one authorisation panel, for the firewall entry, because
// it is machine-wide state with no per-account route. A second site is how that
// becomes two panels, or a panel on a path nobody argued for, and it is the
// kind of change that reads as harmless in a diff.
func TestTheLifecycleVerbsElevateInExactlyOnePlace(t *testing.T) {
	sites := map[string][]int{}
	forEachLifecycleSourceLine(t, func(rel string, lineNo int, line string) {
		code, _, _ := strings.Cut(line, "//")
		if strings.Contains(code, "administrator privileges") {
			sites[rel] = append(sites[rel], lineNo)
		}
	})

	// Two constants hold the two AppleScript commands — grant and remove — and
	// both are executed by one function. That is one site in the sense that
	// matters: one process start, one panel, one place to review.
	if len(sites) != 1 {
		t.Errorf("administrator rights are asked for in %d files (%v); the package is allowed one elevation, "+
			"for the firewall entry, and it lives in elevate.go", len(sites), sites)
		return
	}
	for rel, lines := range sites {
		if filepath.Base(rel) != "elevate.go" {
			t.Errorf("%s asks for administrator rights; the one elevation belongs in elevate.go, where the "+
				"argument for it is written down", rel)
		}
		if len(lines) > 2 {
			t.Errorf("%s asks for administrator rights on %d lines (%v); the grant and the removal are the two "+
				"the criteria allow", rel, len(lines), lines)
		}
	}
}

// Nothing in the package reads standard input.
//
// Under `curl … | bash` the installer script's own remaining text IS standard
// input, so a read consumes the rest of the installer and corrupts the run. A
// verb that needs consent either raises the authorisation panel or refuses and
// names the flag that would have answered it. The one permitted mention is the
// question "is it a terminal", which is a stat and reads nothing.
func TestNoLifecycleVerbReadsStandardInput(t *testing.T) {
	forEachLifecycleSourceLine(t, func(rel string, lineNo int, line string) {
		code, _, _ := strings.Cut(line, "//")
		if !strings.Contains(code, "os.Stdin") {
			return
		}
		if strings.Contains(code, "isTerminal(os.Stdin)") {
			return
		}
		t.Errorf("%s:%d touches standard input: %s\n"+
			"Under a piped bootstrap that is the rest of the installer script. Ask through the system "+
			"authorisation panel, or refuse and name the flag that would have answered it",
			rel, lineNo, strings.TrimSpace(line))
	})
}

// The control plane is READ from this package and never asked to do anything.
//
// The route that would make a cross-account quit cheap is exactly the route the
// boundary forbids: it would hand every local account a stop button on a plane
// with no bearer check. So the package has ONE place that speaks to the running
// server, it is a GET, and the routes it may ask for are listed here.
var readOnlyControlPlaneRoutes = map[string]string{
	"/api/state": "the snapshot the control panel already polls — status reads the address and the models from it, and update reads the running server's version",
}

func TestTheLifecycleVerbsOnlyEverREADTheControlPlane(t *testing.T) {
	sites := map[string][]int{}
	routes := map[string]bool{}

	forEachLifecycleFile(t, func(rel string, fset *token.FileSet, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			// A verb that asked the server to DO something would spell it as
			// one of these. Get is counted too, so the single read site is
			// visible rather than merely allowed.
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				switch sel.Sel.Name {
				case "Get", "Post", "PostForm", "Head", "Do":
					sites[rel] = append(sites[rel], fset.Position(call.Pos()).Line)
				}
			}
			// And every route this package names, wherever it is named.
			for _, arg := range call.Args {
				if lit, ok := stringLit(arg); ok && strings.HasPrefix(lit, "/api/") {
					routes[lit] = true
				}
			}
			return true
		})
	})

	for rel, lines := range sites {
		if filepath.Base(rel) != "status.go" {
			t.Errorf("%s speaks to the control plane on lines %v. This package has one read site, in status.go, "+
				"and it is a GET: a verb that could ask a route to act would put a self-quit or a "+
				"self-replacement behind an HTTP surface (spc-2609111812370705)", rel, lines)
		}
		if len(lines) > 1 {
			t.Errorf("%s speaks to the control plane on %d lines (%v); one call site is what makes the "+
				"rule checkable by reading the file", rel, len(lines), lines)
		}
	}
	for route := range routes {
		if readOnlyControlPlaneRoutes[route] == "" {
			t.Errorf("the lifecycle verbs ask for %q, which is not one of the read-only routes recorded here. "+
				"If it genuinely reveals only what a world-readable bundle reveals, add it with the reason; "+
				"if it makes the server DO anything, it does not belong in a terminal verb at all", route)
		}
	}
}

// Nothing on the update's fetch path reads the environment.
//
// The bootstrap has an asset-directory seam, refused outside CI, because the
// release workflow must install artefacts it has just built. A verb a person
// types has no CI case, and a caller who could set one variable would otherwise
// substitute the whole integrity control silently: the checksums would be read
// from the same place as the bundle, and the verification would prove only that
// a directory is self-consistent.
func TestTheUpdatePathReadsNoEnvironmentVariable(t *testing.T) {
	for _, name := range []string{"update.go", "updatefetch.go", "updatereport.go"} {
		rel := lifecycleDir + "/" + name
		for i, line := range strings.Split(readRepoFile(t, repoRootDir(t), rel), "\n") {
			code, _, _ := strings.Cut(line, "//")
			for _, read := range []string{"os.Getenv", "os.LookupEnv", "os.Environ"} {
				if strings.Contains(code, read) {
					t.Errorf("%s:%d reads the environment (%s). The update's origin is fixed in the binary: "+
						"no variable and no flag may point the download, or the checksums that verify it, "+
						"at anywhere else", rel, i+1, read)
				}
			}
		}
	}
}

// The swap's staging name is declared in one file and used from one file.
//
// WHAT THIS CATCHES: a verb that stages a bundle for itself rather than
// reaching PlaceBundle, which is the copy-paste a reviewer waves through. The
// swap's guarantee — no failure path leaves this Mac without an application —
// is a property of that one implementation and of the behavioural tests beside
// it, and a second one would carry neither.
//
// WHAT IT CANNOT DO, so a green run is not over-read: it reads the identifier
// stagingPrefix, so a second staging implementation that declared a prefix of
// its own would pass. It refuses the accident, not the determined rewrite —
// which is what every rule in this directory does. The rule that actually binds
// update to the one placer is the function-pointer assertion in
// TestTheUpdateUsesTheSwapTheInstallerAlreadyPerforms.
func TestTheStagingNameIsDeclaredAndUsedInOneFile(t *testing.T) {
	files := map[string]bool{}
	forEachLifecycleSourceLine(t, func(rel string, _ int, line string) {
		code, _, _ := strings.Cut(line, "//")
		if strings.Contains(code, "stagingPrefix") {
			files[rel] = true
		}
	})
	if len(files) != 1 {
		t.Errorf("%d files stage a bundle for the swap (%v); there is one, in swap.go, and every verb that "+
			"places a bundle reaches it", len(files), files)
	}
	for rel := range files {
		if filepath.Base(rel) != "swap.go" {
			t.Errorf("%s stages a bundle for the swap; that belongs in swap.go with the argument for its order", rel)
		}
	}
}

// forEachLifecycleFile parses every non-test Go file of the package.
func forEachLifecycleFile(t *testing.T, fn func(rel string, fset *token.FileSet, file *ast.File)) {
	t.Helper()
	root := repoRootDir(t)
	fset := token.NewFileSet()
	walkRepoFiles(t, filepath.Join(root, lifecycleDir), walkOptions{}, func(path string, d fs.DirEntry) error {
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		fn(filepath.ToSlash(rel), fset, file)
		return nil
	})
}

// forEachLifecycleSourceLine walks the package's non-test source line by line.
func forEachLifecycleSourceLine(t *testing.T, fn func(rel string, lineNo int, line string)) {
	t.Helper()
	root := repoRootDir(t)
	walkRepoFiles(t, filepath.Join(root, lifecycleDir), walkOptions{}, func(path string, d fs.DirEntry) error {
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for i, line := range strings.Split(readRepoFile(t, root, filepath.ToSlash(rel)), "\n") {
			fn(filepath.ToSlash(rel), i+1, line)
		}
		return nil
	})
}
