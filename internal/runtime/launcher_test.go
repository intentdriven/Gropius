package runtime

import "testing"

// Distinct repo ids must map to distinct log files. ValidRepoID admits
// underscores in both components, so a separator the id itself can contain
// collapses ids like these into one path — and Launch opens the log O_TRUNC,
// so the collision truncates a live log, not just a stale one.
func TestLogFileNamesDoNotCollideAcrossDistinctRepoIDs(t *testing.T) {
	a, b := logFileName("a/b_c"), logFileName("a_b/c")
	if a == b {
		t.Fatalf("logFileName maps distinct repo ids to one file %q — launching the second model truncates the first model's live log", a)
	}
}
