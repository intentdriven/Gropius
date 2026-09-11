package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/instance"
)

// fakeEnv is a Mac where everything Gropius owns is healthy: the runtime is
// provisioned, the root is writable, this account's own server holds the port,
// and the firewall query answered the way it answers for a path it has an entry
// for — which is also the way it answers for a path it does not.
func fakeEnv(t *testing.T) DoctorEnv {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "Library", "Application Support", "Gropius")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return DoctorEnv{
		Version:      "test",
		Paths:        config.NewPaths(root),
		Port:         11535,
		Binary:       filepath.Join(home, "Applications", "Gropius.app", "Contents", "MacOS", "gropius"),
		Home:         home,
		Holder:       func() instance.Holder { return instance.HolderOurs },
		RuntimeReady: func() bool { return true },
		Writable:     func(string) error { return nil },
		Firewall:     func(string) (string, error) { return "is permitted to respond to incoming connections", nil },
		Settings:     func() SettingsState { return SettingsState{Present: true} },
	}
}

// findingNamed returns the finding a report carries under name.
func findingNamed(t *testing.T, r Report, name string) Finding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no finding named %q in %+v", name, r.Findings)
	return Finding{}
}

// Every finding carries exactly one of the three labels, and the label says how
// much its answer is worth: what Gropius verified, what it only observed, and
// what cannot be determined from here at all.
func TestEveryFindingCarriesExactlyOneLabel(t *testing.T) {
	r := Diagnose(fakeEnv(t), DefaultChecks())
	if len(r.Findings) == 0 {
		t.Fatal("doctor reported nothing")
	}
	for _, f := range r.Findings {
		switch f.Label {
		case Verified, Observed, Undeterminable:
		default:
			t.Errorf("finding %q carries the label %q, which is not one of the three", f.Name, f.Label)
		}
		if f.Summary == "" {
			t.Errorf("finding %q says nothing", f.Name)
		}
	}
}

