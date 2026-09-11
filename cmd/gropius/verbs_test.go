package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/lifecycle"
)

// writeConfig puts a settings file in a temporary root and points this process
// at it, the way an operator's own root would be found.
func writeConfig(t *testing.T, body string) config.Paths {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GROPIUS_ROOT", dir)
	paths := config.NewPaths(dir)
	if err := os.MkdirAll(filepath.Dir(paths.Config), 0o755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(paths.Config, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

// A verb must ask about the port the server would actually bind.
//
// config.Load returns an error for a file that parses and then fails
// validation, and the configuration it parsed with it. The server keeps that
// configuration — loadStartupConfig locks the bind down to loopback and keeps
// everything else, the port included — so a verb that threw the error away and
// took the default port would probe a port nothing is on and report "nothing is
// serving" about a server that is serving. The two now read the settings
// through the same function, so they cannot disagree.
func TestAVerbReadsThePortTheServerWouldBind(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       string
		wantPort   int
		wantSaysSo bool
	}{
		{
			name:     "no settings file is the shipping default",
			body:     "",
			wantPort: config.Default().Port,
		},
		{
			name:     "a file that loads",
			body:     `{"port":11999}`,
			wantPort: 11999,
		},
		{
			name: "a file whose bind will not validate keeps the rest, port included",
			// A host that is neither an address nor a host name is what the
			// server locks down to loopback while keeping everything else —
			// the case where the verb and the server would otherwise disagree
			// about which port to look at.
			body:       `{"port":11999,"host":"not a host name"}`,
			wantPort:   11999,
			wantSaysSo: true,
		},
		{
			name: "a file that is invalid beyond its bind falls back with the server",
			// decode_concurrency below one is still refused after the bind is
			// locked down, so the server starts from the shipping defaults and
			// so does the verb.
			body:       `{"port":11999,"decode_concurrency":0}`,
			wantPort:   config.Default().Port,
			wantSaysSo: true,
		},
		{
			name:       "a file that will not parse falls back with the server",
			body:       `{"port":`,
			wantPort:   config.Default().Port,
			wantSaysSo: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := writeConfig(t, tc.body)
			env, err := verbEnv()
			if err != nil {
				t.Fatalf("verbEnv: %v", err)
			}
			if env.Port != tc.wantPort {
				t.Errorf("Port = %d, want %d", env.Port, tc.wantPort)
			}
			if (env.SettingsProblem != "") != tc.wantSaysSo {
				t.Errorf("SettingsProblem = %q, want a problem: %v", env.SettingsProblem, tc.wantSaysSo)
			}
			// The property behind all four rows: the verb and the server read
			// the same file through the same function.
			if server := loadStartupConfig(paths.Config).Config.Port; server != env.Port {
				t.Errorf("the verb would probe port %d and the server would bind %d", env.Port, server)
			}
		})
	}
}

// The progress line goes to standard error and the answer goes to standard
// output, so `gropius status --json` stays clean for a pipe while a verb that
// takes minutes can still redraw a line for the person watching it.
func TestTheAnswerAndTheProgressLineTakeDifferentStreams(t *testing.T) {
	writeConfig(t, "")
	env, err := verbEnv()
	if err != nil {
		t.Fatalf("verbEnv: %v", err)
	}
	if env.Out != os.Stdout || env.Term.Out != os.Stdout {
		t.Error("the answer does not go to standard output")
	}
	if env.Err != os.Stderr || env.Progress.Out != os.Stderr {
		t.Error("the progress line does not go to standard error")
	}
}

// version prints the build and nothing else, and refuses a stray word like
// every other verb: a word this build has no meaning for is never quietly
// ignored, whichever verb it followed.
func TestVersionPrintsTheBuildAndRefusesAStrayArgument(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runCommandVerb(commandLine{Kind: kindVerb, Verb: "version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), version) {
		t.Errorf("version printed %q, which does not name this build", out.String())
	}

	out.Reset()
	errOut.Reset()
	code := runCommandVerb(commandLine{Kind: kindVerb, Verb: "version", Args: []string{"--json"}}, &out, &errOut)
	if code != lifecycle.ExitUsage {
		t.Fatalf("exit = %d, want %d for an argument version does not take", code, lifecycle.ExitUsage)
	}
	if !strings.Contains(errOut.String(), "--json") {
		t.Errorf("refusal %q does not name the argument", errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("a refused command line still printed %q", out.String())
	}
}

// A verb this build knows and does not carry says so by name, and exits 2: the
// person who read the record and typed it correctly learns that they typed it
// correctly.
func TestAVerbThisBuildDoesNotCarryIsRefusedByName(t *testing.T) {
	// update is itd-2609081420471761 and is not in this build; install and
	// uninstall are, so running them here would run them for real.
	for _, verb := range []string{"update"} {
		var out, errOut bytes.Buffer
		if code := runCommandVerb(commandLine{Kind: kindVerb, Verb: verb}, &out, &errOut); code != lifecycle.ExitUsage {
			t.Errorf("%s: exit = %d, want %d", verb, code, lifecycle.ExitUsage)
		}
		if !strings.Contains(errOut.String(), verb) || !strings.Contains(errOut.String(), "not in this build yet") {
			t.Errorf("%s: refusal = %q", verb, errOut.String())
		}
	}
}

// The whole path a person takes, with no server running: the command line, the
// environment built from this account's root, the probe that creates nothing,
// and the JSON on standard output.
func TestStatusRunsEndToEndAgainstNoServer(t *testing.T) {
	writeConfig(t, "")
	var out, errOut bytes.Buffer
	code := runCommandVerb(commandLine{Kind: kindVerb, Verb: "status", Args: []string{"--json"}}, &out, &errOut)
	if code != lifecycle.ExitOK {
		t.Fatalf("exit = %d (%s)", code, errOut.String())
	}
	var answer struct {
		Serving bool   `json:"serving"`
		Reason  string `json:"reason"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out.Bytes(), &answer); err != nil {
		t.Fatalf("status --json did not write JSON: %v\n%s", err, out.String())
	}
	if answer.Serving {
		t.Error("status reports a server against a root nothing is serving from")
	}
	if answer.Reason == "" || answer.Version != version {
		t.Errorf("status = %+v, want a reason and this build's version", answer)
	}
}

// And doctor's, which is the path that reads the filesystem and runs the system
// query. Nothing here is a fault Gropius owns — a temporary root is writable,
// the settings file is absent, the runtime is not installed and no server is on
// the port — so it exits zero with every check reported.
func TestDoctorRunsEndToEndAndExitsZeroOnWarnings(t *testing.T) {
	writeConfig(t, "")
	var out, errOut bytes.Buffer
	code := runCommandVerb(commandLine{Kind: kindVerb, Verb: "doctor", Args: []string{"--json"}}, &out, &errOut)
	if code != lifecycle.ExitOK {
		t.Fatalf("exit = %d, want %d; warnings exit zero\n%s\n%s", code, lifecycle.ExitOK, out.String(), errOut.String())
	}
	var report struct {
		Findings []struct {
			Name     string `json:"name"`
			Label    string `json:"label"`
			Severity string `json:"severity"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("doctor --json did not write JSON: %v\n%s", err, out.String())
	}
	if len(report.Findings) != len(lifecycle.DefaultChecks()) {
		t.Fatalf("doctor reported %d findings, want %d", len(report.Findings), len(lifecycle.DefaultChecks()))
	}
	for _, f := range report.Findings {
		if f.Label == "" || f.Severity == "" {
			t.Errorf("finding %q reached the JSON without a label or a severity", f.Name)
		}
	}
}
