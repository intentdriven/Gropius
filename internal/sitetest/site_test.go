// Package sitetest holds the landing page to the things it makes claims about:
// the app icon it shows, the app bundle it describes, and the canonical identity
// block it says it renders from. It is a test-only package in the archtest and
// mlxtest mould — no network, no browser, no running server — so `make test`
// fails the day the page and the app disagree.
//
// Every check runs against a real render into t.TempDir() (or, for the property
// that a render is not a lint, against a fixture tree with an edited identity
// block), not against a committed copy of the output: a golden file would
// re-state the renderer's behavior rather than test it.
package sitetest_test

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The repository root, from this package's directory.
const repoRoot = "../.."

// The viewport the acceptance criteria name.
const (
	foldWidth  = 1280.0
	foldHeight = 800.0
)

var (
	// The page and stylesheet rendered from the committed tree, shared by every
	// test that does not need its own fixture.
	page string
	css  string
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gropius-site")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	out, err := renderTree(".", dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	page, css = out.page, out.css
	os.Exit(m.Run())
}

type rendered struct {
	page string
	css  string
}

// renderTree runs the renderer exactly as the Makefile does: root is the tree to
// compose from, out the directory to write into, and no release record — the
// page with nothing but what the repository composes.
func renderTree(root, out string) (rendered, error) {
	return renderTreeWithRelease(root, out, "")
}

