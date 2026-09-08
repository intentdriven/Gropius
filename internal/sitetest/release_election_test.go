package sitetest_test

// Which release `releases/latest` resolves to is the installer's whole world:
// install.sh fetches `releases/latest/download/<asset>`, and the landing page
// names the release the forge flags latest. A pre-release that captures that
// pointer is therefore shipped to every new user, not offered to them.
//
// Two independent defects allowed it, and either one alone still leaves a hole.
//
//  1. `gh release create` carried no `--prerelease` anywhere in the file, so
//     `v0.4.0-rc1` was published as an ordinary release. Nothing downstream can
//     then exclude it, because there is nothing marking it.
//  2. The candidate list is fetched with `--exclude-pre-releases`, but `$TAG`
//     is appended to that list before the sort — so the exclusion protects the
//     comparison and not the outcome. Even a correctly flagged pre-release wins
//     the election against itself.
//
// And `sort -V` orders `v0.4.0-rc1` AFTER `v0.4.0`, the opposite of the
// semantic version rule that a pre-release precedes the version it leads to, so
// once such a tag exists it wins every later comparison too.
//
// These are text assertions on the workflow, as every other check on this file
// is — the logic lives in YAML and cannot be called from a test. They hold the
// properties the fix rests on rather than its exact spelling.

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestAPreReleaseTagIsPublishedAsOne(t *testing.T) {
	job := releasePublishJob(t)

	// The tag's own shape decides, with no lookup and nothing to fall out of
	// step: under semantic versioning a hyphen introduces the pre-release
	// identifiers, so `v0.4.0-rc1` is classifiable without asking the forge.
	if !regexp.MustCompile(`\*-\*\)`).MatchString(job) {
		t.Errorf("the release job never branches on the tag's pre-release shape, so a v*-rc tag is published as an ordinary release:\n%s", job)
	}
	if !strings.Contains(job, "--prerelease") {
		t.Errorf("the release job never passes --prerelease; a release candidate is then created as a full release that no later --exclude-pre-releases query can filter:\n%s", job)
	}
}

func TestAPreReleaseTagNeverWinsTheLatestElection(t *testing.T) {
	job := releasePublishJob(t)

	// The defect is the append, not the query: the candidate list is fetched
	// with --exclude-pre-releases and then has $TAG added to it, so the tag
	// under consideration bypasses the very filter that would have excluded it.
	// It is fixed by not electing at all on a pre-release tag.
	if !regexp.MustCompile(`--latest=false`).MatchString(job) {
		t.Errorf("no path publishes with --latest=false, so a pre-release tag can still take releases/latest:\n%s", job)
	}

	// Defence in depth for the tags that already exist. A candidate published
	// before this rule carries no pre-release flag on the forge, so relying on
	// --exclude-pre-releases alone leaves it in the candidate set for ever — and
	// by sort -V's ordering it beats the real release that follows it.
	if !regexp.MustCompile(`\*-\*\)\s*continue`).MatchString(job) {
		t.Errorf("candidates are not filtered for pre-release shape, so one published before this rule still pins latest:\n%s", job)
	}
	// The election is seeded from the tag being published and only displaced by
	// a released tag that beats it. Seeding it any other way reintroduces the
	// original defect, where $TAG was appended to a list it had to be compared
	// against and so bypassed the filter applied to that list.
	if !strings.Contains(job, `newest="$TAG"`) {
		t.Errorf("the election is not seeded from the tag being published, so the tag can bypass the filter applied to its rivals:\n%s", job)
	}
}

// releasePublishJob returns the release job's text, which is where the election
// and the publish both live.
func releasePublishJob(t *testing.T) string {
	t.Helper()
	wf := read(t, filepath.Join(repoRoot, ".github", "workflows", "release.yml"))
	return jobIn(t, wf, "release")
}
