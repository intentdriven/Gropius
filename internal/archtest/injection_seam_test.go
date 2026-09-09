package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// netshape.SetEnumerator is test-only API, and it is exported because the
// assertions that need it are in other packages — internal/gateway's endpoint
// list and internal/ui's panel — where an export_test.go cannot reach.
// Exported test-only API is still API once it is in the shipped binary: a
// supported way for anything linked in to make the private-network classifier
// say whatever it likes, which is the one thing the mark is supposed not to
// do.
//
// So it is compiled out of the release build instead of hidden. This is the
// rule that keeps that true, in both halves: the seam carries the tag, and the
// target that produces the bundle Gropius ships passes it.
func TestTheClassifiersInjectionSeamIsBuiltOutOfTheReleaseBinary(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	const seam = "internal/netshape/inject.go"
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(seam)))
	if err != nil {
		t.Fatalf("reading %s: %v — SetEnumerator lives here precisely so it can be built out; if it moved, this rule has to move with it", seam, err)
	}
	if first := strings.SplitN(string(src), "\n", 2)[0]; strings.TrimSpace(first) != "//go:build !prod" {
		t.Errorf("%s starts with %q, want %q — without the constraint the seam is in every build, release included", seam, first, "//go:build !prod")
	}
	if !strings.Contains(string(src), "func SetEnumerator(") {
		t.Errorf("%s no longer declares SetEnumerator — either it moved into a file with no build constraint, in which case it ships, or it is gone and this rule should go with it", seam)
	}
	// And nothing else may declare it, or the constraint on this file is
	// decoration.
	entries, err := os.ReadDir(filepath.Join(root, "internal", "netshape"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if name == "inject.go" || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, "internal", "netshape", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "func SetEnumerator(") {
			t.Errorf("internal/netshape/%s also declares SetEnumerator, which is in every build — the constraint on %s then does nothing", name, seam)
		}
	}

	assertEveryBundlePathCarriesTheTag(t, root)
}

// bundleGoals is every way `make` can be asked to produce the .app bundle. The
// list is not decoration: `TAGS = prod` used to be a target-specific variable
// on `app`, and a target-specific variable reaches only the prerequisites make
// rebuilds FOR THAT TARGET. `make build app` runs `build` as a goal in its own
// right first — with no TAGS and no strip — and `app`'s dependency on it is
// then already satisfied, so the bundle was assembled from a dev binary
// carrying the injection seam. `make all app` and `make run app` do the same
// thing through `all` and `run`.
//
// So the rule is behavioural rather than textual: ask make what it WOULD run
// for each ordering, and require the compile that feeds the bundle to carry
// the tag. A grep for two literals could not see any of this.
var bundleGoals = [][]string{
	{"app"},
	{"build", "app"},
	{"all", "app"},
	{"run", "app"},
	{"app", "build"},
}

