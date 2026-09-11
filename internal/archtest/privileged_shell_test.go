package archtest_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

// TestInstallerPinsEveryCommandItRuns refuses ANY command install.sh runs that
// is not written as an absolute path.
//
// It replaces a five-name allow-list — shasum, ditto, xattr, mktemp, pgrep —
// which pinned the commands somebody had thought about and left nine others
// (mv, open, mkdir, curl, gh, sleep, seq, sw_vers, sysctl) resolving through
// the caller's PATH. That list is the defect: an allow-list of the sharp ones
// asks every future edit to notice that its new command is sharp, and
// iss-2609081310119313 is what happens when one does not.
//
// WHY EVERY COMMAND AND NOT ONLY THE DANGEROUS ONES. Two records say so. On a
// shared Mac a group-writable directory ahead of /usr/bin is an
// account-to-account boundary (iss-2609081435387952) — and `curl | bash` runs
// with the invoking user's PATH, which routinely puts user-writable
// directories first. And with no attacker at all, a release step once resolved
// a name to a tool that was not the tool meant, invisibly, because the failure
// was swallowed (iss-9). Pinning is a defence against ambiguity as much as
// against a planted binary.
//
// WHAT IS EXEMPT, and why each exemption is safe: the shell's own builtins and
// keywords, which are not programs PATH resolves at all, and the script's own
// functions, which are defined in the file being read.
//
// WHAT THIS CANNOT DO, so a green run is not over-read. It is a line scan with
// a shell's grammar approximated, not parsed: it reads the first word of each
// command position after stripping comments and here-document bodies. A
// command reached through a variable — the bootstrap's handover to
// "$verified_bin" — is deliberately allowed, because that path is one the
// script computed rather than a name handed to PATH, and the Go-side scan makes
// the same allowance for the same reason. A name assembled at runtime, or run
// through eval, passes this scan.
func TestInstallerPinsEveryCommandItRuns(t *testing.T) {
	root := repoRootDir(t)
	src := readRepoFile(t, root, "install.sh")
	local := shellFunctionNames(src)

	delimiters := heredocDelimiters(src)
	for i, line := range joinContinuations(strings.Split(stripHeredocs(src), "\n")) {
		code, _, _ := strings.Cut(line, "#")
		if isCasePatternLine(code) || delimiters[strings.TrimSpace(code)] {
			continue
		}
		for _, word := range commandPositions(code) {
			if strings.HasPrefix(word, "/") {
				continue
			}
			if shellBuiltins[word] || shellKeywords[word] {
				continue
			}
			if local[word] {
				continue
			}
			t.Errorf("install.sh:%d runs %q, which resolves through the caller's PATH — "+
				"name it by absolute path (or, if it is the script's own function, define it above): %s",
				i+1, word, strings.TrimSpace(line))
		}
	}
}

// shellBuiltins are the words a command position may hold without a path: the
// shell's own builtins and keywords. They are exempt because they are not
// programs at all — no PATH lookup happens, and there is nothing to plant.
//
// Recorded as a list rather than inferred, so adding to it is a line in a diff
// somebody reviews. `test` and `[` are here as builtins of bash, which is the
// interpreter this script declares.
// shellKeywords introduce a command rather than being one: what follows them on
// the same line is still a command position.
var shellKeywords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"for": true, "while": true, "until": true, "do": true, "done": true,
	"case": true, "esac": true, "in": true, "function": true, "select": true,
	"!": true, "time": true, "{": true, "}": true,
}

var shellBuiltins = map[string]bool{
	":": true, ".": true, "alias": true, "bg": true, "bind": true, "break": true,
	"builtin": true, "cd": true, "command": true, "continue": true, "declare": true,
	"echo": true, "enable": true, "eval": true, "exec": true, "exit": true,
	"export": true, "false": true, "fg": true, "getopts": true, "hash": true,
	"help": true, "jobs": true, "kill": true, "let": true, "local": true,
	"logout": true, "mapfile": true, "printf": true, "pwd": true, "read": true,
	"readarray": true, "readonly": true, "return": true, "set": true, "shift": true,
	"shopt": true, "source": true, "test": true, "[": true, "times": true,
	"trap": true, "true": true, "type": true, "typeset": true, "ulimit": true,
	"umask": true, "unalias": true, "unset": true, "wait": true,
}

