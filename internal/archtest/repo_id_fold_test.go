package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every part that keys anything by a repo id must fold it through
// config.FoldRepoID, and no part may keep its own copy of the rule.
//
// The pool holds at most one entry per model, the registry holds at most one
// model per key, and the models list joins the two — and all three rest on the
// same claim: two repo ids naming the same model fold to the same string
// everywhere. That was once four separate `strings.ToLower(repoID)` expressions
// in four packages, true only because they happened to match. If one of them
// ever folded more loosely than the registry, two models would share a pool
// entry and a client asking for one would be served the other's weights.
// Folding more strictly would load a model twice under one budget.
//
// Two checks below, because either alone is evadable:
//
//   - In the packages that key by repo id, any case-folding call at all is a
//     finding unless it is on the allow-list. This is what catches a fold
//     hidden behind a local variable, which a name-shaped check cannot see.
//   - Everywhere else under internal/, a case-folding call whose argument is
//     spelled like a repo id is a finding. Those packages fold plenty of
//     things that are not repo ids — file names, URL hosts, header names — so
//     the check there is necessarily name-shaped, and it is a backstop rather
//     than a proof.
func TestRepoIDFoldHasOneHome(t *testing.T) {
	// The file that owns the rule. Everything else must call into it.
	const home = "config/config.go"

	// The packages that key something by a repo id: the registry's index, the
	// pool's entries, the models list's join, the downloader's serialization.
	// A case fold in any of these is a repo-id fold until proven otherwise.
	keyed := map[string]bool{"registry": true, "runtime": true, "app": true, "gateway": true}

	// Case folds in those packages that are not repo-id folds. Keyed on the
	// source line so that adding one, or changing one, has to be deliberate —
	// which is the whole point of the check.
	allowed := map[string]string{
		`if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {`: "the Bearer scheme name, which HTTP defines as case-insensitive",
		`if hopByHopHeaders[strings.ToLower(k)] {`:                                          "an HTTP header name, likewise",
		`if strings.EqualFold(m.Name(), requested) {`:                                       "resolveModel's short-name convenience match, which is a lookup and not a key: the identity path is registry.Get, and this only decides whether a bare model name is unambiguous",
	}

	anyFold := regexp.MustCompile(`strings\.(ToLower|ToUpper|EqualFold)\(`)
	repoIDFold := regexp.MustCompile(`strings\.(ToLower|ToUpper|EqualFold)\([^)]*[rR]epo[Ii][dD]`)

	root := ".." // internal/
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
		rel = filepath.ToSlash(rel)
		if rel == home {
			return nil
		}
		pkg, _, _ := strings.Cut(rel, "/")

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			where := rel + ":" + strconv.Itoa(i+1)

			if keyed[pkg] && anyFold.MatchString(line) {
				if _, ok := allowed[trimmed]; !ok {
					t.Errorf("%s folds a string in a package that keys by repo id: %s\n"+
						"\tcall config.FoldRepoID if this is a repo id, so every key space stays the same one;\n"+
						"\tif it is not, add the line to this test's allow-list with the reason",
						where, trimmed)
				}
				continue
			}
			if repoIDFold.MatchString(line) {
				t.Errorf("%s folds a repo id itself: %s\n"+
					"\tcall config.FoldRepoID so every key space stays the same one", where, trimmed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}
}

// An allow-list entry that no longer matches any line is a stale exemption: it
// would silently widen the check the day someone reuses that wording. This
// keeps the list honest without the check itself having to.
func TestRepoIDFoldAllowListIsNotStale(t *testing.T) {
	allowed := []string{
		`if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {`,
		`if hopByHopHeaders[strings.ToLower(k)] {`,
		`if strings.EqualFold(m.Name(), requested) {`,
	}

	var lines []string
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, line := range strings.Split(string(body), "\n") {
			lines = append(lines, strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}

	present := make(map[string]bool, len(lines))
	for _, l := range lines {
		present[l] = true
	}
	for _, a := range allowed {
		if !present[a] {
			t.Errorf("the fold allow-list still exempts a line that no longer exists; drop it: %s", a)
		}
	}
}
