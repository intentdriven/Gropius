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
		if !isShellScript(path) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(b), "\n") {
			if col := unbracedBeforeNonASCII(line); col >= 0 {
				offences = append(offences, rel+":"+itoa(i+1)+": "+strings.TrimSpace(line))
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

func isShellScript(path string) bool {
	if strings.HasSuffix(path, ".sh") || strings.HasSuffix(path, ".bash") {
		return true
	}
	// A hook or a script with no extension still counts: read the shebang.
	b, err := os.ReadFile(path)
	if err != nil || len(b) < 2 || b[0] != '#' || b[1] != '!' {
		return false
	}
	nl := strings.IndexByte(string(b), '\n')
	if nl < 0 {
		nl = len(b)
	}
	first := string(b[:nl])
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
