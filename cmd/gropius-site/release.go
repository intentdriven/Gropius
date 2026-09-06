package main

// The release record: reading it, refusing it, and electing which release it
// describes. THE SCHEMA AND THE ROUNDING RULE ARE IN THE PACKAGE COMMENT
// (main.go), where `go doc ./cmd/gropius-site` shows them; what is here is why
// the checks are the checks.
//
// Nothing in this file reaches the network. The release run reads the forge
// once, writes the record as a file, and hands over the path — so the render
// stays offline and deterministic given that file, which is what lets it run in
// a job that holds no credential.
//
// WHY REFUSING IS THE RIGHT FAILURE. A record that is wrong in a way the
// renderer accepts becomes a wrong fact on a public page — a version that is not
// one, an asset three orders of magnitude too large, a download link pointing
// off the forge. Every check below therefore STOPS the render. The ABSENCE of a
// record is the graceful case, and it is the caller's to decide: the release run
// asks validate-release whether a record is fit to render and drops it if not,
// so an unacceptable record costs the page its release facts rather than its
// deploy. A page with no release facts is complete and honest; a page with wrong
// ones is not.

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// checksumsAsset is the file a reader verifies a download against. It is
// matched by name rather than composed from a URL pattern, so a release that
// does not carry one produces no record instead of a link that 404s.
const checksumsAsset = "SHA256SUMS.txt"

// maxAssetBytes is where a size stops being a file and starts being a mistake.
// The forge's own ceiling for a release asset is 2 GiB and this project's
// largest asset is an app bundle of a few megabytes, so five times the forge's
// own limit is comfortably absurd: it cannot refuse a real asset, and it does
// refuse a byte count that has been scaled, sign-flipped or read out of the
// wrong field.
const maxAssetBytes = 10 << 30

