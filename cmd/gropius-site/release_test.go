package main

// The release record's refusals, and the two derivations the page shows that
// the record does not carry: the day, and the size in human units.
//
// A record that is wrong in a way the renderer accepts becomes a wrong fact on
// a public page — a version that is not one, an asset three orders of magnitude
// too large, a checksums link pointing at some other release. Each of those is a
// refusal here, and the render STOPS: a page missing its release facts is a
// visible outage the workflow reports, while a page carrying wrong ones is not.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The repository goodRecord's links belong to. loadRelease pins every URL in a
// record to this, so a fixture that is not pinned to it is refused.
const fixtureRepo = "https://example.invalid"

const goodRecord = `{
  "version": "v1.2.3",
  "published_at": "2026-09-05T09:41:07Z",
  "html_url": "https://example.invalid/releases/tag/v1.2.3",
  "checksums_url": "https://example.invalid/releases/download/v1.2.3/SHA256SUMS.txt",
  "assets": [
    {"name": "Gropius.app.zip", "size_bytes": 20971520, "url": "https://example.invalid/releases/download/v1.2.3/Gropius.app.zip"},
    {"name": "SHA256SUMS.txt", "size_bytes": 210, "url": "https://example.invalid/releases/download/v1.2.3/SHA256SUMS.txt"}
  ]
}`

// writeRecord puts one record in a file and returns its path.
func writeRecord(t *testing.T, record string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.json")
	if err := os.WriteFile(path, []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// patchRecord edits the good record as JSON, so a test says only what it changes.
func patchRecord(t *testing.T, edit func(m map[string]any)) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(goodRecord), &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTheGoodRecordLoads(t *testing.T) {
	r, err := loadRelease(writeRecord(t, goodRecord), fixtureRepo)
	if err != nil {
		t.Fatalf("the good record must load: %v", err)
	}
	if r.Version != "v1.2.3" {
		t.Errorf("version is %q", r.Version)
	}
	if r.Published != "5 September 2026" {
		t.Errorf("published is %q; the page shows the day, in the page's own language", r.Published)
	}
	if len(r.Assets) != 2 {
		t.Fatalf("the record carries %d assets, not 2", len(r.Assets))
	}
	if r.Assets[0].Size != "20.9 MB" {
		t.Errorf("size is %q", r.Assets[0].Size)
	}
	if r.ChecksumsURL != "https://example.invalid/releases/download/v1.2.3/SHA256SUMS.txt" {
		t.Errorf("checksums URL is %q", r.ChecksumsURL)
	}
}

// MARKUP IN A RECORD VALUE is refused rather than escaped. Escaping would also
// be safe — html/template escapes contextually and the region is ordinary
// markup — but a release asset genuinely called `<img src=x onerror=…>` is not
// an asset whose name should reach a public page at all, and a grammar that
// admits only what a release actually carries is a smaller thing to be sure of
// than a chain of escapes. The two cases below ("a version carrying markup",
// "an asset name carrying markup") are that bar.
func TestTheRecordIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record string
		says   string
	}{
		{"not JSON", "{", "release"},
		{"an unknown field", patchRecord(t, func(m map[string]any) { m["body"] = "generated notes" }), "body"},
		{"a missing version", patchRecord(t, func(m map[string]any) { delete(m, "version") }), "version"},
		{"a missing date", patchRecord(t, func(m map[string]any) { delete(m, "published_at") }), "published_at"},
		{"a missing release page", patchRecord(t, func(m map[string]any) { delete(m, "html_url") }), "html_url"},
		{"no assets", patchRecord(t, func(m map[string]any) { m["assets"] = []any{} }), "assets"},
		{"a version without the v", patchRecord(t, func(m map[string]any) { m["version"] = "1.2.3" }), "version"},
		{"a version that is a branch name", patchRecord(t, func(m map[string]any) { m["version"] = "vmain" }), "version"},
		{"a two-part version", patchRecord(t, func(m map[string]any) { m["version"] = "v1.2" }), "version"},
		{"a version carrying markup", patchRecord(t, func(m map[string]any) { m["version"] = "v1.2.3<script>" }), "version"},
		{"a date that is not one", patchRecord(t, func(m map[string]any) { m["published_at"] = "yesterday" }), "published_at"},
		{"a release page that is not https", patchRecord(t, func(m map[string]any) { m["html_url"] = "javascript:alert(1)" }), "html_url"},
		{"a release page on another host", patchRecord(t, func(m map[string]any) {
			m["html_url"] = "https://evil.invalid/releases/tag/v1.2.3"
		}), "html_url"},
		{"a release page for another version", patchRecord(t, func(m map[string]any) {
			m["html_url"] = fixtureRepo + "/releases/tag/v9.9.9"
		}), "html_url"},
		{"an absurd size", patchAsset(t, 0, func(a map[string]any) { a["size_bytes"] = 1 << 44 }), "size_bytes"},
		{"a size of nothing", patchAsset(t, 0, func(a map[string]any) { a["size_bytes"] = 0 }), "size_bytes"},
		{"a negative size", patchAsset(t, 0, func(a map[string]any) { a["size_bytes"] = -1 }), "size_bytes"},
		{"an asset name that is a path", patchAsset(t, 0, func(a map[string]any) { a["name"] = "../etc/passwd" }), "name"},
		{"an asset name carrying markup", patchAsset(t, 0, func(a map[string]any) { a["name"] = `<img src=x onerror=alert(1)>` }), "name"},
		{"an asset URL that is not https", patchAsset(t, 0, func(a map[string]any) { a["url"] = "javascript:alert(1)" }), "url"},
		{"an asset served from another host", patchAsset(t, 0, func(a map[string]any) {
			a["url"] = "https://cdn.evil.invalid/Gropius.app.zip"
		}), "url"},
		{"an asset from another repository", patchAsset(t, 0, func(a map[string]any) {
			a["url"] = "https://example.invalid/someone-else/releases/download/v1.2.3/Gropius.app.zip"
		}), "url"},
		{"an asset from another release", patchAsset(t, 0, func(a map[string]any) {
			a["url"] = fixtureRepo + "/releases/download/v0.0.1/Gropius.app.zip"
		}), "url"},
		{"a checksums link on another host", patchRecord(t, func(m map[string]any) {
			m["checksums_url"] = "https://evil.invalid/SHA256SUMS.txt"
		}), "checksums_url"},
		{"a second JSON document appended", goodRecord + "\n" + goodRecord, "one JSON document"},
		{"two assets with one name", patchRecord(t, func(m map[string]any) {
			as := m["assets"].([]any)
			m["assets"] = append(as, as[0])
		}), "twice"},
		{"no checksums file among the assets", patchRecord(t, func(m map[string]any) {
			as := m["assets"].([]any)
			m["assets"] = as[:1]
		}), "SHA256SUMS.txt"},
		{"a checksums link to another release", patchRecord(t, func(m map[string]any) {
			m["checksums_url"] = "https://example.invalid/releases/download/v0.0.1/SHA256SUMS.txt"
		}), "checksums_url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadRelease(writeRecord(t, tc.record), fixtureRepo)
			if err == nil {
				t.Fatalf("%s was accepted; it must be refused", tc.name)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("the refusal does not name %q: %v", tc.says, err)
			}
		})
	}
}

