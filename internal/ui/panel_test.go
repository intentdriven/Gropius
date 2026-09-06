package ui

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The control panel is plain JavaScript with no build step and no test
// runner, so the card's context label lives in one pure function that a test
// can lift out of the file and evaluate on its own. Evaluating app.js whole
// would need a DOM; extracting the function needs nothing but node, and the
// wiring test below keeps the extracted function from drifting out of use.
func TestContextLabelOnTheModelCard(t *testing.T) {
	src := readPanelSource(t)
	fn := extractFunction(t, src, "contextLabel")

	cases := []struct {
		name  string
		model string
		want  string
	}{
		{"a wide window", `{"context_length":262144}`, "max context 256K"},
		{"a small window", `{"context_length":40960}`, "max context 40K"},
		{"under a kibitoken", `{"context_length":512}`, "max context 512"},
		{"no figure", `{}`, ""},
		{"zero", `{"context_length":0}`, ""},
		{"negative", `{"context_length":-1}`, ""},
		{"not a number", `{"context_length":"262144"}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evalJS(t, fn+"\nprocess.stdout.write(JSON.stringify(contextLabel("+c.model+")));")
			var s string
			if err := json.Unmarshal([]byte(got), &s); err != nil {
				t.Fatalf("contextLabel returned %q, which is not a string: %v", got, err)
			}
			if s != c.want {
				t.Errorf("contextLabel(%s) = %q, want %q", c.model, s, c.want)
			}
		})
	}
}

// The label says "max context" because the figure is the model's
// architectural maximum; a card that said only "context" would be read as the
// window this Mac can serve, which is a smaller and separate number.
func TestModelCardShowsTheContextLabelBesideTheSize(t *testing.T) {
	src := readPanelSource(t)
	body := extractFunction(t, src, "renderModels")
	if !strings.Contains(body, "contextLabel(m)") {
		t.Error("renderModels does not put the context label on the card")
	}
	if !strings.Contains(extractFunction(t, src, "contextLabel"), "max context") {
		t.Error(`the card's label must say "max context", so the figure is not read as the effective window`)
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
// declaration, found by matching braces from the opening one.
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
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start : i+1]
			}
		}
	}
	t.Fatalf("function %s is unbalanced", name)
	return ""
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
	cmd := exec.Command(node, "-e", snippet)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	return string(out)
}
