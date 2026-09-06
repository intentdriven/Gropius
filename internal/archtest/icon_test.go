package archtest_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The app bundle's icon is committed art. Rasterising build/icon.svg needs
// librsvg, which no workflow installs and the GitHub macOS runner does not
// carry, so `make app` must copy the committed AppIcon.icns and must never
// depend on regenerating it: a release builds the bundle AFTER its tag is
// pushed, which is the most expensive place to discover a missing tool.
func TestAppIconIsCommittedAndBuildable(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	icns := filepath.Join(repoRoot, "build", "AppIcon.icns")
	info, err := os.Stat(icns)
	if err != nil {
		t.Fatalf("build/AppIcon.icns must be committed so a clean checkout can build the app: %v", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		t.Fatalf("build/AppIcon.icns is not a non-empty regular file (mode %s, %d bytes)", info.Mode(), info.Size())
	}

	// The `app` target's recipe and prerequisites, up to the next target.
	makefile, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	target := regexp.MustCompile(`(?ms)^app: build.*?(?:\n[a-z][a-zA-Z-]*:|\z)`).Find(makefile)
	if target == nil {
		t.Fatal("the Makefile has no `app: build` target; if it was renamed, this test needs updating")
	}
	if strings.Contains(string(target), "mkicon") {
		t.Error("the app target runs mkicon.sh; building the bundle must not need librsvg — `make icon` regenerates the art by hand")
	}
	if regexp.MustCompile(`(?m)^app:.*\bicon\b`).Match(makefile) {
		t.Error("the app target depends on `icon`; the committed AppIcon.icns is what the bundle copies")
	}
}

// The .icns is a rasterisation of build/icon.svg, and nothing at build time
// re-derives it. mkicon.sh records the hash of the art it rasterised, so editing
// the SVG without rerunning `make icon` is visible here rather than in a Dock
// icon that no longer matches the landing page's mark.
func TestAppIconMatchesTheCommittedArt(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	svg, err := os.ReadFile(filepath.Join(repoRoot, "build", "icon.svg"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(svg)
	recorded, err := os.ReadFile(filepath.Join(repoRoot, "build", "AppIcon.icns.source-sha256"))
	if err != nil {
		t.Fatalf("build/AppIcon.icns.source-sha256 is missing; run `make icon` to record which art the .icns was built from: %v", err)
	}
	if got, want := strings.TrimSpace(string(recorded)), hex.EncodeToString(sum[:]); got != want {
		t.Errorf("build/AppIcon.icns was built from art with hash %s, but build/icon.svg hashes to %s — run `make icon` (needs librsvg)", got, want)
	}
}
