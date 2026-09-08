package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPrivilegedAppleScriptHasNoInlineExpansion catches the obvious spelling of
// an interpolated root command: a `$` on the same shell-script line as
// `administrator privileges`.
//
// `do shell script … with administrator privileges` hands a string to root.
// Building that string with a shell expansion puts whatever the expansion
// resolves to into a root command line, and install.sh is a script users are
// told to pipe straight into bash — so the interpolated value need not be
// exotic to matter. The installer's own destination is derived from $HOME.
//
// The safe construction is the one install.sh uses: every `-e` argument is a
// single-quoted literal that bash cannot expand into, the varying path travels
// as an `osascript … -- "$PATH"` argument, and AppleScript's `quoted form of`
// escapes it for the root shell.
//
// What this does NOT do, stated so nobody reads more into a green run than is
// there: it is a line-local text scan, so it sees nothing of a variable
// assembled on a previous line, a here-doc, a value read from a file, or a
// command built in Go and handed to osascript from outside a shell script. It
// is a proxy for the safe shape being written the obvious way, not a proof that
// no interpolation reaches root. The stronger property is not mechanically
// checkable here, and a guard that claimed it would have to be walked back.
//
// Like the braced-expansion rule next door, this is invisible to `bash -n` and
// to every unit test, because nothing here executes the script.
func TestPrivilegedAppleScriptHasNoInlineExpansion(t *testing.T) {
	forEachShellScriptLine(t, func(path string, lineNo int, line string) {
		if !strings.Contains(line, "administrator privileges") {
			return
		}
		if strings.Contains(line, "$") {
			t.Errorf("%s:%d: shell expansion on a line that runs a command as root — "+
				"pass the value as an osascript argument and escape it with `quoted form of` instead: %s",
				path, lineNo, strings.TrimSpace(line))
		}
	})
}

// TestOsascriptIsInvokedByAbsolutePath refuses a bare `osascript` in any shell
// script.
//
// osascript is how these scripts raise the macOS authentication panel, so the
// binary that resolves under that name decides what the user types their
// administrator password into. `curl | bash` runs with the invoking user's
// PATH, and a normal developer PATH puts several user-writable directories
// ahead of /usr/bin — `~/.local/bin` among them, which needs no privileges to
// write. Unprivileged code running as the user can drop an `osascript` shim
// there and this script would hand it the elevation, whereupon it draws its own
// panel and harvests the password.
//
// Pinning the interpreter is what makes the argument escaping worth anything:
// `quoted form of` protects the argument and cannot protect against an
// attacker-supplied interpreter. The rule is deliberately wider than the
// privileged call sites, because a script that raises any panel at all teaches
// the user that panels are legitimate here.
//
// Scope, so a green run is not over-read: this covers shell scripts only. Go
// code invokes no osascript today, and if that changes — a lifecycle subcommand
// shelling out to raise the same panel — this guard sees none of it, and the
// rule has to be re-armed wherever that call lands. Nor does it speak for the
// other PATH-resolved commands in the install path; those are their own record.
func TestOsascriptIsInvokedByAbsolutePath(t *testing.T) {
	forEachShellScriptLine(t, func(path string, lineNo int, line string) {
		code, _, _ := strings.Cut(line, "#")
		for _, field := range strings.FieldsFunc(code, func(r rune) bool {
			return r == ' ' || r == '\t' || r == ';' || r == '|' || r == '&' || r == '(' || r == '$'
		}) {
			if field == "osascript" {
				t.Errorf("%s:%d: bare `osascript` resolves through the caller's PATH, "+
					"which can put a user-writable directory ahead of /usr/bin — "+
					"invoke it as /usr/bin/osascript: %s", path, lineNo, strings.TrimSpace(line))
				return
			}
		}
	})
}

