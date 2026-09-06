package sitetest_test

// The release facts: the page is a view over the release record rather than a
// copy of it (itd-2609061353258535). Every check here renders the COMMITTED
// tree — the same manifest, template and stylesheet the deploy chain renders —
// against a fixture release record, so what is tested is the page the release
// run produces and not a second description of it.
//
// No network and no forge: the record arrives as a file, which is the whole
// point of the transport. Which release the record describes is the forge's
// `releases/latest` election, asserted here as the shape of the workflow step
// that reads it (see TestTheReleaseRecordIsTheFlaggedLatestRelease).

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
	newer := strings.ReplaceAll(fixtureRelease, "9.9.9", "9.9.10")
	second := renderWithRelease(t, newer)

	if !strings.Contains(releaseRegion(t, first), "v9.9.9") {
		t.Errorf("the first render does not name v9.9.9:\n%s", releaseRegion(t, first))
	}
	if !strings.Contains(releaseRegion(t, second), "v9.9.10") {
		t.Errorf("the second render does not name v9.9.10:\n%s", releaseRegion(t, second))
	}
	if strings.Contains(releaseRegion(t, second), "v9.9.9\n") || strings.Contains(releaseRegion(t, second), ">v9.9.9<") {
		t.Error("the second render still names v9.9.9; the page keeps a release the record no longer describes")
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
	for _, a := range record.Assets {
		if !strings.Contains(region, a.URL) {
			t.Errorf("the region does not link %s at the record's URL %q", a.Name, a.URL)
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
	if !strings.HasSuffix(button, "/releases/latest/download/Gropius.app.zip") {
		t.Errorf("the download button is %q; the release facts must not rewrite it to a versioned URL", button)
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
// The election is the forge's: `gh release view` with no tag reads the same
// `releases/latest` the download button and install.sh follow, which by
// definition excludes drafts and pre-releases and is not the newest created
// release. There is no selection logic in this repository to unit-test, so what
// is held here is the shape of the step that reads it: it asks for latest, and
// it cannot be steered by the tag the run happens to carry.

func TestTheReleaseRecordIsTheFlaggedLatestRelease(t *testing.T) {
	step := releaseStep(t)

	// `gh release view <tag>` takes the tag as a POSITIONAL argument, so a flag
	// immediately after the verb is what proves none is passed.
	if !regexp.MustCompile(`gh release view\s+--repo\s+"\$GITHUB_REPOSITORY"\s+--json\s+tagName,publishedAt,assets,url`).MatchString(step) {
		t.Errorf("the release-record step does not read the flagged latest release with `gh release view --repo \"$GITHUB_REPOSITORY\" --json tagName,publishedAt,assets,url`:\n%s", step)
	}
	// The tag this run carries must not reach the record: a redeploy of an older
	// tag renders the page for the release the forge flags, not for its own ref.
	for _, steer := range []string{"INPUT_TAG", "inputs.tag", "needs.resolve.outputs.tag"} {
		if strings.Contains(step, steer) {
			t.Errorf("the release-record step reads %s; the page names the flagged latest release, never the tag the run carries", steer)
		}
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
	if !strings.HasSuffix(href, "/releases/latest/download/Gropius.app.zip") {
		t.Errorf("the download button is %q without a release record; it must be unchanged", href)
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

func TestThePageRequestsNothingOffItsOwnOrigin(t *testing.T) {
	for _, p := range []string{page, renderWithRelease(t, fixtureRelease)} {
		// Subresources: src= on any element, and the stylesheet/preload links.
		for _, m := range regexp.MustCompile(`\bsrc="([^"]+)"`).FindAllStringSubmatch(p, -1) {
			checkReference(t, "src", m[1])
		}
		for _, tag := range regexp.MustCompile(`<link\b[^>]*>`).FindAllString(p, -1) {
			a := attrs(tag)
			switch a["rel"] {
			case "stylesheet", "preload", "prefetch", "icon", "apple-touch-icon", "manifest":
				checkReference(t, "link rel="+a["rel"], a["href"])
			}
		}
		if strings.Contains(p, "<script") {
			t.Error("the page carries a script element; it must run nothing")
		}
		for _, cookie := range []string{"document.cookie", `http-equiv="set-cookie"`, `http-equiv="Set-Cookie"`} {
			if strings.Contains(p, cookie) {
				t.Errorf("the page carries %q; it sets no cookie of its own", cookie)
			}
		}
	}
	// The stylesheet may not pull anything in either.
	for _, m := range regexp.MustCompile(`url\(\s*['"]?([^'")]+)`).FindAllStringSubmatch(css, -1) {
		checkReference(t, "css url()", m[1])
	}
}

func checkReference(t *testing.T, where, ref string) {
	t.Helper()
	ref = strings.TrimSpace(ref)
	switch {
	case ref == "", strings.HasPrefix(ref, "#"), strings.HasPrefix(ref, "data:"):
		return
	case !strings.Contains(ref, "//"):
		return // relative: the page's own origin
	}
	for _, host := range fontHosts {
		if strings.HasPrefix(ref, "https://"+host+"/") {
			return
		}
	}
	t.Errorf("%s fetches %q; the page requests nothing but its own origin and the web font service", where, ref)
}

// The serve-time half: the policy shipped beside the page allows exactly the
// hosts the page references and nothing more, so the build-time walk above and
// the header a visitor's browser enforces cannot drift apart.
func TestTheContentSecurityPolicyMatchesThePagesReferences(t *testing.T) {
	headers := read(t, filepath.Join(repoRoot, "site-src", "headers"))
	csp := ""
	for _, line := range strings.Split(headers, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Content-Security-Policy:"); ok {
			csp = strings.TrimSpace(v)
		}
	}
	if csp == "" {
		t.Fatalf("site-src/headers ships no Content-Security-Policy:\n%s", headers)
	}
	if !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("the policy does not start from default-src 'none': %q", csp)
	}
	for _, host := range fontHosts {
		if !strings.Contains(csp, host) {
			t.Errorf("the policy does not admit %s, which the page references: %q", host, csp)
		}
	}
	// Nothing the page does not reference. A host in the policy that the page
	// never fetches is a permission granted for no reason.
	for _, m := range regexp.MustCompile(`https://([a-z0-9.-]+)`).FindAllStringSubmatch(csp, -1) {
		found := false
		for _, host := range fontHosts {
			if m[1] == host {
				found = true
			}
		}
		if !found {
			t.Errorf("the policy admits %s, which the page never fetches", m[1])
		}
	}
	if strings.Contains(csp, "script-src") && !strings.Contains(csp, "script-src 'none'") {
		t.Errorf("the policy admits a script source; the page runs nothing: %q", csp)
	}
	// The rule must cover the path the page is served under.
	if !strings.Contains(headers, "/Gropius/*") {
		t.Errorf("the headers file carries no rule for /Gropius/*, which is where the page is served:\n%s", headers)
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
