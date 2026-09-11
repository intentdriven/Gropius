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
// in place — so no failure path leaves the Mac with no application.

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
	defer os.RemoveAll(staging)

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
	// another process created in the window since the rename above, and
	// renaming over it would either fail obscurely or — for an empty directory,
	// which rename(2) does replace — succeed silently on somebody else's
	// behalf.
	if _, err := os.Lstat(dest); err == nil {
		restore(rename, retired, dest)
		return fmt.Errorf("%s was created while the new bundle was being staged; refusing to replace it", dest)
	}

	if err := rename(staged, dest); err != nil {
		restore(rename, retired, dest)
		return fmt.Errorf("move the new bundle into place: %w (the previous copy is left as it was)", err)
	}

	// And only now is the set-aside copy removed, by the deferred RemoveAll of
	// the staging directory it sits in.
	return nil
}

// restore puts a set-aside bundle back, so a failure is a no-op rather than an
// uninstall. A failure to restore is not reported over the failure that caused
// it: the caller is already returning the cause, and there is nothing a second
// error would let them do differently.
func restore(rename renameFunc, retired, dest string) {
	if retired == "" {
		return
	}
	_ = rename(retired, dest)
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
