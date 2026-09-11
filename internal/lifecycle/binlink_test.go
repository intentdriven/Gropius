package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The link that puts the verbs within reach is made in THIS account's own bin
// directory and elevates for nothing. The panel exists for the firewall, which
// is machine-wide state with no per-account equivalent; a link has one, so
// there is nothing here to ask an administrator for.

func TestTheLinkGoesInThisAccountsOwnBinDirectory(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(t.TempDir(), "Gropius.app", "Contents", "MacOS", "gropius")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	link, err := linkCommand(home, binary)
	if err != nil {
		t.Fatalf("linkCommand: %v", err)
	}
	if want := filepath.Join(home, ".local", "bin", "gropius"); link != want {
		t.Errorf("the link is at %s, want %s", link, want)
	}
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("the link is not a symbolic link: %v", err)
	}
	if target != binary {
		t.Errorf("the link points at %s, want the installed binary %s", target, binary)
	}
}

// An install over an install replaces the link. The previous one points into
// the bundle that has just been replaced, which on a per-user install is a
// directory that may no longer exist.
func TestTheLinkReplacesAnOlderOneWithoutFollowingIt(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The link that is there points at a file this test watches: following it
	// rather than replacing it would write through to that file.
	witness := filepath.Join(t.TempDir(), "somebody-elses-binary")
	if err := os.WriteFile(witness, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(witness, filepath.Join(dir, "gropius")); err != nil {
		t.Fatal(err)
	}

	binary := filepath.Join(t.TempDir(), "gropius")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link, err := linkCommand(home, binary)
	if err != nil {
		t.Fatalf("linkCommand: %v", err)
	}
	if target, _ := os.Readlink(link); target != binary {
		t.Errorf("the link still points at %s", target)
	}
	b, err := os.ReadFile(witness)
	if err != nil || string(b) != "untouched" {
		t.Errorf("what the old link pointed at was written through: %q, %v", b, err)
	}
	if left := staleNames(t, dir); len(left) > 0 {
		t.Errorf("the link was replaced through %v, which is left behind", left)
	}
}

// Whether that directory is on the account's search path is a pure function of
// a PATH string, and the answer decides whether the install has something more
// to say.
func TestWhetherTheBinDirectoryIsOnTheSearchPath(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()

	// An entry that is a symlink to the same directory resolves to the same
	// place, and a person whose profile spells it that way has nothing to fix.
	linked := filepath.Join(t.TempDir(), "link-to-bin")
	if err := os.Symlink(dir, linked); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{"present", "/usr/bin:" + dir + ":/bin", true},
		{"absent", "/usr/bin:" + other, false},
		{"present, with a trailing separator", "/usr/bin:" + dir + ":", true},
		{"present, with a trailing slash on the entry", "/usr/bin:" + dir + "/", true},
		{"present through a symlink to the same directory", "/usr/bin:" + linked, true},
		{"an empty PATH", "", false},
		// An empty entry means the working directory, which is not this one
		// however the command happens to be run.
		{"an empty entry only", "::", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := onSearchPath(dir, tc.path); got != tc.want {
				t.Errorf("onSearchPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// And when it is not on the path, the install names the line that would put it
// there rather than leaving a link that resolves for nobody.
func TestThePathAdviceNamesTheLineToAdd(t *testing.T) {
	home := t.TempDir()
	advice := pathAdvice(filepath.Join(home, ".local", "bin"), home)

	if !strings.Contains(advice, `export PATH="$HOME/.local/bin:$PATH"`) {
		t.Errorf("the advice does not carry the line that would add the directory: %q", advice)
	}
	if strings.Contains(advice, home) {
		t.Errorf("the advice spells this account's home directory out: %q", advice)
	}
}

// staleNames is whatever is left in dir besides the link itself.
func staleNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		if e.Name() != "gropius" {
			left = append(left, e.Name())
		}
	}
	return left
}
