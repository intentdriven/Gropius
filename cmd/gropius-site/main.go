// Command gropius-site renders the public landing page at intentdriven.sh/Gropius.
//
// The page is a COMPOSITION, not a document: every sentence it shows is either
// the canonical identity block (.abcd/development/IDENTITY.md), a span of a
// repository file selected by path and heading in .abcd/site.json, or one of
// the interface words in the closed list at site-src/ui.json. Editing the
// tagline in the identity block therefore changes the page, which is the
// property the landing-page record is built on; a lint could only report the
// drift after someone had already written it twice.
//
// It reads only the files the manifest names, writes only inside --out, and
// reaches no network — so it can run in a job that holds no credential.
//
// Usage:
//
//	go run ./cmd/gropius-site --out site
//
// Why this lives here rather than in abcd's `site` verb: that verb renders its
// own fixed site shape (a home page plus chapters from docs/) and has no
// custom-template capability, and this page is a single template. The manifest
// deliberately keeps abcd's identity/ui_strings shape so the verb can take the
// page over when it grows one.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := flag.String("root", ".", "repository root; every path in the manifest is resolved against it")
	manifestPath := flag.String("manifest", "", "composition manifest (default <root>/.abcd/site.json)")
	out := flag.String("out", "site", "directory to render into; nothing outside it is written")
	flag.Parse()

	if *manifestPath == "" {
		*manifestPath = filepath.Join(*root, ".abcd", "site.json")
	}
	if err := render(*root, *manifestPath, *out); err != nil {
		fmt.Fprintln(os.Stderr, "gropius-site:", err)
		os.Exit(1)
	}
}

// manifest is the composition: where each block of the page comes from. Unknown
// keys are refused (see decodeStrict), so a manifest written against a newer
// shape fails loudly instead of rendering a page with a silently missing block.
type manifest struct {
	SchemaVersion int    `json:"schema_version"`
	Purpose       string `json:"purpose"`
	Identity      struct {
		File    string `json:"file"`
		Heading string `json:"heading"`
	} `json:"identity"`
	UIStrings string   `json:"ui_strings"`
	Template  string   `json:"template"`
	Static    []string `json:"static"`
	OutSubdir string   `json:"out_subdir"`
	Forge     struct {
		Base          string `json:"base"`
		Repository    string `json:"repository"`
		Branch        string `json:"branch"`
		DownloadAsset string `json:"download_asset"`
	} `json:"forge"`
	Sources struct {
		Pillars      source   `json:"pillars"`
		Install      source   `json:"install"`
		InstallNote  source   `json:"install_note"`
		Requirements []source `json:"requirements"`
	} `json:"sources"`
}

// uiStrings is the closed allowlist of words the renderer may add. A key the
// struct does not carry is refused, so the file cannot grow prose by accident.
type uiStrings struct {
	Purpose           string   `json:"_purpose"`
	Lang              string   `json:"lang"`
	NavLabel          string   `json:"nav_label"`
	NavDownload       string   `json:"nav_download"`
	NavInstall        string   `json:"nav_install"`
	NavSource         string   `json:"nav_source"`
	NavGettingStarted string   `json:"nav_getting_started"`
	Eyebrow           string   `json:"eyebrow"`
	Headline          []string `json:"headline"`
	HeadlineAccent    string   `json:"headline_accent"`
	MarkLabel         string   `json:"mark_label"`
	DownloadLabel     string   `json:"download_label"`
	DownloadNote      string   `json:"download_note"`
	RepositoryLabel   string   `json:"repository_label"`
	CtaNote           string   `json:"cta_note"`
	FactRequires      string   `json:"fact_requires"`
	FactEndpoint      string   `json:"fact_endpoint"`
	FactRuntime       string   `json:"fact_runtime"`
	FactLicence       string   `json:"fact_licence"`
	Endpoint          string   `json:"endpoint"`
	Runtime           string   `json:"runtime"`
	Licence           string   `json:"licence"`
	InstallHeading    string   `json:"install_heading"`
	InstallComments   []string `json:"install_comments"`
	AssetsHeading     string   `json:"assets_heading"`
	Assets            []struct {
		File string `json:"file"`
		Note string `json:"note"`
	} `json:"assets"`
	PillarsLabel   string `json:"pillars_label"`
	FooterNote     string `json:"footer_note"`
	FooterReleases string `json:"footer_releases"`
	FooterSource   string `json:"footer_source"`
}

// identity is the canonical block: the three lines every public surface renders.
type identity struct {
	Title   string
	Tagline string
	Pitch   string
}

type links struct {
	Home           string
	Repository     string
	RepositoryName string
	Download       string
	Releases       string
	GettingStarted string
}

type pillar struct {
	Title template.HTML
	Body  template.HTML
}

type installStep struct {
	Comment string
	Command string
}