// shellFunctionNames is every function the script defines, which is what makes
// a call to one of them not a PATH lookup.
func shellFunctionNames(src string) map[string]bool {
	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(\)\s*\{`).FindAllStringSubmatch(src, -1) {
		names[m[1]] = true
	}
	return names
}

// commandPositions returns the first word of every command position on a line,
// with the shell's quoting honoured: a word inside quotes is text, an expansion
// is a value, and a command substitution is a command position of its own.
//
// It is an approximation of a grammar, not a parse. What it is careful about is
// the cases this script actually contains — quoted prose full of words like
// `curl`, `${VAR:-default}`, `2>/dev/null`, `$(…)` inside a double-quoted
// assignment — because a scan that reported those would be turned off rather
// than obeyed.
func commandPositions(line string) []string {
	var out []string
	atCommand := true
	skipWord := false
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '\\' && i+1 < len(line):
			i += 2
		case c == '\'':
			// Single quotes: text, whatever is inside.
			if j := strings.IndexByte(line[i+1:], '\''); j >= 0 {
				i += j + 2
			} else {
				i = len(line)
			}
			atCommand = false
		case c == '"':
			// Double quotes: text, except a command substitution inside them,
			// which is where `macos_version="$(/usr/bin/sw_vers …)"` lives.
			end := closingQuote(line, i)
			out = append(out, substitutionsIn(line[i:end])...)
			i = end
			atCommand = false
		case c == '$' && i+1 < len(line) && line[i+1] == '(':
			inner, end := balanced(line, i+1, '(', ')')
			out = append(out, commandPositions(inner)...)
			i = end
			atCommand = false
		case c == '$' && i+1 < len(line) && line[i+1] == '{':
			_, end := balanced(line, i+1, '{', '}')
			i = end
			atCommand = false
		case c == '$' || c == '`':
			// A bare expansion, or a backquote this script does not use.
			i++
			atCommand = false
		case c == ';' || c == '&' || c == '|' || c == '(' || c == ')':
			i++
			atCommand = true
		case c == '>' || c == '<':
			// A redirection and its target are not a command.
			i++
			for i < len(line) && (line[i] == '>' || line[i] == '&') {
				i++
			}
			for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
				i++
			}
			for i < len(line) && !isWordBreak(line[i]) {
				i++
			}
		default:
			start := i
			for i < len(line) && !isWordBreak(line[i]) {
				i++
			}
			word := line[start:i]
			if !atCommand {
				continue
			}
			switch {
			case strings.HasPrefix(word, "-"):
				// An option, not a command: no command in this script is
				// spelled with a leading dash.
			case isAssignment(word):
				// `VAR=value command …` still leaves a command after it.
			case word == "for" || word == "select":
				// What follows is the loop's VARIABLE, and the command is
				// further along, after the `do`.
				skipWord = true
			case skipWord:
				skipWord = false
			case shellKeywords[word]:
				// A keyword introduces the command that follows it.
			case shellBuiltins[word]:
				atCommand = false
			default:
				out = append(out, word)
				atCommand = false
			}
		}
	}
	return out
}

// isWordBreak reports whether a byte ends a bare word.
func isWordBreak(c byte) bool {
	switch c {
	case ' ', '\t', ';', '&', '|', '(', ')', '<', '>', '"', '\'', '`', '$':
		return true
	}
	return false
}

// isAssignment reports whether a word is NAME=… or NAME+=…, which runs
// nothing: the second is how an array gains an element.
func isAssignment(word string) bool {
	name, _, found := strings.Cut(word, "=")
	if !found || name == "" {
		return false
	}
	name = strings.TrimSuffix(name, "+")
	return regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(name)
}

