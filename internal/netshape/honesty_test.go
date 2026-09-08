package netshape

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

// This file carries no build constraint on purpose. It scans this package's
// own string literals and drives nothing, so it is the part of the mark's
// honesty rule that still runs in the release configuration, where
// SetEnumerator — and every test that fixes an interface list through it — is
// compiled out.

// A network name is an observation, and observations survive the transitions
// Gropius cannot see: the network can be published to the internet or shared
// with machines the operator does not own, and the interface and the address
// are untouched by both. A name that says where the address lives is still
// true afterwards; one that says it is encrypted, secure, or reachable only by
// the operator's own devices is not (adr-2609081118587999, rule 1). Naming the
// vendor is refused for a second reason: this classifier cannot tell one
// product on the range from another, so the name would be a guess.
func TestNoStringInThisPackageNamesAVendorOrPromisesAnything(t *testing.T) {
	for _, lit := range stringLiterals(t, ".") {
		assertHonest(t, "a string literal in internal/netshape", lit)
	}
}

// forbidden is the vocabulary the mark may not use: vendor names, and words an
// operator would read as a statement about their exposure rather than about
// which network the address is on.
var forbidden = []string{
	"tailscale", "tailnet", "headscale", "zerotier", "wireguard", "nebula",
	"encrypt", "secure", "safe", "only", "vpn", "private and",
}

// assertHonest fails when text uses any of the forbidden vocabulary. Each
// surface the mark reaches — this package, the endpoint list, the panel that
// renders it — carries its own copy of this check against its own strings;
// there is no shared test package to hold a single one.
func assertHonest(t *testing.T, what, text string) {
	t.Helper()
	lower := strings.ToLower(text)
	for _, word := range forbidden {
		if strings.Contains(lower, word) {
			t.Errorf("%s contains %q: %q — the mark states which network an address is on and nothing more", what, word, text)
		}
	}
}

// stringLiterals returns every string literal in the non-test Go source of a
// package directory. Comments are deliberately not scanned: they explain the
// classifier to whoever maintains it and never reach an operator.
func stringLiterals(t *testing.T, dir string) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}
	var out []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				out = append(out, s)
				return true
			})
		}
	}
	if len(out) == 0 {
		t.Fatalf("no string literals found in %s — the scan is asserting nothing", dir)
	}
	return out
}
