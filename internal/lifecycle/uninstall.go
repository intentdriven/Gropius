package lifecycle

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"

	"github.com/intentdriven/Gropius/internal/config"
)

// `gropius uninstall` removes what this installation put on the Mac, and
// nothing else.
//
// EVERY DELETION PATH IS FIXED. Not one of them comes from a flag or from the
// environment: GROPIUS_ROOT is read only so the output can say which root was
// NOT removed. A data root a caller can name is a data root any local account
// can pre-create as a symlink, and it would then choose what is deleted.
//
// NOTHING ELEVATES AND NOTHING SHELLS OUT. Every removal is os.RemoveAll in
// this process, acting with this account's own rights: a file this account
// cannot delete is reported, never elevated for. The single exception is the
// firewall entry, which is machine-wide state with no per-account route, and it
// goes through the same one authorisation panel as the grant — a refusal leaves
// everything else removed and reports the entry as what remains.
//
// THE MODELS STAY. They are the expensive thing to fetch again, so removing
// them is a separate decision with its own flag; under a shared root they are
// not even this account's to remove, and only the files it owns can go.

// UninstallEnv is everything uninstall acts on, resolved before anything is
// removed so the plan can be read in one place.
type UninstallEnv struct {
	Paths config.Paths
	Home  string
	// Bundles are the fixed locations an installed bundle can be at: the
	// machine-wide applications directory and this account's own. Both are in
	// play and they are not symmetric — one bundle in the machine-wide
	// directory is what every account on the Mac launches.
	Bundles []string
	// Link is this account's own gropius command.
	Link string
	// Binary is the path the firewall entry is keyed to.
	Binary string
	// SharedRoot is the machine-wide data root when this installation uses one,
	// and empty otherwise. It is never removed.
	SharedRoot string
	// NamedRoot is what GROPIUS_ROOT names, so the output can say it was not
	// acted on. It is never a deletion path.
	NamedRoot string
	// Uid is this account, which under a shared root decides what may be
	// removed.
	Uid int
	// Terminal says whether standard input is a terminal, which is the only
	// thing that could answer a question — and nothing here reads it either
	// way.
	Terminal bool
	// Firewall removes the entry, raising the one authorisation panel.
	Firewall func(binary string) error
	// OwnerOf reads which account owns a path.
	OwnerOf func(path string) (int, error)
}

// RunUninstall is the uninstall verb.
func RunUninstall(env Env, args []string) int {
	ue, err := liveUninstallEnv(env)
	if err != nil {
		writeLine(env.Err, "gropius uninstall: "+err.Error())
		return ExitFailed
	}
	return runUninstall(env, args, ue)
}

// liveUninstallEnv resolves what a real run acts on, from the fixed locations
// and from nothing else.
func liveUninstallEnv(env Env) (UninstallEnv, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return UninstallEnv{}, err
	}
	root, err := config.InstalledRoot()
	if err != nil {
		return UninstallEnv{}, err
	}
	paths := config.NewPaths(root)
	shared := ""
	if config.IsSharedRoot(root) {
		shared = root
	}
	bundles := []string{
		filepath.Join(systemApplications, bundleName),
		filepath.Join(home, "Applications", bundleName),
	}
	binary := filepath.Join(bundles[1], binaryInBundle)
	for _, b := range bundles {
		if _, err := os.Lstat(b); err == nil {
			binary = filepath.Join(b, binaryInBundle)
			break
		}
	}
	return UninstallEnv{
		Paths:      paths,
		Home:       home,
		Bundles:    bundles,
		Link:       filepath.Join(binDir(home), linkFileName),
		Binary:     binary,
		SharedRoot: shared,
		NamedRoot:  os.Getenv("GROPIUS_ROOT"),
		Uid:        os.Getuid(),
		Terminal:   isTerminal(os.Stdin),
		Firewall:   revokeFirewall,
		OwnerOf:    ownerOf,
	}, nil
}