type pageData struct {
	Identity     identity
	UI           uiStrings
	Links        links
	Pillars      []pillar
	Install      []installStep
	InstallNote  []template.HTML
	Requirements []template.HTML
}

func render(root, manifestPath, out string) error {
	var m manifest
	if err := decodeStrict(manifestPath, &m); err != nil {
		return err
	}
	if m.SchemaVersion != 1 {
		return fmt.Errorf("%s: schema_version %d is not supported (this renderer speaks 1)", manifestPath, m.SchemaVersion)
	}
	resolve := func(p string) string { return filepath.Join(root, filepath.FromSlash(p)) }

	var ui uiStrings
	if err := decodeStrict(resolve(m.UIStrings), &ui); err != nil {
		return err
	}
	if len(ui.Headline) == 0 {
		return fmt.Errorf("%s: headline is empty", m.UIStrings)
	}

	id, err := readIdentity(resolve(m.Identity.File), m.Identity.Heading)
	if err != nil {
		return err
	}

	data := pageData{Identity: id, UI: ui, Links: forgeLinks(m)}

	// The three pillars are the first bullets of the README's feature list, and
	// the template draws one of the mark's three shapes beside each. A fourth
	// would arrive without a shape, so the count is a hard requirement rather
	// than a truncation.
	pillarSpans, err := selectSpans(root, m.Sources.Pillars)
	if err != nil {
		return fmt.Errorf("pillars: %w", err)
	}
	if len(pillarSpans) != 3 {
		return fmt.Errorf("pillars: the manifest selects %d spans; the page draws exactly 3", len(pillarSpans))
	}
	for _, s := range pillarSpans {
		if s.Title == "" {
			return fmt.Errorf("pillars: %q has no bold lead to use as its heading", s.Body)
		}
		data.Pillars = append(data.Pillars, pillar{Title: inlineHTML(s.Title), Body: inlineHTML(sentenceCase(s.Body))})
	}

	// The install commands are the README's own, character for character: a
	// command a visitor pastes must be the command the project documents.
	installSpans, err := selectSpans(root, m.Sources.Install)
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}
	if len(installSpans) != len(ui.InstallComments) {
		return fmt.Errorf("install: the manifest selects %d commands but ui.json labels %d", len(installSpans), len(ui.InstallComments))
	}
	for i, s := range installSpans {
		data.Install = append(data.Install, installStep{Comment: ui.InstallComments[i], Command: s.Body})
	}

	noteSpans, err := selectSpans(root, m.Sources.InstallNote)
	if err != nil {
		return fmt.Errorf("install_note: %w", err)
	}
	for _, s := range noteSpans {
		data.InstallNote = append(data.InstallNote, inlineHTML(s.Body))
	}

	for i, src := range m.Sources.Requirements {
		spans, err := selectSpans(root, src)
		if err != nil {
			return fmt.Errorf("requirements[%d]: %w", i, err)
		}
		for _, s := range spans {
			data.Requirements = append(data.Requirements, inlineHTML(s.Body))
		}
	}
	if len(data.Requirements) == 0 {
		return fmt.Errorf("requirements: the manifest selected nothing; the page must state the platform it needs")
	}

	tmplPath := resolve(m.Template)
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		return err
	}

	dir := filepath.Join(out, filepath.Base(m.OutSubdir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var page strings.Builder
	if err := tmpl.Execute(&page, data); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(page.String()), 0o644); err != nil {
		return err
	}
	// Static inputs land beside the page under their own base name, so every
	// reference from the page stays relative — the page is served under a path,
	// not a domain root, and an absolute "/site.css" would 404 there.
	for _, s := range m.Static {
		b, err := os.ReadFile(resolve(s))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(s)), b, 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("gropius-site: rendered %s\n", filepath.Join(dir, "index.html"))
	return nil
}

// forgeLinks derives every outbound link from the repository named in the
// manifest. The download link is the forge's latest-release redirect: it names
// no version, so the page cannot go stale between releases and there is nothing
// on it for a release job to rewrite.
func forgeLinks(m manifest) links {
	repo := strings.TrimSuffix(m.Forge.Base, "/") + "/" + m.Forge.Repository
	return links{
		// Relative: the page is served from a path under a shared domain.
		Home:           "./",
		Repository:     repo,
		RepositoryName: m.Forge.Repository,
		Download:       repo + "/releases/latest/download/" + m.Forge.DownloadAsset,
		Releases:       repo + "/releases",
		GettingStarted: repo + "/blob/" + m.Forge.Branch + "/docs/getting-started.md",
	}
}

// decodeStrict reads a JSON file into v and refuses any key v does not carry.
// Both files it is used on are allowlists — the manifest of what the page is
// composed from, and the words the renderer may add — and an allowlist that
// silently ignores what it does not recognise is not one.
func decodeStrict(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