// A release tag, and nothing else that could be typed where one belongs. The
// same shape .github/workflows/site.yml validates a tag against, so the record
// and the chain that writes it cannot disagree about what a version is.
var versionRe = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$`)

// An asset file name: what a release actually carries, and never a path, a
// space, or anything that would read as markup if the escaping ever slipped.
var assetNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// releaseRecord is the file's shape, exactly.
type releaseRecord struct {
	Version      string        `json:"version"`
	PublishedAt  string        `json:"published_at"`
	HTMLURL      string        `json:"html_url"`
	ChecksumsURL string        `json:"checksums_url"`
	Assets       []recordAsset `json:"assets"`
}

type recordAsset struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	URL       string `json:"url"`
}

// releaseView is the record as the page shows it: the day rather than the
// instant, and each size in the units a reader thinks in.
type releaseView struct {
	Version      string
	Published    string
	HTMLURL      string
	ChecksumsURL string
	Assets       []assetView
}

type assetView struct {
	Name string
	Size string
	URL  string
}

// loadRelease reads one release record and turns it into what the page shows.
func loadRelease(path, repoURL string) (*releaseView, error) {
	var rec releaseRecord
	if err := decodeStrict(path, &rec); err != nil {
		return nil, fmt.Errorf("release record: %w", err)
	}
	published, err := rec.validate(repoURL)
	if err != nil {
		return nil, fmt.Errorf("release record %s: %w", path, err)
	}
	view := &releaseView{
		Version: rec.Version,
		// British English, because the page is: "5 September 2026", not a
		// timestamp and not an ambiguous all-numeric date.
		Published:    published.Format("2 January 2006"),
		HTMLURL:      rec.HTMLURL,
		ChecksumsURL: rec.ChecksumsURL,
	}
	for _, a := range rec.Assets {
		view.Assets = append(view.Assets, assetView{Name: a.Name, Size: humanSize(a.SizeBytes), URL: a.URL})
	}
	return view, nil
}

// validate refuses everything the page must not show, and returns the instant
// the release was published. repoURL is this repository's own address on the
// forge; every URL in the record is required to sit under its release path.
func (r releaseRecord) validate(repoURL string) (time.Time, error) {
	var zero time.Time
	if !versionRe.MatchString(r.Version) {
		return zero, fmt.Errorf("version %q is not a release tag (vX.Y.Z, with the leading v)", r.Version)
	}
	published, err := time.Parse(time.RFC3339, r.PublishedAt)
	if err != nil {
		return zero, fmt.Errorf("published_at %q is not an RFC 3339 instant", r.PublishedAt)
	}
	if repoURL == "" {
		return zero, fmt.Errorf("no repository URL to pin the record's links to")
	}
	// The release's own page, and the prefix every asset download sits under.
	// Both carry the version, so a record whose version and links name different
	// releases is refused rather than rendered.
	releasePage := repoURL + "/releases/tag/" + r.Version
	downloads := repoURL + "/releases/download/" + r.Version + "/"
	if r.HTMLURL != releasePage {
		return zero, fmt.Errorf("html_url %q is not this release's page (%q)", r.HTMLURL, releasePage)
	}
	if !strings.HasPrefix(r.ChecksumsURL, downloads) {
		return zero, fmt.Errorf("checksums_url %q is not a download of this release (%q…)", r.ChecksumsURL, downloads)
	}
	if len(r.Assets) == 0 {
		return zero, fmt.Errorf("assets is empty; a release the page names carries files")
	}

	seen := map[string]bool{}
	checksums := ""
	for i, a := range r.Assets {
		if !assetNameRe.MatchString(a.Name) {
			return zero, fmt.Errorf("assets[%d]: name %q is not a release asset's file name", i, a.Name)
		}
		if seen[a.Name] {
			return zero, fmt.Errorf("assets: %s appears twice; each asset is listed once", a.Name)
		}
		seen[a.Name] = true
		if a.SizeBytes <= 0 || a.SizeBytes > maxAssetBytes {
			return zero, fmt.Errorf("assets[%d] (%s): size_bytes %d is outside 1..%d", i, a.Name, a.SizeBytes, int64(maxAssetBytes))
		}
		if !strings.HasPrefix(a.URL, downloads) {
			return zero, fmt.Errorf("assets[%d] (%s): url %q is not a download of this release (%q…)", i, a.Name, a.URL, downloads)
		}
		if a.Name == checksumsAsset {
			checksums = a.URL
		}
	}
	if checksums == "" {
		return zero, fmt.Errorf("assets carries no %s; the page links the checksums a reader verifies against", checksumsAsset)
	}
	// The link the page shows must be THIS release's checksums file, not a URL
	// composed elsewhere that happens to end in the right name.
	if r.ChecksumsURL != checksums {
		return zero, fmt.Errorf("checksums_url %q is not this release's %s (%q)", r.ChecksumsURL, checksumsAsset, checksums)
	}
	return published, nil
}

// humanSize is a byte count in the units a download page is read in.
//
// THE ROUNDING RULE, because a page that overstates a download is a page that
// lies: decimal units (1 kB is 1000 bytes, which is what the platform's own
// file listings show), the largest unit in which the count is at least one, and
// the fraction TRUNCATED to one decimal place rather than rounded. 20,971,520
// bytes is 20.97… MB and shows as 20.9 MB; it never shows as 21.0 MB. Under a
// kilobyte the exact count is shown, since there is nothing to round.
//
// Zero and negative counts are not release assets and validate refuses them
// before this is reached; they are formatted rather than refused here so this
// stays a pure formatter with no second opinion about what a size may be.
func humanSize(n int64) string {
	switch {
	case n == 1:
		return "1 byte"
	case n < 1000:
		return fmt.Sprintf("%d bytes", n)
	}
	unit := int64(1000)
	name := "kB"
	for _, step := range []struct {
		div  int64
		name string
	}{{1_000_000, "MB"}, {1_000_000_000, "GB"}} {
		if n >= step.div {
			unit, name = step.div, step.name
		}
	}
	whole := n / unit
	tenths := (n % unit) * 10 / unit
	return fmt.Sprintf("%d.%d %s", whole, tenths, name)
}

// --- Which release ---------------------------------------------------------

// forgeRelease is one entry of the forge's release list, in the shape
// `gh release list --json tagName,isLatest,isDraft,isPrerelease,publishedAt`
// produces it.
type forgeRelease struct {
	TagName      string `json:"tagName"`
	IsLatest     bool   `json:"isLatest"`
	IsDraft      bool   `json:"isDraft"`
	IsPrerelease bool   `json:"isPrerelease"`
	PublishedAt  string `json:"publishedAt"`
}

// electLatest picks the release the page names: the one the forge FLAGS as
// latest, never the most recently created and never a draft or a pre-release.
//
// The election lives here rather than in the workflow so it can be tested
// against a fixture — the case the page's record is about (a release created
// after the flagged one) cannot be produced against the real forge without
// hand-making a stale release, and a check that only reads the workflow's text
// cannot fail for the reason the criterion names.
//
// The forge's own flag is followed rather than recomputed: it is the same
// election the download button's `releases/latest` redirect and install.sh
// follow, so the page and the button cannot disagree. What is added here is
// refusal — of a list where nothing is flagged, of one where two things are, and
// of a flagged entry that is a draft, a pre-release, or not a release tag at
// all.
func electLatest(list []forgeRelease) (forgeRelease, error) {
	var flagged []forgeRelease
	for _, r := range list {
		if r.IsLatest {
			flagged = append(flagged, r)
		}
	}
	switch {
	case len(flagged) == 0:
		return forgeRelease{}, fmt.Errorf("no release in the list of %d is flagged latest", len(list))
	case len(flagged) > 1:
		return forgeRelease{}, fmt.Errorf("%d releases are flagged latest; exactly one is", len(flagged))
	}
	r := flagged[0]
	switch {
	case r.IsDraft:
		return forgeRelease{}, fmt.Errorf("the release flagged latest (%s) is a draft", r.TagName)
	case r.IsPrerelease:
		return forgeRelease{}, fmt.Errorf("the release flagged latest (%s) is a pre-release", r.TagName)
	case !versionRe.MatchString(r.TagName):
		return forgeRelease{}, fmt.Errorf("the release flagged latest is tagged %q, which is not a release tag", r.TagName)
	}
	return r, nil
}

// electLatestFrom reads a release list and returns the elected tag.
func electLatestFrom(path string) (string, error) {
	var list []forgeRelease
	if err := decodeStrict(path, &list); err != nil {
		return "", fmt.Errorf("release list: %w", err)
	}
	r, err := electLatest(list)
	if err != nil {
		return "", fmt.Errorf("release list %s: %w", path, err)
	}
	return r.TagName, nil
}
