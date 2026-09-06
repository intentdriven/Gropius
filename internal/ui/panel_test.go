package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The control panel is plain JavaScript with no build step and no test
// runner, so the card's one piece of real logic — what its info line says —
// lives in pure functions a test can lift out of the file and evaluate.
// Evaluating app.js whole would need a DOM; these need nothing but node.

// This is the acceptance test for the card: it asserts the string the card
// actually shows, not that a call appears in the source. The label says "max
// context" because the figure is the model's architectural maximum; a card
// that said only "context" would be read as the window this Mac can serve.
func TestModelCardInfoLineShowsTheMaximumContext(t *testing.T) {
	cases := []struct {
		name  string
		model string
		want  string
	}{
		{"ready, with a figure", `{"state":"ready","bytes":1024,"context_length":262144}`, "1.0 KB · max context 256K"},
		{"ready, no figure", `{"state":"ready","bytes":1024}`, "1.0 KB"},
		{"ready, figure not a number", `{"state":"ready","bytes":1024,"context_length":"262144"}`, "1.0 KB"},
		// The abbreviation must never round up: a card reading 256K for a
		// model that declares 262,143 shows one token more than it has.
		{"ready, an inexact figure", `{"state":"ready","bytes":1024,"context_length":262143}`, "1.0 KB · max context 255K"},
		{"ready, under a kibitoken", `{"state":"ready","bytes":1024,"context_length":512}`, "1.0 KB · max context 512"},
		// The other two branches keep their own shape: no figure appears on a
		// download in flight or on a failure, and a failure's text is escaped
		// (the harness's escapeHtml marks what it was given).
		{"downloading", `{"state":"downloading","progress":42.4,"size_bytes":2048}`, "downloading… 42% of 2.0 KB"},
		{"failed", `{"state":"failed","err":"boom","context_length":262144}`, "esc(boom)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evalPanel(t, "modelInfoLine("+c.model+")", "bytes", "contextLabel", "modelInfoLine")
			if got != c.want {
				t.Errorf("modelInfoLine(%s) = %q, want %q", c.model, got, c.want)
			}
		})
	}
}

// modelInfoLine is only worth testing while the card is both built from it
// and shows what it returns. Those two lines of the renderer are the seam the
// tests above cannot reach without a DOM: one computes the string, the other
// places it in the card's markup, and a card that computed the info line and
// then dropped it would show no size and no context figure at all.
func TestModelCardIsBuiltFromTheInfoLine(t *testing.T) {
	body := extractFunction(t, readPanelSource(t), "renderModels")
	for _, fragment := range []string{
		"const info = modelInfoLine(m);",
		`<div class="info">${info}</div>`,
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("renderModels no longer contains %s — the card's info line is then asserted by nothing", fragment)
		}
	}
}

func readPanelSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// extractFunction returns the source of a top-level `function name(...) {...}`
// declaration, found by matching braces from the opening one. Braces inside
// strings, template literals and comments are skipped: app.js is full of
// template literals holding HTML, and counting their braces would truncate a
// body silently and make the test fail — or pass — for reasons that have
// nothing to do with the panel.
func extractFunction(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, "function "+name+"(")
	if start < 0 {
		t.Fatalf("no function %s in the control panel source", name)
	}
	open := strings.Index(src[start:], "{")
	if open < 0 {
		t.Fatalf("function %s has no body", name)
	}
	depth := 0
	for i := start + open; i < len(src); i++ {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			i += skipTo(src[i:], "\n")
		case strings.HasPrefix(src[i:], "/*"):
			i += skipTo(src[i:], "*/")
		case src[i] == '\'' || src[i] == '"' || src[i] == '`':
			i += skipQuoted(src[i:])
		case src[i] == '{':
			depth++
		case src[i] == '}':
			depth--
			if depth == 0 {
				return src[start : i+1]
			}
		}
	}
	t.Fatalf("function %s is unbalanced", name)
	return ""
}

// skipTo reports how far past the start of s the terminator ends, or the end
// of s when it never appears.
func skipTo(s, terminator string) int {
	if i := strings.Index(s[len(terminator):], terminator); i >= 0 {
		return i + 2*len(terminator) - 1
	}
	return len(s) - 1
}

// skipQuoted reports how far past the start of s its opening quote closes,
// honoring backslash escapes. It scans for the next occurrence of the opening
// quote character, so a template literal holding another template inside a
// ${...} substitution — contextLabel has one — stops at the inner backtick and
// leaves that substitution's braces to the caller's counter. That is safe
// while every substitution is itself brace-balanced, which is true of app.js
// and is what the unbalanced-function failure above would catch if it stopped
// being true.
func skipQuoted(s string) int {
	quote := s[0]
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case quote:
			return i
		}
	}
	return len(s) - 1
}

// evalPanel evaluates one expression against the named functions lifted out
// of app.js. escapeHtml is stubbed with a marker rather than reimplemented:
// the real one needs a DOM, and what the test needs to know is that the
// failed branch passes its text through it.
func evalPanel(t *testing.T, expr string, functions ...string) string {
	t.Helper()
	src := readPanelSource(t)
	var b strings.Builder
	b.WriteString("const escapeHtml = (s) => `esc(${s})`;\n")
	for _, name := range functions {
		b.WriteString(extractFunction(t, src, name))
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "process.stdout.write(JSON.stringify(%s));", expr)

	out := evalJS(t, b.String())
	var s string
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("the panel returned %q, which is not a string: %v", out, err)
	}
	return s
}

// evalJS runs a snippet under node and returns what it wrote to stdout. The
// control panel has no JavaScript toolchain of its own, so a machine without
// node skips rather than reporting a green it did not earn.
func evalJS(t *testing.T, snippet string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the control panel's JavaScript is unverified on this machine")
	}
	out, err := exec.Command(node, "-e", snippet).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	return string(out)
}

// evalPanelArray evaluates an expression returning an array against the named
// functions lifted out of app.js.
func evalPanelArray(t *testing.T, expr string, functions ...string) []any {
	t.Helper()
	out := evalPanelExpr(t, expr, functions...)
	var v []any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("the panel returned %q, which is not an array: %v", out, err)
	}
	return v
}

// evalPanelNumber evaluates an expression returning a number.
func evalPanelNumber(t *testing.T, expr string, functions ...string) float64 {
	t.Helper()
	out := evalPanelExpr(t, expr, functions...)
	var v float64
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("the panel returned %q, which is not a number: %v", out, err)
	}
	return v
}

// evalPanelExpr is the JSON text of one expression evaluated against the named
// functions lifted out of app.js.
func evalPanelExpr(t *testing.T, expr string, functions ...string) string {
	t.Helper()
	src := readPanelSource(t)
	var b strings.Builder
	for _, name := range functions {
		b.WriteString(extractFunction(t, src, name))
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "process.stdout.write(JSON.stringify(%s));", expr)
	return evalJS(t, b.String())
}
