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

// Every subprocess Gropius starts is named by an absolute path, never by a
// bare name PATH resolves.
//
// Two arguments say so, and neither carries the rule alone.
//
// The first is account-to-account. This product's premise is one Mac serving
// several user accounts — the shared-cache install mode with its 3775
// directory semantics, per-account MLX runtime executables, and the gateway's
// loopback exemption, which exists precisely because requests arrive from
// other macOS accounts on the same machine. So the question is not whether an
// attacker can already run code as this user, but whether a different account
// on this Mac can plant something the server account's process will execute. A
// group-writable directory on that PATH answers yes: on a stock developer Mac
// /opt/homebrew/bin is drwxrwsr-x owned by admin, setgid, and the local admin
// group has several members. `internal/capability` pinned /usr/sbin/sysctl for
// this reason and was for a while the only site that did
// (iss-2609081435387952).
//
// The second argument has no attacker in it at all. A pre-release step on a
// GitHub runner — a machine nobody shares — resolved a name to a tool that was
// not the tool meant, and the wrongness was invisible because the failure was
// swallowed. Pinning is therefore a defence against AMBIGUITY as much as
// against a planted binary, which is why the rule is "pin it" rather than "pin
// it where an attacker could reach" (.abcd/work/DECISIONS.md, 2026-09-08).
//
// What is checked: the program argument of every non-test exec.Command and
// exec.CommandContext. A string literal must begin with "/". Anything that is
// not a literal is allowed and deliberately so — the managed runtime's own
// interpreter (p.Paths.UV(), the venv's python) is a path Gropius computed and
// owns, not a name handed to PATH — so a green run here means no BARE NAME is
// executed, not that every executed path is trustworthy. A name assembled into
// a variable would pass; that is the limit of a static check, and the shape it
// catches is the one that is actually written.
func TestEverySubprocessIsPinnedToAnAbsolutePath(t *testing.T) {
	root := repoRootDir(t)
	fset := token.NewFileSet()

	walkRepoFiles(t, root, walkOptions{}, func(path string, d fs.DirEntry) error {
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "exec" {
				return true
			}
			// The program is the first argument to Command and the second to
			// CommandContext, whose first is the context.
			var program ast.Expr
			switch sel.Sel.Name {
			case "Command":
				if len(call.Args) > 0 {
					program = call.Args[0]
				}
			case "CommandContext":
				if len(call.Args) > 1 {
					program = call.Args[1]
				}
			}
			if program == nil {
				return true
			}
			lit, ok := program.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			spelled, err := strconv.Unquote(lit.Value)
			if err != nil || strings.HasPrefix(spelled, "/") {
				return true
			}
			t.Errorf("%s:%d runs %q, which PATH resolves: on the shared Mac this product is "+
				"designed for, another account can put a directory it writes ahead of /usr/bin "+
				"on this account's PATH, and on a machine with no attacker at all a bare name "+
				"is simply not proof of which tool ran. Name it by absolute path, as "+
				"internal/capability does for /usr/sbin/sysctl",
				rel, fset.Position(lit.Pos()).Line, spelled)
			return true
		})
		return nil
	})
}
