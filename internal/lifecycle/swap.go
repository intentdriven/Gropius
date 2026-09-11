package lifecycle

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// The staged swap, in Go, because the shell cannot express it.
//
// The shipped script removes the installed bundle and only then renames the
// staged one into its place. A destination that exists as a directory ends up
// with the staged bundle nested inside it; a destination that is a symlink is
// written through and left standing; and both exit 0, so an upgrade reports
// success while every launcher goes on opening the old binary
// (iss-2609081310071028). `mv` has no dependable "fail if the destination
// exists" mode, and any test-then-move in shell is a race by construction.
//
// os.Rename is rename(2): it replaces a symlink rather than following it, and
// it refuses a destination that is a non-empty directory. The order below is
// what makes the failure safe — the installed bundle is renamed ASIDE, the new
// one is renamed in, and the set-aside copy is removed only once the new one is
// in place.
//
// So no failure path leaves the Mac with no application, and that sentence is
// only true because of the last rule below: where the new bundle did not go in
// AND the set-aside one could not be put back, the staging directory holding it
// is NOT cleaned up, and the failure says where the only remaining copy is. An
// earlier version removed it and destroyed the installation on a path it
// claimed to protect — rename(2) refuses a non-empty directory, so anything
// that creates one at the destination between the two renames is enough to
// reach it.

// stagingPrefix names a staging directory. It is a dot name so it does not
// appear in a Finder listing of the destination while the swap runs.
const stagingPrefix = ".gropius-incoming-"

// renameFunc is os.Rename, handed in so a test can fail one call of it. The
// failure that produced the defect is an ordinary one — a full disk, a locked
// file, a permission revoked between two calls — and it cannot be provoked on a
// real filesystem on demand.
type renameFunc func(oldpath, newpath string) error

// stagingDir creates a staging directory inside dir, under an unguessable name.
//
// os.MkdirTemp draws the suffix from the same source as any other random name
// in this repository and creates the directory 0700. The name matters: the
// shipped script used the process id before this, and on a Mac where the
// destination is group-writable by the admin group — /Applications is, on stock
// macOS — a predictable name hands another account a reliable signal for when
// to act on the directory.
func stagingDir(dir string) (string, error) { return os.MkdirTemp(dir, stagingPrefix) }

// PlaceBundle installs the bundle at src as dest, replacing whatever is there.
func PlaceBundle(src, dest string) error { return placeBundle(src, dest, os.Rename) }

// placeBundle is PlaceBundle with the rename handed in.
func placeBundle(src, dest string, rename renameFunc) error {
	destDir := filepath.Dir(dest)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("prepare %s: %w", destDir, err)
	}

	// Stage INSIDE the destination directory, so the rename that puts the
	// bundle in place is within one filesystem and cannot fail part way
	// through. A copy is what can run out of space, and it runs first.
	staging, err := stagingDir(destDir)
	if err != nil {
		return fmt.Errorf("stage the new bundle in %s: %w", destDir, err)
	}
	// Cleared only where the staging directory may safely go: while it holds
	// the only copy of the application, it stays.
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(staging)
		}
	}()

	staged := filepath.Join(staging, filepath.Base(dest))
	if err := copyTree(src, staged); err != nil {
		return fmt.Errorf("copy the verified bundle into %s: %w", destDir, err)
	}

	// Set the installed bundle aside rather than deleting it. Lstat, not Stat:
	// a destination that is a symlink to a directory that no longer exists is
	// still a name that has to be got out of the way, and Stat would report it
	// absent and leave the rename below to fail.
	retired := ""
	if _, err := os.Lstat(dest); err == nil {
		retired = filepath.Join(staging, filepath.Base(dest)+".retired")
		if err := rename(dest, retired); err != nil {
			return fmt.Errorf("set the installed bundle aside: %w (it is untouched)", err)
		}
	}

	// Nothing may be at the destination now. Something that is, is a name
	// another process created in the window since the rename above, and it is
	// not this installer's to replace — with the new bundle OR with the old
	// one.
	//
	// So the set-aside copy is NOT put back here, which is the difference
	// between refusing a clobber and performing it with a different bundle: a
	// restore is a rename onto that same name, and whether it lands is the
	// filesystem's business rather than this file's (macOS answers EEXIST for
	// an empty directory and ENOTDIR for a symbolic link; POSIX permits
	// replacing an empty directory, and Linux does). The installed bundle stays
	// where it was set aside, the staging directory that holds it is kept, and
	// the failure names both it and what appeared, because a person now has two
	// things to look at and one of them is their application.
	if fi, err := os.Lstat(dest); err == nil {
		keep = retired != ""
		return keptWhen(keep, retired, fmt.Errorf(
			"%s was created by something else while the new bundle was being staged (%s); "+
				"refusing to replace it%s", dest, describe(fi), retiredNote(retired, dest)))
	}

	if err := rename(staged, dest); err != nil {
		put, note := restore(rename, retired, dest)
		keep = !put && retired != ""
		return keptWhen(keep, retired, fmt.Errorf("move the new bundle into place: %w%s", err, note))
	}

	// And only now is the set-aside copy removed, by the deferred RemoveAll of
	// the staging directory it sits in.
	return nil
}