// renderTreeWithRelease is the deploy chain's invocation: the same render, with
// the release record the workflow writes beside the build. An empty release path
// passes no --release at all, which is the record's absence and not an empty
// one.
func renderTreeWithRelease(root, out, release string) (rendered, error) {
	args := []string{"run", "./cmd/gropius-site", "--root", root, "--manifest", filepath.Join(root, ".abcd", "site.json"), "--out", out}
	if release != "" {
		args = append(args, "--release", release)
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot
	if b, err := cmd.CombinedOutput(); err != nil {
		return rendered{}, fmt.Errorf("render: %v\n%s", err, b)
	}
	html, err := os.ReadFile(filepath.Join(out, "Gropius", "index.html"))
	if err != nil {
		return rendered{}, err
	}
	style, err := os.ReadFile(filepath.Join(out, "Gropius", "site.css"))
	if err != nil {
		return rendered{}, err
	}
	return rendered{page: string(html), css: string(style)}, nil
}

// --- Criterion 1 -----------------------------------------------------------
//
// "Given Alice opens the page at a 1280x800 viewport, when it has loaded, then
// the download button and the link to the repository are both visible without
// scrolling."

func TestDownloadAndSourceSitAboveTheFold(t *testing.T) {
	// Document order first: the two actions must precede the install section,
	// which is where the prototype already put them.
	actions := strings.Index(page, `id="get"`)
	install := strings.Index(page, `id="install"`)
	if actions < 0 || install < 0 {
		t.Fatalf("the page has no download section (%d) or no install section (%d)", actions, install)
	}
	if actions > install {
		t.Errorf("the install section precedes the action row; the download button and the source link must come first")
	}
	row := strings.Index(page, `class="cta-row"`)
	if row < 0 || row > install {
		t.Fatalf("no action row above the install section")
	}
	// Both elements the criterion names, in the one row above the fold.
	actionRow := between(t, page, `class="cta-row"`, "</div>")
	// The primary action is the install command, not a direct download: a
	// browser-fetched archive arrives quarantined, and an ad-hoc-signed bundle
	// is then refused by Gatekeeper or moved to the Bin. See the dated note in
	// the landing-page intent's Audit Notes.
	if !strings.Contains(actionRow, `href="#install"`) {
		t.Error("the action row holds no link to the install command")
	}
	if strings.Contains(actionRow, "/releases/latest/download/") {
		t.Error("the action row offers a direct download; the page must not hand out a quarantined archive")
	}
	// The repository's own URL, not merely a prefix of one: the download link
	// starts with it, so a substring test would pass with the second action gone.
	if !strings.Contains(actionRow, `href="`+repositoryURL(t)+`"`) {
		t.Error("the action row holds no link to the repository itself")
	}

	// Then the height of everything above the action row, at 1280px wide, from
	// the stylesheet's own numbers. The line counts are the model's assumption
	// and are derived from the text that is actually rendered: a line holds
	// max-width/0.5 characters, half an em being a workable average advance for
	// the faces this page loads.
	total, breakdown := foldBudget(t)
	if total > foldHeight {
		t.Errorf("the action row starts %.0fpx down a %.0fpx viewport:\n%s", total, foldHeight, breakdown)
	} else {
		t.Logf("action row starts %.0fpx down a %.0fpx viewport:\n%s", total, foldHeight, breakdown)
	}
}

func foldBudget(t *testing.T) (float64, string) {
	t.Helper()
	var b strings.Builder
	total := 0.0
	add := func(what string, px float64) {
		total += px
		fmt.Fprintf(&b, "  %-28s %7.1f\n", what, px)
	}

	// Top bar: its own padding, the wordmark's mark, its rule.
	barPad := boxPx(t, decl(t, css, ".bar", "padding"))
	add("bar padding", barPad.top+barPad.bottom)
	add("bar wordmark", px(t, decl(t, css, ".wordmark .dot", "height")))
	add("bar rule", px(t, firstToken(decl(t, css, ".bar", "border-bottom"))))

	// Hero.
	heroPad := boxPx(t, decl(t, css, ".hero", "padding"))
	add("hero padding-top", heroPad.top)
	eyebrow := px(t, decl(t, css, ".eyebrow", "font-size"))
	add("eyebrow", eyebrow*1.5+boxPx(t, decl(t, css, ".eyebrow", "margin")).bottom)

	headline := clampPx(t, decl(t, css, "h1", "font-size"), foldWidth)
	lines := headlineLines(t)
	add("headline", headline*num(t, decl(t, css, "h1", "line-height"))*float64(lines)+
		boxPx(t, decl(t, css, "h1", "margin")).bottom)

	id := identityBlock(t)
	add("tagline", textHeight(t, ".lede", id.tagline))
	add("pitch", textHeight(t, ".pitch", id.pitch)+boxPx(t, decl(t, css, ".pitch", "margin")).top)

	add("hero padding-bottom", heroPad.bottom)
	add("hero rule", px(t, firstToken(decl(t, css, ".hero", "border-bottom"))))

	// The action row itself. The criterion names TWO elements — the download
	// button and the repository link — and .cta-row is `flex-wrap: wrap`, so
	// they share a row only while they fit the column. When they do not, the
	// second drops to a row of its own and the fold has to carry both.
	add("actions padding-top", boxPx(t, decl(t, css, ".actions", "padding")).top)
	height := buttonHeight(t)
	rows := actionRows(t)
	gap := 0.0
	if rows > 1 {
		gap = boxGap(t, decl(t, css, ".cta-row", "gap"))
	}
	add(fmt.Sprintf("action row (%d row(s))", rows), float64(rows)*height+float64(rows-1)*gap)

	fmt.Fprintf(&b, "  %-28s %7.1f", "total", total)
	return total, b.String()
}

// textHeight is one paragraph's rendered height: its own type size and leading,
// times the lines the text needs inside its max-width.
func textHeight(t *testing.T, selector, text string) float64 {
	t.Helper()
	size := px(t, decl(t, css, selector, "font-size"))
	leading := num(t, decl(t, css, selector, "line-height"))
	width := em(t, decl(t, css, selector, "max-width"))
	lines := math.Ceil(0.5 * float64(len([]rune(text))) / width)
	return size * leading * lines
}

func headlineLines(t *testing.T) int {
	t.Helper()
	var ui struct {
		Headline []string `json:"headline"`
	}
	readJSON(t, filepath.Join(repoRoot, "site-src", "ui.json"), &ui)
	if len(ui.Headline) == 0 {
		t.Fatal("site-src/ui.json carries no headline")
	}
	return len(ui.Headline)
}

// --- Criterion 2 -----------------------------------------------------------
//
// The criterion as written says the download button fetches the latest
// release's Gropius.app.zip. The page no longer carries that button: a browser
// download arrives with the quarantine attribute, and because the bundles are
// ad-hoc signed and not notarized, macOS refuses them and offers to move them
// to the Bin — so the button led users to an app they could not open. The
// criterion is left as written, and the divergence is recorded in the
// landing-page intent's Audit Notes for the maintainer's decision.
//
// What is held instead: the page offers no direct asset download at all, and
// still names no version, which is the mechanism the original criterion
// existed to protect.

func TestThePageOffersNoDirectDownload(t *testing.T) {
	href := attr(t, page, `<a class="btn btn-primary" href="([^"]+)"`)
	if href != "#install" {
		t.Errorf("the primary action is %q; it must be the install command", href)
	}
	// No element may hand out an application bundle. The hazard is specific: a
	// browser-fetched .app.zip carries the quarantine attribute, and an ad-hoc
	// signed bundle is then refused. A checksums file is still linked, because
	// it is text — nothing launches it, and it is what a careful user verifies
	// a download against.
	if regexp.MustCompile(`href="[^"]*\.app\.zip"`).MatchString(page) {
		t.Error("the page links an application bundle directly; a browser-fetched archive is quarantined and cannot be opened")
	}
	// The mechanism that keeps the page current between releases: nothing on it
	// names a version, so there is nothing to go stale.
	if m := regexp.MustCompile(`v[0-9]+\.[0-9]+\.[0-9]+`).FindString(page); m != "" {
		t.Errorf("the page carries the version literal %q; a static page cannot keep one current", m)
	}
}

// The interface strings are LABELS. Anything the page asserts about the product
// — what the app is, what a release contains, what it requires, what it is
// licensed under — is the identity block or a span of a repository file, so
// editing the sentence in the README changes the page. A sentence copied into
// ui.json instead is a promise someone has to keep by hand in two places, and
// the page keeps its copy after the README moves on.
func TestInterfaceStringsAreLabelsNotProse(t *testing.T) {
	corpus := normalizeProse(read(t, filepath.Join(repoRoot, "README.md")) + "\n" +
		read(t, filepath.Join(repoRoot, "docs", "getting-started.md")))

	var raw map[string]any
	readJSON(t, filepath.Join(repoRoot, "site-src", "ui.json"), &raw)
	for key, value := range raw {
		// _purpose documents the file to a human reading it; lang is a tag.
		if key == "_purpose" || key == "lang" {
			continue
		}
		for _, v := range flatten(value) {
			if sentences := sentenceCount(v); sentences > 1 || (sentences == 1 && words(v) > 6) {
				t.Errorf("ui.json %q is prose, not a label: %q — select it from a repository file in .abcd/site.json", key, v)
				continue
			}
			if n := normalizeProse(v); words(v) >= 4 && strings.Contains(corpus, n) {
				t.Errorf("ui.json %q repeats repository prose: %q — select it in .abcd/site.json so the page follows the file", key, v)
			}
		}
	}
}

// flatten reduces a ui.json value to the strings inside it.
func flatten(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			out = append(out, flatten(e)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, e := range t {
			out = append(out, flatten(e)...)
		}
		return out
	default:
		return nil
	}
}

// sentenceCount counts sentence-ending stops: one inside a file name
// ("Gropius.app.zip") is not one, because it is followed by a letter.
func sentenceCount(v string) int {
	n := 0
	for _, m := range regexp.MustCompile(`\.(\s|$)`).FindAllString(v, -1) {
		_ = m
		n++
	}
	return n
}

func words(v string) int { return len(strings.Fields(v)) }

// normalizeProse strips the markup a Markdown source carries and the line
// breaks it wraps at, so a sentence can be compared with the same sentence as
// the page would show it.
func normalizeProse(v string) string {
	v = strings.ToLower(v)
	for _, c := range []string{"**", "`", "*", "_"} {
		v = strings.ReplaceAll(v, c, "")
	}
	return " " + strings.Join(strings.Fields(v), " ") + " "
}

// --- Criterion 3 -----------------------------------------------------------
//
// "Given the page is registered as an identity surface, when the identity check
// runs, then it reports no drift between the page and the canonical block."

func TestPageIsAnIdentitySurfaceAndCarriesTheTagline(t *testing.T) {
	var pos struct {
		Surfaces []struct {
			ID       string   `json:"id"`
			Files    []string `json:"files"`
			Kind     string   `json:"kind"`
			Patterns []string `json:"patterns"`
			Requires []string `json:"requires"`
			Template string   `json:"template"`
		} `json:"surfaces"`
	}
	readJSON(t, filepath.Join(repoRoot, ".abcd", "positioning.json"), &pos)

	var surface *struct {
		ID       string   `json:"id"`
		Files    []string `json:"files"`
		Kind     string   `json:"kind"`
		Patterns []string `json:"patterns"`
		Requires []string `json:"requires"`
		Template string   `json:"template"`
	}
	for i := range pos.Surfaces {
		if pos.Surfaces[i].ID == "landing-hero" {
			surface = &pos.Surfaces[i]
		}
	}
	if surface == nil {
		t.Fatal(".abcd/positioning.json has no landing-hero surface; the page is not registered as one")
	}
	if len(surface.Requires) != 1 || surface.Requires[0] != "tagline" {
		t.Errorf("landing-hero requires %v; the surfaces in this repository hold the tagline", surface.Requires)
	}
	// The surface names a build artifact, so it reports absent — not adrift — on
	// an unrendered checkout (recorded in .abcd/work/DECISIONS.md, and the
	// reason the assertion below, not `abcd identity`, is what holds the page on
	// every run). Bind the registration to what the build actually writes, so it
	// cannot rot into naming a path nothing produces.
	if want := renderedPagePath(t); len(surface.Files) != 1 || surface.Files[0] != want {
		t.Errorf("landing-hero names %v; `make site` writes %s", surface.Files, want)
	}

	// The identity check reports drift by pulling the surface's capture out of
	// the file and comparing it with the block. Run the same patterns over the
	// render, so `make test` fails before `abcd identity` would.
	want := identityBlock(t).tagline
	var found string
	for _, p := range surface.Patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			t.Fatalf("landing-hero pattern %q does not compile: %v", p, err)
		}
		if m := re.FindStringSubmatch(page); m != nil {
			found = strings.TrimSpace(m[1])
			break
		}
	}
	if found == "" {
		t.Fatalf("no landing-hero pattern matches the rendered page; the identity check would report it absent")
	}
	if found != want {
		t.Errorf("the page's tagline is\n  %q\nthe canonical block's is\n  %q", found, want)
	}
}

