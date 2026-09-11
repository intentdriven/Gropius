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

// The failure the file's own claim rests on: the new bundle does not go in AND
// the set-aside bundle cannot be put back. The set-aside copy is then the only
// one there is, and it must survive — with the failure naming where it is.
//
// Reachable without anything exotic: rename(2) refuses a non-empty directory,
// so anything that creates one at the destination between the two renames makes
// the restore fail with ENOTEMPTY. Removing the staging directory on that path
// would delete the last copy of the application.
func TestSwapKeepsTheSetAsideBundleWhenItCannotBePutBack(t *testing.T) {
	dir := t.TempDir()
	src := bundleAt(t, filepath.Join(dir, "verified", "Gropius.app"), "new")
	dest := bundleAt(t, filepath.Join(dir, "Applications", "Gropius.app"), "old")

	// Call 1 sets the installed bundle aside. Call 2 puts the new one in
	// place, and fails. Call 3 is the restore, and fails too.
	calls := 0
	rename := func(oldpath, newpath string) error {
		calls++
		if calls >= 2 {
			return errors.New("no space left on device")
		}
		return os.Rename(oldpath, newpath)
	}

	err := placeBundle(src, dest, rename)
	if err == nil {
		t.Fatal("placeBundle reported success although nothing moved into place")
	}

	// The set-aside bundle is the only copy left, so it must still exist and
	// the message must say where.
	retired := findRetired(t, filepath.Dir(dest))
	if retired == "" {
		t.Fatal("the swap deleted the set-aside bundle after failing to put it back — the Mac is left with no application")
	}
	if got := markerAt(t, retired); got != "old" {
		t.Errorf("what was set aside holds %q, want the installed bundle", got)
	}
	if !strings.Contains(err.Error(), retired) {
		t.Errorf("the failure %q does not say where the only remaining copy of the application is", err)
	}

	// And it says it as a VALUE, not only inside a sentence. A caller that has
	// to scrape a path out of prose gets it wrong — and a caller did: the first
	// version of the update report searched the message for the staging name
	// and printed what it found from that point on, which drops the directory
	// the staging name sits in and leaves a relative path a person cannot go
	// to.
	var kept *keptStagingError
	if !errors.As(err, &kept) {
		t.Fatalf("the failure does not say IN A VALUE that the staging directory was kept: %v", err)
	}
	if kept.Path != retired {
		t.Errorf("keptStagingError.Path = %q, want the absolute path of the only remaining copy (%q)", kept.Path, retired)
	}
}

// The failures that keep NOTHING must not say they kept something: the placer
// removes the staging directory on those paths, and a caller that treated any
// swap failure as a kept one would send a person to a directory that is gone.
func TestOnlyTheSwapThatKeepsTheOnlyCopySaysSo(t *testing.T) {
	dir := t.TempDir()
	src := bundleAt(t, filepath.Join(dir, "verified", "Gropius.app"), "new")
	dest := bundleAt(t, filepath.Join(dir, "Applications", "Gropius.app"), "old")

	// Call 1 sets the installed bundle aside, call 2 fails to move the new one
	// in, and call 3 — the restore — succeeds. The installed bundle is back at
	// the destination and the staging directory goes.
	calls := 0
	rename := func(oldpath, newpath string) error {
		calls++
		if calls == 2 {
			return errors.New("no space left on device")
		}
		return os.Rename(oldpath, newpath)
	}

	err := placeBundle(src, dest, rename)
	if err == nil {
		t.Fatal("placeBundle reported success although nothing moved into place")
	}
	var kept *keptStagingError
	if errors.As(err, &kept) {
		t.Errorf("a swap that put the installed bundle back reports a kept staging directory at %q", kept.Path)
	}
	if got := markerAt(t, dest); got != "old" {
		t.Errorf("the destination holds %q, want the installed bundle put back", got)
	}
}

// findRetired returns the set-aside bundle left inside a staging directory, or
// an empty string when there is none.
func findRetired(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), stagingPrefix) {
			continue
		}
		inner, err := os.ReadDir(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for _, f := range inner {
			if strings.HasSuffix(f.Name(), ".retired") {
				return filepath.Join(dir, e.Name(), f.Name())
			}
		}
	}
	return ""
}