func runUninstall(env Env, args []string, ue UninstallEnv) int {
	fs := flags("uninstall", env.Err)
	purge := fs.Bool("purge", false, "remove the downloaded models as well")
	yes := fs.Bool("yes", false, "answer the confirmation --purge would otherwise need a terminal for")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		writeLine(env.Err, "gropius uninstall: unexpected argument "+quote(fs.Arg(0)))
		return ExitUsage
	}

	// The consent check comes first, and it deletes nothing before it. Nothing
	// reads standard input: under a piped bootstrap that is the rest of the
	// installer, so the refusal names the flag that would have answered it.
	if *purge && !ue.Terminal && !*yes {
		writeLine(env.Err, "gropius uninstall --purge deletes the downloaded models, and standard input is not a terminal, "+
			"so there is nobody to confirm it with. Nothing has been deleted.")
		writeLine(env.Err, "Pass --yes to confirm it without a terminal: gropius uninstall --purge --yes")
		return ExitUsage
	}

	failed := false
	var remaining []string

	// The firewall entry first, while the binary it is keyed to still exists.
	if err := ue.Firewall(ue.Binary); err != nil {
		remaining = append(remaining,
			"the firewall entry for "+abbreviate(ue.Binary, ue.Home)+" ("+err.Error()+")\n"+
				"    remove it with: "+firewallRemoveCommand(ue.Binary))
	} else {
		writeLine(env.Out, "removed the firewall entry.")
	}

	for _, target := range removalTargets(ue) {
		if _, err := os.Lstat(target); err != nil {
			continue
		}
		if err := os.RemoveAll(target); err != nil {
			// Never elevated for, and never handed to an external removal
			// command: a path this account cannot delete is reported as what it
			// is.
			remaining = append(remaining, abbreviate(target, ue.Home)+" ("+err.Error()+")")
			failed = true
			continue
		}
		writeLine(env.Out, "removed "+abbreviate(target, ue.Home)+".")
	}

	if ok := removeLink(ue); ok {
		writeLine(env.Out, "removed the "+linkFileName+" command.")
	}

	reportModels(env, ue, *purge, &failed)

	if len(remaining) > 0 {
		writeLine(env.Out, "")
		writeLine(env.Out, "What is left:")
		for _, r := range remaining {
			writeLine(env.Out, "  - "+r)
		}
	}
	if ue.NamedRoot != "" {
		writeLine(env.Out, "")
		writeLine(env.Out, "GROPIUS_ROOT names "+abbreviate(ue.NamedRoot, ue.Home)+
			". Uninstall never derives a deletion path from the environment, so nothing there was removed.")
	}
	if failed {
		return ExitFailed
	}
	return ExitOK
}

// removalTargets is what uninstall removes, in the order it removes it. Every
// entry is derived from the resolved layout; none of them is a path a caller
// named.
//
// The data root itself is NOT on the list even on a per-user install, because
// the models live inside it: what goes is each thing this installation put
// there, by name.
func removalTargets(ue UninstallEnv) []string {
	targets := append([]string{}, ue.Bundles...)
	return append(targets,
		ue.Paths.Venv,
		ue.Paths.Python,
		ue.Paths.Bin,
		ue.Paths.Config,
		ue.Paths.State,
		ue.Paths.Logs,
		ue.Paths.Stats,
	)
}

// removeLink removes this account's gropius command, and only when it is a
// symbolic link to a binary inside a Gropius bundle. A real file of that name
// belongs to whoever put it there.
func removeLink(ue UninstallEnv) bool {
	if ue.Link == "" {
		return false
	}
	fi, err := os.Lstat(ue.Link)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return false
	}
	target, err := os.Readlink(ue.Link)
	if err != nil || filepath.Base(filepath.Dir(target)) != "MacOS" {
		return false
	}
	return os.Remove(ue.Link) == nil
}

// reportModels says what happens to the downloaded models: what was left and
// how much of it, or — under --purge — what was removed and what could not be.
func reportModels(env Env, ue UninstallEnv, purge bool, failed *bool) {
	dirs := []string{ue.Paths.Models, ue.Paths.HFCache}
	shared := ue.SharedRoot != ""

	if !purge {
		size := 0
		for _, d := range dirs {
			size += dirSize(d)
		}
		writeLine(env.Out, "")
		writeLine(env.Out, "The downloaded models are still there: "+formatSize(size)+".")
		writeLine(env.Out, "Remove them too with: gropius uninstall --purge")
		if shared {
			reportSharedRoot(env, ue)
		}
		return
	}

	removed, kept := 0, 0
	otherAccounts := map[int]bool{}
	for _, d := range dirs {
		if !shared {
			// A per-user root holds this account's models and nothing else.
			size := dirSize(d)
			if err := os.RemoveAll(d); err != nil {
				writeLine(env.Err, "warning: "+abbreviate(d, ue.Home)+" could not be removed ("+err.Error()+")")
				*failed = true
				kept += size
				continue
			}
			removed += size
			continue
		}
		// Under a shared root only what this account owns may go, which the
		// sticky bit on 3775 makes the only removal the filesystem permits
		// anyway. Removing another account's models is a separate act, not the
		// unelevated half of this one.
		//
		// File by file rather than directory by directory: two accounts' models
		// sit under the same organisation directory, so a decision taken at the
		// top level would either spare this account's models or remove
		// somebody else's.
		gone, left, owners := purgeOwned(d, ue.Uid, ue.OwnerOf)
		removed += gone
		kept += left
		for uid := range owners {
			otherAccounts[uid] = true
		}
	}

	writeLine(env.Out, "")
	writeLine(env.Out, "removed "+formatSize(removed)+" of downloaded models.")
	if kept > 0 {
		writeLine(env.Out, "Left in place: "+formatSize(kept)+", which "+accountsPhrase(len(otherAccounts))+
			" and this account may not remove.")
	}
	if shared {
		reportSharedRoot(env, ue)
	}
}