// patchAsset edits one asset of the good record.
func patchAsset(t *testing.T, i int, edit func(a map[string]any)) string {
	t.Helper()
	return patchRecord(t, func(m map[string]any) {
		edit(m["assets"].([]any)[i].(map[string]any))
	})
}

// A --release naming a file that is not there is a bug in the caller, not an
// outage: the workflow decides whether a record exists and passes the flag only
// then, so a missing file here means the two disagree.
func TestAMissingRecordFileIsRefused(t *testing.T) {
	if _, err := loadRelease(filepath.Join(t.TempDir(), "absent.json"), fixtureRepo); err == nil {
		t.Fatal("a --release pointing at nothing was accepted")
	}
}

// The rounding rule, stated as a table: decimal units, truncated, so the page
// never claims a file is larger than it is.
func TestHumanSizeNeverOverReports(t *testing.T) {
	for _, tc := range []struct {
		bytes int64
		want  string
	}{
		// validate refuses these before humanSize sees them; the rows pin what
		// the formatter does with them anyway, so the two cannot drift.
		{0, "0 bytes"},
		{-1, "-1 bytes"},
		{1, "1 byte"},
		{210, "210 bytes"},
		{999, "999 bytes"},
		{1000, "1.0 kB"},
		{1999, "1.9 kB"},
		{999999, "999.9 kB"},
		{1000000, "1.0 MB"},
		{20971520, "20.9 MB"},
		{4718592, "4.7 MB"},
		{999999999, "999.9 MB"},
		{1000000000, "1.0 GB"},
		{4500000000, "4.5 GB"},
	} {
		if got := humanSize(tc.bytes); got != tc.want {
			t.Errorf("humanSize(%d) = %q; want %q", tc.bytes, got, tc.want)
		}
	}
}

// --- Which release the page names ------------------------------------------
//
// The criterion: "given a release created more recently than the one flagged
// latest, when the page is produced, then the page names the release flagged
// latest." That case cannot be produced against the real forge without
// hand-making a stale release, which is precisely why the election is a
// function here and the fixture below is the test of it.

