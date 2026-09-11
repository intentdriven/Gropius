package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/instance"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// socketfilterfw is the macOS Application Firewall's command-line interface,
// named by absolute path like every other system tool a lifecycle verb
// invokes: a bare name is resolved through PATH, and a normal developer PATH
// puts directories another account on this Mac can write to ahead of the system
// ones (internal/archtest/pinned_subprocess_test.go).
const socketfilterfw = "/usr/libexec/ApplicationFirewall/socketfilterfw"

// The names the report and its tests know each check by. They are the words a
// person reads, so they are written once here and rendered rather than
// reassembled.
const (
	runtimeCheckName      = "MLX runtime"
	rootCheckName         = "data root"
	settingsCheckName     = "settings file"
	versionCheckName      = "this build"
	portCheckName         = "server port"
	firewallCheckName     = "firewall entry"
	localNetworkCheckName = "Local Network Privacy"
)

// Label says how much a finding's answer is worth. Every finding carries
// exactly one.
type Label string

const (
	// Verified: state Gropius owns. A severity here means what it says.
	Verified Label = "verified"
	// Observed: a query about state Gropius does not own answered, and what it
	// answered could not be established. The firewall entry is the case in
	// hand: the query answers "permitted" for a path that has no entry and for
	// a path that does not exist, and an ad-hoc signature's designated
	// requirement is its code-directory hash, so whatever was observed may
	// already be about a bundle that is gone (adr-2609111126115848).
	Observed Label = "observed"
	// Undeterminable: there is no query at all. Local Network Privacy is not
	// part of TCC, cannot be read, reset or pre-seeded.
	Undeterminable Label = "undeterminable"
)

// Severity travels in the machine-readable output and never in the exit code:
// doctor exits zero on warnings, and a script reads the severity rather than
// inferring one from a number.
type Severity string

const (
	SeverityOK      Severity = "ok"
	SeverityWarning Severity = "warning"
	SeverityFailed  Severity = "failed"
	// SeverityUndetermined is the only severity a finding that is not Verified
	// may carry. It says the state could not be determined — never that a
	// fault was found, which is the constraint the carve-out's first condition
	// puts on the machine-readable output as much as on the prose.
	SeverityUndetermined Severity = "undetermined"
)

// Answer is what one check found.
type Answer struct {
	Summary string
	// Severity as the check saw it. For an Observed or Undeterminable check it
	// is overruled where the report is assembled, so a check cannot report a
	// fault about state Gropius does not own however it is written.
	Severity Severity
	// Commands are what a person would run to establish or restore the state.
	// Every finding that is not Verified carries them: an observation a reader
	// cannot act on collapses into a verdict in the reading, because the only
	// thing left to do with it is believe it (condition 2).
	Commands []string
}

// Check is one question doctor asks. The set is a table so a test can put its
// own questions in and assert on the shape of the report rather than on a Mac.
type Check struct {
	Name  string
	Label Label
	Ask   func(DoctorEnv) Answer
}

// Finding is one check's answer as the report carries it.
type Finding struct {
	Name     string   `json:"name"`
	Label    Label    `json:"label"`
	Severity Severity `json:"severity"`
	Summary  string   `json:"summary"`
	Commands []string `json:"commands,omitempty"`
}

// Report is the whole of what doctor says.
type Report struct {
	Version  string    `json:"version"`
	Findings []Finding `json:"findings"`
}

