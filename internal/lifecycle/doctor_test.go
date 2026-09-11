package lifecycle

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/instance"
	"github.com/intentdriven/Gropius/internal/runtime"
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
		Version:  "test",
		Paths:    config.NewPaths(root),
		Port:     11535,
		Binary:   filepath.Join(home, "Applications", "Gropius.app", "Contents", "MacOS", "gropius"),
		Home:     home,
		Holder:   func() instance.Holder { return instance.HolderOurs },
		Runtime:  func() runtime.SetupStatus { return runtime.SetupStatus{Stage: runtime.StageReady, Ready: true} },
		Writable: func(string) error { return nil },
		Firewall: func(string) (string, error) { return "is permitted to respond to incoming connections", nil },
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
			name: "a runtime that is not there",
			alter: func(e *DoctorEnv) {
				e.Runtime = func() runtime.SetupStatus {
					return runtime.SetupStatus{Stage: runtime.StageFailed, Err: "uv would not install"}
				}
			},
			check: runtimeCheckName,
			want:  SeverityFailed,
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
	env.Writable = func(string) error { return errors.New("permission denied") }
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

// abbreviate is the whole of that rule, and it is a pure function over a path
// and a home directory.
func TestAbbreviateReplacesTheHomeDirectory(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "somewhere", "an-account")
	for _, tc := range []struct{ path, want string }{
		{filepath.Join(home, "Library", "Logs"), "~/Library/Logs"},
		{home, "~"},
		{filepath.Join(string(filepath.Separator), "Users", "Shared", "Gropius"), "/Users/Shared/Gropius"},
		// A different account's directory that merely starts with the same
		// letters is not this account's home and is not abbreviated.
		{home + "-else", home + "-else"},
		{"", ""},
	} {
		if got := abbreviate(tc.path, home); got != tc.want {
			t.Errorf("abbreviate(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
	// With no home to compare against, a path is left exactly as it is.
	if got := abbreviate("/tmp/x", ""); got != "/tmp/x" {
		t.Errorf("abbreviate with no home = %q, want the path unchanged", got)
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
	for _, hedge := range []string{
		"does not establish",
		"cannot be determined",
		"could not",
		"cannot",
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