// The firewall entry is reported as observed and never as a verdict: the query
// answers "permitted" for a path that has no entry and for a path that does not
// exist, so a check built on it would confirm a healthy grant at exactly the
// moment an update invalidated one (adr-2609111126115848 condition 1).
//
// And the commands that would settle it are printed beside it, always
// (condition 2): an observation a reader cannot act on collapses into a verdict
// in the reading, because the only thing left to do with it is believe it.
func TestTheFirewallIsObservedAndCarriesItsRemedy(t *testing.T) {
	r := Diagnose(fakeEnv(t), DefaultChecks())
	f := findingNamed(t, r, firewallCheckName)
	if f.Label != Observed {
		t.Errorf("the firewall finding is labelled %q, want %q", f.Label, Observed)
	}
	if len(f.Commands) != 2 {
		t.Fatalf("the firewall finding names %d commands, want the two that re-grant it: %+v", len(f.Commands), f.Commands)
	}
	for _, want := range []string{"--add", "--unblockapp"} {
		found := false
		for _, c := range f.Commands {
			if strings.Contains(c, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no command names %q: %+v", want, f.Commands)
		}
	}
	for _, c := range f.Commands {
		if !strings.Contains(c, "/usr/libexec/ApplicationFirewall/socketfilterfw") {
			t.Errorf("command %q does not name the tool by absolute path", c)
		}
	}
}

// A query that could not be run at all is still not a verdict in the other
// direction: the finding stays observed and says what it could not do.
func TestAFirewallQueryThatFailedIsStillNotAVerdict(t *testing.T) {
	env := fakeEnv(t)
	env.Firewall = func(string) (string, error) { return "", errors.New("exit status 1") }
	f := findingNamed(t, Diagnose(env, DefaultChecks()), firewallCheckName)
	if f.Label != Observed {
		t.Errorf("label = %q, want %q even when the query failed", f.Label, Observed)
	}
	if f.Severity != SeverityUndetermined {
		t.Errorf("severity = %q, want %q", f.Severity, SeverityUndetermined)
	}
}

// The firewall query answers with the binary's path INSIDE a sentence, and
// doctor's output is written to be pasted into a bug report — so the account
// name has to go from the quoted answer too, not only from the path doctor
// prints beside it.
//
// The real tool answers "Incoming connection to <path> is permitted."; a fake
// that answered without a path is why this went unnoticed.
func TestTheQuotedFirewallAnswerCarriesNoHomeDirectory(t *testing.T) {
	env := fakeEnv(t)
	env.Firewall = func(path string) (string, error) {
		return "Incoming connection to " + path + " is permitted.\n", nil
	}

	f := findingNamed(t, Diagnose(env, DefaultChecks()), firewallCheckName)
	if strings.Contains(f.Summary, env.Home) {
		t.Errorf("the firewall finding spells this account's home directory out, which is what a person pastes "+
			"into a bug report:\n%s", f.Summary)
	}
	if !strings.Contains(f.Summary, "~/Applications/Gropius.app") {
		t.Errorf("the abbreviated path is gone from the answer altogether, so the finding no longer says which "+
			"binary was asked about:\n%s", f.Summary)
	}
}

// Local Network Privacy has no query interface of any kind. The check is
// reported, never omitted and never guessed — in the JSON and in the text
// alike, because a check that is missing from the page a person reads is a
// check that was guessed by whoever reads it.
func TestLocalNetworkPrivacyIsReportedAsUndeterminable(t *testing.T) {
	r := Diagnose(fakeEnv(t), DefaultChecks())
	f := findingNamed(t, r, localNetworkCheckName)
	if f.Label != Undeterminable {
		t.Errorf("label = %q, want %q", f.Label, Undeterminable)
	}
	if len(f.Commands) == 0 {
		t.Error("an undeterminable finding with no command beside it leaves the reader nothing to do but believe it")
	}
	var buf bytes.Buffer
	RenderDoctor(Terminal{Out: &buf}, r)
	if !strings.Contains(buf.String(), localNetworkCheckName) {
		t.Errorf("the text rendering omits the check:\n%s", buf.String())
	}
}

// A signal reported under the carve-out never carries a fault: the severity
// says the state could not be determined, whatever the check itself returned.
// This is enforced where the report is assembled rather than trusted to each
// check, because "observed" is a label a future line can keep while its
// sentence quietly hardens into a conclusion.
func TestAnObservedFindingCannotReportAFault(t *testing.T) {
	overreaching := []Check{
		{
			Name:  "an observed check that thinks it found something",
			Label: Observed,
			Ask:   func(DoctorEnv) Answer { return Answer{Summary: "the query answered", Severity: SeverityFailed} },
		},
		{
			Name:  "an undeterminable check that thinks it found something",
			Label: Undeterminable,
			Ask:   func(DoctorEnv) Answer { return Answer{Summary: "nothing to query", Severity: SeverityWarning} },
		},
	}
	r := Diagnose(fakeEnv(t), overreaching)
	for _, f := range r.Findings {
		if f.Severity != SeverityUndetermined {
			t.Errorf("finding %q carries severity %q, want %q", f.Name, f.Severity, SeverityUndetermined)
		}
	}
	if code := r.ExitCode(); code != ExitOK {
		t.Errorf("exit = %d, want %d: nothing here was verified", code, ExitOK)
	}
}

// Severities live in the machine-readable output, not in the exit code:
// warnings exit zero, and only a check Gropius genuinely verified can make
// doctor exit non-zero.
func TestExitCodeFollowsTheVerifiedChecksAlone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		checks []Check
		want   int
	}{
		{
			name:   "all well",
			checks: []Check{{Name: "a", Label: Verified, Ask: ok("fine")}},
			want:   ExitOK,
		},
		{
			name: "a warning is still zero",
			checks: []Check{
				{Name: "a", Label: Verified, Ask: ok("fine")},
				{Name: "b", Label: Verified, Ask: func(DoctorEnv) Answer {
					return Answer{Summary: "worth knowing", Severity: SeverityWarning}
				}},
			},
			want: ExitOK,
		},
		{
			name: "a verified failure is not",
			checks: []Check{
				{Name: "a", Label: Verified, Ask: func(DoctorEnv) Answer {
					return Answer{Summary: "the root cannot be written", Severity: SeverityFailed}
				}},
			},
			want: ExitFailed,
		},
		{
			name: "an observed signal never decides the code",
			checks: []Check{
				{Name: "a", Label: Observed, Ask: func(DoctorEnv) Answer {
					return Answer{Summary: "the query answered", Severity: SeverityFailed}
				}},
			},
			want: ExitOK,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Diagnose(fakeEnv(t), tc.checks).ExitCode(); got != tc.want {
				t.Errorf("ExitCode = %d, want %d", got, tc.want)
			}
		})
	}
}

