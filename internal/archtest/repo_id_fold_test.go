package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every part that keys anything by a repo id must fold it through
// config.FoldRepoID, and no part may keep its own copy of the rule.
//
// The pool holds at most one entry per model, the registry holds at most one
// model per key, and the models list joins the two — and all three of those
// rest on the same claim: two repo ids naming the same model fold to the same
// string everywhere. That was once three separate `strings.ToLower(repoID)`
// expressions in three packages, true only because they happened to match. If
// one of them ever folded more loosely than the registry, two models would
// share a pool entry and a client asking for one would be served the other's
// weights. Folding more strictly would load a model twice under one budget.
//
// So the shape is banned rather than the outcome trusted: this fails on any
// non-test file under internal/ that lowercases something named like a repo id,
// outside the one file that owns the rule.
func TestRepoIDFoldHasOneHome(t *testing.T) {
	// The file that owns the rule. Everything else must call into it.
	const home = "config/config.go"

	// strings.ToLower applied to anything that names a repo id: the parameter
	// itself, or any struct field called RepoID.
	fold := regexp.MustCompile(`strings\.ToLower\(\s*(\w+\.)?[rR]epo[Ii][dD]\s*\)`)

	root := filepath.Join("..") // internal/
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if filepath.ToSlash(rel) == home {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(body), "\n") {
			if fold.MatchString(line) {
				offenders = append(offenders,
					filepath.ToSlash(rel)+":"+itoa(i+1)+" "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}
	for _, o := range offenders {
		t.Errorf("%s folds a repo id itself; call config.FoldRepoID so every key space stays the same one", o)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