// closingQuote returns the index just past the double quote that closes the one
// at i, honouring backslash escapes.
func closingQuote(line string, i int) int {
	for j := i + 1; j < len(line); j++ {
		switch line[j] {
		case '\\':
			j++
		case '"':
			return j + 1
		}
	}
	return len(line)
}

// substitutionsIn returns the command positions of every command substitution
// inside a double-quoted word. The quoted text around them is prose — this
// script is full of sentences carrying words like `curl` — and only what is
// inside $( … ) runs.
func substitutionsIn(quoted string) []string {
	var out []string
	for i := 0; i+1 < len(quoted); i++ {
		if quoted[i] == '\\' {
			i++
			continue
		}
		if quoted[i] == '$' && quoted[i+1] == '(' {
			inner, end := balanced(quoted, i+1, '(', ')')
			out = append(out, commandPositions(inner)...)
			i = end - 1
		}
	}
	return out
}

// balanced returns the text inside a bracketed construct starting at the
// opening byte at i, and the index just past its close.
func balanced(line string, i int, open, close byte) (string, int) {
	depth := 0
	for j := i; j < len(line); j++ {
		switch line[j] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return line[i+1 : j], j + 1
			}
		}
	}
	return line[i+1:], len(line)
}

// heredocDelimiters are the words that close a here-document in this script.
// stripHeredocs blanks a document's CONTENT and leaves its delimiter line
// standing, which reads as a command named DONE.
func heredocDelimiters(src string) map[string]bool {
	out := map[string]bool{}
	for _, m := range regexp.MustCompile("<<-?[ \t]*(?:'([^']*)'|\"([^\"]*)\"|([A-Za-z_][A-Za-z0-9_]*))").FindAllStringSubmatch(src, -1) {
		out[m[1]+m[2]+m[3]] = true
	}
	return out
}

// joinContinuations folds a backslash-continued line into the line it
// continues, so the `-e` arguments of a multi-line osascript call are read as
// arguments rather than as commands of their own. The line NUMBERS stay those
// of the first line of each logical line, which is where a reader would look.
func joinContinuations(lines []string) []string {
	out := make([]string, len(lines))
	for i := 0; i < len(lines); i++ {
		start, joined := i, lines[i]
		for strings.HasSuffix(joined, "\\") && i+1 < len(lines) {
			i++
			joined = strings.TrimSuffix(joined, "\\") + " " + strings.TrimSpace(lines[i])
		}
		out[start] = joined
	}
	return out
}

// isCasePatternLine reports whether a line is a `case` alternative — `server)`,
// `*)` — whose word is a pattern rather than a command.
func isCasePatternLine(code string) bool {
	return regexp.MustCompile(`^\s*[^()]+\)\s*$`).MatchString(code)
}

// TestInstallerNeverDeletesTheBundleBeforeTheReplacementLands pins the ordering
// of the staged swap.
//
// The installed bundle must be renamed ASIDE, never deleted, before the new one
// is moved into place. An earlier version deleted it first and, on a failed
// rename, deleted the staged copy too — so an ordinary rename failure left no
// application at all, which is the exact outcome the staging comment says the
// staging exists to prevent. The bug needed no attacker and no unusual
// filesystem: one failing rename was enough.
//
// This checks the property that is cheap to check mechanically — that no `rm`
// of the destination bundle appears before the move that replaces it. It does
// not prove the swap is atomic, which it is not: `mv` nests into an existing
// directory and follows a symlink, and closing that needs os.Rename.
func TestInstallerNeverDeletesTheBundleBeforeTheReplacementLands(t *testing.T) {
	path := filepath.Join(repoRootDir(t), "install.sh")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	src := string(b)

	move := strings.Index(src, `mv "$staged/$APP.app" "$DEST/$APP.app"`)
	if move < 0 {
		t.Fatal("install.sh no longer moves the staged bundle into place — update this test")
	}
	if del := strings.Index(src, `rm -rf "$DEST/$APP.app"`); del >= 0 && del < move {
		t.Errorf("install.sh deletes the installed bundle before the replacement is in "+
			"place (offset %d, before the move at %d); rename it aside instead, so a "+
			"failed rename leaves a working application", del, move)
	}
}

