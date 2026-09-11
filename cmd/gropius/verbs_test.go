package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
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