// --- Criterion 4 -----------------------------------------------------------
//
// "Given the tagline in the canonical identity block is edited, when the page is
// rendered again, then the page carries the new tagline."
//
// This is the criterion that distinguishes a render from a lint: nothing here
// edits the page.

func TestEditingTheIdentityBlockChangesThePage(t *testing.T) {
	const altered = "A fixture tagline, to prove the page is rendered from the block rather than written into the template."
	old := identityBlock(t).tagline

	root := fixtureTree(t, func(path string, content string) string {
		if filepath.Base(path) != "IDENTITY.md" {
			return content
		}
		return strings.Replace(content, old, altered, 1)
	})
	out, err := renderTree(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.page, altered) {
		t.Errorf("the page rendered from an edited identity block does not carry the edited tagline")
	}
	if strings.Contains(out.page, old) {
		t.Errorf("the page still carries the tagline from the committed block; it is written into the page, not selected from the block")
	}
}

// fixtureTree copies every file the manifest names into a temporary root,
// passing each through mutate. Copying only what the manifest names is
// deliberate: a file the fixture render needs but the manifest does not name
// would show up here as a failure to render.
func fixtureTree(t *testing.T, mutate func(path, content string) string) string {
	t.Helper()
	root := t.TempDir()
	manifest := filepath.Join(".abcd", "site.json")
	files := []string{manifest}
	// Every "file": in the manifest, plus the template, the strings and the
	// static inputs.
	raw := read(t, filepath.Join(repoRoot, manifest))
	for _, m := range regexp.MustCompile(`"(?:file|ui_strings|template|headers)"\s*:\s*"([^"]+)"`).FindAllStringSubmatch(raw, -1) {
		files = append(files, filepath.FromSlash(m[1]))
	}
	var man struct {
		Static []string `json:"static"`
	}
	readJSON(t, filepath.Join(repoRoot, manifest), &man)
	for _, s := range man.Static {
		files = append(files, filepath.FromSlash(s))
	}
	for _, f := range files {
		content := read(t, filepath.Join(repoRoot, f))
		dst := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(mutate(f, content)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// --- Criterion 5 -----------------------------------------------------------
//
// "Given the platform requirement printed on the page, when it is compared with
// the minimum the shipped app bundle declares, then both say macOS 26."

func TestPageAndBundleAgreeOnTheMinimumMacOS(t *testing.T) {
	plist := read(t, filepath.Join(repoRoot, "build", "Info.plist"))
	m := regexp.MustCompile(`<key>LSMinimumSystemVersion</key>\s*<string>([0-9.]+)</string>`).FindStringSubmatch(plist)
	if m == nil {
		t.Fatal("build/Info.plist declares no LSMinimumSystemVersion")
	}
	major := strings.SplitN(m[1], ".", 2)[0]
	want := "Requires macOS " + major
	if !strings.Contains(page, want) {
		t.Errorf("the bundle declares LSMinimumSystemVersion %s, so the page must say %q; it does not", m[1], want)
	}
}

// --- Criterion 6 -----------------------------------------------------------
//
// "Given the page rendered in its dark theme, when its four-form mark is
// compared with the app icon source, then the arrangement and the four fill
// colors are the same."
//
// Geometry is deliberately not compared: the icon lays 340px cells on a 1024
// tile and the page's mark uses its own proportions, which is a design choice.
// What must not differ is which shape sits in which quadrant and what color it
// is.

func TestMarkMatchesTheAppIcon(t *testing.T) {
	icon := shapesIn(t, read(t, filepath.Join(repoRoot, "build", "icon.svg")))
	mark := shapesIn(t, between(t, page, `<svg class="mark"`, "</svg>"))

	// The arrangement the icon is drawn in, asserted first so a change to the
	// icon cannot quietly redefine what the page is held to.
	for quadrant, kind := range map[string]string{
		"top-left":     "polygon",
		"top-right":    "rect",
		"bottom-left":  "rect",
		"bottom-right": "circle",
	} {
		if got := icon[quadrant].kind; got != kind {
			t.Fatalf("build/icon.svg has a %s in the %s; the mark is a triangle, a grey square, a blue square and a red circle", got, quadrant)
		}
	}

	for quadrant, want := range icon {
		got, ok := mark[quadrant]
		if !ok {
			t.Errorf("the page's mark has nothing in the %s", quadrant)
			continue
		}
		if got.kind != want.kind {
			t.Errorf("%s: the icon has a %s, the page a %s", quadrant, want.kind, got.kind)
		}
		token := cssVar(got.fill)
		if token == "" {
			t.Errorf("%s: the page's fill is %q; it must be a theme token so the two themes stay separable", quadrant, got.fill)
			continue
		}
		// Both dark blocks: the media query for the system setting and the
		// explicit data-theme override. A value defined in only one of them
		// leaves the other theme free to drift.
		for _, block := range darkBlocks(t) {
			value := cssValue(block.body, token)
			if value == "" {
				t.Errorf("%s: %s is not defined in %s", quadrant, token, block.name)
				continue
			}
			if !strings.EqualFold(value, want.fill) {
				t.Errorf("%s: %s is %s in %s; build/icon.svg fills it %s", quadrant, token, value, block.name, want.fill)
			}
		}
	}
}

type shape struct {
	kind string
	fill string
}

// shapesIn maps each quadrant of an SVG to the shape drawn in it. A shape that
// fills the whole tile (the icon's background) is not in a quadrant and is
// skipped.
func shapesIn(t *testing.T, svg string) map[string]shape {
	t.Helper()
	vb := regexp.MustCompile(`viewBox="0 0 ([0-9.]+) ([0-9.]+)"`).FindStringSubmatch(svg)
	if vb == nil {
		t.Fatal("the SVG has no viewBox")
	}
	w, h := num(t, vb[1]), num(t, vb[2])
	out := map[string]shape{}
	place := func(cx, cy float64, s shape) {
		if cx == w/2 && cy == h/2 {
			return // a full-tile background, not one of the four forms
		}
		vertical, horizontal := "top", "left"
		if cy > h/2 {
			vertical = "bottom"
		}
		if cx > w/2 {
			horizontal = "right"
		}
		out[vertical+"-"+horizontal] = s
	}
	for _, m := range regexp.MustCompile(`<polygon ([^/>]*)/>`).FindAllStringSubmatch(svg, -1) {
		// Attributes are read by name, in any order: reordering points and fill
		// is a legal, semantically identical edit to the art.
		a := attrs(m[1])
		var sx, sy float64
		pts := strings.Fields(a["points"])
		if len(pts) == 0 {
			t.Fatalf("a polygon has no points: %s", m[1])
		}
		for _, p := range pts {
			xy := strings.SplitN(p, ",", 2)
			sx += coord(t, xy[0])
			sy += coord(t, xy[1])
		}
		place(sx/float64(len(pts)), sy/float64(len(pts)), shape{kind: "polygon", fill: a["fill"]})
	}
	for _, m := range regexp.MustCompile(`<rect ([^/>]*)/>`).FindAllStringSubmatch(svg, -1) {
		// x and y default to 0 in SVG, and the icon's background rect omits both.
		a := attrs(m[1])
		place(coord(t, a["x"])+coord(t, a["width"])/2, coord(t, a["y"])+coord(t, a["height"])/2,
			shape{kind: "rect", fill: a["fill"]})
	}
	for _, m := range regexp.MustCompile(`<circle ([^/>]*)/>`).FindAllStringSubmatch(svg, -1) {
		a := attrs(m[1])
		place(coord(t, a["cx"]), coord(t, a["cy"]), shape{kind: "circle", fill: a["fill"]})
	}
	if len(out) != 4 {
		t.Fatalf("expected four forms, one per quadrant; found %d: %v", len(out), out)
	}
	return out
}

type namedBlock struct {
	name string
	body string
}

func darkBlocks(t *testing.T) []namedBlock {
	t.Helper()
	media := blockAfter(t, css, "@media (prefers-color-scheme: dark)")
	return []namedBlock{
		{name: `@media (prefers-color-scheme: dark)`, body: blockAfter(t, media, `:root:not([data-theme="light"])`)},
		{name: `:root[data-theme="dark"]`, body: blockAfter(t, css, `:root[data-theme="dark"]`)},
	}
}

// --- Criterion 7 -----------------------------------------------------------
//
// "Given the page is viewed at a 375-pixel-wide viewport, when it renders, then
// nothing scrolls horizontally and the download button is the first action."

func TestNothingScrollsSidewaysOnAPhone(t *testing.T) {
	if !strings.Contains(page, `<meta name="viewport" content="width=device-width, initial-scale=1">`) {
		t.Error("the page has no viewport meta; a phone would render it at desktop width and scale it down")
	}
	if got := decl(t, css, ".snippet", "overflow-x"); got != "auto" {
		t.Errorf(".snippet overflow-x is %q; the install one-liner is wider than a phone and must scroll inside its own box", got)
	}
	if got := decl(t, css, "img", "max-width"); got != "100%" {
		t.Errorf("img max-width is %q; an image wider than the viewport would push the page sideways", got)
	}
	// A fixed width above a phone's viewport is the other way a page starts
	// scrolling. max-width is a ceiling, not a floor, so it is not one.
	for _, m := range regexp.MustCompile(`(?m)(?:^|[\s;{])(?:min-)?width:\s*([0-9.]+)px`).FindAllStringSubmatch(css, -1) {
		if num(t, m[1]) > 390 {
			t.Errorf("the stylesheet fixes a width of %spx; nothing above 390px may be fixed", m[1])
		}
	}
	for _, m := range regexp.MustCompile(`style="[^"]*width:\s*([0-9.]+)px`).FindAllStringSubmatch(page, -1) {
		if num(t, m[1]) > 390 {
			t.Errorf("the page sets an inline width of %spx", m[1])
		}
	}

	// The download button is the first thing in the action row, on every
	// viewport: the row is source order, and the phone layout stacks it.
	row := between(t, page, `class="cta-row"`, "</div>")
	first := regexp.MustCompile(`(?s)<a class="([^"]+)"`).FindStringSubmatch(row)
	if first == nil {
		t.Fatal("the action row holds no links")
	}
	if !strings.Contains(first[1], "btn-primary") {
		t.Errorf("the first action is %q; the download button comes first", first[1])
	}
}

// A grid item's min-width is auto, not zero, so a track holds at the intrinsic
// width of the widest unwrappable thing inside it however small its fr share
// says it should be. The install one-liner is 894px of unbreakable command, so
// every section that shares a row with it was pushed off the page: measured in
// a browser before the rule under test existed, the document scrolled sideways
// by 547px at a 375px viewport, 286px at 900px and 86px at 1100px — at every
// width tried between 375 and 1200. The .snippet overflow-x rule above cannot
// help, and was measurably never engaging: the track grew instead of the box
// scrolling. TestNothingScrollsSidewaysOnAPhone passed throughout, because it
// reads declarations and a viewport is the one thing a stylesheet cannot state.
func TestEveryMultiColumnSectionCanShrinkBelowItsContent(t *testing.T) {
	var covered string
	for _, m := range regexp.MustCompile(`(?s)([^{}]+)\{[^{}]*min-width:\s*0\s*[;}]`).FindAllStringSubmatch(css, -1) {
		covered += m[1]
	}
	for _, section := range []string{".hero", ".actions", ".install"} {
		if !strings.Contains(covered, section+" > *") {
			t.Errorf("nothing lets the children of %s shrink below their content; one unwrappable line in either column pushes the other off the page at every viewport", section)
		}
	}
}

// The dark ground belongs to the box, not to the thing scrolling inside it. A
// background painted on the <pre> is only ever as wide as the box, so scrolling
// the one-liner slid the ground out from under it and left the tail of the
// command sitting on the bare page. Nobody saw it while the track was growing
// instead of scrolling; the moment the snippet actually scrolled, it showed.
func TestTheCodeBoxStaysPutWhileTheCommandScrolls(t *testing.T) {
	if got := decl(t, css, ".snippet", "background"); got != "var(--code-bg)" {
		t.Errorf(".snippet background is %q; the ground belongs to the box that stays put", got)
	}
	if got := decl(t, css, ".snippet", "border-left"); !strings.Contains(got, "var(--rule)") {
		t.Errorf(".snippet border-left is %q; the rule marks the box, so it cannot scroll away with the text", got)
	}
	// And the scrolling child paints no ground of its own, or the two grounds
	// come apart again the moment they differ in width.
	if body := blockAfter(t, css, "pre"); strings.Contains(body, "background:") {
		t.Error("pre paints its own background; a ground on the scrolling child is only as wide as the box")
	}
}

// A visitor's next act after reading the install section is to copy the command
// out of it, and selecting a URL-bearing one-liner by dragging is fiddly on a
// trackpad and worse on a phone. One click selects the whole command instead.
// It is CSS rather than a clipboard button on purpose: the page carries no
// script and the policy in site-src/headers refuses one, which release_test.go
// holds at script-src 'none'.
func TestOneClickSelectsAWholeCommand(t *testing.T) {
	for _, property := range []string{"user-select", "-webkit-user-select"} {
		if got := decl(t, css, ".snippet .cmd", property); got != "all" {
			t.Errorf(".snippet .cmd %s is %q; one click must take the whole command, not the word under the pointer", property, got)
		}
	}
	// Every command the page shows is selectable, not just the first: the
	// client install is the one a visitor on an Intel Mac needs.
	commands := strings.Count(page, `<span class="cmd">`)
	steps := strings.Count(page, `<span class="c">`)
	if commands != steps || commands == 0 {
		t.Errorf("the page has %d selectable commands for %d install steps; every step's command carries the handle", commands, steps)
	}
}

// The mark is decoration. On a narrow viewport the stacked layout put it above
// the headline, so the first thing on a phone was a 220px logo and the sentence
// saying what Gropius is fell below it.
func TestTheMarkStepsAsideOnANarrowViewport(t *testing.T) {
	narrow := blockAfter(t, css, "@media (max-width: 820px)")
	if got := decl(t, narrow, ".mark", "display"); got != "none" {
		t.Errorf(".mark display is %q on a narrow viewport; the decoration must not take the top of the page from the headline", got)
	}
}

// The facts are a two-column definition list. On a phone the labels hold their
// own column and the values wrap in what is left of 375px, which is why they
// stack instead below the width where that stops reading as a list.
func TestTheFactListStacksOnAPhone(t *testing.T) {
	phone := blockAfter(t, css, "@media (max-width: 560px)")
	if got := decl(t, phone, ".facts", "grid-template-columns"); got != "1fr" {
		t.Errorf(".facts grid-template-columns is %q on a phone; the label column leaves too little for the value", got)
	}
}

// buttonHeight is one .btn: its padding, its border, and the two lines of text
// inside it.
func buttonHeight(t *testing.T) float64 {
	t.Helper()
	pad := boxPx(t, decl(t, css, ".btn", "padding"))
	text := px(t, decl(t, css, ".btn", "font-size"))*num(t, decl(t, css, ".btn", "line-height")) +
		px(t, decl(t, css, ".btn small", "font-size"))*num(t, decl(t, css, ".btn small", "line-height"))
	return pad.top + pad.bottom + 2*px(t, firstToken(decl(t, css, ".btn", "border"))) + text
}

// actionRows is how many rows the two buttons occupy in the left column at
// 1280px wide. The advance model is half an em per character plus whatever
// tracking the stylesheet declares, which is what makes a wider label — or the
// Futura fallback when the web font does not load — show up here as a second row
// rather than as a surprise below the fold.
func actionRows(t *testing.T) int {
	t.Helper()
	column := actionColumnWidth(t)
	gap := boxGap(t, decl(t, css, ".cta-row", "gap"))
	var total float64
	for i, b := range buttons(t) {
		if i > 0 {
			total += gap
		}
		total += b
	}
	if total <= column {
		return 1
	}
	return 2
}

// buttons is each action's rendered width: padding, border, the icon and its
// gap, and the wider of the label and the note beneath it.
func buttons(t *testing.T) []float64 {
	t.Helper()
	pad := boxPx(t, decl(t, css, ".btn", "padding"))
	frame := pad.left + pad.right + 2*px(t, firstToken(decl(t, css, ".btn", "border"))) +
		px(t, decl(t, css, ".btn svg", "width")) + boxGap(t, decl(t, css, ".btn", "gap"))
	label := advance(t, ".btn")
	note := advance(t, ".btn small")

	var ui struct {
		DownloadLabel   string `json:"download_label"`
		DownloadNote    string `json:"download_note"`
		RepositoryLabel string `json:"repository_label"`
	}
	readJSON(t, filepath.Join(repoRoot, "site-src", "ui.json"), &ui)
	var man struct {
		Forge struct {
			Repository string `json:"repository"`
		} `json:"forge"`
	}
	readJSON(t, filepath.Join(repoRoot, ".abcd", "site.json"), &man)

	widest := func(label, note string, labelAdv, noteAdv float64) float64 {
		return frame + math.Max(float64(len([]rune(label)))*labelAdv, float64(len([]rune(note)))*noteAdv)
	}
	return []float64{
		widest(ui.DownloadLabel, ui.DownloadNote, label, note),
		widest(ui.RepositoryLabel, man.Forge.Repository, label, note),
	}
}

// advance is one character's width for a selector: half an em, plus the tracking
// the stylesheet declares for it.
func advance(t *testing.T, selector string) float64 {
	t.Helper()
	size := px(t, decl(t, css, selector, "font-size"))
	return size * (0.5 + em(t, decl(t, css, selector, "letter-spacing")))
}

// actionColumnWidth is the left column of .actions at 1280px: the page's own
// width and padding, then the grid's fractions and gap.
func actionColumnWidth(t *testing.T) float64 {
	t.Helper()
	pagePad := boxPx(t, decl(t, css, ".page", "padding"))
	content := math.Min(foldWidth, px(t, decl(t, css, ".page", "max-width"))) - pagePad.left - pagePad.right
	cols := regexp.MustCompile(`([0-9.]+)fr\s+([0-9.]+)fr`).FindStringSubmatch(decl(t, css, ".actions", "grid-template-columns"))
	if cols == nil {
		t.Fatalf("cannot read .actions grid-template-columns")
	}
	left, right := num(t, cols[1]), num(t, cols[2])
	gap := boxGap(t, decl(t, css, ".actions", "gap"))
	return (content - gap) * left / (left + right)
}

// boxGap reads a gap shorthand, whose last value is the column gap.
func boxGap(t *testing.T, v string) float64 {
	t.Helper()
	f := strings.Fields(v)
	return px(t, f[len(f)-1])
}

func repositoryURL(t *testing.T) string {
	t.Helper()
	var man struct {
		Forge struct {
			Base       string `json:"base"`
			Repository string `json:"repository"`
		} `json:"forge"`
	}
	readJSON(t, filepath.Join(repoRoot, ".abcd", "site.json"), &man)
	return strings.TrimSuffix(man.Forge.Base, "/") + "/" + man.Forge.Repository
}

// renderedPagePath is where `make site` puts the page: the Makefile's --out
// directory, the manifest's out_subdir, index.html.
func renderedPagePath(t *testing.T) string {
	t.Helper()
	makefile := read(t, filepath.Join(repoRoot, "Makefile"))
	m := regexp.MustCompile(`gropius-site\s+--out\s+(\S+)`).FindStringSubmatch(makefile)
	if m == nil {
		t.Fatal("the Makefile has no `site` target invoking the renderer with --out")
	}
	var man struct {
		OutSubdir string `json:"out_subdir"`
	}
	readJSON(t, filepath.Join(repoRoot, ".abcd", "site.json"), &man)
	return path.Join(m[1], man.OutSubdir, "index.html")
}

// --- helpers ---------------------------------------------------------------

type identityLines struct {
	title   string
	tagline string
	pitch   string
}

func identityBlock(t *testing.T) identityLines {
	t.Helper()
	var man struct {
		Identity struct {
			File string `json:"file"`
		} `json:"identity"`
	}
	readJSON(t, filepath.Join(repoRoot, ".abcd", "site.json"), &man)
	md := read(t, filepath.Join(repoRoot, filepath.FromSlash(man.Identity.File)))
	get := func(name string) string {
		m := regexp.MustCompile(`(?m)^- \*\*` + name + `:\*\*\s*(.+?)\s*$`).FindStringSubmatch(md)
		if m == nil {
			t.Fatalf("the identity block carries no %s", name)
		}
		return m[1]
	}
	return identityLines{title: get("Title"), tagline: get("Tagline"), pitch: get("Pitch")}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(read(t, path)), v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func attr(t *testing.T, s, pattern string) string {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("no match for %s", pattern)
	}
	return m[1]
}

func attrs(s string) map[string]string {
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`([a-zA-Z-]+)="([^"]*)"`).FindAllStringSubmatch(s, -1) {
		out[m[1]] = m[2]
	}
	return out
}

func between(t *testing.T, s, from, to string) string {
	t.Helper()
	i := strings.Index(s, from)
	if i < 0 {
		t.Fatalf("no %q", from)
	}
	rest := s[i:]
	j := strings.Index(rest, to)
	if j < 0 {
		t.Fatalf("no %q after %q", to, from)
	}
	return rest[:j+len(to)]
}

// decl returns one declaration of one top-level rule. Rules in this stylesheet
// start at column 0, so the selector is matched at the start of a line and
// ".bar" does not match ".bar nav".
func decl(t *testing.T, style, selector, property string) string {
	t.Helper()
	body := blockAfter(t, style, selector)
	m := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(property) + `:\s*([^;]+);`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("%s has no %s", selector, property)
	}
	return strings.TrimSpace(m[1])
}

// blockAfter returns the braced block that follows marker, brace-matched so a
// nested rule (a media query's own contents) comes back whole.
func blockAfter(t *testing.T, s, marker string) string {
	t.Helper()
	loc := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(marker) + `\s*\{`).FindStringIndex(s)
	if loc == nil {
		t.Fatalf("no rule for %s", marker)
	}
	depth, start := 0, loc[1]-1
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start+1 : i]
			}
		}
	}
	t.Fatalf("unbalanced braces after %s", marker)
	return ""
}

