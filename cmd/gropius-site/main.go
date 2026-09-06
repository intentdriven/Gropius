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
	"reflect"
	"strings"
)

func main() {
	root := flag.String("root", ".", "repository root; every path in the manifest is resolved against it")
	manifestPath := flag.String("manifest", "", "composition manifest (default <root>/.abcd/site.json)")
	out := flag.String("out", "site", "directory to render into; nothing outside it is written")
	release := flag.String("release", "", "release record to render the release facts from; without it the page carries none")
	flag.Parse()

	if *manifestPath == "" {
		*manifestPath = filepath.Join(*root, ".abcd", "site.json")
	}
	if err := render(*root, *manifestPath, *out, *release); err != nil {
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
		CtaNote      source   `json:"cta_note"`
		Endpoint     source   `json:"endpoint"`
		Runtime      source   `json:"runtime"`
		License      source   `json:"license"`
		Status       source   `json:"status"`
		FooterNote   source   `json:"footer_note"`
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
	FactRequires      string   `json:"fact_requires"`
	FactEndpoint      string   `json:"fact_endpoint"`
	FactRuntime       string   `json:"fact_runtime"`
	FactLicense       string   `json:"fact_license"`
	FactStatus        string   `json:"fact_status"`
	InstallHeading    string   `json:"install_heading"`
	InstallComments   []string `json:"install_comments"`
	ReleaseHeading    string   `json:"release_heading"`
	ReleasePublished  string   `json:"release_published"`
	ChecksumsLabel    string   `json:"checksums_label"`
	AssetsHeading     string   `json:"assets_heading"`
	Assets            []struct {
		File string `json:"file"`
		Note string `json:"note"`
	} `json:"assets"`
	PillarsLabel    string `json:"pillars_label"`
	FooterSeparator string `json:"footer_separator"`
	FooterReleases  string `json:"footer_releases"`
	FooterSource    string `json:"footer_source"`
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
	// Selected prose. Everything the page asserts about the product is a span
	// of a repository file, so a sentence edited in the README changes the page
	// and cannot be left behind in a string table.
	CtaNote    template.HTML
	Endpoint   template.HTML
	Runtime    template.HTML
	License    template.HTML
	Status     template.HTML
	FooterNote template.HTML
	// Release is the release the forge flags as latest, or nil when the render
	// was handed no record. Nil is not an error: the page is static files, and
	// it must serve with its download button whether or not the forge could be
	// read when it was produced.
	Release *releaseView
}

func render(root, manifestPath, out, releasePath string) error {
	var m manifest
	if err := decodeStrict(manifestPath, &m); err != nil {
		return err
	}
	if m.SchemaVersion != 1 {
		return fmt.Errorf("%s: schema_version %d is not supported (this renderer speaks 1)", manifestPath, m.SchemaVersion)
	}
	src := repo{root: root}

	uiPath, err := src.path(m.UIStrings)
	if err != nil {
		return fmt.Errorf("ui_strings: %w", err)
	}
	var ui uiStrings
	if err := decodeStrict(uiPath, &ui); err != nil {
		return err
	}
	// An allowlist that refuses an unknown key but accepts a missing one is only
	// half a list: a deleted key renders an empty slot, and an empty download
	// button is a page that still passes every check about where its link points.
	if err := ui.validate(); err != nil {
		return fmt.Errorf("%s: %w", m.UIStrings, err)
	}

	idPath, err := src.path(m.Identity.File)
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	id, err := readIdentity(idPath, m.Identity.Heading)
	if err != nil {
		return err
	}

	data := pageData{Identity: id, UI: ui, Links: forgeLinks(m)}

	// The release facts, when the caller has a record to give. Reading it here
	// rather than through the manifest is deliberate: the record is produced by
	// the release run and is never committed, so it is an ARGUMENT to a render
	// and not part of the composition.
	if releasePath != "" {
		release, err := loadRelease(releasePath)
		if err != nil {
			return err
		}
		data.Release = release
	}

	// The three pillars are the first bullets of the README's feature list, and
	// the template draws one of the mark's three shapes beside each. A fourth
	// would arrive without a shape, so the count is a hard requirement rather
	// than a truncation.
	pillarSpans, err := selectSpans(src, m.Sources.Pillars)
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
	installSpans, err := selectSpans(src, m.Sources.Install)
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}
	if len(installSpans) != len(ui.InstallComments) {
		return fmt.Errorf("install: the manifest selects %d commands but ui.json labels %d", len(installSpans), len(ui.InstallComments))
	}
	for i, s := range installSpans {
		data.Install = append(data.Install, installStep{Comment: ui.InstallComments[i], Command: s.Body})
	}

	noteSpans, err := selectSpans(src, m.Sources.InstallNote)
	if err != nil {
		return fmt.Errorf("install_note: %w", err)
	}
	for _, s := range noteSpans {
		data.InstallNote = append(data.InstallNote, inlineHTML(s.Body))
	}

	for i, requirement := range m.Sources.Requirements {
		spans, err := selectSpans(src, requirement)
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

	for _, sel := range []struct {
		name string
		src  source
		into *template.HTML
	}{
		{"cta_note", m.Sources.CtaNote, &data.CtaNote},
		{"endpoint", m.Sources.Endpoint, &data.Endpoint},
		{"runtime", m.Sources.Runtime, &data.Runtime},
		{"license", m.Sources.License, &data.License},
		{"status", m.Sources.Status, &data.Status},
		{"footer_note", m.Sources.FooterNote, &data.FooterNote},
	} {
		spans, err := selectSpans(src, sel.src)
		if err != nil {
			return fmt.Errorf("%s: %w", sel.name, err)
		}
		if len(spans) != 1 {
			return fmt.Errorf("%s: the manifest selects %d spans; the page has one slot", sel.name, len(spans))
		}
		*sel.into = inlineHTML(spans[0].Body)
	}

	tmplPath, err := src.path(m.Template)
	if err != nil {
		return fmt.Errorf("template: %w", err)
	}
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		return err
	}

	// The output subdirectory is a NAME, not a path: filepath.Base(a) leaves
	// ".." as "..", which would put the page in the parent of --out. "writes
	// nothing outside --out" is the reason the deploy chain lets an
	// uncredentialed job run this, so it is checked rather than assumed.
	subdir, err := pathElement(m.OutSubdir)
	if err != nil {
		return fmt.Errorf("out_subdir: %w", err)
	}
	dir := filepath.Join(out, subdir)
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
	for _, static := range m.Static {
		from, err := src.path(static)
		if err != nil {
			return fmt.Errorf("static: %w", err)
		}
		name, err := pathElement(filepath.Base(static))
		if err != nil {
			return fmt.Errorf("static %q: %w", static, err)
		}
		b, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
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
// silently ignores what it does not recognize is not one.
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

// repo resolves a manifest path against the tree being composed. Every path in
// the manifest is repository-relative by construction, and this is the only way
// a file is opened: an absolute path, or one that climbs out of the tree, is
// refused rather than quietly reinterpreted. One implementation, so the reads
// and the writes cannot disagree about what a manifest path means.
type repo struct{ root string }

func (r repo) path(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("no path given")
	}
	clean := filepath.Clean(filepath.FromSlash(p))
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("%q is absolute; manifest paths are repository-relative", p)
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%q climbs out of the repository", p)
	}
	return filepath.Join(r.root, clean), nil
}