// reportSharedRoot says what is in the shared root, whose it is — counted, not
// named — and the one deliberate command that removes it.
func reportSharedRoot(env Env, ue UninstallEnv) {
	size := dirSize(ue.SharedRoot)
	writeLine(env.Out, "")
	writeLine(env.Out, "This Mac uses a shared model cache at "+ue.SharedRoot+", which uninstall never touches.")
	writeLine(env.Out, "It holds "+formatSize(size)+", and "+accountsPhrase(otherAccountsUnder(ue))+".")
	writeLine(env.Out, "Removing it removes every account's models, and is one deliberate command:")
	writeLine(env.Out, "  sudo /bin/rm -rf "+ue.SharedRoot)
}

// otherAccountsUnder counts the accounts other than this one that own something
// under the shared root's models. Counted rather than named: which account it
// is, is that account's business, and this output is written to be pasted into
// a bug report.
func otherAccountsUnder(ue UninstallEnv) int {
	seen := map[int]bool{}
	for _, f := range filesUnder(ue.Paths.Models) {
		if uid, err := ue.OwnerOf(f); err == nil && uid != ue.Uid {
			seen[uid] = true
		}
	}
	return len(seen)
}

// purgeOwned removes the files under dir that uid owns, and reports how much
// went, how much stayed, and which other accounts the remainder belongs to.
//
// Directories left empty are removed afterwards; one that still holds another
// account's file stays, which is also what the filesystem would insist on under
// the shared root's sticky bit.
func purgeOwned(dir string, uid int, ownerOf func(string) (int, error)) (removed, kept int, others map[int]bool) {
	others = map[int]bool{}
	files := filesUnder(dir)
	mine, theirs := partitionByOwner(files, uid, ownerOf)

	for _, f := range mine {
		size := fileSize(f)
		if err := os.Remove(f); err != nil {
			kept += size
			continue
		}
		removed += size
	}
	for _, f := range theirs {
		kept += fileSize(f)
		if owner, err := ownerOf(f); err == nil && owner != uid {
			others[owner] = true
		}
	}
	pruneEmptyDirs(dir)
	return removed, kept, others
}

// filesUnder is every regular file under dir, deepest last, following no
// symbolic link.
func filesUnder(dir string) []string {
	var files []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		files = append(files, p)
		return nil
	})
	sort.Strings(files)
	return files
}

// pruneEmptyDirs removes the directories under dir that hold nothing, deepest
// first, and then dir itself. A directory that still holds another account's
// file stays, and a removal that is refused is left alone: nothing here
// elevates to finish a deletion.
func pruneEmptyDirs(dir string) {
	var dirs []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i])
	}
}

func fileSize(path string) int {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	return int(fi.Size())
}

// accountsPhrase renders a count of other accounts without naming any of them.
func accountsPhrase(n int) string {
	switch n {
	case 0:
		return "belongs to no other account on this Mac"
	case 1:
		return "belongs to 1 other account on this Mac"
	default:
		return "belongs to " + strconv.Itoa(n) + " other accounts on this Mac"
	}
}

// partitionByOwner splits paths into the ones uid owns and the ones it does
// not. An owner that cannot be read counts as somebody else's: an unreadable
// owner is not evidence that a path is ours to delete.
func partitionByOwner(paths []string, uid int, ownerOf func(string) (int, error)) (mine, others []string) {
	for _, p := range paths {
		owner, err := ownerOf(p)
		if err != nil || owner != uid {
			others = append(others, p)
			continue
		}
		mine = append(mine, p)
	}
	return mine, others
}

// ownerOf reads which account owns a path, following no symbolic link.
func ownerOf(path string) (int, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, os.ErrInvalid
	}
	return int(st.Uid), nil
}

// dirSize is how much a path holds, following no symbolic link and tolerating
// what it cannot read — a size is a thing to report, never a thing to fail on.
func dirSize(path string) int {
	total := 0
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += int(info.Size())
		}
		return nil
	})
	return total
}

// formatSize renders a byte count for a person, in the units the rest of the
// product uses.
func formatSize(n int) string {
	const unit = 1024
	if n < unit {
		return strconv.Itoa(n) + " B"
	}
	div, exp := int64(unit), 0
	for m := int64(n) / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	value := float64(n) / float64(div)
	return strconv.FormatFloat(value, 'f', 1, 64) + " " + []string{"KB", "MB", "GB", "TB"}[exp]
}