func ok(summary string) func(DoctorEnv) Answer {
	return func(DoctorEnv) Answer { return Answer{Summary: summary, Severity: SeverityOK} }
}

// The runtime, the root and the port's holder are state Gropius owns, so a
// severity on them means what it says.
func TestTheVerifiedChecksReportWhatGropiusOwns(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(*DoctorEnv)
		check string
		want  Severity
	}{
		{
			name:  "a provisioned runtime",
			alter: func(*DoctorEnv) {},
			check: runtimeCheckName,
			want:  SeverityOK,
		},
		{
			// A warning and not a failure: a Mac where the runtime has not
			// been provisioned yet is a Mac that has not finished starting,
			// which is the ordinary state of a fresh install and not a fault
			// to exit non-zero over.
			name: "a runtime that is not there yet",
			alter: func(e *DoctorEnv) {
				e.RuntimeReady = func() bool { return false }
			},
			check: runtimeCheckName,
			want:  SeverityWarning,
		},
		{
			name:  "a writable root",
			alter: func(*DoctorEnv) {},
			check: rootCheckName,
			want:  SeverityOK,
		},
		{
			name: "a root that cannot be written",
			alter: func(e *DoctorEnv) {
				e.Writable = func(string) error { return errors.New("permission denied") }
			},
			check: rootCheckName,
			want:  SeverityFailed,
		},
		{
			name:  "this account's own server on the port",
			alter: func(*DoctorEnv) {},
			check: portCheckName,
			want:  SeverityOK,
		},
		{
			name: "nothing on the port",
			alter: func(e *DoctorEnv) {
				e.Holder = func() instance.Holder { return instance.HolderNone }
			},
			check: portCheckName,
			want:  SeverityWarning,
		},
		{
			name: "somebody else on the port",
			alter: func(e *DoctorEnv) {
				e.Holder = func() instance.Holder { return instance.HolderForeign }
			},
			check: portCheckName,
			want:  SeverityWarning,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := fakeEnv(t)
			tc.alter(&env)
			f := findingNamed(t, Diagnose(env, DefaultChecks()), tc.check)
			if f.Label != Verified {
				t.Errorf("label = %q, want %q", f.Label, Verified)
			}
			if f.Severity != tc.want {
				t.Errorf("severity = %q, want %q (%q)", f.Severity, tc.want, f.Summary)
			}
		})
	}
}

// A process on the port that is not this account's Gropius is another account's
// business: doctor counts it and does not name it, and says that the version it
// reports is its own and not necessarily the version being served.
func TestTheForeignPortHolderIsCountedAndNotNamed(t *testing.T) {
	env := fakeEnv(t)
	env.Holder = func() instance.Holder { return instance.HolderForeign }
	f := findingNamed(t, Diagnose(env, DefaultChecks()), portCheckName)
	if !strings.Contains(f.Summary, "1 ") {
		t.Errorf("summary = %q, which does not count the holder", f.Summary)
	}
	if !strings.Contains(strings.ToLower(f.Summary), "not named") {
		t.Errorf("summary = %q, which does not say the holder is left unnamed", f.Summary)
	}
	version := findingNamed(t, Diagnose(env, DefaultChecks()), versionCheckName)
	if !strings.Contains(version.Summary, "test") {
		t.Errorf("the version finding %q does not report this build", version.Summary)
	}
}

// Doctor's output is written to be pasted into a bug report, so no line carries
// this account's home directory: paths are abbreviated to "~".
func TestOutputIsPasteSafe(t *testing.T) {
	env := fakeEnv(t)
	// Both stubs put the path where it actually turns up: inside a sentence
	// somebody else wrote. A rule that only stripped a prefix would pass a
	// test whose stubs said "permission denied" and nothing else, and leak in
	// the two findings an operator pastes.
	env.Writable = func(dir string) error {
		return &os.PathError{Op: "open", Path: filepath.Join(dir, "probe.tmp"), Err: syscall.EACCES}
	}
	env.Firewall = func(path string) (string, error) {
		return "Incoming connection to " + path + " is permitted.", nil
	}
	r := Diagnose(env, DefaultChecks())

	var buf bytes.Buffer
	RenderDoctor(Terminal{Out: &buf}, r)
	text := buf.String()
	if strings.Contains(text, env.Home) {
		t.Errorf("the report carries this account's home directory:\n%s", text)
	}
	if !strings.Contains(text, "~/") {
		t.Errorf("no path in the report was abbreviated, so the test proves nothing:\n%s", text)
	}
	for _, f := range r.Findings {
		if strings.Contains(f.Summary, env.Home) {
			t.Errorf("finding %q carries this account's home directory: %q", f.Name, f.Summary)
		}
		for _, c := range f.Commands {
			if strings.Contains(c, env.Home) {
				t.Errorf("command %q carries this account's home directory", c)
			}
		}
	}
}