// The one race this file exists to refuse must not be ATTEMPTED with the old
// bundle instead.
//
// If something appears at the destination between the Lstat that found it empty
// and the one that checks again, the swap refuses — but it used to refuse by
// calling restore, which renames the retired bundle onto whatever appeared.
// The message then said "refusing to replace it" about a call that had just
// tried to replace it.
//
// WHAT SAVED IT, MEASURED RATHER THAN ASSUMED, and why it is still wrong.
// Renaming a DIRECTORY onto a non-directory is ENOTDIR, and onto an existing
// empty directory macOS answers EEXIST (verified on APFS: "file exists" for an
// empty directory, "not a directory" for a symbolic link). The retired bundle
// is always a directory, so on this platform the attempt fails and the old
// bundle stays put. The behaviour is therefore correct today by the
// filesystem's grace and not by anything this file does — POSIX permits
// replacing an empty directory, and Linux does — which is exactly the kind of
// invariant that stops being true somewhere else.
//
// So what is asserted here is the property that does not depend on any of that:
// no rename is issued ONTO the destination once something has appeared there.
// Nothing that appeared is touched, the set-aside copy stays where it is, and
// the failure names both paths, because a person now has two things to look at.
func TestSwapRefusesToClobberWhatAppearedAtTheDestination(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reappear func(t *testing.T, dest string)
		check    func(t *testing.T, dest string)
	}{
		{
			name: "an empty directory",
			reappear: func(t *testing.T, dest string) {
				if err := os.Mkdir(dest, 0o755); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, dest string) {
				entries, err := os.ReadDir(dest)
				if err != nil {
					t.Fatalf("what appeared at the destination is gone: %v", err)
				}
				if len(entries) != 0 {
					t.Errorf("what appeared at the destination now holds %d entries; it was empty", len(entries))
				}
			},
		},
		{
			name: "a symbolic link",
			reappear: func(t *testing.T, dest string) {
				if err := os.Symlink(filepath.Join(t.TempDir(), "somewhere"), dest); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, dest string) {
				fi, err := os.Lstat(dest)
				if err != nil {
					t.Fatalf("what appeared at the destination is gone: %v", err)
				}
				if fi.Mode()&os.ModeSymlink == 0 {
					t.Error("the symbolic link that appeared was replaced; rename(2) does that silently, which is " +
						"the whole reason this branch exists")
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := bundleAt(t, filepath.Join(dir, "verified", "Gropius.app"), "new")
			dest := bundleAt(t, filepath.Join(dir, "Applications", "Gropius.app"), "old")

			// The window: something takes the destination's name in the instant
			// after the installed bundle is renamed aside. Every rename is
			// recorded, because the assertion is about a call that must not be
			// made rather than about what the filesystem did with it.
			first := true
			var onto []string
			rename := func(oldpath, newpath string) error {
				if !first {
					onto = append(onto, newpath)
				}
				if err := os.Rename(oldpath, newpath); err != nil {
					return err
				}
				if first {
					first = false
					tc.reappear(t, dest)
				}
				return nil
			}

			err := placeBundle(src, dest, rename)
			for _, target := range onto {
				if target == dest {
					t.Errorf("the swap issued a rename onto %s after something had appeared there; whether that "+
						"call succeeds is the filesystem's business, and on another one it replaces what it found",
						dest)
				}
			}
			if err == nil {
				t.Fatal("placeBundle reported success although it refused the destination")
			}
			tc.check(t, dest)

			retired := findRetired(t, filepath.Dir(dest))
			if retired == "" {
				t.Fatal("the set-aside bundle is gone; it is the only copy of the application there is")
			}
			if got := markerAt(t, retired); got != "old" {
				t.Errorf("what was set aside holds %q, want the installed bundle", got)
			}
			if !strings.Contains(err.Error(), retired) {
				t.Errorf("the failure %q does not say where the installed bundle is", err)
			}
			if !strings.Contains(err.Error(), dest) {
				t.Errorf("the failure %q does not name the destination it refused", err)
			}
		})
	}
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