func cssValue(block, name string) string {
	m := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `:\s*([^;]+);`).FindStringSubmatch(block)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func cssVar(fill string) string {
	m := regexp.MustCompile(`^var\((--[a-z-]+)\)$`).FindStringSubmatch(strings.TrimSpace(fill))
	if m == nil {
		return ""
	}
	return m[1]
}

type box struct{ top, right, bottom, left float64 }

// boxPx reads a padding or margin shorthand.
func boxPx(t *testing.T, v string) box {
	t.Helper()
	f := strings.Fields(v)
	get := func(i int) float64 {
		if strings.HasSuffix(f[i], "px") {
			return px(t, f[i])
		}
		return 0 // "0" and "auto" contribute nothing to the fold
	}
	switch len(f) {
	case 1:
		return box{get(0), get(0), get(0), get(0)}
	case 2:
		return box{get(0), get(1), get(0), get(1)}
	case 3:
		return box{get(0), get(1), get(2), get(1)}
	case 4:
		return box{get(0), get(1), get(2), get(3)}
	}
	t.Fatalf("cannot read the box shorthand %q", v)
	return box{}
}

func firstToken(v string) string { return strings.Fields(v)[0] }

func px(t *testing.T, v string) float64 {
	t.Helper()
	return num(t, strings.TrimSuffix(strings.TrimSpace(v), "px"))
}