// TestInstallerPinsTheCommandsItTrusts refuses a PATH-resolved invocation of the
// commands install.sh depends on for integrity or for placing the bundle.
//
// `curl | bash` runs with the invoking user's PATH, and a normal developer PATH
// puts user-writable directories ahead of /usr/bin. The sharp one is shasum: it
// is the only integrity control in the whole install path, so a shim there
// defeats the verification silently. The others place, unpack and inspect the
// bundle, and a shim in any of them subverts what lands on disk.
//
// The ordinary case matters as much as the hostile one. A Homebrew coreutils or
// another implementation earlier on PATH need not accept the same flags —
// `--ignore-missing` is not universal — and a checksum check that quietly stops
// checking is worse than no check at all, because it still prints reassurance.
//
// Scoped to install.sh: it is the script that runs on a machine whose PATH
// nobody here controls. `open` is deliberately absent, being a launch
// convenience with no integrity role.
func TestInstallerPinsTheCommandsItTrusts(t *testing.T) {
	path := filepath.Join(repoRootDir(t), "install.sh")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}

	pinned := []string{"shasum", "ditto", "xattr", "mktemp", "pgrep"}
	for i, line := range strings.Split(string(b), "\n") {
		code, _, _ := strings.Cut(line, "#")
		for _, field := range strings.FieldsFunc(code, func(r rune) bool {
			return r == ' ' || r == '\t' || r == ';' || r == '|' || r == '&' ||
				r == '(' || r == ')' || r == '$' || r == '"'
		}) {
			for _, name := range pinned {
				if field == name {
					t.Errorf("install.sh:%d: %q resolves through the caller's PATH — "+
						"invoke it as /usr/bin/%s: %s", i+1, name, name, strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestInstallerElevatesThroughTheAuthenticationPanel refuses `sudo` as the
// installer's route to administrator rights.
//
// sudo can only ever accept the invoking user's own password, and a standard
// (non-administrator) account is not in the sudoers set at all. On exactly the
// accounts that most often run the installer — a shared Mac, where the account
// wanting the app is the one without admin rights — a sudo prompt is
// unsatisfiable, and no password the person can type will do. The macOS
// authentication panel takes an administrator's name AND password, so someone
// else's credentials can be entered.
//
// `sudo` inside an echoed fallback message is fine: that text is instructions
// for an administrator to run by hand, not the installer elevating itself.
func TestInstallerElevatesThroughTheAuthenticationPanel(t *testing.T) {
	path := filepath.Join(repoRootDir(t), "install.sh")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	src := string(b)

	for i, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "echo ") {
			continue
		}
		if strings.Contains(trimmed, "sudo ") {
			t.Errorf("install.sh:%d: installer elevates with sudo, which a standard "+
				"account cannot satisfy — raise the authentication panel instead: %s", i+1, trimmed)
		}
	}

	if !strings.Contains(src, "with administrator privileges") {
		t.Error("install.sh no longer raises the macOS authentication panel at all")
	}
}

// TestInstallerAuthorizesBeforeItWrites requires the administrator gate to come
// before the installer downloads or writes anything.
//
// The gate exists so that an account which cannot produce administrator
// credentials is turned away having lost nothing. Ordered after the download it
// still reports the right error, but only once a large archive has been fetched
// and, worse, once the app has been placed — which is the state the old
// end-of-run prompt left behind: installed, and quietly unable to serve the LAN.
func TestInstallerAuthorizesBeforeItWrites(t *testing.T) {
	path := filepath.Join(repoRootDir(t), "install.sh")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	src := string(b)

	gate := strings.Index(src, "\tadmin_authorize ||")
	if gate < 0 {
		t.Fatal("install.sh has no admin_authorize gate")
	}
	for _, write := range []string{"mktemp -d", "fetch \"$ASSET\"", "ditto -x -k", "cp -R"} {
		at := strings.Index(src, write)
		if at < 0 {
			t.Errorf("install.sh no longer contains %q — update this test", write)
			continue
		}
		if at < gate {
			t.Errorf("install.sh runs %q before the admin_authorize gate; a declined "+
				"authorization must cost the user nothing", write)
		}
	}
}

// forEachShellScriptLine walks every shell script in the repository.
func forEachShellScriptLine(t *testing.T, fn func(path string, lineNo int, line string)) {
	t.Helper()
	root := repoRootDir(t)

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// A git worktree checked out under a dot-directory holds a whole
			// second copy of the tree; scanning it would fail this test for
			// somebody else's branch. Excluding every dot-directory rather than
			// naming .claude is what prompt_content_test.go and
			// statistics_switch_test.go already do, and it is the version that
			// keeps working when the next tool picks a different directory.
			// Measured here: 12 shell scripts are visible without it, 8 of them
			// inside .claude/worktrees/, against 4 the repository tracks.
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			switch d.Name() {
			case "node_modules", "dist", "bin", "site":
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// isShellScript wants the source too: a hook or an extensionless script
		// is recognised by its shebang rather than by its name.
		if !isShellScript(path, string(b)) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for i, line := range strings.Split(string(b), "\n") {
			fn(rel, i+1, line)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
