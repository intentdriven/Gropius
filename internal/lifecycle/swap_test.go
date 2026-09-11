package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The swap is the one part of the installer that cannot be written in shell,
// and these are the three cases the shipped script gets wrong
// (iss-2609081310071028). `mv` nests into a destination that already exists as
// a directory, follows one that is a symlink, and exits 0 in both cases; and
// the sequence it is written in deletes the installed bundle before the
// replacement has landed, so one failing rename leaves the Mac with no
// application at all.
//
// Every case below is a behavioural test in a temporary directory, because
// that is the only thing that can observe any of it: no scan of the source can
// tell a rename that refuses an existing directory from one that nests inside
// it.

// bundleAt writes a directory that stands in for a bundle, carrying one file
// whose contents name which bundle it is.
func bundleAt(t *testing.T, path, marker string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "Contents", "MacOS", "gropius"), []byte(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// markerAt reads back which bundle is at path.
func markerAt(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(path, "Contents", "MacOS", "gropius"))
	if err != nil {
		t.Fatalf("no bundle at %s: %v", path, err)
	}
	return string(b)
}

// The ordinary case: nothing at the destination yet.
func TestSwapInstallsIntoAnEmptyDestination(t *testing.T) {
	dir := t.TempDir()
	src := bundleAt(t, filepath.Join(dir, "verified", "Gropius.app"), "new")
	dest := filepath.Join(dir, "Applications", "Gropius.app")

	if err := placeBundle(src, dest, os.Rename); err != nil {
		t.Fatalf("placeBundle: %v", err)
	}
	if got := markerAt(t, dest); got != "new" {
		t.Errorf("the destination holds %q, want the new bundle", got)
	}
	assertNoStagingLeft(t, filepath.Dir(dest))
}

// A destination that already exists as a directory is REPLACED, not nested
// into. `mv` would leave the new bundle at Gropius.app/Gropius.app and exit 0,
// so an upgrade would report success while every launcher went on opening the
// old binary.
func TestSwapReplacesAnInstalledBundleRatherThanNestingInsideIt(t *testing.T) {
	dir := t.TempDir()
	src := bundleAt(t, filepath.Join(dir, "verified", "Gropius.app"), "new")
	dest := bundleAt(t, filepath.Join(dir, "Applications", "Gropius.app"), "old")

	if err := placeBundle(src, dest, os.Rename); err != nil {
		t.Fatalf("placeBundle: %v", err)
	}
	if got := markerAt(t, dest); got != "new" {
		t.Errorf("the destination holds %q, want the new bundle", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "Gropius.app")); err == nil {
		t.Error("the new bundle was nested inside the installed one, which is what `mv` does and what this replaces")
	}
	assertNoStagingLeft(t, filepath.Dir(dest))
}

// A destination that is a SYMLINK is replaced rather than followed. `mv`
// writes through the link, which leaves the link standing and installs the
// bundle wherever it points — a directory the account that planted the link
// chooses.
func TestSwapReplacesASymlinkRatherThanFollowingIt(t *testing.T) {
	dir := t.TempDir()
	src := bundleAt(t, filepath.Join(dir, "verified", "Gropius.app"), "new")
	elsewhere := bundleAt(t, filepath.Join(dir, "elsewhere", "Gropius.app"), "somebody else's")

	apps := filepath.Join(dir, "Applications")
	if err := os.MkdirAll(apps, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(apps, "Gropius.app")
	if err := os.Symlink(elsewhere, dest); err != nil {
		t.Fatal(err)
	}

	if err := placeBundle(src, dest, os.Rename); err != nil {
		t.Fatalf("placeBundle: %v", err)
	}
	fi, err := os.Lstat(dest)
	if err != nil {
		t.Fatalf("lstat destination: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("the destination is still a symlink, so the bundle was written through it")
	}
	if got := markerAt(t, dest); got != "new" {
		t.Errorf("the destination holds %q, want the new bundle", got)
	}
	if got := markerAt(t, elsewhere); got != "somebody else's" {
		t.Errorf("what the symlink pointed at now holds %q — the swap followed the link", got)
	}
	assertNoStagingLeft(t, apps)
}

// The failure that produced the defect: the rename that puts the new bundle in
// place fails. The Mac must still have an application, and it must be the one
// that was working.
func TestSwapLeavesTheInstalledBundleWhenTheSecondRenameFails(t *testing.T) {
	dir := t.TempDir()
	src := bundleAt(t, filepath.Join(dir, "verified", "Gropius.app"), "new")
	dest := bundleAt(t, filepath.Join(dir, "Applications", "Gropius.app"), "old")

	// The first rename sets the installed bundle aside; the second is the one
	// that fails, which is the ordinary failure — a full disk, a locked file, a
	// permission revoked between the two calls.
	boom := errors.New("no space left on device")
	calls := 0
	rename := func(oldpath, newpath string) error {
		calls++
		if calls == 2 {
			return boom
		}
		return os.Rename(oldpath, newpath)
	}

	err := placeBundle(src, dest, rename)
	if err == nil {
		t.Fatal("placeBundle reported success although the bundle never moved into place")
	}
	if !strings.Contains(err.Error(), boom.Error()) {
		t.Errorf("the failure %q does not carry the cause", err)
	}
	if got := markerAt(t, dest); got != "old" {
		t.Errorf("the destination holds %q; a failed swap must leave the installed bundle where it was", got)
	}
	assertNoStagingLeft(t, filepath.Dir(dest))
}

// The staging name is unguessable. A predictable name — the pid, the bundle's
// own name — hands anyone watching the destination directory a reliable signal
// for when to act on it, and on a Mac where /Applications is group-writable by
// the admin group that is another account.
func TestTheStagingNameIsUnguessable(t *testing.T) {
	dir := t.TempDir()

	first, err := stagingDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := stagingDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Errorf("two staging directories in one run share the name %q", first)
	}
	for _, name := range []string{first, second} {
		base := filepath.Base(name)
		if strings.Contains(base, strconv.Itoa(os.Getpid())) {
			t.Errorf("the staging name %q carries the process id, which anyone watching the directory can read", base)
		}
		fi, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("the staging directory is mode %o; nothing but this account has any business in it", perm)
		}
	}
}

// assertNoStagingLeft fails when a staging directory survived the swap. The
// half-copied bundle inside one is the size of the application, and a
// destination littered with them is what a failed upgrade would otherwise
// leave.
func assertNoStagingLeft(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), stagingPrefix) {
			t.Errorf("the swap left %s behind in %s", e.Name(), dir)
		}
	}
}
