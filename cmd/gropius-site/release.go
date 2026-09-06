package main

// THE RELEASE RECORD.
//
// The page's release facts are a view over the release the forge flags as
// latest, and this file is the whole of the contract between the release run
// and the page. The run reads the release once, writes it as a JSON file beside
// the build, and hands the path to the render with --release. Nothing here
// reaches the network, so the render stays offline and deterministic given that
// file — which is what lets it run in a job that holds no credential.
//
// THE SCHEMA. One JSON object:
//
//	{
//	  "version":       "v0.1.2",                     the release's tag
//	  "published_at":  "2026-09-05T09:41:07Z",       RFC 3339, the instant it was published
//	  "html_url":      "https://…/releases/tag/…",   the release's own page
//	  "checksums_url": "https://…/SHA256SUMS.txt",   the checksums asset OF THIS RELEASE
//	  "assets": [
//	    {"name": "Gropius.app.zip", "size_bytes": 20971520, "url": "https://…"}
//	  ]
//	}
//
// Every field is required and no other field is admitted. It is deliberately
// NOT the forge's own release JSON: a schema that accepts whatever the forge
// sends is a schema that renders whatever the forge sends, and this one is
// small enough to state completely and check completely.
//
// WHY REFUSING IS THE RIGHT FAILURE. A record that is wrong in a way the
// renderer accepts becomes a wrong fact on a public page — a version that is
// not one, an asset three orders of magnitude too large, a checksums link
// pointing at some other release. Every check below therefore STOPS the render.
// The absence of a record is the graceful case, handled by the caller not
// passing --release at all: a page with no release facts is complete and
// honest, while a page with wrong ones is not.

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
// largest asset is an app bundle measured in tens of megabytes, so five times
// the forge's own limit is comfortably absurd: it cannot refuse a real asset,
// and it does refuse a byte count that has been scaled, sign-flipped or read
// out of the wrong field.
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
func loadRelease(path string) (*releaseView, error) {
	var rec releaseRecord
	if err := decodeStrict(path, &rec); err != nil {
		return nil, fmt.Errorf("release record: %w", err)
	}
	published, err := rec.validate()
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
// the release was published.
func (r releaseRecord) validate() (time.Time, error) {
	var zero time.Time
	if !versionRe.MatchString(r.Version) {
		return zero, fmt.Errorf("version %q is not a release tag (vX.Y.Z, with the leading v)", r.Version)
	}
	published, err := time.Parse(time.RFC3339, r.PublishedAt)
	if err != nil {
		return zero, fmt.Errorf("published_at %q is not an RFC 3339 instant", r.PublishedAt)
	}
	if err := httpsURL("html_url", r.HTMLURL); err != nil {
		return zero, err
	}
	if err := httpsURL("checksums_url", r.ChecksumsURL); err != nil {
		return zero, err
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
		if err := httpsURL(fmt.Sprintf("assets[%d] (%s): url", i, a.Name), a.URL); err != nil {
			return zero, err
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

// httpsURL admits one shape and names the field when it refuses. Only https:
// because every URL in the record points at the forge, and anything else in a
// href on a public page is a scheme nobody chose.
func httpsURL(field, u string) error {
	if !strings.HasPrefix(u, "https://") || strings.ContainsAny(u, " \t\r\n\"'<>") {
		return fmt.Errorf("%s %q is not an https URL", field, u)
	}
	return nil
}

// humanSize is a byte count in the units a download page is read in.
//
// THE ROUNDING RULE, because a page that overstates a download is a page that
// lies: decimal units (1 kB is 1000 bytes, which is what the platform's own
// file listings show), the largest unit in which the count is at least one, and
// the fraction TRUNCATED to one decimal place rather than rounded. 20,971,520
// bytes is 20.97… MB and shows as 20.9 MB; it never shows as 21.0 MB. Under a
// kilobyte the exact count is shown, since there is nothing to round.
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