func em(t *testing.T, v string) float64 {
	t.Helper()
	return num(t, strings.TrimSuffix(strings.TrimSpace(v), "em"))
}

// coord reads an SVG coordinate attribute; an absent one is 0, which is what
// SVG itself does with a missing x or y.
func coord(t *testing.T, v string) float64 {
	t.Helper()
	if strings.TrimSpace(v) == "" {
		return 0
	}
	return num(t, v)
}

func num(t *testing.T, v string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		t.Fatalf("not a number: %q", v)
	}
	return f
}

// clampPx evaluates clamp(min, preferred, max) at one viewport width, which is
// what the browser does before anything else on the page has a height.
func clampPx(t *testing.T, v string, viewport float64) float64 {
	t.Helper()
	m := regexp.MustCompile(`^clamp\(([^,]+),([^,]+),([^)]+)\)$`).FindStringSubmatch(strings.ReplaceAll(v, " ", ""))
	if m == nil {
		return px(t, v)
	}
	lo, hi := px(t, m[1]), px(t, m[3])
	pref := m[2]
	var got float64
	switch {
	case strings.HasSuffix(pref, "vw"):
		got = num(t, strings.TrimSuffix(pref, "vw")) / 100 * viewport
	default:
		got = px(t, pref)
	}
	return math.Min(math.Max(got, lo), hi)
}