// describe says what kind of thing took a name, for a message a person has to
// act on: a symbolic link, a directory and a file each mean something different
// about who put it there.
func describe(fi os.FileInfo) string {
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return "a symbolic link"
	case fi.IsDir():
		return "a directory"
	default:
		return "a file"
	}
}

// retiredNote says where the installed bundle is when it has been set aside and
// is not going back on its own.
func retiredNote(retired, dest string) string {
	if retired == "" {
		return " (nothing was installed there before, and nothing was removed)"
	}
	return ". The installed bundle was already set aside and is intact at " + retired +
		"; move it back to " + dest + " once you have dealt with what is there."
}

// restore puts a set-aside bundle back, so a failure is a no-op rather than an
// uninstall. It reports whether the bundle is back, and the sentence the caller
// should append to the failure it is already returning.
//
// A restore that fails is the one case where a person has to do something: the
// application is not where it belongs, and the only copy of it is sitting under
// a name they would never think to look for. So the failure names that path,
// and the caller keeps the directory it is in.
func restore(rename renameFunc, retired, dest string) (put bool, note string) {
	if retired == "" {
		return true, " (nothing was installed there before, and nothing was removed)"
	}
	if err := rename(retired, dest); err != nil {
		return false, " — and the installed bundle could NOT be put back (" + err.Error() + "). " +
			"It is intact at " + retired + "; move it to " + dest + " to restore the application."
	}
	return true, " (the previous copy is left as it was)"
}

// copyTree copies a directory recursively: regular files, directories and
// symbolic links, with their permission bits.
//
// In-process rather than through /usr/bin/ditto, because a copy that is one
// function is a copy whose failure modes are visible in this file. What it does
// not carry is extended attributes, which is correct here: the bootstrap clears
// the quarantine attribute on the verified bundle before handing over, and an
// ad-hoc signature lives inside the Mach-O and in _CodeSignature, both ordinary
// files.
func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case info.IsDir():
		if err := os.Mkdir(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	case info.Mode().IsRegular():
		return copyFile(src, dst, info.Mode().Perm())
	default:
		// A device node, a socket or a fifo inside an application bundle is
		// not something to reproduce silently.
		return fmt.Errorf("%s is not a file, a directory or a symbolic link", src)
	}
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// keptStagingError is the ONE swap failure a person has to act on: the
// application is not where it belongs, and the only copy of it is sitting under
// a name nobody would think to look for.
//
// It carries that path as a VALUE rather than only inside its sentence, and
// that is the whole reason it exists. The first version of the update report
// searched the failure text for the staging name and printed from there on —
// which drops the directory the staging name sits in, so the report named a
// relative path nobody could go to, and fired on the failures where the placer
// had already REMOVED the staging directory. A caller that has to scrape a path
// out of prose gets it wrong; this is the placer stating it.
type keptStagingError struct {
	// Path is the set-aside bundle: the only remaining copy of the application.
	Path string
	err  error
}

func (e *keptStagingError) Error() string { return e.err.Error() }
func (e *keptStagingError) Unwrap() error { return e.err }

// keptWhen wraps a failure as one that kept the only copy, and leaves every
// other failure exactly as it was: the failures that keep nothing must not say
// they kept something.
func keptWhen(keep bool, retired string, err error) error {
	if !keep || retired == "" {
		return err
	}
	return &keptStagingError{Path: retired, err: err}
}
