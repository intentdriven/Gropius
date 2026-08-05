package archtest_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The UI toolkit (systray/AppKit, via cgo) must stay confined to cmd/gropius.
// If it leaks into internal/, the business logic can no longer be tested without
// a display server, and the daemon can no longer run headless under launchd.
func TestNoGUIToolkitInInternalPackages(t *testing.T) {
	// Enumerate the internal packages with a wildcard so a new package is
	// covered the day it appears. Rooting the scan on every internal package
	// matters: one imported only from test files (internal/mlxtest) or only by
	// cmd/gropius (internal/ui) is in no other internal package's dependency
	// closure and would escape a scan rooted anywhere narrower.
	out, err := exec.Command("go", "list",
		"github.com/intentdriven/Gropius/internal/...",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	// Now list every listed package's full dependency set.
	roots := strings.Fields(string(out))
	out, err = exec.Command("go", append([]string{"list", "-deps"}, roots...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.Contains(dep, "fyne.io/systray") {
			t.Errorf("an internal package imports %s — the GUI toolkit must stay in cmd/gropius, "+
				"or internal packages can no longer run headless or be tested without a display", dep)
		}
	}
}

// The no-absolute-paths-in-docs pre-commit hook exempts the shared-cache path
// /Users/Shared/... (a macOS system directory, not a username) by piping its
// grep through `grep -v "/Users/Shared/"`. That second grep filters whole
// lines, not individual matches, so a line that mentions the exempted path
// *and* a genuine private path together (a natural thing to write when
// contrasting shared-cache vs. per-user locations, as docs/getting-started.md
// already does) was silently dropped in its entirety — the private path never
// got flagged. This extracts the hook's actual shell command straight out of
// .pre-commit-config.yaml and runs it, so the test tracks the real config
// rather than a copy that could drift from it.
func TestNoAbsolutePathsHookCatchesPathsOnAnExemptedLine(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(repoRoot, ".pre-commit-config.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	script := extractHookScript(t, string(raw), "no-absolute-paths-in-docs")

	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cases := []struct {
		name      string
		content   string
		wantFlags bool
	}{
		{"shared_only.md", "The shared cache lives at /Users/Shared/Gropius.\n", false},
		{"private_only.md", "Alice's install is at /Users/alice/Library/Application Support/Gropius.\n", true},
		{"mixed.md", "Compare /Users/Shared/Gropius with a per-user install at /Users/bob/Library/Application Support/Gropius.\n", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := write(c.name, c.content)
			cmd := exec.Command("bash", "-c", script, "--", path)
			out, runErr := cmd.CombinedOutput()
			flagged := runErr != nil
			if flagged != c.wantFlags {
				t.Errorf("hook flagged=%v, want %v (output: %s)", flagged, c.wantFlags, out)
			}
		})
	}
}

// extractHookScript pulls the inline `bash -c '<script>' --` command out of a
// local pre-commit hook's `entry: >-` line, given the hook's id.
func extractHookScript(t *testing.T, yaml, hookID string) string {
	t.Helper()
	idx := strings.Index(yaml, "id: "+hookID)
	if idx < 0 {
		t.Fatalf("hook id %q not found in .pre-commit-config.yaml", hookID)
	}
	rest := yaml[idx:]
	entryIdx := strings.Index(rest, "entry: >-\n")
	if entryIdx < 0 {
		t.Fatalf("no folded entry block for hook %q", hookID)
	}
	line, _, _ := strings.Cut(rest[entryIdx+len("entry: >-\n"):], "\n")
	line = strings.TrimSpace(line)

	const prefix, suffix = "bash -c '", "' --"
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
		t.Fatalf("hook %q entry does not match the expected `bash -c '...' --` shape: %q", hookID, line)
	}
	return line[len(prefix) : len(line)-len(suffix)]
}