// DoctorEnv is everything the checks are allowed to ask, handed in as functions
// so every case is a test with no Mac: a runtime that is not installed, a root
// that cannot be written, a settings file that will not parse, a port another
// account holds, a firewall query that would not run.
type DoctorEnv struct {
	Version string
	Paths   config.Paths
	Port    int
	// Binary is this build's own executable, which is the path a firewall
	// entry would be keyed to.
	Binary string
	// Home is this account's home directory, and the only reason it is here is
	// paste safety: it is what every string in the report is redacted against.
	Home string
	// Holder classifies the process on the server port through the instance
	// challenge.
	Holder func() instance.Holder
	// RuntimeReady says whether the private Python and MLX runtime is
	// installed and usable, which the provisioner answers by reading the
	// filesystem.
	//
	// Whether it is installed is all a separate process can honestly ask. The
	// STAGE of a provisioning run belongs to the process running it: a
	// provisioner this command constructs has never run one, so its stage is
	// "idle" on every Mac — including one that is provisioning right now — and
	// reporting that would be reporting a field rather than a fact.
	RuntimeReady func() bool
	// Writable says whether a directory can be written to, and why not.
	Writable func(dir string) error
	// Settings is what this account's settings file did when it was read.
	Settings func() SettingsState
	// Firewall runs the system's firewall query for a path and returns what it
	// said. Its answer is reported and is never allowed to decide anything.
	Firewall func(path string) (string, error)
}

// SettingsState is what this account's settings file did when it was read. It
// is a value rather than three return values so a test can put any of the four
// states in without a file.
type SettingsState struct {
	// Present is false when there is no file at all, which is the state of
	// every install until somebody saves. It is not a fault: the shipping
	// defaults are in force and the server runs on them.
	Present bool
	// Notices is what Load had to change to make the file usable: settings in
	// force in a changed form, and settings not in force at all.
	Notices config.Notices
	// Err is a file that could not be read or parsed. The defaults are in
	// force and the operator's settings are not, which is a fault.
	Err error
}

// DefaultChecks is the set a real run asks, in the order it prints them: what
// Gropius owns first, then the two states nobody can settle from here.
func DefaultChecks() []Check {
	return []Check{
		{Name: runtimeCheckName, Label: Verified, Ask: checkRuntime},
		{Name: rootCheckName, Label: Verified, Ask: checkRoot},
		{Name: settingsCheckName, Label: Verified, Ask: checkSettings},
		{Name: versionCheckName, Label: Verified, Ask: checkVersion},
		{Name: portCheckName, Label: Verified, Ask: checkPort},
		{Name: firewallCheckName, Label: Observed, Ask: checkFirewall},
		{Name: localNetworkCheckName, Label: Undeterminable, Ask: checkLocalNetwork},
	}
}

// Diagnose asks every check and assembles the report.
//
// The one rule it applies over what the checks return is the carve-out's: a
// finding that is not Verified carries SeverityUndetermined whatever its check
// said. That is enforced here rather than trusted to each check because
// "observed" is a label a future line can keep while its sentence quietly
// hardens into a conclusion, and this is the one place every line passes
// through.
func Diagnose(env DoctorEnv, checks []Check) Report {
	r := Report{Version: env.Version, Findings: make([]Finding, 0, len(checks))}
	for _, c := range checks {
		a := c.Ask(env)
		severity := a.Severity
		if c.Label != Verified {
			severity = SeverityUndetermined
		}
		// Redaction happens here, over every string that reaches the report,
		// and not at the call sites. A check builds its summary out of paths
		// and out of errors other people wrote — an *os.PathError reads "open
		// <path>: permission denied", and the firewall answers with the path
		// in the middle of a sentence — so a rule applied per check is a rule
		// the next check forgets, in the output an operator pastes in public.
		commands := make([]string, 0, len(a.Commands))
		for _, cmd := range a.Commands {
			commands = append(commands, redact(cmd, env.Home))
		}
		if len(commands) == 0 {
			commands = nil
		}
		r.Findings = append(r.Findings, Finding{
			Name:     c.Name,
			Label:    c.Label,
			Severity: severity,
			Summary:  redact(a.Summary, env.Home),
			Commands: commands,
		})
	}
	return r
}

