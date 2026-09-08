package sitetest_test

// The release facts: the page is a view over the release record rather than a
// copy of it (itd-2609061353258535). Every check here renders the COMMITTED
// tree — the same manifest, template and stylesheet the deploy chain renders —
// against a fixture release record, so what is tested is the page the release
// run produces and not a second description of it.
//
// No network and no forge: the record arrives as a file, which is the whole
// point of the transport. WHICH release the record describes is decided by
// electLatest in cmd/gropius-site and tested against a fixture there
// (TestTheFlaggedLatestIsElectedAndNotTheNewest); what is held here is the shape
// of the workflow that calls it — that it lists the releases with the forge's
// own flag, elects through `gropius-site select`, fetches the elected tag, and
// validates the record before the render commits to it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A release that cannot be confused with a real one: no v9 exists, and the
// sizes are chosen so the truncation rule is visible in the expected strings
// (20971520 bytes is 20.97… MB and must show as 20.9 MB, never 21.0 MB).
const fixtureRelease = `{
  "version": "v9.9.9",
  "published_at": "2026-09-05T09:41:07Z",
  "html_url": "https://github.com/intentdriven/Gropius/releases/tag/v9.9.9",
  "checksums_url": "https://github.com/intentdriven/Gropius/releases/download/v9.9.9/SHA256SUMS.txt",
  "assets": [
    {
      "name": "Gropius.app.zip",
      "size_bytes": 20971520,
      "url": "https://github.com/intentdriven/Gropius/releases/download/v9.9.9/Gropius.app.zip"
    },
    {
      "name": "GropiusChat.app.zip",
      "size_bytes": 4718592,
      "url": "https://github.com/intentdriven/Gropius/releases/download/v9.9.9/GropiusChat.app.zip"
    },
    {
      "name": "SHA256SUMS.txt",
      "size_bytes": 210,
      "url": "https://github.com/intentdriven/Gropius/releases/download/v9.9.9/SHA256SUMS.txt"
    }
  ]
}`

