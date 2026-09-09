package archtest_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// walkRepoFiles is the one walk every architecture scan of the tree goes
// through, and it owns the skip rule so that no scan decides it privately.
//
// The rule has two halves.
//
// Every dot-directory is skipped. This is not tidiness: a git worktree checked
// out under .claude/ holds a whole second copy of this tree, so a walk that
// descends into one scans another branch's files as if they were this
// checkout's. A non-compliant line written in an agent's worktree then turns
// the main checkout's suite red for a file nobody can locate in the
// repository, and a worktree's copy of a file can just as easily hold a scan
// green. Measured in the main checkout while this was written: a walk
// excluding only .git, node_modules, dist, bin and site saw 12 shell scripts,
// 8 of them inside .claude/worktrees/, against the 4 the repository tracks.
// Excluding every dot-directory rather than naming .claude is what keeps
// working the day the next tool picks a different directory.
//
// Build output and vendored dependencies are skipped too: they hold files this
// module did not write, and an assertion about them is an assertion about
// somebody else's code.
//
// Four hand-rolled WalkDir bodies each decided this for themselves and two of
// them had never heard of the worktree hazard (iss-2609081427104462). One
// walker owning the rule is the point; a scan that needs a narrower subject
// says so through walkOptions rather than by writing the rule out again.
func walkRepoFiles(t *testing.T, root string, opts walkOptions, fn func(path string, d fs.DirEntry) error) {
	t.Helper()

	skip := map[string]bool{
		// Vendored or generated; not this module's source.
		"node_modules": true,
		"dist":         true,
		"bin":          true,
		"site":         true,
	}
	for _, name := range opts.AlsoSkip {
		skip[name] = true
	}
	keepDot := make(map[string]bool, len(opts.DotDirs))
	for _, name := range opts.DotDirs {
		keepDot[name] = true
	}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return fn(path, d)
		}
		// The root is walked whatever it is called: ".." is a legitimate root
		// and its base name starts with a dot.
		if path == root {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") && !keepDot[name] {
			return fs.SkipDir
		}
		if skip[name] {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
}

// walkOptions narrows a single scan's subject. Neither field can reach the
// worktree hazard by accident: AlsoSkip only ever removes directories, and
// DotDirs names tracked directories a scan genuinely needs — .github, whose
// workflows carry shell in their `run:` blocks, is the only one so far. Naming
// one is a line in a diff somebody reviews, which is what the hazard costs.
type walkOptions struct {
	// AlsoSkip are directory names this scan's subject does not include.
	AlsoSkip []string
	// DotDirs are dot-directories this scan does need despite the rule.
	DotDirs []string
}

// The walker is the thing every other scan now trusts, so it is tested rather
// than assumed: a tree with a file planted under a dot-directory, and the walk
// must not see it.
func TestWalkRepoFilesSkipsDotDirectories(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"tracked.txt",
		"pkg/tracked.txt",
		".claude/worktrees/branch/tracked.txt",
		".github/workflows/ci.yml",
		"node_modules/dep/index.js",
		"bin/gropius",
		"client/main.swift",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	seen := func(opts walkOptions) []string {
		var out []string
		walkRepoFiles(t, root, opts, func(path string, _ fs.DirEntry) error {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
		sort.Strings(out)
		return out
	}

	got := seen(walkOptions{})
	want := []string{"client/main.swift", "pkg/tracked.txt", "tracked.txt"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("default walk saw %v, want %v — a dot-directory holds another branch's copy of the tree and must not be scanned", got, want)
	}

	got = seen(walkOptions{AlsoSkip: []string{"client"}, DotDirs: []string{".github"}})
	want = []string{".github/workflows/ci.yml", "pkg/tracked.txt", "tracked.txt"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("narrowed walk saw %v, want %v", got, want)
	}
}

// A dot-directory named as the ROOT is still walked: internal/archtest's own
// scans root themselves at ".." and at the checkout, and a repository cloned
// into a directory whose name begins with a dot would otherwise be scanned as
// nothing at all — a green suite that asserted nothing.
func TestWalkRepoFilesWalksADotNamedRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, ".checkout")
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "a.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var seen int
	walkRepoFiles(t, root, walkOptions{}, func(string, fs.DirEntry) error {
		seen++
		return nil
	})
	if seen != 1 {
		t.Errorf("walking a dot-named root saw %d files, want 1", seen)
	}
}