// The goal orderings above are a rule about ORDER, and `make -j` is the flag
// that removes order. Under it `build` and `app` run concurrently, both write
// bin/gropius, and the `cp` that feeds the bundle is ordered against neither.
//
// Measured on this Makefile before `.NOTPARALLEL:` was added, three
// consecutive runs of `make -j8 build app` from a removed bin/ and dist/ left
// bin/gropius as the 13,826,866-byte untagged dev binary while the bundle
// carried the 9,031,200-byte prod one: two writers of one path, and a `cp`
// that won a race it is not ordered to win. It is the same untagged-binary
// hazard the sub-make in `app` exists to close, reintroduced by a flag rather
// than by a goal ordering — and had the `cp` lost instead, the bundle would
// have been the dev binary, seam and all. With `.NOTPARALLEL:` the same three
// runs, and a serial `make build app`, all produce a byte-identical
// bin/gropius.
//
// This assertion is TEXTUAL, and that is a limit rather than a preference.
// `make -n` cannot see the fault: it prints the recipes in dependency order
// whether or not -j is passed, so the -n runs above are identical with and
// without it. Reproducing the race needs two real Go compiles racing on a
// removed bin/, which is minutes of wall clock and writes into the working
// tree, and neither belongs in the unit suite. What is checked here is that
// the declaration is present; the measurement above is the evidence that it
// is the right declaration.
func TestTheMakefileRefusesToRunItsGoalsInParallel(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^\.NOTPARALLEL:`).Match(makefile) {
		t.Error("the Makefile does not declare .NOTPARALLEL:, so `make -j8 build app` runs the untagged top-level `build` concurrently with `app`'s tagged sub-make — both write bin/gropius, and the cp into the bundle is ordered against neither")
	}
}

func assertEveryBundlePathCarriesTheTag(t *testing.T, root string) {
	t.Helper()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not on PATH")
	}
	for _, goals := range bundleGoals {
		t.Run(strings.Join(goals, "+"), func(t *testing.T) {
			// -n prints the recipes without running them. Lines invoking
			// $(MAKE) are still recursed into, with -n passed down, which is
			// what makes the sub-make's compile visible here.
			cmd := exec.Command("make", append([]string{"-n"}, goals...)...)
			cmd.Dir = root
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("make -n %s: %v\n%s", strings.Join(goals, " "), err, out)
			}
			// The compile that matters is the one whose output the bundle
			// copies in, so it is found by position relative to that copy
			// rather than by being first or last: `make app build` compiles
			// the bundle's binary and then a dev one, and `make build app`
			// does the reverse.
			lines := strings.Split(string(out), "\n")
			copiedAt := -1
			for i, line := range lines {
				if strings.Contains(line, "cp ") && strings.Contains(line, filepath.FromSlash("dist/Gropius.app/Contents/MacOS/gropius")) {
					copiedAt = i
				}
			}
			if copiedAt < 0 {
				t.Fatalf("make -n %s never copies a binary into the bundle — this rule is then asserting nothing\n%s", strings.Join(goals, " "), out)
			}
			last := ""
			for _, line := range lines[:copiedAt] {
				if strings.Contains(line, "go build") && strings.Contains(line, "-o bin/gropius") {
					last = strings.TrimSpace(line)
				}
			}
			if last == "" {
				t.Fatalf("make -n %s copies a binary into the bundle without compiling one first\n%s", strings.Join(goals, " "), out)
			}
			if !strings.Contains(last, `-tags "prod"`) {
				t.Errorf("`make %s` builds the bundle's binary with %q — the shipped binary then carries netshape.SetEnumerator, a supported way for anything linked in to make the private-network classifier say whatever it likes", strings.Join(goals, " "), last)
			}
			if !strings.Contains(last, "-s -w") {
				t.Errorf("`make %s` builds the bundle's binary with %q — the distributed binary then ships its DWARF, which the bundle target says it strips", strings.Join(goals, " "), last)
			}
		})
	}
}

// The other half of building the seam out: the tests that drive it must be
// built out with it, or the release configuration does not compile at all.
//
// It did not. `go vet -tags prod ./...` and `go test -tags prod ./...` both
// failed on `undefined: SetEnumerator` in internal/netshape and
// internal/gateway, which meant the configuration Gropius actually ships was
// exercised for the first time by the release job, after the tag was pushed.
// CI now builds and vets it; this is the rule that keeps a new test from
// breaking it again, and it is a static check so it fails on the machine that
// wrote the test rather than three commits later.
func TestEveryTestDrivingTheInjectionSeamIsBuiltOutWithIt(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	const seam = "SetEnumerator"
	var found int
	// The walk goes through walkRepoFiles, which skips every dot-directory: this
	// scan excluded only .git, so a git worktree checked out under .claude/ was
	// counted as if its test files were this checkout's — a branch someone else
	// is working on could both hold this scan green and fail it
	// (iss-2609081427104462).
	walkRepoFiles(t, root, walkOptions{}, func(path string, d fs.DirEntry) error {
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		// A CALL, not a mention: this file names SetEnumerator in its own
		// prose and in a string literal, and neither compiles into anything.
		if !callsSeam(t, path, seam) {
			return nil
		}
		found++
		rel, _ := filepath.Rel(root, path)
		if !hasBuildConstraint(src, "!prod") {
			t.Errorf("%s calls netshape.%s but carries no `//go:build !prod` constraint — the release configuration then does not compile, so `go vet -tags prod ./...` and `go test -tags prod ./...` fail and the shipped build is first exercised after the tag is pushed", rel, seam)
		}
		return nil
	})
	if found == 0 {
		t.Fatalf("nothing in the module drives netshape.%s any more — either the seam is unused, in which case delete it and the rules around it, or this walk is looking in the wrong place", seam)
	}
}

// callsSeam reports whether a file calls the named function, by any spelling —
// bare inside internal/netshape, qualified everywhere else.
func callsSeam(t *testing.T, path, name string) bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	var found bool
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			found = found || fn.Name == name
		case *ast.SelectorExpr:
			found = found || fn.Sel.Name == name
		}
		return true
	})
	return found
}

// hasBuildConstraint reports whether a file's //go:build line names the given
// term. Only the constraint block at the top of the file counts, which is the
// only place the compiler reads one.
func hasBuildConstraint(src, term string) bool {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//go:build ") {
			for _, f := range strings.Fields(strings.TrimPrefix(line, "//go:build ")) {
				if f == term {
					return true
				}
			}
			return false
		}
		if line != "" && !strings.HasPrefix(line, "//") {
			return false
		}
	}
	return false
}
