package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// TestShellVariablesAreBracedBeforeNonASCII refuses an unbraced `$VAR`
// immediately followed by a non-ASCII byte in any shell script.
//
// This is a mechanism standing in for vigilance, because vigilance lost three
// times. `echo "Downloading $APP…"` resolves the variable name as APP plus the
// first byte of the UTF-8 ellipsis: the shell's identifier scan is byte-wise,
// and a continuation byte is not a delimiter. Under `set -u` — which every
// script here sets — that is an unbound variable and the script aborts before
// doing anything.
//
// It cost the documented one-line install twice. The first instance shipped and
// broke installation on every shell tested; the fix for it introduced a second
// instance on the very next line written, because the sweep that found the
// first was run before that line existed. The failure is invisible to review
// (the ellipsis renders as one glyph beside the variable), invisible to
// `bash -n` (the syntax is valid), and invisible to every unit test (nothing
// executes the script). A test is the only place it can be caught cheaply.
//
// The fix is always the same: brace the expansion, `${APP}…`, which terminates
// the identifier explicitly.
//
// Shell does not only live in .sh files. A workflow's `run:` block is shell
// too, runs under `set -u` by the same convention, and is read by exactly the
// same byte-wise identifier scan — and the release workflow's installer gate is
// a long one. Those blocks are scanned here as well; scanning only files with a
// shell extension or a shebang left them out, which is where the next instance
// of this failure would have landed unseen.
func TestShellVariablesAreBracedBeforeNonASCII(t *testing.T) {
	root := repoRootDir(t)
	var offences []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", "bin", "site":
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, line := range shellLines(path, string(b)) {
			if col := unbracedBeforeNonASCII(line.text); col >= 0 {
				offences = append(offences, rel+":"+itoa(line.number)+": "+strings.TrimSpace(line.text))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	for _, o := range offences {
		t.Errorf("unbraced expansion immediately before a non-ASCII byte — brace it as ${VAR}: %s", o)
	}
}

// shellLine is one line of shell, with its 1-based number in the file it came
// from — which is not the line's position in the shell, once a workflow's
// `run:` block is what supplied it.
type shellLine struct {
	number int
	text   string
}

// shellLines returns the shell a file contains, in file order: every line of a
// shell script, or the `run:` bodies of a YAML file.
func shellLines(path, src string) []shellLine {
	if isShellScript(path, src) {
		lines := strings.Split(src, "\n")
		out := make([]shellLine, 0, len(lines))
		for i, line := range lines {
			out = append(out, shellLine{i + 1, line})
		}
		return out
	}
	if strings.HasSuffix(path, ".yml") || strings.HasSuffix(path, ".yaml") {
		return yamlRunLines(src)
	}
	return nil
}

// yamlRunLines returns the body of every `run:` block scalar in a YAML file,
// plus any single-line `run:`. A block scalar's body is the run of lines
// indented deeper than the `run:` key that opened it; blank lines belong to it
// but carry no shell.
func yamlRunLines(src string) []shellLine {
	var out []shellLine
	opened := -1 // indent of the `run:` key whose body we are in, or -1
	for i, line := range strings.Split(src, "\n") {
		if opened >= 0 {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if leadingSpaces(line) > opened {
				out = append(out, shellLine{i + 1, line})
				continue
			}
			opened = -1
		}
		// `run:` is a step's own key, so it appears either on its own or as the
		// key that opens the step's sequence entry.
		key := strings.TrimPrefix(strings.TrimLeft(line, " \t"), "- ")
		if !strings.HasPrefix(key, "run:") {
			continue
		}
		switch value := strings.TrimSpace(strings.TrimPrefix(key, "run:")); {
		case strings.HasPrefix(value, "|"), strings.HasPrefix(value, ">"):
			opened = leadingSpaces(line)
		default:
			out = append(out, shellLine{i + 1, line})
		}
	}
	return out
}

func leadingSpaces(line string) int {
	n := 0
	for n < len(line) && (line[n] == ' ' || line[n] == '\t') {
		n++
	}
	return n
}

// unbracedBeforeNonASCII returns the byte offset of the first `$name` that is
// followed directly by a non-ASCII byte, or -1. `${name}` and `$name` followed
// by anything ASCII are both fine.
func unbracedBeforeNonASCII(line string) int {
	for i := 0; i < len(line); i++ {
		if line[i] != '$' || i+1 >= len(line) {
			continue
		}
		j := i + 1
		if line[j] == '{' { // already braced
			continue
		}
		start := j
		for j < len(line) && isNameByte(line[j]) {
			j++
		}
		if j == start || j >= len(line) {
			continue // not a name, or ends the line
		}
		if line[j] >= utf8.RuneSelf {
			return i
		}
	}
	return -1
}

func isNameByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') ||
		unicode.IsLetter(rune(c)) && c < utf8.RuneSelf
}

func isShellScript(path, src string) bool {
	if strings.HasSuffix(path, ".sh") || strings.HasSuffix(path, ".bash") {
		return true
	}
	// A hook or a script with no extension still counts: read the shebang.
	if len(src) < 2 || src[0] != '#' || src[1] != '!' {
		return false
	}
	first := src
	if nl := strings.IndexByte(src, '\n'); nl >= 0 {
		first = src[:nl]
	}
	return strings.Contains(first, "sh")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