// ExitCode is zero unless something Gropius verified is wrong. Warnings exit
// zero, and so does every signal reported under the carve-out, whatever it saw.
func (r Report) ExitCode() int {
	for _, f := range r.Findings {
		if f.Label == Verified && f.Severity == SeverityFailed {
			return ExitFailed
		}
	}
	return ExitOK
}

func checkRuntime(env DoctorEnv) Answer {
	if env.RuntimeReady() {
		return Answer{Summary: "installed and usable", Severity: SeverityOK}
	}
	// A warning rather than a failure. A Mac where the runtime is not there
	// yet is usually one that has not finished its first start — provisioning
	// takes minutes and runs in the server's own process — and doctor exiting
	// non-zero on the ordinary state of a fresh install would make the code
	// meaningless.
	return Answer{
		Summary:  "not installed yet, so no model can be loaded until it is",
		Severity: SeverityWarning,
		// Starting the server is what provisions it: the run that installs the
		// runtime is the server's own first start.
		Commands: []string{"gropius"},
	}
}

func checkRoot(env DoctorEnv) Answer {
	if err := env.Writable(env.Paths.Root); err != nil {
		return Answer{
			Summary:  env.Paths.Root + " cannot be written: " + err.Error(),
			Severity: SeverityFailed,
		}
	}
	return Answer{Summary: env.Paths.Root + " is writable", Severity: SeverityOK}
}

// checkSettings reports what config.json did when it was read. It is verified
// rather than observed: the file is this account's own, Gropius reads it, and a
// severity on it means what it says.
//
// A file that is not there is not a fault. Every install has none until
// somebody saves, and the shipping defaults are what the server runs on.
func checkSettings(env DoctorEnv) Answer {
	s := env.Settings()
	switch {
	case s.Err != nil:
		return Answer{
			Summary:  "cannot be used as written, so the shipping defaults are in force: " + s.Err.Error(),
			Severity: SeverityFailed,
			Commands: []string{"gropius status"},
		}
	case !s.Present:
		return Answer{Summary: "not written yet, so the shipping defaults are in force", Severity: SeverityOK}
	case !s.Notices.Empty():
		return Answer{
			Summary: "loads, with " + strconv.Itoa(len(s.Notices.All())) + " setting(s) this build could not use as written; " +
				"the control panel names them, and a save rewrites the file from what is in force",
			Severity: SeverityWarning,
		}
	default:
		return Answer{Summary: "loads", Severity: SeverityOK}
	}
}

// checkVersion reads this binary and contacts nothing. Whether this build is
// the current one is a question for a release check, which is opt-in, off by
// default, and a record of its own.
func checkVersion(env DoctorEnv) Answer {
	return Answer{Summary: env.Version, Severity: SeverityOK}
}

func checkPort(env DoctorEnv) Answer {
	port := strconv.Itoa(env.Port)
	switch env.Holder() {
	case instance.HolderOurs:
		return Answer{Summary: "this account's Gropius is on port " + port, Severity: SeverityOK}
	case instance.HolderForeign:
		// Counted, not named. Which account it belongs to is that account's
		// business, and this output is written to be pasted into a bug report.
		return Answer{
			Summary: "port " + port + " is held by 1 process that is not this account's Gropius; it is not named here, " +
				"and the build reported above is this one's own and not necessarily the build being served",
			Severity: SeverityWarning,
			Commands: []string{"gropius status"},
		}
	default:
		return Answer{
			Summary:  "nothing is serving on port " + port,
			Severity: SeverityWarning,
			Commands: []string{"gropius"},
		}
	}
}

// checkFirewall reports what the system query returned, and nothing more.
//
// It issues no verdict in either direction, because the query cannot support
// one: it answers "permitted" for a path with no entry at all and for a path
// that does not exist, and the grant is keyed to a code identity that changes
// with every build of an ad-hoc-signed bundle. So the line says what came back,
// says it does not establish that the grant still covers this build, and prints
// the two commands that re-grant it (adr-2609111126115848 conditions 1 and 2).
func checkFirewall(env DoctorEnv) Answer {
	// Two renderings of one path, because the prose and the command are read
	// by different things. The prose is for a person, so the path is
	// abbreviated there by Diagnose like every other string in the report. The
	// command is for a shell, so it is rendered by shellArg: quoted against a
	// space or a quote in the path, and written "$HOME/…" rather than "~/…" so
	// that it still carries no account name and still runs.
	binary := redact(env.Binary, env.Home)
	arg := shellArg(env.Binary, env.Home)
	answer := Answer{
		Commands: []string{
			"sudo " + socketfilterfw + " --add " + arg,
			"sudo " + socketfilterfw + " --unblockapp " + arg,
		},
	}
	out, err := env.Firewall(env.Binary)
	if err != nil {
		answer.Summary = "the firewall query could not be run, so nothing was observed about the entry for " + binary
		return answer
	}
	answer.Summary = "the firewall query answered " + quote(strings.TrimSpace(out)) +
		" for " + binary + "; that does not establish that a grant covers this build, because the query answers " +
		"the same way for a path it has no entry for, and this build's code identity changes with every build"
	return answer
}

// checkLocalNetwork reports the state nothing can read.
//
// Local Network Privacy has no query interface of any kind, is not part of TCC,
// and cannot be reset or pre-seeded. The check is printed every time rather than
// omitted: a check that disappears when it has nothing to say is a check the
// reader answers for themselves.
func checkLocalNetwork(env DoctorEnv) Answer {
	return Answer{
		Summary: "cannot be determined from here — macOS offers no way to read this grant, and it is lost on every build " +
			"of an ad-hoc-signed app",
		Commands: []string{
			"open 'x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork'",
		},
	}
}

// RenderDoctor writes the report for a person. It is a rendering of the value
// the JSON carries, and the JSON is what a script should read.
func RenderDoctor(t Terminal, r Report) {
	for _, f := range r.Findings {
		fmt.Fprintln(t.Out, t.Paint(severityColor(f.Severity), pad(string(f.Severity)))+"  "+
			t.Paint(Dim, pad2(string(f.Label)))+"  "+f.Name)
		fmt.Fprintln(t.Out, "      "+f.Summary)
		for _, c := range f.Commands {
			fmt.Fprintln(t.Out, "      "+t.Paint(Dim, "run: ")+c)
		}
	}
}

func severityColor(s Severity) Color {
	switch s {
	case SeverityOK:
		return Green
	case SeverityFailed:
		return Red
	case SeverityWarning:
		return Yellow
	default:
		return Dim
	}
}

// pad and pad2 line the two columns up without a formatter's width verbs, which
// count bytes rather than the characters a terminal draws.
func pad(s string) string  { return padTo(s, len(string(SeverityUndetermined))) }
func pad2(s string) string { return padTo(s, len(string(Undeterminable))) }

func padTo(s string, width int) string {
	for len(s) < width {
		s += " "
	}
	return s
}

// redact replaces every occurrence of this account's home directory with "~",
// so that what doctor prints can be pasted into a bug report without carrying
// an account name.
//
// Every occurrence, and not a prefix. The path usually arrives inside a
// sentence somebody else wrote: an *os.PathError reads "open <path>: permission
// denied", and the firewall answers "Incoming connection to <path> is
// permitted." A prefix rule matches neither, and leaves the account name in
// both — in the output an operator pastes in public.
//
// The cost is that a sibling directory whose name merely starts with the same
// letters is folded too. That is the deliberate trade: over-redaction costs a
// reader one confusing path, under-redaction costs them their account name. A
// home of "/" is treated as no home at all rather than as a rule that would put
// a tilde between every character of every string.
func redact(s, home string) string {
	home = strings.TrimRight(home, string(filepath.Separator))
	if home == "" || s == "" {
		return s
	}
	s = strings.ReplaceAll(s, home+string(filepath.Separator), "~"+string(filepath.Separator))
	return strings.ReplaceAll(s, home, "~")
}

// shellArg renders a path as one shell argument that is both runnable and safe
// to paste in public.
//
// A path inside this account's home is written as "$HOME/…" in double quotes:
// the shell expands $HOME on the machine the command is run on, and the account
// name never reaches the page. Abbreviating it to "~" instead would satisfy
// neither half — a tilde inside quotes is a literal character, so the command
// would name a directory that does not exist.
//
// Everything else is single-quoted, with an embedded single quote closing the
// quoting, escaping itself and reopening it: the only escape a single-quoted
// shell string has.
func shellArg(path, home string) string {
	home = strings.TrimRight(home, string(filepath.Separator))
	if home != "" {
		if path == home {
			return `"$HOME"`
		}
		if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
			return `"$HOME/` + escapeInDoubleQuotes(rest) + `"`
		}
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// escapeInDoubleQuotes escapes the four characters a double-quoted shell string
// still reads: the backtick that would run a command, the dollar that would
// expand another variable, the backslash that escapes, and the quote that would
// end the string. $HOME is written outside this, so it is the one expansion
// left in the argument.
func escapeInDoubleQuotes(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '"', '$', '`':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// liveDoctorEnv is the environment a real run asks its questions of.
func liveDoctorEnv(env Env) DoctorEnv {
	home, _ := os.UserHomeDir()
	binary, err := os.Executable()
	if err != nil {
		binary = ""
	}
	return DoctorEnv{
		Version: env.Version,
		Paths:   env.Paths,
		Port:    env.Port,
		Binary:  binary,
		Home:    home,
		// The probe that creates nothing, here as in status: a diagnostic that
		// created the data root it was asked about would be reporting on its
		// own work, and the root check beside it is what says the root is
		// missing.
		Holder:       func() instance.Holder { return instance.ProbeExisting(env.Paths, env.Port) },
		RuntimeReady: func() bool { return runtime.NewProvisioner(env.Paths).Status().Ready },
		Writable:     writableDir,
		Settings:     func() SettingsState { return loadSettings(env.Paths.Config) },
		Firewall:     queryFirewall,
	}
}

// writableDir reports whether this account can create a file in dir, which is
// the only question the root check has: a directory that exists and refuses a
// write is the failure an operator is looking at.
func writableDir(dir string) error {
	f, err := os.CreateTemp(dir, ".doctor-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// firewallQueryTimeout is how long doctor waits for the system firewall to
// answer. It is a system tool talking to a system daemon, and a daemon that is
// wedged would otherwise wedge the command somebody ran precisely because
// something is already wrong.
const firewallQueryTimeout = 5 * time.Second

// queryFirewall asks the macOS Application Firewall what it says about a path.
//
// Read-only, and its answer is reported rather than believed. The exit status
// is folded into the error so a query that could not run is reported as
// nothing observed rather than as an absent grant.
func queryFirewall(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("this build's own path is not known")
	}
	// Written out in full here rather than through the constant above: the
	// pinned-subprocess scan reads string literals, so a path that reaches
	// exec through an identifier passes it in silence. The two spellings are a
	// few lines apart and the scan covers the one that matters.
	return runQuery(firewallQueryTimeout, "/usr/libexec/ApplicationFirewall/socketfilterfw", "--getappblocked", path)
}

// runQuery runs one read-only system tool and returns what it printed, giving
// up after timeout rather than waiting on it forever.
func runQuery(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("%s did not answer within %s", name, timeout)
	}
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// loadSettings reads this account's settings the way the server reads them, and
// reports what happened rather than what they say. Nothing here is printed but
// the outcome: the file holds an API key and a HuggingFace token, and doctor's
// output is written to be pasted in public.
func loadSettings(path string) SettingsState {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return SettingsState{}
	}
	_, notices, err := config.Load(path)
	return SettingsState{Present: true, Notices: notices, Err: err}
}
