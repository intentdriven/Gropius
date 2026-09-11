// Package lifecycle holds the verbs a person types at this installation: what
// it is doing now (status), why it is not working (doctor), and — as the
// installing and removing halves land — putting it there and taking it away.
//
// The name is about the installation's life, not the model server's:
// internal/runtime is the MLX subprocess and internal/app is the running
// server's state. The verbs sit at the edge of the package as thin shells and
// everything that decides anything is a pure function over values, which is
// what lets the criteria be tested without a Mac, a panel or a network.
//
// Nothing on the control plane's path may import this package, and this package
// imports nothing of internal/gateway. That is what keeps a route from ever
// driving a self-replacement or an elevation, and it is what holds doctor's
// report on the right side of adr-2609081118587999 rule 2: the report gates
// nothing. The rule is armed by the closure in
// internal/archtest/lifecycle_boundary_test.go rather than asserted here.
package lifecycle

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/intentdriven/Gropius/internal/config"
)

// Env is the world a verb runs in, handed in rather than reached for, so a test
// can put a temporary root, a fixed version and a pair of buffers in its place.
type Env struct {
	// Version is this build, read from the binary itself. No verb contacts
	// anything to learn it: whether this build is the current one is a
	// question for a release check, which is opt-in and off by default.
	Version string
	Paths   config.Paths
	Port    int
	// Config is the settings the server would start from, read the way the
	// server reads them, so a file that parses and fails validation is
	// reported rather than refused. SettingsProblem below says what reading it
	// had to do.
	Config config.Config
	// Out carries the verb's answer — the text, or the JSON a caller parses.
	Out io.Writer
	// Err carries progress and refusals, so a progress line never lands in the
	// middle of the JSON on Out.
	Err io.Writer
	// Term is what Out will bear: colour for the answer.
	Term Terminal
	// Progress is what Err will bear. It is a Terminal of its own rather than
	// Term because the two streams are not the same stream: `status --json`
	// pipes Out into a decoder while Err is still the operator's terminal, and
	// a progress line drawn on Out would be in the middle of the JSON.
	Progress Terminal
	// SettingsProblem is what reading config.json had to do to make it usable,
	// in the words the server logs at startup, and empty when it loaded
	// cleanly. A verb states it on Err rather than in its answer: it is the
	// reason a figure below might not be the one the operator wrote, and it is
	// not part of any verb's contract.
	//
	// doctor does not repeat it — it has a check of its own that reports the
	// settings file, in the report where a severity belongs.
	SettingsProblem string
}

// Exit codes. Zero is the answer, whatever the answer says: doctor's warnings
// are carried by the severity in its report and never by the code (the intent's
// exit-zero criterion).
const (
	// ExitOK: the verb ran and answered.
	ExitOK = 0
	// ExitFailed: the verb ran and something it verified is wrong.
	ExitFailed = 1
	// ExitUsage: the command line could not be honoured — an unknown flag, or
	// a verb this build does not carry yet.
	ExitUsage = 2
)

// flags builds a verb's flag set, quiet and writing its own refusal, because
// the default one exits the process and prints a usage block this package did
// not write.
func flags(name string, err io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("gropius "+name, flag.ContinueOnError)
	fs.SetOutput(err)
	return fs
}

// RunStatus is the status verb: what this installation is doing right now.
func RunStatus(env Env, args []string) int {
	return runStatus(env, args, liveStatusEnv(env.Version, env.Paths, env.Port))
}

func runStatus(env Env, args []string, st StatusEnv) int {
	fs := flags("status", env.Err)
	asJSON := fs.Bool("json", false, "write the machine-readable form, which is the contract")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		writeLine(env.Err, "gropius status: unexpected argument "+Quote(fs.Arg(0)))
		return ExitUsage
	}

	// On Err, and before the answer: what follows may be about a port the
	// operator did not choose, and a caller piping Out into a decoder must not
	// have to parse around a warning.
	if env.SettingsProblem != "" {
		writeLine(env.Err, "gropius status: "+env.SettingsProblem)
	}

	s := StatusOf(st)
	if *asJSON {
		if err := writeJSON(env.Out, s); err != nil {
			writeLine(env.Err, "gropius status: "+err.Error())
			return ExitFailed
		}
		return ExitOK
	}
	RenderStatus(env.Term, s)
	return ExitOK
}

// writeJSON writes v as the indented machine-readable form. Indented because a
// person reads it too — a status pasted into a bug report is the ordinary case
// — and a decoder does not care either way.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeLine(w io.Writer, s string) { fmt.Fprintln(w, s) }

// Quote wraps a value for display and keeps it readable when it is empty or
// carries spaces.
//
// Exported because cmd/gropius refuses command lines this package never sees —
// an unknown verb, a verb after the server's flags — and it was spelling the
// same two lines itself. One helper, so a refusal from the command and a
// refusal from a verb quote a word the same way.
func Quote(s string) string { return "\"" + s + "\"" }

// RunDoctor is the doctor verb: the expensive checks, and an honest account of
// the two states nobody can settle from here.
func RunDoctor(env Env, args []string) int {
	return runDoctor(env, args, liveDoctorEnv(env), DefaultChecks())
}

func runDoctor(env Env, args []string, d DoctorEnv, checks []Check) int {
	fs := flags("doctor", env.Err)
	asJSON := fs.Bool("json", false, "write the machine-readable form, which carries the severities")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		writeLine(env.Err, "gropius doctor: unexpected argument "+Quote(fs.Arg(0)))
		return ExitUsage
	}

	r := Diagnose(d, checks)
	if *asJSON {
		if err := writeJSON(env.Out, r); err != nil {
			writeLine(env.Err, "gropius doctor: "+err.Error())
			return ExitFailed
		}
	} else {
		RenderDoctor(env.Term, r)
	}
	// The severity is carried in the report; the code says only whether
	// something Gropius verified is wrong.
	return r.ExitCode()
}