// redact is the whole of that rule, and it is a pure function over a string and
// a home directory. It replaces EVERY occurrence, not a prefix: the path an
// operator pastes usually arrives inside somebody else's sentence — an
// *os.PathError reads "open <path>: permission denied", and the firewall's
// answer puts the path in the middle of a line — and a prefix rule leaves the
// account name in both.
func TestRedactReplacesEveryOccurrenceOfTheHomeDirectory(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "somewhere", "an-account")
	for _, tc := range []struct{ in, want string }{
		{filepath.Join(home, "Library", "Logs"), "~/Library/Logs"},
		{home, "~"},
		{"open " + filepath.Join(home, "Library") + ": permission denied", "open ~/Library: permission denied"},
		{"Incoming connection to " + home + "/a is permitted.", "Incoming connection to ~/a is permitted."},
		{home + " and " + home, "~ and ~"},
		{filepath.Join(string(filepath.Separator), "Users", "Shared", "Gropius"), "/Users/Shared/Gropius"},
		{"", ""},
	} {
		if got := redact(tc.in, home); got != tc.want {
			t.Errorf("redact(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// A sibling directory that merely starts with the same letters is folded
	// too, and that is the deliberate trade: over-redaction costs a reader one
	// confusing path, under-redaction costs them their account name in a public
	// bug report.
	if got := redact(home+"-else", home); got != "~-else" {
		t.Errorf("redact(%q) = %q, want the occurrence replaced", home+"-else", got)
	}
	// With no home to compare against, a string is left exactly as it is — and
	// a home of "/" is no home at all, rather than a rule that puts a tilde
	// between every character.
	if got := redact("/tmp/x", ""); got != "/tmp/x" {
		t.Errorf("redact with no home = %q, want the string unchanged", got)
	}
	if got := redact("/tmp/x", string(filepath.Separator)); got != "/tmp/x" {
		t.Errorf("redact with a root home = %q, want the string unchanged", got)
	}
}

// The failure an operator actually pastes: the root check asks the filesystem,
// the filesystem answers with an *os.PathError, and the path sits in the middle
// of the message. Redaction happens where the report is assembled, so no check
// can forget it.
func TestAnErrorCarryingTheHomePathMidMessageIsRedacted(t *testing.T) {
	env := fakeEnv(t)
	leaky := &os.PathError{Op: "open", Path: filepath.Join(env.Paths.Root, "probe.tmp"), Err: syscall.EACCES}
	env.Writable = func(string) error { return leaky }

	f := findingNamed(t, Diagnose(env, DefaultChecks()), rootCheckName)
	if strings.Contains(f.Summary, env.Home) {
		t.Errorf("the data-root finding carries this account's home directory: %q", f.Summary)
	}
	if !strings.Contains(f.Summary, "permission denied") {
		t.Errorf("redaction lost what the failure was: %q", f.Summary)
	}
}

// The same shape from the other side: the firewall's own answer puts the path
// mid-line, and what it answers is quoted into the report.
func TestTheFirewallsAnswerIsRedactedWhereverThePathSits(t *testing.T) {
	env := fakeEnv(t)
	env.Firewall = func(path string) (string, error) {
		return "Incoming connection to " + path + " is permitted.", nil
	}
	f := findingNamed(t, Diagnose(env, DefaultChecks()), firewallCheckName)
	if strings.Contains(f.Summary, env.Home) {
		t.Errorf("the firewall finding carries this account's home directory: %q", f.Summary)
	}
}

// Every command reaching the report is redacted too, not only the summaries: a
// remedy line carries a path by construction.
func TestCommandsAreRedactedAsWellAsSummaries(t *testing.T) {
	env := fakeEnv(t)
	leaking := []Check{{
		Name:  "a check that built a command out of a path",
		Label: Verified,
		Ask: func(e DoctorEnv) Answer {
			return Answer{Summary: "fine", Severity: SeverityOK, Commands: []string{"ls " + e.Paths.Root}}
		},
	}}
	for _, f := range Diagnose(env, leaking).Findings {
		for _, c := range f.Commands {
			if strings.Contains(c, env.Home) {
				t.Errorf("command %q carries this account's home directory", c)
			}
		}
	}
}

// The re-grant commands are printed to be run, so the path in them is rendered
// the way a shell reads it — a space must not split the argument, a quote must
// not end it — and it must still carry no account name. A path under this
// account's home is written as "$HOME/...", which the shell expands on the
// machine the command is run on; a tilde would not expand there at all, because
// a shell takes it literally inside quotes.
func TestTheRegrantArgumentIsRunnableAndCarriesNoAccountName(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "somewhere", "an-account")
	for _, tc := range []struct{ in, want string }{
		{"/Applications/Gropius.app/Contents/MacOS/gropius", "'/Applications/Gropius.app/Contents/MacOS/gropius'"},
		{"/tmp/an app/gropius", "'/tmp/an app/gropius'"},
		{"/tmp/it's here/gropius", `'/tmp/it'\''s here/gropius'`},
		{home + "/Applications/an app/gropius", `"$HOME/Applications/an app/gropius"`},
		{home, `"$HOME"`},
		// The four characters a double-quoted shell string still reads: a
		// backtick would run a command, a dollar would expand another
		// variable, a backslash escapes, and a quote would end the string.
		{home + "/a`b$c\\d\"e", "\"$HOME/a\\`b\\$c\\\\d\\\"e\""},
		{"", "''"},
	} {
		if got := shellArg(tc.in, home); got != tc.want {
			t.Errorf("shellArg(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// And the commands the firewall check actually prints carry it: quoted for a
// bundle whose path has a space in it, and expanded rather than abbreviated for
// one inside this account's home.
func TestTheFirewallCommandsAreRunnable(t *testing.T) {
	env := fakeEnv(t)
	env.Binary = "/Applications/Gropius beta.app/Contents/MacOS/gropius"
	f := findingNamed(t, Diagnose(env, DefaultChecks()), firewallCheckName)
	for _, c := range f.Commands {
		if !strings.Contains(c, "'/Applications/Gropius beta.app/Contents/MacOS/gropius'") {
			t.Errorf("command %q does not quote the bundle path", c)
		}
	}

	env = fakeEnv(t) // Binary is under the fake home
	f = findingNamed(t, Diagnose(env, DefaultChecks()), firewallCheckName)
	for _, c := range f.Commands {
		if strings.Contains(c, env.Home) {
			t.Errorf("command %q carries this account's home directory", c)
		}
		if !strings.Contains(c, `"$HOME/Applications/Gropius.app/Contents/MacOS/gropius"`) {
			t.Errorf("command %q does not name a path a shell would resolve", c)
		}
		if strings.Contains(c, "~") {
			t.Errorf("command %q carries a tilde, which a shell does not expand inside quotes", c)
		}
	}
}

// The settings file is state Gropius owns, so it is a verified check: it loads,
// it loads with something repaired or dropped, it is not there at all, or it
// cannot be used as written.
func TestTheSettingsFileIsChecked(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state SettingsState
		want  Severity
		says  string
	}{
		{
			name:  "no file yet is not a fault",
			state: SettingsState{},
			want:  SeverityOK,
			says:  "defaults",
		},
		{
			name:  "a file that loads cleanly",
			state: SettingsState{Present: true},
			want:  SeverityOK,
			says:  "loads",
		},
		{
			name:  "a file with settings repaired or dropped",
			state: SettingsState{Present: true, Notices: config.Notices{Repaired: []string{"grace"}, Ignored: []string{"old_key"}}},
			want:  SeverityWarning,
			says:  "2",
		},
		{
			name:  "a file that cannot be used as written",
			state: SettingsState{Present: true, Err: errors.New("unexpected end of JSON input")},
			want:  SeverityFailed,
			says:  "unexpected end of JSON input",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := fakeEnv(t)
			env.Settings = func() SettingsState { return tc.state }
			f := findingNamed(t, Diagnose(env, DefaultChecks()), settingsCheckName)
			if f.Label != Verified {
				t.Errorf("label = %q, want %q: the settings file is state Gropius owns", f.Label, Verified)
			}
			if f.Severity != tc.want {
				t.Errorf("severity = %q, want %q (%q)", f.Severity, tc.want, f.Summary)
			}
			if !strings.Contains(f.Summary, tc.says) {
				t.Errorf("summary = %q, which does not say %q", f.Summary, tc.says)
			}
		})
	}
}

// The wording test adr-2609111126115848 condition 3 requires, and the one that
// has to exist: condition 1 is a property of sentences, and sentences drift.
// No line reporting a signal under the carve-out may be phrased as a
// conclusion, in either direction — not "the grant is in place", and not "the
// firewall is blocking this".
func TestNoObservedLineIsPhrasedAsAConclusion(t *testing.T) {
	// Every shape of answer the system can give, so the scan sees every
	// sentence these checks can produce.
	envs := []DoctorEnv{fakeEnv(t), fakeEnv(t), fakeEnv(t)}
	envs[1].Firewall = func(string) (string, error) { return "", errors.New("exit status 1") }
	envs[2].Firewall = func(string) (string, error) {
		return "is blocked from responding to incoming connections", nil
	}

	// What is scanned is doctor's own voice. The system's answer travels
	// inside quotation marks, and it is what the query said rather than what
	// doctor concluded — which is exactly the distinction the hedge that must
	// follow it keeps visible.
	banned := []string{
		"is in place",
		"is not in place",
		"is present",
		"is missing",
		"is blocking",
		"is allowed through",
		"is protected",
		"your firewall",
		"everything is fine",
		"no action is needed",
	}
	for _, env := range envs {
		r := Diagnose(env, DefaultChecks())
		for _, f := range r.Findings {
			if f.Label == Verified {
				continue
			}
			lines := append([]string{f.Summary}, f.Commands...)
			for _, line := range lines {
				lower := strings.ToLower(line)
				for _, phrase := range banned {
					if strings.Contains(lower, phrase) {
						t.Errorf("the %s line is phrased as a conclusion (%q): %q", f.Label, phrase, line)
					}
				}
			}
			if !carriesAHedge(f.Summary) {
				t.Errorf("the %s finding %q says what it saw without saying it could not establish it: %q",
					f.Label, f.Name, f.Summary)
			}
		}
	}
}

// carriesAHedge reports whether a line says, in one of the few ways this
// package says it, that what it reports was not established.
func carriesAHedge(line string) bool {
	// The exact phrases this package hedges with. A bare "cannot" used to
	// count, which would have passed a line like "the firewall cannot be
	// reached, so it is blocking you" — a conclusion with a modal verb in it.
	for _, hedge := range []string{
		"does not establish",
		"cannot be determined",
		"could not be run",
	} {
		if strings.Contains(strings.ToLower(line), hedge) {
			return true
		}
	}
	return false
}

// The JSON carries the severity and the label for every finding, because that
// is where a script reads them: the exit code says only whether something
// verified went wrong.
func TestDoctorJSONCarriesTheLabelsAndSeverities(t *testing.T) {
	env, out, _ := testEnv()
	if code := runDoctor(env, []string{"--json"}, fakeEnv(t), DefaultChecks()); code != ExitOK {
		t.Fatalf("exit = %d, want %d on a healthy Mac", code, ExitOK)
	}
	var r Report
	if err := decodeInto(out.Bytes(), &r); err != nil {
		t.Fatalf("doctor --json did not write JSON: %v\n%s", err, out)
	}
	if len(r.Findings) != len(DefaultChecks()) {
		t.Fatalf("doctor --json reported %d findings, want %d", len(r.Findings), len(DefaultChecks()))
	}
	for _, f := range r.Findings {
		if f.Label == "" || f.Severity == "" {
			t.Errorf("finding %q reached the JSON without a label or a severity: %+v", f.Name, f)
		}
	}
}

// A verified failure exits non-zero through the verb as well as through the
// report, and the JSON is still written: a script that reads the report should
// not have to choose between the exit code and the findings.
func TestDoctorVerbExitsNonZeroOnAVerifiedFailure(t *testing.T) {
	fake := fakeEnv(t)
	fake.Writable = func(string) error { return errors.New("permission denied") }
	env, out, _ := testEnv()
	if code := runDoctor(env, []string{"--json"}, fake, DefaultChecks()); code != ExitFailed {
		t.Fatalf("exit = %d, want %d", code, ExitFailed)
	}
	if out.Len() == 0 {
		t.Error("doctor --json wrote nothing on the failing path")
	}
}

// Doctor runs in a process of its own, so it can read whether the runtime is
// installed and cannot read what a provisioning run in another process is
// doing. It says the first and does not guess at the second — a stage read from
// a provisioner this process just constructed would be "idle" on every Mac,
// including one that is provisioning right now.
func TestTheRuntimeFindingReportsInstallationAndNotAStage(t *testing.T) {
	env := fakeEnv(t)
	env.RuntimeReady = func() bool { return false }
	f := findingNamed(t, Diagnose(env, DefaultChecks()), runtimeCheckName)
	if f.Severity != SeverityWarning {
		t.Errorf("severity = %q, want %q", f.Severity, SeverityWarning)
	}
	for _, stage := range []string{"idle", "failed", "installing"} {
		if strings.Contains(f.Summary, stage) {
			t.Errorf("summary = %q, which reports a stage this process cannot see", f.Summary)
		}
	}
	if len(f.Commands) == 0 {
		t.Error("a runtime that is not installed yet leaves the reader nothing to run")
	}
	if code := Diagnose(env, DefaultChecks()).ExitCode(); code != ExitOK {
		t.Errorf("exit = %d, want %d: a runtime still being installed is not a failure", code, ExitOK)
	}
}

// A query that will not return must not hold the terminal. socketfilterfw is a
// system tool talking to a system daemon, and a daemon that is wedged would
// otherwise wedge doctor with it — on the command somebody runs precisely
// because something is already wrong.
func TestAQueryThatHangsIsGivenUpOn(t *testing.T) {
	start := time.Now()
	// /bin/sleep rather than the firewall itself: the behaviour under test is
	// the timeout, and a test that needed a wedged daemon could not be written.
	out, err := runQuery(100*time.Millisecond, "/bin/sleep", "5")
	if err == nil {
		t.Fatalf("runQuery returned %q and no error for a command that outlives its timeout", out)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("runQuery waited %s; the timeout did not stop it", elapsed)
	}
}

// And a query that answers comes back with what it said.
func TestAQueryThatAnswersIsReported(t *testing.T) {
	out, err := runQuery(5*time.Second, "/bin/echo", "an answer")
	if err != nil {
		t.Fatalf("runQuery: %v", err)
	}
	if !strings.Contains(out, "an answer") {
		t.Errorf("runQuery = %q, want what the command printed", out)
	}
}

// goldenEnv is a Mac written down rather than one found: fixed paths, fixed
// answers, and a home directory that belongs to nobody. It is what makes the
// report below a fixture a reviewer can read a diff against, the way the status
// contract has one.
func goldenEnv() DoctorEnv {
	home := filepath.Join(string(filepath.Separator), "somewhere", "an-account")
	return DoctorEnv{
		Version:      "test",
		Paths:        config.NewPaths(filepath.Join(home, "Library", "Application Support", "Gropius")),
		Port:         11535,
		Binary:       filepath.Join(home, "Applications", "Gropius.app", "Contents", "MacOS", "gropius"),
		Home:         home,
		Holder:       func() instance.Holder { return instance.HolderForeign },
		RuntimeReady: func() bool { return false },
		Writable:     func(string) error { return nil },
		Settings:     func() SettingsState { return SettingsState{Present: true} },
		Firewall: func(path string) (string, error) {
			return "Incoming connection to " + path + " is permitted.", nil
		},
	}
}

// The report is a contract too: a script reads the labels and the severities,
// and a person reads the summaries out of a pasted bug report. Compared as a
// decoded value, so a renamed field or a changed label is a visible diff here
// rather than a silent break in whatever reads it.
func TestDoctorJSONMatchesTheGolden(t *testing.T) {
	got := encodeJSON(t, Diagnose(goldenEnv(), DefaultChecks()))
	want := decodeFile(t, filepath.Join("testdata", "doctor_report.json"))
	if !reflect.DeepEqual(got, want) {
		b, _ := json.MarshalIndent(got, "", "  ")
		t.Errorf("doctor --json does not match testdata/doctor_report.json:\n%s", b)
	}
}

// Nothing in that report names a real place or a real account: it is the
// fixture a bug report would carry, and it is committed.
func TestTheGoldenReportCarriesNoAccount(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "doctor_report.json"))
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" && strings.Contains(string(b), home) {
		t.Error("the golden report carries this machine's home directory")
	}
}
