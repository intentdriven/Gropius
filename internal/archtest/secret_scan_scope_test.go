package archtest_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// gitleaksGitInvocation matches a `gitleaks git` command line in a workflow's
// run block. Whitespace between the two words is not fixed, so a line that
// spells it with a tab or a double space is still seen.
var gitleaksGitInvocation = regexp.MustCompile(`(^|[\s;&|])gitleaks\s+git(\s|$)`)

// TestTheSecretScanIsScopedToTheRefUnderTest refuses a `gitleaks git`
// invocation that walks refs other than the checked-out HEAD.
//
// Recorded as iss-2609081014210884. gitleaks anchors a finding to the commit
// that INTRODUCED the string, and version 8.24.3's sources.NewGitLogCmd runs
//
//	git -C . log -p -U0 --full-history --all
//
// when `--log-opts` is empty. `--all` is every ref in the checkout, and the job
// checks out with fetch-depth 0, which fetches every branch and tag — so a
// finding on ANY pushed branch fails the default branch's own run, whose tree
// contains nothing of it. That is not a hypothetical: a run on the default
// branch went red with two findings that both resolved to a commit on an
// unrelated feature branch, while its sibling jobs passed. The red pointed the
// reader at a tree that could not contain the cause.
//
// The cost is why this is a guard and not a note. Because the finding is
// anchored to the introducing commit, deleting the string in a later commit
// does not clear it: the offending commit has to stop existing on every ref. In
// a repository that refuses force-pushes, that meant rebuilding a branch onto a
// fresh base, folding the fixture into the commit that introduced it, pushing
// under a new name, deleting the old remote branch, and reopening its pull
// request. A false positive on any branch escalated into a history rewrite
// before the default branch could go green.
//
// The fix this guard pins: pass `--log-opts`. In 8.24.3 a non-empty value does
// not ADD to the default arguments, it REPLACES them — NewGitLogCmd builds
// `log -p -U0` and then appends the user's words verbatim, so `--full-history
// --all` is gone and only what is written here is walked. `--full-history HEAD`
// therefore keeps the whole history of the ref under test and drops every other
// ref.
//
// Coverage is not lost by narrowing, which is the point worth stating plainly:
// every branch push scans its own HEAD, every pull request scans the merge
// result, and every merge-queue entry scans the candidate. Nothing can reach
// the default branch without having been scanned as part of some HEAD.
//
// What this does NOT check: that gitleaks still behaves this way. The argument
// above was read out of the pinned version's source, and the version is pinned
// in the same job. If GITLEAKS_VERSION moves, re-read NewGitLogCmd before
// trusting this guard — a future release that made `--log-opts` additive would
// leave this test green and the scope wrong.
func TestTheSecretScanIsScopedToTheRefUnderTest(t *testing.T) {
	root := repoRootDir(t)
	ci := readRepoFile(t, root, filepath.Join(".github", "workflows", "ci.yml"))
	job := withoutComments(workflowJob(t, ci, "gitleaks"))

	found := false
	for i, line := range strings.Split(job, "\n") {
		if !gitleaksGitInvocation.MatchString(line) {
			continue
		}
		found = true
		trimmed := strings.TrimSpace(line)

		opts, ok := logOptsValue(line)
		if !ok {
			t.Errorf("ci.yml gitleaks job, line %d of the job: `gitleaks git` runs without `--log-opts`, "+
				"so it walks `--full-history --all` — every fetched branch and tag, not the ref under "+
				"test (iss-2609081014210884): %s", i+1, trimmed)
			continue
		}
		if !strings.Contains(opts, "HEAD") {
			t.Errorf("ci.yml gitleaks job, line %d of the job: `--log-opts=%q` does not name HEAD, so the "+
				"scan is not scoped to the ref under test (iss-2609081014210884): %s", i+1, opts, trimmed)
		}
		if strings.Contains(opts, "--all") {
			t.Errorf("ci.yml gitleaks job, line %d of the job: `--log-opts=%q` passes `--all`, which walks "+
				"every fetched ref and reproduces iss-2609081014210884: %s", i+1, opts, trimmed)
		}
	}
	if !found {
		t.Error("ci.yml's gitleaks job runs no `gitleaks git` command; this guard exists to scope that " +
			"scan, and a rename that hides it from the guard leaves the scope unpinned " +
			"(iss-2609081014210884)")
	}
}

// TestTheSecretScanKeepsTheWholeHistoryOfTheRefUnderTest refuses a shallow
// checkout in the gitleaks job.
//
// Scoping the walk to HEAD is only half the property. gitleaks scans the diff
// of each commit it walks, so a truncated fetch means the commits below the cut
// are never examined at all — the scan would still be green over a secret that
// was committed and later removed. `fetch-depth: 0` is what makes
// `--full-history HEAD` mean the whole history of this ref rather than the last
// commit of it, and the two lines are only correct together.
func TestTheSecretScanKeepsTheWholeHistoryOfTheRefUnderTest(t *testing.T) {
	root := repoRootDir(t)
	ci := readRepoFile(t, root, filepath.Join(".github", "workflows", "ci.yml"))
	job := withoutComments(workflowJob(t, ci, "gitleaks"))

	if !regexp.MustCompile(`(?m)^\s*fetch-depth:\s*0\s*$`).MatchString(job) {
		t.Error("ci.yml's gitleaks job does not check out with `fetch-depth: 0`; without the full fetch " +
			"the scan cannot see the history of the ref it is scoped to, and passes over anything " +
			"below the cut (iss-2609081014210884)")
	}
}

// logOptsValue returns the value of a `--log-opts` flag on a command line,
// accepting the `=` and the space-separated spellings and stripping one layer
// of shell quoting. The value runs to the closing quote when quoted, and to the
// next whitespace when it is not.
func logOptsValue(line string) (string, bool) {
	idx := strings.Index(line, "--log-opts")
	if idx < 0 {
		return "", false
	}
	rest := strings.TrimPrefix(line[idx+len("--log-opts"):], "=")
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" {
		return "", false
	}
	if q := rest[0]; q == '\'' || q == '"' {
		end := strings.IndexByte(rest[1:], q)
		if end < 0 {
			return "", false
		}
		return rest[1 : 1+end], true
	}
	if end := strings.IndexAny(rest, " \t"); end >= 0 {
		return rest[:end], true
	}
	return rest, true
}
