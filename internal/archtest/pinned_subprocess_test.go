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
// WHAT IS CHECKED. Every non-test Go file that imports os/exec, through the
// name that file binds it to: the program argument of exec.Command and
// exec.CommandContext, and the argument of exec.LookPath, which is a PATH
// search by definition. A string literal there must begin with "/". Matching
// the word "exec" instead would have been decoration — `import xc "os/exec"`
// renames every call site in a file, and is one line, so the import spec is
// read and a dot-import is reported rather than skipped over.
//
// WHAT IS NOT, stated so a green run is not over-read. A program argument that
// is not a string literal is allowed, deliberately: the managed runtime's own
// interpreter (p.Paths.UV(), the venv's python) is a path Gropius computed and
// owns, not a name handed to PATH. So a bare name reaching exec through a
// constant, a variable or a concatenation passes this scan. So does a process
// started without os/exec at all — syscall.Exec, os.StartProcess — and so does
// an exec.Cmd composite literal whose Path is filled in by hand. A green run
// means no bare-name LITERAL is handed to exec's process starters; it is not a
// proof that every path executed is one Gropius chose.
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

		// The name THIS file calls os/exec by. A file that does not import it
		// starts no subprocess through it.
		pkgName, imported := execImportName(t, rel, file)
		if !imported {
			return nil
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
			// The program is the first argument to Command and to LookPath,
			// and the second to CommandContext, whose first is the context.
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
			t.Errorf("%s:%d hands %q to %s.%s, which resolves it through PATH: on the shared Mac "+
				"this product is designed for, another account can put a directory it writes ahead "+
				"of /usr/bin on this account's PATH, and on a machine with no attacker at all a bare "+
				"name is simply not proof of which tool ran. Name it by absolute path, as "+
				"internal/capability does for /usr/sbin/sysctl",
				rel, fset.Position(lit.Pos()).Line, spelled, pkgName, sel.Sel.Name)
			return true
		})
		return nil
	})
}

// execImportName returns the name a file binds os/exec to, and whether it is
// usable at all.
//
// An alias is the ordinary way past a check that matches the word "exec", and
// it costs one line: `import xc "os/exec"` renames every call site in the
// file. A blank import starts no process, so there is nothing to look at. A
// dot-import is a finding in itself rather than something to skip: its calls
// are spelled `Command(...)` with no qualifier, which this scan cannot
// distinguish from any other function of that name, so the scan would go quiet
// exactly where it is needed.
func execImportName(t *testing.T, rel string, file *ast.File) (string, bool) {
	t.Helper()
	for _, spec := range file.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil || imported != "os/exec" {
			continue
		}
		if spec.Name == nil {
			return "exec", true
		}
		switch spec.Name.Name {
		case "_":
			return "", false
		case ".":
			t.Errorf("%s dot-imports os/exec, so its calls are spelled Command(…) with no "+
				"qualifier and this scan cannot see them at all. Import it under a name",
				rel)
			return "", false
		default:
			return spec.Name.Name, true
		}
	}
	return "", false
}
