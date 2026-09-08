package main

// The renderer's refusals. Every check here is a way the page could go wrong
// quietly — a slot that renders empty, a span selected from the wrong section, a
// file written outside the output directory — so each one asserts that the
// render STOPS rather than producing a page that looks finished.
//
// The tests compose a fixture tree rather than the repository's own files: the
// committed manifest is exercised end to end by internal/sitetest, and a fixture
// is what makes a second "## Features" or a deleted interface string something a
// test can create.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fixtureIdentity = `# Identity

## Identity (canonical)

- **Title:** Fixture
- **Tagline:** A fixture tagline, rendered from the block.
- **Pitch:** A fixture pitch, long enough to stand in for the real one on a page.
`

	fixtureReadme = "# Fixture\n" + `
A lead paragraph before any heading.

It manages its own runtime: it installs a private Python of its own. A second
sentence the page does not take.

## Status

Experimental. Cross-machine use works; nothing else is claimed here.

## Features

- **One** — the first thing it does.
- **Two** — the second thing it does.
- **Three** — the third thing it does.
- **Four** — a fourth the page has no shape for.

## Install

` + "```sh\ncurl -fsSL https://example.invalid/install.sh | bash\n```" + `

` + "```sh\ncurl -fsSL https://example.invalid/install.sh | bash -s -- client\n```" + `

The installer verifies the download before installing it: it checks it against
the checksums published beside it. The binaries are ad-hoc signed, not
notarized. A third sentence, to be left behind.

## Licence

MIT. See LICENSE.
`

	fixtureGettingStarted = `# Getting started

## 1. Requirements

- An **Apple Silicon** Mac (M1 or later). A sentence of qualification after it.
- **Requires macOS 26.** A sentence of qualification after it.

## 5. Talk to it — from another machine

