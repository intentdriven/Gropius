package archtest_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The app bundle's icon is committed art. Rasterizing build/icon.svg needs
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
	target := regexp.MustCompile(`(?ms)^app:.*?(?:\n[a-z][a-zA-Z-]*:|\z)`).Find(makefile)
	if target == nil {
		t.Fatal("the Makefile has no `app` target; if it was renamed, this test needs updating")
	}
	if strings.Contains(string(target), "mkicon") {
		t.Error("the app target runs mkicon.sh; building the bundle must not need librsvg — `make icon` regenerates the art by hand")
	}

	// And ask make, rather than reading the Makefile, whether anything on the
	// way to the bundle rasterizes the art.
	//
	// The check this replaces was `^app:.*\bicon\b` over the Makefile. It
	// tested the app target's PREREQUISITE LIST, and the restructure that put
	// the tagged build behind a sub-make left `app` with no prerequisites at
	// all, so it could not fire whatever the Makefile said — a passing
	// assertion that had stopped asserting anything. Neither it nor the
	// textual mkicon check above would have caught the shape that same
	// restructure made available: `$(MAKE) icon` inside the recipe, which
	// reaches mkicon.sh through a target no regexp over the `app:` line looks
	// at.
	//
	// `make -n` recurses into $(MAKE) lines with -n passed down, so a
	// sub-make's recipes are visible here. What to look for is "mkicon" and
	// not "make icon": the recipe's own error message names `make icon` as
	// the by-hand remedy, and that sentence must not fail this.
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not on PATH; the textual checks above still ran")
	}
	cmd := exec.Command("make", "-n", "app")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n app: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "AppIcon.icns") {
		t.Fatalf("`make -n app` never mentions AppIcon.icns, so this rule is asserting nothing about the icon\n%s", out)
	}
	if strings.Contains(string(out), "mkicon") {
		t.Errorf("`make -n app` reaches mkicon.sh — building the bundle then needs librsvg, which no workflow installs and the GitHub macOS runner does not carry, so the release goes red AFTER the tag is pushed\n%s", out)
	}
}

// The .icns is a rasterization of build/icon.svg, and nothing at build time
// re-derives it. mkicon.sh records the hash of the art it rasterized, so editing
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

// TestControlPanelMarkMatchesTheAppIcon holds the control panel's header mark
// and its favicon to build/icon.svg, the declared source of the drawing.
//
// internal/sitetest already holds the WEBSITE's mark to that file, so the page
// and the Dock icon cannot disagree. The control panel was outside that check
// and drifted: it carried a different arrangement (a red circle where the icon
// has a yellow triangle, a downward blue triangle where the icon has a square)
// in a different palette, so the app a user looked at every day showed a
// different logo from the one on its own website and in its own Dock tile.
//
// The mark is compared by the shapes' own coordinates rather than by rendering:
// the panel uses the icon's viewBox so the two files carry the same numbers,
// which is what makes a drift a textual difference a test can see.
func TestControlPanelMarkMatchesTheAppIcon(t *testing.T) {
	root := repoRootDir(t)
	read := func(rel ...string) string {
		b, err := os.ReadFile(filepath.Join(append([]string{root}, rel...)...))
		if err != nil {
			t.Fatalf("read %v: %v", rel, err)
		}
		return string(b)
	}
	icon := read("build", "icon.svg")
	panel := read("internal", "ui", "static", "index.html")

	// The header mark and the favicon are checked SEPARATELY. Checking the file
	// as a whole lets one satisfy the assertion for the other: with the header's
	// red drifted and the favicon's intact, a whole-file containment test still
	// passes, because the favicon carries the shape the header lost. Watched
	// doing exactly that before this split.
	header := between(panel, `<svg class="logo"`, "</svg>")
	favicon := between(panel, `<link rel="icon"`, `</svg>"`)
	if header == "" || favicon == "" {
		t.Fatal("the control panel carries no header mark or no favicon")
	}

	shape := regexp.MustCompile(`<(?:polygon|rect|circle)[^>]*fill="(#[0-9A-Fa-f]{6})"[^>]*/?>`)
	var want []string
	for _, m := range shape.FindAllString(icon, -1) {
		if strings.Contains(m, `width="1024"`) {
			continue // the ground; the header supplies its own
		}
		want = append(want, normaliseShape(m))
	}
	if len(want) != 4 {
		t.Fatalf("expected four shapes in build/icon.svg, found %d", len(want))
	}
	for _, region := range []struct{ name, body string }{
		{"header mark", header}, {"favicon", favicon},
	} {
		got := normaliseShape(region.body)
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("the control panel %s is missing the icon's shape %q — it must carry build/icon.svg's drawing", region.name, w)
			}
		}
	}
}

// normaliseShape collapses attribute quoting and whitespace so the same drawing
// compares equal whether it is written as markup or embedded in a data: URL.
func normaliseShape(s string) string {
	s = strings.ReplaceAll(s, "'", `"`)
	s = strings.ReplaceAll(s, "%23", "#")
	s = strings.Join(strings.Fields(s), " ")
	return strings.ToUpper(s)
}

// between returns the text from the first occurrence of open through the next
// close, or "" when either is absent.
func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], close)
	if j < 0 {
		return ""
	}
	return s[i : i+j+len(close)]
}
