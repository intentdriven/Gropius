package archtest_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The file that owns the rule. Everything else must call into it.
const repoIDFoldHome = "internal/config/config.go"

// The packages that key something by a repo id: the registry's index, the
// pool's entries, the models list's join, the downloader's serialization. A
// case fold in any of these is a repo-id fold until proven otherwise.
var repoIDKeyedPackages = []string{
	"internal/registry/",
	"internal/runtime/",
	"internal/app/",
	"internal/gateway/",
}

// repoIDFoldAllowList holds the case folds in those packages that are not
// repo-id folds, keyed on the source line so that adding one, or changing one,
// has to be deliberate — which is the whole point of the check.
//
// One list, read by both checks below. There were two, and the second was a
// hand-copied subset that had drifted to three of five entries: the two added
// later were exempted by the first check and watched by nothing, so the
// staleness check had itself gone stale (iss-2609081441311030).
var repoIDFoldAllowList = map[string]string{
	`if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {`: "the Bearer scheme name, which HTTP defines as case-insensitive",
	`if hopByHopHeaders[strings.ToLower(k)] {`:                                          "an HTTP header name, likewise",
	`if gropiusHeaders[strings.ToLower(k)] {`:                                           "an HTTP header name too — the two headers Gropius writes itself, which an upstream may not add a second value to",
	`if strings.EqualFold(m.Name(), requested) {`:                                       "resolveModel's short-name convenience match, which is a lookup and not a key: the identity path is registry.Get, and this only decides whether a bare model name is unambiguous",
	`if strings.EqualFold(k, field) {`:                                                  "a settings JSON field name (models) matched the way encoding/json matches struct fields; not a repo id",
	`if !strings.EqualFold(key, field) {`:                                               "a settings JSON field name (api_key, hf_token) matched the same way, so a refused save can say whether the body asked to change a secret without comparing it with the stored one; not a repo id",
}

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
//   - Everywhere else in the tree, a case-folding call whose argument is
//     spelled like a repo id is a finding. Those packages fold plenty of
//     things that are not repo ids — file names, URL hosts, header names — so
//     the check there is necessarily name-shaped, and it is a backstop rather
//     than a proof.
//
// The subject is the whole checkout. It was internal/, by way of a ".." root
// that made the subject depend on the test binary's working directory; the
// maintainer settled the scope on 2026-09-09 (iss-2609081441311030). cmd/
// performs no case fold at all today, so widening changed no result — and it
// means the backstop half covers cmd/ the day one appears there.
func TestRepoIDFoldHasOneHome(t *testing.T) {
	anyFold := regexp.MustCompile(`strings\.(ToLower|ToUpper|EqualFold)\(`)
	repoIDFold := regexp.MustCompile(`strings\.(ToLower|ToUpper|EqualFold)\([^)]*[rR]epo[Ii][dD]`)

	root := repoRootDir(t)
	forEachShippingGoFile(t, root, func(rel string, body string) {
		if rel == repoIDFoldHome {
			return
		}
		keyed := false
		for _, pkg := range repoIDKeyedPackages {
			if strings.HasPrefix(rel, pkg) {
				keyed = true
				break
			}
		}

		for i, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			where := rel + ":" + strconv.Itoa(i+1)

			if keyed && anyFold.MatchString(line) {
				if _, ok := repoIDFoldAllowList[trimmed]; !ok {
					t.Errorf("%s folds a string in a package that keys by repo id: %s\n"+
						"\tcall config.FoldRepoID if this is a repo id, so every key space stays the same one;\n"+
						"\tif it is not, add the line to repoIDFoldAllowList with the reason",
						where, trimmed)
				}
				continue
			}
			if repoIDFold.MatchString(line) {
				t.Errorf("%s folds a repo id itself: %s\n"+
					"\tcall config.FoldRepoID so every key space stays the same one", where, trimmed)
			}
		}
	})
}

// An allow-list entry that no longer matches any line is a stale exemption: it
// would silently widen the check the day someone reuses that wording. This
// keeps the list honest without the check itself having to.
func TestRepoIDFoldAllowListIsNotStale(t *testing.T) {
	present := map[string]bool{}
	forEachShippingGoFile(t, repoRootDir(t), func(_ string, body string) {
		for _, line := range strings.Split(body, "\n") {
			present[strings.TrimSpace(line)] = true
		}
	})

	for entry, why := range repoIDFoldAllowList {
		if !present[entry] {
			t.Errorf("the fold allow-list still exempts a line that no longer exists (%s); drop it: %s", why, entry)
		}
	}
}

// forEachShippingGoFile hands fn every Go source file that ships, by its
// repo-relative path and its contents. Test files are excluded: a test may
// fold whatever it likes, and the rule is about what runs for a user.
func forEachShippingGoFile(t *testing.T, root string, fn func(rel, body string)) {
	t.Helper()
	walkRepoFiles(t, root, walkOptions{}, func(path string, d fs.DirEntry) error {
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fn(filepath.ToSlash(rel), string(body))
		return nil
	})
}
