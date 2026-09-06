package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// MaxConfigBytes caps how much of config.json Load reads. A real config is a
// few hundred bytes; anything approaching this is broken or hostile.
const MaxConfigBytes = 1 << 20

// OpenRegular opens path for reading and refuses anything but a regular file.
//
// Every state file Gropius keeps in its data root — config.json, registry.json,
// the PID ledger, the instance token — is created lazily, and in shared-cache
// mode that root is group-writable (/Users/Shared/Gropius, mode 3775): another
// local account can plant a FIFO or a symlink under any of those names before
// the first write, and the sticky bit then stops this account from ever
// removing it. A plain os.Open would block forever on the FIFO (wedging startup
// with no error and no way to recover from the app) or follow the symlink and
// read an arbitrary file with this account's privileges. O_NONBLOCK makes the
// open itself unblockable, O_NOFOLLOW refuses symlinks outright, and the fstat
// on the opened handle (not the path, so a swap between check and open cannot
// be raced in) refuses anything but a regular file before any read. A missing
// file still surfaces as fs.ErrNotExist, so "absent means defaults" branches
// keep working. The caller owns the returned file.
func OpenRegular(path string) (*os.File, os.FileInfo, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	return f, info, nil
}

// ReadRegular reads at most max bytes from the regular file at path, refusing
// (rather than truncating) a larger one: os.ReadFile has no cap, and a symlink
// to an endless device never reaches EOF. See OpenRegular for why the open is
// hardened.
func ReadRegular(path string, max int64) ([]byte, error) {
	f, _, err := OpenRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s exceeds %d bytes", filepath.Base(path), max)
	}
	return b, nil
}