// pathElement admits one ordinary directory or file name and nothing else: no
// separator, no "." or "..", no leading dot. It is what keeps the render inside
// --out, which is the property the deploy chain rests on when it lets a job
// with no credential run this code.
func pathElement(name string) (string, error) {
	switch {
	case name == "":
		return "", fmt.Errorf("is empty")
	case name == "." || name == "..":
		return "", fmt.Errorf("%q is not a name", name)
	case strings.HasPrefix(name, "."):
		return "", fmt.Errorf("%q starts with a dot", name)
	case strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator):
		return "", fmt.Errorf("%q contains a path separator; it must be a single name", name)
	case filepath.Clean(name) != name:
		return "", fmt.Errorf("%q is not a clean path element", name)
	}
	return name, nil
}

// validate refuses a missing interface string. The struct is walked rather than
// listed, so a key added to uiStrings is covered the day it appears; _purpose is
// documentation for a human reader and is the one field allowed to be absent.
func (u uiStrings) validate() error {
	v := reflect.ValueOf(u)
	t := v.Type()
	var missing []string
	for i := 0; i < t.NumField(); i++ {
		name := strings.SplitN(t.Field(i).Tag.Get("json"), ",", 2)[0]
		if name == "" || name == "_purpose" {
			continue
		}
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			if strings.TrimSpace(f.String()) == "" {
				missing = append(missing, name)
			}
		case reflect.Slice:
			if f.Len() == 0 {
				missing = append(missing, name)
				continue
			}
			for j := 0; j < f.Len(); j++ {
				e := f.Index(j)
				switch e.Kind() {
				case reflect.String:
					if strings.TrimSpace(e.String()) == "" {
						missing = append(missing, fmt.Sprintf("%s[%d]", name, j))
					}
				case reflect.Struct:
					for k := 0; k < e.NumField(); k++ {
						if e.Field(k).Kind() == reflect.String && strings.TrimSpace(e.Field(k).String()) == "" {
							sub := strings.SplitN(e.Type().Field(k).Tag.Get("json"), ",", 2)[0]
							missing = append(missing, fmt.Sprintf("%s[%d].%s", name, j, sub))
						}
					}
				}
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("these interface strings are missing or empty, and the page would render an empty slot for each: %s", strings.Join(missing, ", "))
	}
	return nil
}