It lists the exact base URLs to use, for example ` + "`http://fixture.invalid:11535/v1`" + `.
From any other machine on the same network:
`
)

// tree writes a fixture repository: the committed template, stylesheet,
// interface strings and manifest, over sources this test controls.
func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{
		filepath.Join("site-src", "index.html.tmpl"),
		filepath.Join("site-src", "site.css"),
		filepath.Join("site-src", "ui.json"),
		filepath.Join("site-src", "headers"),
		filepath.Join(".abcd", "site.json"),
	} {
		b, err := os.ReadFile(filepath.Join("..", "..", f))
		if err != nil {
			t.Fatal(err)
		}
		write(t, root, f, string(b))
	}
	write(t, root, filepath.Join(".abcd", "development", "IDENTITY.md"), fixtureIdentity)
	write(t, root, "README.md", fixtureReadme)
	write(t, root, filepath.Join("docs", "getting-started.md"), fixtureGettingStarted)
	return root
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// patchManifest applies one edit to the fixture's manifest, as JSON, so a test
// can say what it is changing without restating the whole file.
func patchManifest(t *testing.T, root string, edit func(m map[string]any)) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(read(t, root, filepath.Join(".abcd", "site.json"))), &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, filepath.Join(".abcd", "site.json"), string(b))
}

func renderFixture(t *testing.T, root string) (string, error) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	return out, render(root, filepath.Join(root, ".abcd", "site.json"), out, "")
}

// The baseline: without it, every refusal below could be passing for the wrong
// reason.
func TestFixtureRenders(t *testing.T) {
	root := tree(t)
	out, err := renderFixture(t, root)
	if err != nil {
		t.Fatalf("the fixture must render: %v", err)
	}
	page := read(t, out, filepath.Join("Gropius", "index.html"))
	for _, want := range []string{
		"A fixture tagline, rendered from the block.",
		"The first thing it does.",
		"Requires macOS 26.",
		"curl -fsSL https://example.invalid/install.sh | bash",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the rendered fixture does not carry %q", want)
		}
	}
	if strings.Contains(page, "A second sentence, to be cut.") {
		t.Error("first-sentence did not narrow the install note")
	}
	// The prose the page asserts about the product, each selected rather than
	// written here: the matched sentence, the code span, the lead, the license.
	for _, want := range []string{
		"The binaries are ad-hoc signed, not notarized.",
		"http://fixture.invalid:11535/v1",
		"It manages its own runtime: it installs a private Python of its own.",
		"MIT.",
		"Experimental.",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the rendered fixture does not carry the selected span %q", want)
		}
	}
	if strings.Contains(page, "A third sentence, to be left behind.") {
		t.Error("matched-sentence did not narrow the call-to-action note")
	}
}

// "Writes nothing outside --out" is the reason the deploy chain lets a job with
// no credential run this code, so out_subdir is a name and not a path.
func TestOutSubdirMustBeASingleName(t *testing.T) {
	for _, subdir := range []string{"..", "a/b", ".", "", ".hidden", "./Gropius", "Gropius/"} {
		t.Run(subdir, func(t *testing.T) {
			root := tree(t)
			patchManifest(t, root, func(m map[string]any) { m["out_subdir"] = subdir })
			out, err := renderFixture(t, root)
			if err == nil {
				t.Fatalf("out_subdir %q rendered; it must be refused", subdir)
			}
			if !strings.Contains(err.Error(), "out_subdir") {
				t.Errorf("the error does not name out_subdir: %v", err)
			}
			// Nothing may have escaped into the parent of --out.
			if entries, _ := os.ReadDir(filepath.Dir(out)); len(entries) != 0 {
				t.Errorf("the refused render still wrote %d entry/entries beside --out", len(entries))
			}
		})
	}
}

func TestPathElement(t *testing.T) {
	for _, ok := range []string{"Gropius", "site.css", "a-b_c.2"} {
		if _, err := pathElement(ok); err != nil {
			t.Errorf("pathElement(%q) = %v; want it admitted", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", ".git", "a/b", "a//b", "./a"} {
		if _, err := pathElement(bad); err == nil {
			t.Errorf("pathElement(%q) was admitted; want a refusal", bad)
		}
	}
}

// A manifest path that climbs out of the tree is refused rather than silently
// reinterpreted, and there is one implementation of that rule for every read.
//
// The escape target is a REAL, READABLE identity block outside the tree, so the
// refusal cannot be passing merely because a file is missing: without the check
// this render succeeds and composes the page from a file the repository does not
// contain.
func TestManifestPathsStayInsideTheRepository(t *testing.T) {
	t.Run("climbs out", func(t *testing.T) {
		root := tree(t)
		outside := filepath.Join(filepath.Dir(root), "outside-identity.md")
		if err := os.WriteFile(outside, []byte(fixtureIdentity), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Remove(outside) })
		patchManifest(t, root, func(m map[string]any) {
			m["identity"] = map[string]any{"file": "../outside-identity.md", "heading": "Identity (canonical)"}
		})
		_, err := renderFixture(t, root)
		if err == nil {
			t.Fatal("a manifest path climbing out of the repository rendered; it must be refused")
		}
		if !strings.Contains(err.Error(), "climbs out") {
			t.Errorf("the error does not say why: %v", err)
		}
	})
	t.Run("absolute", func(t *testing.T) {
		root := tree(t)
		outside := filepath.Join(filepath.Dir(root), "absolute-identity.md")
		if err := os.WriteFile(outside, []byte(fixtureIdentity), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Remove(outside) })
		patchManifest(t, root, func(m map[string]any) {
			m["identity"] = map[string]any{"file": outside, "heading": "Identity (canonical)"}
		})
		_, err := renderFixture(t, root)
		if err == nil {
			t.Fatal("an absolute manifest path rendered; it must be refused")
		}
		if !strings.Contains(err.Error(), "absolute") {
			t.Errorf("an absolute path must be refused as one, not reinterpreted: %v", err)
		}
	})
	t.Run("source file", func(t *testing.T) {
		root := tree(t)
		outside := filepath.Join(filepath.Dir(root), "outside-readme.md")
		if err := os.WriteFile(outside, []byte(fixtureReadme), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Remove(outside) })
		patchManifest(t, root, func(m map[string]any) {
			m["sources"].(map[string]any)["pillars"].(map[string]any)["file"] = "../outside-readme.md"
		})
		if _, err := renderFixture(t, root); err == nil {
			t.Fatal("a source file outside the repository rendered; it must be refused")
		}
	})
}

// The failure a growing README actually produces: a second heading of the same
// name, whose content is plausible, silently replacing the selection.
func TestDuplicateHeadingIsRefused(t *testing.T) {
	root := tree(t)
	write(t, root, "README.md", strings.Replace(fixtureReadme, "## Features", `## Features

- **Decoy one** — not the real feature list.
- **Decoy two** — not the real feature list.
- **Decoy three** — not the real feature list.

## Features`, 1))
	_, err := renderFixture(t, root)
	if err == nil {
		t.Fatal("two sections named Features rendered; the selection is ambiguous and must be refused")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("the error does not say why: %v", err)
	}
}

func TestMissingSourceIsRefused(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		root := tree(t)
		if err := os.Remove(filepath.Join(root, "docs", "getting-started.md")); err != nil {
			t.Fatal(err)
		}
		if _, err := renderFixture(t, root); err == nil {
			t.Fatal("a missing source file rendered; it must be refused")
		}
	})
	t.Run("heading", func(t *testing.T) {
		root := tree(t)
		write(t, root, "README.md", strings.Replace(fixtureReadme, "## Features", "## Capabilities", 1))
		_, err := renderFixture(t, root)
		if err == nil {
			t.Fatal("a missing heading rendered; it must be refused")
		}
		if !strings.Contains(err.Error(), "no heading") {
			t.Errorf("the error does not say which heading is missing: %v", err)
		}
	})
}