// TestTheInstallerElevatesForNothing holds where the elevation went.
//
// The bootstrap used to raise an authentication panel of its own, as a gate
// before the download. It no longer elevates at all: the one panel this product
// raises is raised by `gropius install`, for the firewall entry, after the
// download has been verified — which is also where a credential is never spent
// on an archive that then fails its checksum. internal/archtest's
// lifecycle scan holds that to being exactly one site.
//
// `sudo` stays refused here for the reason it always was. sudo can only ever
// accept the invoking user's own password, and a standard (non-administrator)
// account is not in the sudoers set at all — so on exactly the accounts that
// most often run this script, a sudo prompt is unsatisfiable and no password
// the person can type will do. `sudo` inside an echoed message is fine: that
// text is an instruction for an administrator to run by hand.
func TestTheInstallerElevatesForNothing(t *testing.T) {
	root := repoRootDir(t)
	src := readRepoFile(t, root, "install.sh")

	for i, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "echo ") {
			continue
		}
		if strings.Contains(trimmed, "sudo ") {
			t.Errorf("install.sh:%d elevates with sudo, which a standard account cannot satisfy: %s", i+1, trimmed)
		}
		if strings.Contains(trimmed, "administrator privileges") {
			t.Errorf("install.sh:%d raises an authorization panel. The one panel belongs to `gropius install`, "+
				"which raises it after the download is verified and where a single scan can hold it to one site: %s",
				i+1, trimmed)
		}
	}
}

// TestTheInstallerVerifiesBeforeItHandsOver holds the order of the two halves.
//
// The handover executes a binary out of the downloaded archive, so it must come
// after the checksum verification — and from the directory that was verified,
// never from the bundle being replaced, so that the script and the binary it
// calls are always the same build. A handover before the verification would be
// running an unverified download; a handover to the INSTALLED bundle would be
// an old build being asked to install a new one.
func TestTheInstallerVerifiesBeforeItHandsOver(t *testing.T) {
	root := repoRootDir(t)
	src := readRepoFile(t, root, "install.sh")

	verify := strings.Index(src, "/usr/bin/shasum -a 256 -c --ignore-missing")
	if verify < 0 {
		t.Fatal("install.sh no longer verifies the download against the published checksums")
	}
	handover := strings.Index(src, `handover=("$VERIFIED_BIN" install --bundle`)
	if handover < 0 {
		t.Fatal("install.sh no longer hands over to the verified binary — update this test if the spelling changed, " +
			"but the bootstrap must reach `gropius install` for anything past the download to happen at all")
	}
	if handover < verify {
		t.Error("install.sh hands over before it verifies the download; the binary it executes would be unverified")
	}

	assign := strings.Index(src, `VERIFIED_BIN="$tmp/extract/`)
	if assign < 0 {
		t.Error("install.sh no longer takes the binary it hands over from the directory it verified ($tmp/extract); " +
			"a handover to the installed bundle runs a different build from the one just checked")
	}
}

// forEachShellScriptLine walks every shell script in the repository.
func forEachShellScriptLine(t *testing.T, fn func(path string, lineNo int, line string)) {
	t.Helper()
	root := repoRootDir(t)

	// The dot-directory rule this scan carried in its own body now lives in
	// walkRepoFiles, which every scan of the tree shares: a git worktree
	// checked out under a dot-directory holds a whole second copy of the tree,
	// and scanning it fails this test for somebody else's branch.
	//
	// .github and .githooks are named back in, as the braced-expansion scan
	// next door already does. Both hold shell this repository tracks — the
	// commit hooks are two scripts with shebangs, and a workflow's `run:` block
	// is shell running on a machine whose PATH nobody here controls — and the
	// rules these lines feed (no expansion on a line that runs as root, no bare
	// osascript) are the ones it would be worst to leave them outside of.
	shellDotDirs := walkOptions{DotDirs: []string{".github", ".githooks"}}
	walkRepoFiles(t, root, shellDotDirs, func(path string, _ fs.DirEntry) error {
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
}
