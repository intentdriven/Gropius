package lifecycle

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// The per-user command link, and the advice that goes with it.
//
// The verbs are only as reachable as this account's own bin directory is, which
// is not a given on every Mac — so the install says whether it is on the search
// path and, when it is not, names the line that would put it there. Nothing
// here elevates: the authorisation panel exists for the firewall, which is
// machine-wide state with no per-account equivalent, and a link has one.

// linkDirName is the per-user bin directory, relative to a home directory.
//
// ~/.local/bin rather than ~/bin or /usr/local/bin. /usr/local/bin is
// machine-wide and needs an administrator, which is the elevation this link
// exists to avoid; ~/bin is an older convention that macOS itself puts nothing
// in; ~/.local/bin is where per-user tools go today — uv, pipx and the Python
// installers this product already provisions all use it — so it is the
// directory most likely to be on the search path already.
var linkDirName = filepath.Join(".local", "bin")

// linkFileName is the command a person types.
const linkFileName = "gropius"

// binDir is this account's own bin directory.
func binDir(home string) string { return filepath.Join(home, linkDirName) }

// linkCommand points this account's `gropius` at the installed binary and
// returns where the link is.
//
// The link is made under a random name and renamed over the final one, so an
// existing link is REPLACED rather than followed: os.Symlink refuses a name
// that exists, and removing the name first would leave the account without the
// command for as long as the window lasts. rename(2) replaces a symlink without
// looking at what it points at.
func linkCommand(home, binary string) (string, error) {
	dir := binDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	link := filepath.Join(dir, linkFileName)

	tmp := filepath.Join(dir, "."+linkFileName+"-"+randomSuffix())
	if err := os.Symlink(binary, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return link, nil
}

// randomSuffix is an unguessable name fragment. A predictable one in a
// directory another process can watch is the same hazard the staging name has.
func randomSuffix() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Unreachable in practice; a name that is merely unique is still
		// better than a fixed one, and the rename below is what carries the
		// safety either way.
		return "tmp"
	}
	return hex.EncodeToString(b)
}

// onSearchPath reports whether dir is on the PATH string as this account would
// read it.
//
// Compared by what the entry RESOLVES to rather than by its spelling: a profile
// that puts a symlink to the same directory on the path, or spells it with a
// trailing slash, has nothing to fix, and telling that person to add a line
// they already have is worse than saying nothing. An empty entry means the
// working directory, which is not this one however the command is run.
func onSearchPath(dir, pathEnv string) bool {
	target, err := os.Stat(dir)
	for _, entry := range filepath.SplitList(pathEnv) {
		if entry == "" {
			continue
		}
		if filepath.Clean(entry) == filepath.Clean(dir) {
			return true
		}
		if err != nil {
			continue
		}
		if fi, statErr := os.Stat(entry); statErr == nil && os.SameFile(fi, target) {
			return true
		}
	}
	return false
}

// pathAdvice is what the install says when that directory is not on the search
// path: the line that would add it, spelled so it can be pasted into a profile.
//
// $HOME rather than the expanded path, so the line works in any account's
// profile and so nothing prints an account name into a terminal somebody is
// about to paste into a bug report.
func pathAdvice(dir, home string) string {
	spelled := dir
	if home != "" && strings.HasPrefix(dir, home+string(filepath.Separator)) {
		spelled = "$HOME" + dir[len(home):]
	}
	return "the gropius command is at " + spelled + ", which is not on this account's PATH.\n" +
		"Add this line to ~/.zshrc (or ~/.bash_profile) to reach it by name:\n" +
		"  export PATH=\"" + spelled + ":$PATH\""
}