// The interface strings are an allowlist in both directions: an unknown key is a
// word nobody reviewed, a missing one is an empty slot on a public page.
func TestInterfaceStringsAreAClosedList(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		root := tree(t)
		var ui map[string]any
		if err := json.Unmarshal([]byte(read(t, root, filepath.Join("site-src", "ui.json"))), &ui); err != nil {
			t.Fatal(err)
		}
		delete(ui, "install_label")
		b, _ := json.MarshalIndent(ui, "", "  ")
		write(t, root, filepath.Join("site-src", "ui.json"), string(b))
		_, err := renderFixture(t, root)
		if err == nil {
			t.Fatal("a ui.json without install_label rendered; the install button would carry no label")
		}
		if !strings.Contains(err.Error(), "install_label") {
			t.Errorf("the error does not name the missing string: %v", err)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		root := tree(t)
		ui := read(t, root, filepath.Join("site-src", "ui.json"))
		write(t, root, filepath.Join("site-src", "ui.json"), strings.Replace(ui, "{", `{"marketing_line": "Buy now",`, 1))
		if _, err := renderFixture(t, root); err == nil {
			t.Fatal("an unknown interface string rendered; the list is meant to be closed")
		}
	})
}

func TestManifestShapeIsRefusedEarly(t *testing.T) {
	t.Run("schema_version", func(t *testing.T) {
		root := tree(t)
		patchManifest(t, root, func(m map[string]any) { m["schema_version"] = 2 })
		_, err := renderFixture(t, root)
		if err == nil || !strings.Contains(err.Error(), "schema_version") {
			t.Fatalf("a future schema_version must be refused by name; got %v", err)
		}
	})
	t.Run("unknown key", func(t *testing.T) {
		root := tree(t)
		patchManifest(t, root, func(m map[string]any) { m["footer_html"] = "<b>hi</b>" })
		if _, err := renderFixture(t, root); err == nil {
			t.Fatal("an unknown manifest key rendered; the manifest is a closed shape")
		}
	})
	t.Run("unknown select", func(t *testing.T) {
		root := tree(t)
		patchManifest(t, root, func(m map[string]any) {
			m["sources"].(map[string]any)["pillars"].(map[string]any)["select"] = "paragraphs"
		})
		_, err := renderFixture(t, root)
		if err == nil || !strings.Contains(err.Error(), "unknown select") {
			t.Fatalf("an unknown selector must be refused by name; got %v", err)
		}
	})
	t.Run("unknown part", func(t *testing.T) {
		root := tree(t)
		patchManifest(t, root, func(m map[string]any) {
			m["sources"].(map[string]any)["install_note"].(map[string]any)["part"] = "frist-sentence"
		})
		_, err := renderFixture(t, root)
		if err == nil || !strings.Contains(err.Error(), "unknown part") {
			t.Fatalf("a mistyped part must be refused rather than rendering the whole block; got %v", err)
		}
	})
	t.Run("too few pillars", func(t *testing.T) {
		root := tree(t)
		patchManifest(t, root, func(m map[string]any) {
			m["sources"].(map[string]any)["pillars"].(map[string]any)["limit"] = 2
		})
		if _, err := renderFixture(t, root); err == nil {
			t.Fatal("two pillars rendered; the page draws one of the mark's three shapes beside each")
		}
	})
}

func TestFirstSentence(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"**Requires macOS 26.** And more.", "**Requires macOS 26.**"},
		{"An **Apple Silicon** Mac (M1 or later). MLX runs on Metal.", "An **Apple Silicon** Mac (M1 or later)."},
		{"One sentence only.", "One sentence only."},
		{"No full stop at all", "No full stop at all"},
		{"Ends in `code`. Then more.", "Ends in `code`."},
	} {
		if got := firstSentence(c.in); got != c.want {
			t.Errorf("firstSentence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSentenceCase(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"search the org", "Search the org"},
		{"`/v1/models`, streaming", "`/v1/models`, streaming"},
		{"", ""},
		{"Already capital", "Already capital"},
	} {
		if got := sentenceCase(c.in); got != c.want {
			t.Errorf("sentenceCase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A link in a selected span is a URL a source file wrote. Only the shapes this
// page may carry become anchors; anything else stays visible literal text.
func TestSafeURL(t *testing.T) {
	for _, ok := range []string{"https://example.invalid", "http://example.invalid", "#install", "./site.css", "docs/getting-started.md"} {
		if !safeURL(ok) {
			t.Errorf("safeURL(%q) = false; want it admitted", ok)
		}
	}
	for _, bad := range []string{"javascript:alert(1)", "data:text/html,<b>", "vbscript:x"} {
		if safeURL(bad) {
			t.Errorf("safeURL(%q) = true; want it refused", bad)
		}
	}
}

func TestSelectedTextCannotIntroduceMarkup(t *testing.T) {
	got := string(inlineHTML(`<img src=x onerror=alert(1)> and [x](javascript:alert(2))`))
	if strings.Contains(got, "<img") || strings.Contains(got, "<a href=\"javascript:") {
		t.Errorf("markup from a source span reached the page: %s", got)
	}
}
