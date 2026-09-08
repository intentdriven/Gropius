package archtest_test

import (
	"os"
	"path/filepath"
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

	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	mk := string(makefile)
	for _, want := range []string{
		// The compiler is told about the tag at all,
		`-tags "$(TAGS)"`,
		// and the bundle target is the one that sets it. `app` is what
		// `make install` and the release workflow build; the dev binary and
		// every `go test` carry no tags, which is why the seam is there for
		// the tests that need it.
		"app: TAGS = prod",
	} {
		if !strings.Contains(mk, want) {
			t.Errorf("the Makefile does not contain %q — the release build then carries the classifier's injection seam, whatever inject.go's build constraint says", want)
		}
	}
}