func TestTheFlaggedLatestIsElectedAndNotTheNewest(t *testing.T) {
	// The newest-published entry is NOT the flagged one, twice over: a
	// pre-release cut after it, and a draft after that.
	list := []forgeRelease{
		{TagName: "v2.0.0-rc.2", IsPrerelease: true, PublishedAt: "2026-09-08T00:00:00Z"},
		{TagName: "v2.0.0-draft", IsDraft: true, PublishedAt: "2026-09-09T00:00:00Z"},
		{TagName: "v1.4.0", IsLatest: true, PublishedAt: "2026-09-01T00:00:00Z"},
		{TagName: "v1.3.0", PublishedAt: "2026-08-01T00:00:00Z"},
	}
	got, err := electLatest(list)
	if err != nil {
		t.Fatalf("the flagged release must be elected: %v", err)
	}
	if got.TagName != "v1.4.0" {
		t.Errorf("elected %q; the flagged latest is v1.4.0, and the newer entries are a pre-release and a draft", got.TagName)
	}
}

func TestTheElectionIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		list []forgeRelease
		says string
	}{
		{"an empty list", nil, "flagged latest"},
		{"nothing flagged", []forgeRelease{
			{TagName: "v1.0.0", PublishedAt: "2026-09-01T00:00:00Z"},
		}, "flagged latest"},
		{"two flagged", []forgeRelease{
			{TagName: "v1.0.0", IsLatest: true},
			{TagName: "v1.1.0", IsLatest: true},
		}, "exactly one"},
		{"the flagged one is a draft", []forgeRelease{
			{TagName: "v1.0.0", IsLatest: true, IsDraft: true},
		}, "draft"},
		{"the flagged one is a pre-release", []forgeRelease{
			{TagName: "v1.0.0-rc.1", IsLatest: true, IsPrerelease: true},
		}, "pre-release"},
		{"the flagged one is not a release tag", []forgeRelease{
			{TagName: "nightly", IsLatest: true},
		}, "not a release tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := electLatest(tc.list)
			if err == nil {
				t.Fatalf("%s elected %q; it must be refused", tc.name, got.TagName)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("the refusal does not say why (%q): %v", tc.says, err)
			}
		})
	}
}

// The forge's own list, as `gh release list --json …` writes it, reaches the
// election unchanged: the field names are the forge's and the decode is strict,
// so a renamed field fails here rather than silently electing nothing.
func TestTheForgesReleaseListElects(t *testing.T) {
	list := `[
	  {"isDraft":false,"isLatest":false,"isPrerelease":true,"publishedAt":"2026-09-09T00:00:00Z","tagName":"v0.2.0-rc.1"},
	  {"isDraft":false,"isLatest":true,"isPrerelease":false,"publishedAt":"2026-09-06T15:54:37Z","tagName":"v0.1.2"},
	  {"isDraft":false,"isLatest":false,"isPrerelease":false,"publishedAt":"2026-09-06T14:50:11Z","tagName":"v0.1.1"}
	]`
	path := filepath.Join(t.TempDir(), "releases.json")
	if err := os.WriteFile(path, []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	tag, err := electLatestFrom(path)
	if err != nil {
		t.Fatalf("the forge's own list must elect: %v", err)
	}
	if tag != "v0.1.2" {
		t.Errorf("elected %q; the flagged latest is v0.1.2 and the newest published is a pre-release", tag)
	}
}

// The verbs the release run calls, through the same entry point the run uses.
func TestTheVerbsAnswer(t *testing.T) {
	root := tree(t)
	list := filepath.Join(t.TempDir(), "releases.json")
	if err := os.WriteFile(list, []byte(`[{"isDraft":false,"isLatest":true,"isPrerelease":false,"publishedAt":"2026-09-06T15:54:37Z","tagName":"v0.1.2"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"select", "--from", list}); err != nil {
		t.Errorf("select: %v", err)
	}
	// A record pinned to the fixture manifest's own repository.
	var forge struct {
		Forge struct {
			Base       string `json:"base"`
			Repository string `json:"repository"`
		} `json:"forge"`
	}
	if err := json.Unmarshal([]byte(read(t, root, filepath.Join(".abcd", "site.json"))), &forge); err != nil {
		t.Fatal(err)
	}
	repo := strings.TrimSuffix(forge.Forge.Base, "/") + "/" + forge.Forge.Repository
	record := writeRecord(t, strings.ReplaceAll(goodRecord, fixtureRepo, repo))
	if err := run([]string{"validate-release", "--root", root, record}); err != nil {
		t.Errorf("validate-release on a good record: %v", err)
	}
	bad := writeRecord(t, strings.ReplaceAll(goodRecord, fixtureRepo, "https://evil.invalid"))
	if err := run([]string{"validate-release", "--root", root, bad}); err == nil {
		t.Error("validate-release accepted a record whose links point at another host")
	}
	if err := run([]string{"conjure"}); err == nil {
		t.Error("an unknown verb was accepted")
	}
}