// releaseFile writes a release record for one render.
func releaseFile(t *testing.T, record string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.json")
	if err := os.WriteFile(path, []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// renderWithRelease renders the committed tree against one release record.
func renderWithRelease(t *testing.T, record string) string {
	t.Helper()
	out, err := renderTreeWithRelease(".", t.TempDir(), releaseFile(t, record))
	if err != nil {
		t.Fatalf("the committed tree must render against a release record: %v", err)
	}
	return out.page
}

// releaseRegion is the part of the page the release facts own. The handle is
// the class, not a comment: html/template elides comments, so nothing else in
// the rendered output marks the region.
func releaseRegion(t *testing.T, p string) string {
	t.Helper()
	return between(t, p, `<div class="release-facts">`, "</div>")
}

// --- Criterion 1 -----------------------------------------------------------
//
// "Given a release named on the page, when Bob merges a changelog roll that
// publishes a newer release, then the page names the newer release and the only
// change to the repository since the previous release is that changelog roll."
//
// In test form: the SAME committed tree, rendered twice against two records,
// names each record's own release. Nothing between the two renders edits a byte
// of the page's source, which is the property the criterion is about.

func TestANewerReleaseChangesThePageAndNothingElse(t *testing.T) {
	first := renderWithRelease(t, fixtureRelease)
	newer := strings.ReplaceAll(fixtureRelease, "9.9.9", "9.10.0")
	second := renderWithRelease(t, newer)

	if !strings.Contains(releaseRegion(t, first), "v9.9.9") {
		t.Errorf("the first render does not name v9.9.9:\n%s", releaseRegion(t, first))
	}
	if !strings.Contains(releaseRegion(t, second), "v9.10.0") {
		t.Errorf("the second render does not name v9.10.0:\n%s", releaseRegion(t, second))
	}
	// One release named, not two: a render that kept the previous record's
	// version alongside the new one would satisfy every check above.
	versions := regexp.MustCompile(`v[0-9]+\.[0-9]+\.[0-9]+`).FindAllString(releaseRegion(t, second), -1)
	for _, v := range versions {
		if v != "v9.10.0" {
			t.Errorf("the second render also names %s; the page names one release, the one the record describes", v)
		}
	}
	if len(versions) == 0 {
		t.Error("the second render names no release at all")
	}
	// The two renders must differ only where the record differs. Everything
	// outside the region is composed from the repository, which did not change.
	if strip(first) != strip(second) {
		t.Error("a new release changed the page outside the release region; only the release facts may move with the record")
	}
}

// strip removes the release region, leaving the part of the page the repository
// composes.
func strip(p string) string {
	i := strings.Index(p, `<div class="release-facts">`)
	if i < 0 {
		return p
	}
	rest := p[i:]
	j := strings.Index(rest, "</div>")
	if j < 0 {
		return p
	}
	return p[:i] + rest[j:]
}

// --- Criterion 2 -----------------------------------------------------------
//
// "Given a published release, when Alice reads the release section, then the
// version, the publication date, every asset with its size and the checksums
// link match that release's own record."

func TestTheReleaseSectionMatchesTheRecordFieldByField(t *testing.T) {
	p := renderWithRelease(t, fixtureRelease)
	region := releaseRegion(t, p)

	var record struct {
		Version      string `json:"version"`
		HTMLURL      string `json:"html_url"`
		ChecksumsURL string `json:"checksums_url"`
		Assets       []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal([]byte(fixtureRelease), &record); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(region, ">"+record.Version+"<") {
		t.Errorf("the region does not name the record's version %q:\n%s", record.Version, region)
	}
	if !strings.Contains(region, record.HTMLURL) {
		t.Errorf("the region does not link the record's release page %q", record.HTMLURL)
	}
	// The publication date, in the page's own language. The record carries an
	// instant; the page shows the day, because that is the fact a reader uses.
	if !strings.Contains(region, "5 September 2026") {
		t.Errorf("the region does not carry the record's publication date as a date:\n%s", region)
	}

	// Every asset exactly once, each with its size in human units. The sizes are
	// written here as the literal a reader sees, so a change to the rounding
	// rule fails rather than passing under a recomputed expectation.
	for _, want := range []struct{ name, size string }{
		{"Gropius.app.zip", "20.9 MB"},
		{"GropiusChat.app.zip", "4.7 MB"},
		{"SHA256SUMS.txt", "210 bytes"},
	} {
		if n := strings.Count(region, ">"+want.name+"<"); n != 1 {
			t.Errorf("%s appears %d time(s) in the release region; each asset appears exactly once", want.name, n)
		}
		if !strings.Contains(region, ">"+want.size+"<") {
			t.Errorf("the region does not show %s as %q:\n%s", want.name, want.size, region)
		}
	}
	// The assets are named and sized from the record, but NOT linked: a
	// browser-fetched .app.zip arrives quarantined, and an ad-hoc signed bundle
	// is then refused by Gatekeeper. The region states what a release contains;
	// the install command is how a user gets it. The checksums file below is
	// still linked, because it is text and nothing launches it.
	for _, a := range record.Assets {
		if strings.HasSuffix(a.Name, ".app.zip") && strings.Contains(region, a.URL) {
			t.Errorf("the region links %s at %q; an application bundle must not be offered as a direct download", a.Name, a.URL)
		}
	}

	// The checksums link is the record's SHA256SUMS.txt on that release, not a
	// link to the releases page or to a file named by hand.
	href := attr(t, region, `<a href="([^"]*SHA256SUMS[^"]*)"`)
	if href != record.ChecksumsURL {
		t.Errorf("the checksums link is %q; the record says %q", href, record.ChecksumsURL)
	}

	// Nothing on the page names a release the record does not: the download
	// button is still the forge's latest-release redirect, which carries no
	// version, so the two cannot disagree.
	button := attr(t, p, `<a class="btn btn-primary" href="([^"]+)"`)
	if button != "#install" {
		t.Errorf("the primary action is %q; the release facts must not rewrite it", button)
	}
	for _, m := range regexp.MustCompile(`v[0-9]+\.[0-9]+\.[0-9]+`).FindAllString(strip(p), -1) {
		t.Errorf("the page names the version %q outside the release region; only the region may", m)
	}
}

// --- Criterion 3 -----------------------------------------------------------
//
// "Given a release created more recently than the one flagged latest, when the
// page is produced, then the page names the release flagged latest."
//
// The election is a function in this repository — electLatest, reached through
// `gropius-site select --from <list>` — and the criterion's own case is a
// fixture test of it in cmd/gropius-site: a list whose two newest-published
// entries are a pre-release and a draft, with an older release flagged, must
// elect the flagged one. That case cannot be produced against a live forge
// without hand-making a stale release, which is why the election was moved here
// rather than left to the forge's `releases/latest` endpoint (which makes the
// same election, correctly, but gives nothing that can fail).
//
// What THIS test holds is the wiring the fixture cannot reach: that the workflow
// hands electLatest a list carrying the forge's flag and both exclusions, that
// it elects through the verb rather than in shell, that the release it then
// fetches is the elected one, and that no name the run's own tag could arrive
// under is read anywhere in the step.

func TestTheReleaseRecordIsTheFlaggedLatestRelease(t *testing.T) {
	step := releaseStep(t)

	// The list the election reads must carry the forge's own flag and the two
	// exclusions, or electLatest is deciding on fields it was not given.
	if !regexp.MustCompile(`gh release list\s+--repo\s+"\$GITHUB_REPOSITORY"[^|]*--json\s+tagName,isLatest,isDraft,isPrerelease,publishedAt`).MatchString(step) {
		t.Errorf("the release-record step does not list the releases with the forge's isLatest flag:\n%s", step)
	}
	// The election itself is cmd/gropius-site's, which is what makes the
	// criterion testable against a fixture (TestTheFlaggedLatestIsElectedAndNotTheNewest
	// in that package). A step that elected in shell would be untestable here.
	if !strings.Contains(step, `gropius-site select --from "$list"`) {
		t.Errorf("the release-record step does not elect through `gropius-site select`:\n%s", step)
	}
	// And the release it then fetches is the elected one. Every name the run's
	// own tag could arrive under is refused, so a later edit cannot quietly
	// point the record at the tag being deployed.
	if !strings.Contains(step, `gh release view "$tag" --repo "$GITHUB_REPOSITORY"`) {
		t.Errorf("the release-record step does not fetch the elected tag:\n%s", step)
	}
	for _, steer := range []string{"INPUT_TAG", "inputs.tag", "needs.resolve.outputs.tag", "GITHUB_REF_NAME", "github.ref_name"} {
		if strings.Contains(step, steer) {
			t.Errorf("the release-record step reads %s; the page names the flagged latest release, never the tag the run carries", steer)
		}
	}
	// The record is validated before the render commits to it, so an
	// unacceptable one costs the page its facts and not its deploy.
	if !strings.Contains(step, "gropius-site validate-release") {
		t.Errorf("the release-record step does not validate the record before handing it on:\n%s", step)
	}
}

// releaseStep is the render job's release-record step, delimited by the markers
// the workflow carries for exactly this purpose.
func releaseStep(t *testing.T) string {
	t.Helper()
	wf := read(t, filepath.Join(repoRoot, ".github", "workflows", "site.yml"))
	return between(t, wf, "# release record: begin", "# release record: end")
}

// --- Criterion 4 -----------------------------------------------------------
//
// "Given the release record cannot be read while the page is being produced,
// when Alice loads the page, then it serves successfully with its download
// button and without a release section."

func TestThePageRendersWithoutAReleaseRecord(t *testing.T) {
	// `page` is the committed tree rendered with no --release at all: the local
	// `make site` path, and the path the workflow takes when the record could
	// not be read.
	region := releaseRegion(t, page)

	// The region is the static list of what a release contains — the shipped
	// page, unchanged — and names no release.
	var ui struct {
		AssetsHeading string `json:"assets_heading"`
		Assets        []struct {
			File string `json:"file"`
			Note string `json:"note"`
		} `json:"assets"`
	}
	readJSON(t, filepath.Join(repoRoot, "site-src", "ui.json"), &ui)
	want := "<h2>" + ui.AssetsHeading + `</h2> <ul class="assets">`
	for _, a := range ui.Assets {
		want += ` <li><span class="mono">` + a.File + "</span><span>" + a.Note + "</span></li>"
	}
	want += " </ul>"
	if got := collapse(region); !strings.Contains(got, want) {
		t.Errorf("without a release record the region is not the shipped page's asset list.\ngot:  %s\nwant: %s", got, want)
	}
	if m := regexp.MustCompile(`v[0-9]+\.[0-9]+\.[0-9]+`).FindString(page); m != "" {
		t.Errorf("the page carries the version literal %q with no release record to justify it", m)
	}

	// The download button is untouched by the record's absence.
	href := attr(t, page, `<a class="btn btn-primary" href="([^"]+)"`)
	if href != "#install" {
		t.Errorf("the primary action is %q without a release record; it must be unchanged", href)
	}
}

// The workflow's half of the same criterion: the record is read in a step that
// may fail without failing the job, and --release is passed only when the file
// it would name exists.
func TestTheRenderProceedsWhenTheRecordCannotBeRead(t *testing.T) {
	step := releaseStep(t)
	if !strings.Contains(step, "continue-on-error: true") {
		t.Errorf("the release-record step is not continue-on-error; a forge outage would fail the deploy instead of dropping the release facts:\n%s", step)
	}
	render := renderStep(t)
	if !strings.Contains(render, `if [ -s "$RELEASE_JSON" ]`) {
		t.Errorf("the render step does not test for the record before passing --release:\n%s", render)
	}
	if !strings.Contains(render, "--release") {
		t.Errorf("the render step never passes --release:\n%s", render)
	}
	if !regexp.MustCompile(`go run \./cmd/gropius-site --out site\s`).MatchString(render) {
		t.Errorf("the render step has no invocation without --release; the record's absence must still produce a page:\n%s", render)
	}
}

// The other half of criterion 1: "when Bob merges a changelog roll that
// publishes a newer release". Every other check on this branch exercises
// site.yml's insides, and every one of them would still pass if the release
// chain stopped calling site.yml at all — at which point the page freezes at
// whatever release it was last rendered from, which is the exact staleness this
// record exists to prevent. So the call itself is held here.
func TestTheReleaseChainRendersThePage(t *testing.T) {
	wf := read(t, filepath.Join(repoRoot, ".github", "workflows", "release.yml"))
	job := jobIn(t, wf, "site")

	if !strings.Contains(job, "uses: ./.github/workflows/site.yml") {
		t.Errorf("release.yml's site job does not call site.yml:\n%s", job)
	}
	// After the release, not beside it: the page must render from a release that
	// exists, and a site failure must not be able to precede or replace the
	// publish.
	if !regexp.MustCompile(`(?m)^\s*needs:\s*release\s*$`).MatchString(job) {
		t.Errorf("release.yml's site job does not need the release job:\n%s", job)
	}
	// And it is told which tag was released. The page elects its own release, but
	// the call carries the tag because site.yml resolves the commit to render
	// from it.
	if !regexp.MustCompile(`(?m)^\s*tag:\s*\S`).MatchString(job) {
		t.Errorf("release.yml's site job passes no tag to site.yml:\n%s", job)
	}
	// A called workflow cannot exceed its caller's grants, so the call has to
	// carry the permission site.yml's own jobs ask for.
	if !strings.Contains(job, "contents: read") {
		t.Errorf("release.yml's site job grants site.yml no contents: read; its jobs would be refused:\n%s", job)
	}
}

// jobIn returns one job's block from a workflow: everything from its key at two
// spaces of indent up to the next key at that indent.
func jobIn(t *testing.T, wf, name string) string {
	t.Helper()
	start := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(name) + `:$`).FindStringIndex(wf)
	if start == nil {
		t.Fatalf("the workflow has no %q job", name)
	}
	rest := wf[start[1]:]
	if end := regexp.MustCompile(`(?m)^  [A-Za-z0-9_-]+:$`).FindStringIndex(rest); end != nil {
		rest = rest[:end[0]]
	}
	return rest
}

func renderStep(t *testing.T) string {
	t.Helper()
	wf := read(t, filepath.Join(repoRoot, ".github", "workflows", "site.yml"))
	return between(t, wf, "# render: begin", "# render: end")
}

// --- Criterion 5 -----------------------------------------------------------
//
// "Given the deployed page, when it is loaded with the network inspector open,
// then no request goes anywhere other than the page's own origin and the web
// font service, and the page sets no cookie of its own."
//
// What a browser REQUESTS is subresources: stylesheets, scripts, images, fonts.
// An <a href> is a place the reader may choose to go, not a request the page
// makes, so the anchors to the forge are outside this bar — which is why the
// walk below is over subresource references rather than over every href.

var fontHosts = []string{"fonts.googleapis.com", "fonts.gstatic.com"}

// offOrigin is every reference in a page (and its stylesheet) that would take a
// browser off the page's own origin and off the web font service.
//
// It walks what a browser REACHES FOR, which is more than what it fetches:
// `src`, the link elements that fetch something, `url()` in the stylesheet — and
// `preconnect`/`dns-prefetch`, which are not fetches at all. Those two matter
// most: no CSP fetch directive governs them, so a hint added to the template
// would open a DNS lookup and a TLS handshake to a third party on every load
// with nothing at serve time to stop it. An `<a href>` is deliberately NOT
// walked: it is a place a reader may choose to go, not a request the page makes.
func offOrigin(p, style string) []string {
	var off []string
	check := func(where, ref string) {
		ref = strings.TrimSpace(ref)
		switch {
		case ref == "", strings.HasPrefix(ref, "#"), strings.HasPrefix(ref, "data:"):
			return
		case !strings.Contains(ref, "//"):
			return // relative: the page's own origin
		}
		for _, host := range fontHosts {
			if strings.HasPrefix(ref, "https://"+host+"/") || ref == "https://"+host {
				return
			}
		}
		off = append(off, where+" "+ref)
	}
	for _, m := range regexp.MustCompile(`\bsrc="([^"]+)"`).FindAllStringSubmatch(p, -1) {
		check("src", m[1])
	}
	for _, tag := range regexp.MustCompile(`<link\b[^>]*>`).FindAllString(p, -1) {
		a := attrs(tag)
		switch a["rel"] {
		case "stylesheet", "preload", "prefetch", "preconnect", "dns-prefetch",
			"icon", "apple-touch-icon", "manifest", "modulepreload", "prerender":
			check("link rel="+a["rel"], a["href"])
		}
	}
	for _, m := range regexp.MustCompile(`url\(\s*['"]?([^'")]+)`).FindAllStringSubmatch(style, -1) {
		check("css url()", m[1])
	}
	return off
}

func TestThePageRequestsNothingOffItsOwnOrigin(t *testing.T) {
	for name, p := range map[string]string{
		"without a release record": page,
		"with a release record":    renderWithRelease(t, fixtureRelease),
	} {
		for _, ref := range offOrigin(p, css) {
			t.Errorf("%s: the page reaches for %s; it requests nothing but its own origin and the web font service", name, ref)
		}
		if strings.Contains(p, "<script") {
			t.Errorf("%s: the page carries a script element; it must run nothing", name)
		}
		for _, cookie := range []string{"document.cookie", `http-equiv="set-cookie"`, `http-equiv="Set-Cookie"`} {
			if strings.Contains(p, cookie) {
				t.Errorf("%s: the page carries %q; it sets no cookie of its own", name, cookie)
			}
		}
	}
}

// The audit's own test: an origin added in any of the positions it walks must be
// reported. Without this, the walk above passes for every page it does not read.
func TestTheOriginAuditCatchesAnAddedOrigin(t *testing.T) {
	const foreign = "https://cdn.example.invalid"
	for _, tc := range []struct{ name, page, css string }{
		{"a preconnect", `<link rel="preconnect" href="` + foreign + `">`, ""},
		{"a dns-prefetch", `<link rel="dns-prefetch" href="` + foreign + `">`, ""},
		{"a stylesheet", `<link rel="stylesheet" href="` + foreign + `/x.css">`, ""},
		{"a preload", `<link rel="preload" href="` + foreign + `/x.woff2">`, ""},
		{"an icon", `<link rel="icon" href="` + foreign + `/x.png">`, ""},
		{"an image", `<img src="` + foreign + `/x.png">`, ""},
		{"a script", `<script src="` + foreign + `/x.js"></script>`, ""},
		{"an iframe", `<iframe src="` + foreign + `/x.html"></iframe>`, ""},
		{"a font in the stylesheet", "", `@font-face { src: url("` + foreign + `/x.woff2"); }`},
		{"an image in the stylesheet", "", `body { background: url(` + foreign + `/x.png); }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if off := offOrigin(tc.page, tc.css); len(off) == 0 {
				t.Errorf("%s to %s was not reported; the audit does not walk it", tc.name, foreign)
			}
		})
	}
	// And the page's own shapes are not reported, or the audit above would be
	// passing by failing everything.
	for _, ok := range []struct{ page, css string }{
		{`<link rel="stylesheet" href="site.css">`, ""},
		{`<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>`, ""},
		{`<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Jost">`, ""},
		{`<a href="https://github.com/owner/repo">source</a>`, ""},
	} {
		if off := offOrigin(ok.page, ok.css); len(off) != 0 {
			t.Errorf("the audit reports %v for a reference the page is allowed", off)
		}
	}
}

// The serve-time half. The policy is held DIRECTIVE BY DIRECTIVE, not as a
// string: a policy that names both font hosts and starts from default-src 'none'
// can still block the page's own stylesheet, or admit the font files under the
// directive that governs stylesheets, and a substring check reads both as fine.
func TestTheContentSecurityPolicyMatchesThePagesReferences(t *testing.T) {
	csp := policy(t)["Content-Security-Policy"]
	if csp == "" {
		t.Fatalf("site-src/headers ships no Content-Security-Policy:\n%s", read(t, headersFile(t)))
	}
	d := directives(csp)

	// Nothing is allowed that is not named, and the three source classes the
	// page actually uses are named where a browser looks for them.
	if got := d["default-src"]; !has(got, "'none'") || len(got) != 1 {
		t.Errorf("default-src is %v; the policy starts from 'none'", got)
	}
	// The page's own stylesheet AND the font stylesheet are both style-src.
	if got := d["style-src"]; !has(got, "'self'") {
		t.Errorf("style-src is %v; without 'self' the page's own site.css is blocked and the page is unstyled", got)
	}
	if got := d["style-src"]; !has(got, "https://fonts.googleapis.com") {
		t.Errorf("style-src is %v; the font stylesheet is fetched as a stylesheet, so it belongs here", got)
	}
	// The face FILES are font-src, and only font-src.
	if got := d["font-src"]; !has(got, "https://fonts.gstatic.com") {
		t.Errorf("font-src is %v; the face files come from fonts.gstatic.com", got)
	}
	if has(d["font-src"], "https://fonts.googleapis.com") || has(d["style-src"], "https://fonts.gstatic.com") {
		t.Errorf("the two font hosts are under swapped directives: style-src %v, font-src %v", d["style-src"], d["font-src"])
	}
	// The page runs, submits and frames nothing.
	for _, directive := range []string{"script-src", "form-action", "frame-ancestors", "base-uri"} {
		if got := d[directive]; !has(got, "'none'") {
			t.Errorf("%s is %v; the page has none of these and says so", directive, got)
		}
	}
	// And no host beyond the two the page fetches from.
	for directive, sources := range d {
		for _, src := range sources {
			if !strings.HasPrefix(src, "https://") {
				continue
			}
			allowed := false
			for _, host := range fontHosts {
				if src == "https://"+host {
					allowed = true
				}
			}
			if !allowed {
				t.Errorf("%s admits %s, which the page never fetches", directive, src)
			}
		}
	}

	// The rule must cover the path the page is served under, derived from the
	// manifest rather than written twice: moving out_subdir moves the page, and
	// a policy left behind covers nothing.
	rule := "/" + outSubdir(t) + "*"
	if !strings.Contains(read(t, headersFile(t)), rule+"\n") {
		t.Errorf("the headers file carries no rule for %q, which is where the page is served:\n%s", rule, read(t, headersFile(t)))
	}
	// And the render must put it where the host reads it: the root of the
	// assets directory, not beside the page.
	var man struct {
		Headers string `json:"headers"`
	}
	readJSON(t, filepath.Join(repoRoot, ".abcd", "site.json"), &man)
	if man.Headers != "site-src/headers" {
		t.Errorf("the manifest names %q as the headers file; the committed one is site-src/headers", man.Headers)
	}
}

// policy reads the headers the page is served with, as name -> value.
func policy(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, line := range strings.Split(read(t, headersFile(t)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "/") {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Errorf("the headers file carries a line that is neither a rule, a comment nor a header: %q", line)
			continue
		}
		out[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	return out
}

// directives splits a content-security policy into directive -> sources.
func directives(csp string) map[string][]string {
	out := map[string][]string{}
	for _, part := range strings.Split(csp, ";") {
		f := strings.Fields(part)
		if len(f) == 0 {
			continue
		}
		out[f[0]] = f[1:]
	}
	return out
}

func has(sources []string, want string) bool {
	for _, s := range sources {
		if s == want {
			return true
		}
	}
	return false
}

func headersFile(t *testing.T) string {
	t.Helper()
	var man struct {
		Headers string `json:"headers"`
	}
	readJSON(t, filepath.Join(repoRoot, ".abcd", "site.json"), &man)
	if man.Headers == "" {
		t.Fatal("the manifest names no headers file")
	}
	return filepath.Join(repoRoot, filepath.FromSlash(man.Headers))
}

func outSubdir(t *testing.T) string {
	t.Helper()
	var man struct {
		OutSubdir string `json:"out_subdir"`
	}
	readJSON(t, filepath.Join(repoRoot, ".abcd", "site.json"), &man)
	if man.OutSubdir == "" {
		t.Fatal("the manifest names no out_subdir")
	}
	return man.OutSubdir
}

func TestTheRenderWritesTheHeadersFileAtTheAssetsRoot(t *testing.T) {
	out := t.TempDir()
	if _, err := renderTree(".", out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "_headers"))
	if err != nil {
		t.Fatalf("the render wrote no _headers at the root of the assets directory: %v", err)
	}
	if !strings.Contains(string(b), "Content-Security-Policy:") {
		t.Errorf("the written _headers carries no policy:\n%s", b)
	}
}

// The region is RENDERED, not written: no version, no size and no asset URL is
// a literal in the template. This replaces the handover marker the page's own
// record left for this one — the region has an owner now.
func TestTheReleaseRegionCarriesNoLiteralFact(t *testing.T) {
	tmpl := read(t, filepath.Join(repoRoot, "site-src", "index.html.tmpl"))
	region := between(t, tmpl, `<div class="release-facts">`, "</div>")
	for _, pattern := range []string{
		`v[0-9]+\.[0-9]+\.[0-9]+`,        // a version
		`[0-9]+(\.[0-9]+)?\s?(kB|MB|GB)`, // a size
		`releases/download/`,             // an asset URL
		`[0-9]{4}-[0-9]{2}-[0-9]{2}`,     // a date
	} {
		if m := regexp.MustCompile(pattern).FindString(region); m != "" {
			t.Errorf("the release region writes %q into the template; every release fact comes from the record", m)
		}
	}
}

// collapse reduces rendered HTML to one line, so a region can be compared with
// what it is supposed to be without restating the template's indentation.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }
