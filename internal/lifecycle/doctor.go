package lifecycle

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

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
// so every case is a test with no Mac: a provisioner that says it failed, a
// root that cannot be written, a port another account holds, a firewall query
// that would not run.
type DoctorEnv struct {
	Version string
	Paths   config.Paths
	Port    int
	// Binary is this build's own executable, which is the path a firewall
	// entry would be keyed to.
	Binary string
	// Home is this account's home directory, and the only reason it is here is
	// paste safety: it is what paths are abbreviated against.
	Home string
	// Holder classifies the process on the server port through the instance
	// challenge.
	Holder func() instance.Holder
	// Runtime is the provisioner's own account of itself. Doctor reports what
	// the provisioner says rather than re-deriving it, so the terminal and the
	// panel cannot disagree about whether the runtime is ready.
	Runtime func() runtime.SetupStatus
	// Writable says whether a directory can be written to, and why not.
	Writable func(dir string) error
	// Firewall runs the system's firewall query for a path and returns what it
	// said. Its answer is reported and is never allowed to decide anything.
	Firewall func(path string) (string, error)
}

// DefaultChecks is the set a real run asks, in the order it prints them: what
// Gropius owns first, then the two states nobody can settle from here.
func DefaultChecks() []Check {
	return []Check{
		{Name: runtimeCheckName, Label: Verified, Ask: checkRuntime},
		{Name: rootCheckName, Label: Verified, Ask: checkRoot},
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
		r.Findings = append(r.Findings, Finding{
			Name:     c.Name,
			Label:    c.Label,
			Severity: severity,
			Summary:  a.Summary,
			Commands: a.Commands,
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
	s := env.Runtime()
	if s.Ready {
		return Answer{Summary: "installed and usable", Severity: SeverityOK}
	}
	summary := "not usable yet (" + string(s.Stage) + ")"
	if s.Err != "" {
		summary += ": " + s.Err
	}
	return Answer{
		Summary:  summary,
		Severity: SeverityFailed,
		Commands: []string{"gropius install"},
	}
}

func checkRoot(env DoctorEnv) Answer {
	root := abbreviate(env.Paths.Root, env.Home)
	if err := env.Writable(env.Paths.Root); err != nil {
		return Answer{
			Summary:  root + " cannot be written: " + abbreviate(err.Error(), env.Home),
			Severity: SeverityFailed,
		}
	}
	return Answer{Summary: root + " is writable", Severity: SeverityOK}
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
	binary := abbreviate(env.Binary, env.Home)
	answer := Answer{
		Commands: []string{
			"sudo " + socketfilterfw + " --add '" + binary + "'",
			"sudo " + socketfilterfw + " --unblockapp '" + binary + "'",
		},
	}
	out, err := env.Firewall(env.Binary)
	if err != nil {
		answer.Summary = "the firewall query could not be run, so nothing was observed about the entry for " + binary
		return answer
	}
	answer.Summary = "the firewall query answered " + quote(strings.TrimSpace(abbreviateAll(out, env.Home))) +
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

// abbreviate replaces this account's home directory with "~" so a report can be
// pasted into a bug report without carrying an account name. A path that merely
// starts with the same letters is a different directory and is left alone.
func abbreviate(path, home string) string {
	if home == "" || path == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

// abbreviateAll replaces EVERY occurrence of this account's home directory
// inside free text, which abbreviate cannot do: a path is a whole value and is
// abbreviated at its start, but the firewall's answer is a sentence with the
// path in the middle of it ("Incoming connection to <path> is permitted."). A
// report written to be pasted into a bug report cannot carry an account name in
// either shape.
func abbreviateAll(text, home string) string {
	if home == "" || text == "" {
		return text
	}
	sep := string(filepath.Separator)
	return strings.ReplaceAll(text, home+sep, "~"+sep)
}

// liveDoctorEnv is the environment a real run asks its questions of.
func liveDoctorEnv(env Env) DoctorEnv {
	home, _ := os.UserHomeDir()
	binary, err := os.Executable()
	if err != nil {
		binary = ""
	}
	return DoctorEnv{
		Version:  env.Version,
		Paths:    env.Paths,
		Port:     env.Port,
		Binary:   binary,
		Home:     home,
		Holder:   func() instance.Holder { return instance.Probe(env.Paths, env.Port) },
		Runtime:  func() runtime.SetupStatus { return runtime.NewProvisioner(env.Paths).Status() },
		Writable: writableDir,
		Firewall: queryFirewall,
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

// queryFirewall asks the macOS Application Firewall what it says about a path.
//
// Read-only, and its answer is reported rather than believed. The exit status
// is folded into the error so a query that could not run is reported as
// nothing observed rather than as an absent grant.
func queryFirewall(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("this build's own path is not known")
	}
	// Written out in full at the call site rather than through the constant
	// above: the pinned-subprocess scan reads string literals, so a path that
	// reaches exec through an identifier passes it in silence. The two
	// spellings are one line apart and the scan covers the one that matters.
	out, err := exec.Command("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getappblocked", path).CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
