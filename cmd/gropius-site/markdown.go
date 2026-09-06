package main

// Selection of spans out of the repository's own Markdown, and the small amount
// of inline conversion the page needs. This is not a Markdown implementation:
// it reads the four shapes the manifest is allowed to select — a heading's
// bullets, its fenced code blocks, one bullet or one paragraph matched by text —
// and converts bold, code spans and links inside them. Anything richer belongs
// in the source file, where a reader of that file sees it too.

import (
	"fmt"
	"html"
	"html/template"
	"os"
	"regexp"
	"strings"
	"unicode"
)

// span is one selected piece of a source file. Title is set only where the
// selector splits a bullet's bold lead from its body (the pillars); everywhere
// else the whole selected text is the body.
type span struct {
	Title string
	Body  string
}

// source names one selection in the manifest: which file, under which heading,
// what to take, and optionally which part of it.
type source struct {
	File    string `json:"file"`
	Heading string `json:"heading"`
	Select  string `json:"select"`
	Match   string `json:"match"`
	Part    string `json:"part"`
	Limit   int    `json:"limit"`
}

type block struct {
	kind string // "bullet", "paragraph" or "code"
	text string
}

var (
	headingRe  = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*$`)
	bulletRe   = regexp.MustCompile(`^\s*[-*]\s+(.*)$`)
	boldLeadRe = regexp.MustCompile(`^\*\*(.+?)\*\*\s*(?:—|--)?\s*(.*)$`)
	identityRe = regexp.MustCompile(`^\*\*(Title|Tagline|Pitch):\*\*\s*(.+?)\s*$`)
	codeSpanRe = regexp.MustCompile("`([^`]+)`")
	linkRe     = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	boldRe     = regexp.MustCompile(`\*\*([^*]+)\*\*`)
)

// readIdentity reads the canonical block: the three lines every public surface
// of this project renders from.
func readIdentity(path, heading string) (identity, error) {
	blocks, err := blocksUnder(path, heading)
	if err != nil {
		return identity{}, err
	}
	var id identity
	for _, b := range blocks {
		if b.kind != "bullet" {
			continue
		}
		m := identityRe.FindStringSubmatch(b.text)
		if m == nil {
			continue
		}
		switch m[1] {
		case "Title":
			id.Title = m[2]
		case "Tagline":
			id.Tagline = m[2]
		case "Pitch":
			id.Pitch = m[2]
		}
	}
	if id.Title == "" || id.Tagline == "" || id.Pitch == "" {
		return identity{}, fmt.Errorf("%s: %q does not carry Title, Tagline and Pitch", path, heading)
	}
	return id, nil
}

// selectSpans applies one manifest selection to one file.
func selectSpans(r repo, src source) ([]span, error) {
	path, err := r.path(src.File)
	if err != nil {
		return nil, err
	}
	blocks, err := blocksUnder(path, src.Heading)
	if err != nil {
		return nil, err
	}
	var out []span
	switch src.Select {
	case "bullets":
		// The one selector that splits a bullet's bold lead from its body: the
		// pillars are rendered as a heading plus a sentence, and the README
		// writes them that way already.
		for _, b := range blocks {
			if b.kind != "bullet" {
				continue
			}
			if src.Limit > 0 && len(out) == src.Limit {
				break
			}
			text, err := part(b.text, src.Part)
			if err != nil {
				return nil, err
			}
			if m := boldLeadRe.FindStringSubmatch(text); m != nil {
				out = append(out, span{Title: m[1], Body: m[2]})
				continue
			}
			out = append(out, span{Body: text})
		}
	case "code-blocks":
		for _, b := range blocks {
			if b.kind != "code" {
				continue
			}
			if src.Limit > 0 && len(out) == src.Limit {
				break
			}
			out = append(out, span{Body: b.text})
		}
	case "bullet", "paragraph":
		if src.Match == "" {
			return nil, fmt.Errorf("select %q needs a match", src.Select)
		}
		for _, b := range blocks {
			if b.kind != src.Select || !strings.Contains(b.text, src.Match) {
				continue
			}
			text, err := part(b.text, src.Part)
			if err != nil {
				return nil, err
			}
			out = append(out, span{Body: text})
			break
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("%s: no %s under %q contains %q", src.File, src.Select, src.Heading, src.Match)
		}
	default:
		return nil, fmt.Errorf("unknown select %q", src.Select)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: nothing to select under %q", src.File, src.Heading)
	}
	return out, nil
}

// part narrows a block to the piece the manifest asked for. "first-sentence" is
// the only narrowing: a landing page's fact line wants the requirement, not the
// paragraph of qualification the tutorial rightly carries after it. An
// unrecognised value is refused rather than treated as "all" — a typo would
// otherwise put a whole paragraph of qualification on the page and say nothing.
func part(text, which string) (string, error) {
	switch which {
	case "", "all":
		return text, nil
	case "first-sentence":
		return firstSentence(text), nil
	default:
		return "", fmt.Errorf("unknown part %q (want first-sentence or all)", which)
	}
}

// firstSentence cuts at the first full stop that ends a sentence. A full stop
// inside emphasis ("**Requires macOS 26.**") still ends one, so the closing
// markers are carried across with it — cutting between them would leave the
// emphasis unbalanced and the bold would run to the end of the page.
func firstSentence(text string) string {
	for i := 0; i < len(text); i++ {
		if text[i] != '.' {
			continue
		}
		j := i + 1
		for j < len(text) && (text[j] == '*' || text[j] == '`' || text[j] == ')') {
			j++
		}
		if j == len(text) || text[j] == ' ' {
			return text[:j]
		}
	}
	return text
}

// sentenceCase capitalises the first letter. A README bullet's body continues
// its bold lead ("**Model browser** — search the ...") and reads as a fragment;
// on the page the lead becomes a heading and the body a sentence of its own.
// A body that starts with anything but a lowercase letter — a code span, say —
// is left alone.
func sentenceCase(s string) string {
	r := []rune(s)
	if len(r) == 0 || !unicode.IsLower(r[0]) {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// blocksUnder returns the blocks of one section: everything after the named
// heading, up to the next heading at the same or a higher level.
func blocksUnder(path, heading string) ([]block, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")
	start, level, seen := -1, 0, 0
	for i, line := range lines {
		m := headingRe.FindStringSubmatch(line)
		if m != nil && m[2] == heading {
			seen++
			if start < 0 {
				start, level = i+1, len(m[1])
			}
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("%s: no heading %q", path, heading)
	}
	// Two headings of the same name make the selection ambiguous, and the
	// ambiguity is silent: the first one wins and the page carries whatever sits
	// under it. A file that grows a second "Features" is exactly the case a page
	// composed from a living README will meet, so it is refused.
	if seen > 1 {
		return nil, fmt.Errorf("%s: %d headings named %q; the selection would be ambiguous", path, seen, heading)
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if m := headingRe.FindStringSubmatch(lines[i]); m != nil && len(m[1]) <= level {
			end = i
			break
		}
	}
	return scanBlocks(lines[start:end]), nil
}

func scanBlocks(lines []string) []block {
	var out []block
	var cur *block
	flush := func() {
		if cur != nil {
			cur.text = strings.TrimSpace(cur.text)
			if cur.text != "" {
				out = append(out, *cur)
			}
			cur = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			flush()
			var code []string
			for i++; i < len(lines); i++ {
				if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
					break
				}
				code = append(code, lines[i])
			}
			out = append(out, block{kind: "code", text: strings.Join(code, "\n")})
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if m := bulletRe.FindStringSubmatch(line); m != nil {
			flush()
			cur = &block{kind: "bullet", text: m[1]}
			continue
		}
		// A continuation of whatever is open: Markdown wraps a bullet or a
		// paragraph across lines, and the page wants the sentence, not the
		// source file's line breaks.
		if cur == nil {
			cur = &block{kind: "paragraph"}
		}
		if cur.text != "" {
			cur.text += " "
		}
		cur.text += strings.TrimSpace(line)
	}
	flush()
	return out
}

// inlineHTML renders the inline Markdown the selected spans may carry. The text
// is HTML-escaped FIRST and the three patterns are applied to the escaped
// string, so nothing a source file contains can introduce markup of its own —
// only these three shapes become tags, and only where the source file wrote
// them. Nesting is not supported: a bold link stays as it was written.
func inlineHTML(md string) template.HTML {
	s := html.EscapeString(md)
	s = codeSpanRe.ReplaceAllString(s, "<code>$1</code>")
	s = linkRe.ReplaceAllStringFunc(s, func(m string) string {
		g := linkRe.FindStringSubmatch(m)
		if !safeURL(g[2]) {
			return m
		}
		return `<a href="` + g[2] + `">` + g[1] + `</a>`
	})
	s = boldRe.ReplaceAllString(s, "<strong>$1</strong>")
	return template.HTML(s)
}

// safeURL admits the shapes a link on this page may take. Anything else — a
// javascript: or data: URL above all — is left as the literal text the source
// file wrote, which is visible and harmless.
func safeURL(u string) bool {
	switch {
	case strings.HasPrefix(u, "https://"), strings.HasPrefix(u, "http://"):
		return true
	case strings.HasPrefix(u, "#"), strings.HasPrefix(u, "./"):
		return true
	default:
		return !strings.Contains(u, ":")
	}
}
