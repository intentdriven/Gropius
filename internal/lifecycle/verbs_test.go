package lifecycle

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/instance"
)

// testEnv is a verb's world with both streams captured and no colour, which is
// what a pipe would give it anyway.
func testEnv() (env Env, out, errOut *bytes.Buffer) {
	out, errOut = &bytes.Buffer{}, &bytes.Buffer{}
	return Env{Version: "test", Port: 11535, Out: out, Err: errOut, Term: Terminal{Out: out}}, out, errOut
}

func servingStatusEnv() StatusEnv {
	return StatusEnv{
		Version: "test",
		Port:    11535,
		Holder:  func() instance.Holder { return instance.HolderOurs },
		State:   func() (ServerState, error) { return servingState(), nil },
	}
}

// --json puts the contract on standard output and nothing else with it: a
// caller pipes it straight into a decoder.
func TestStatusJSONGoesToStandardOutputAlone(t *testing.T) {
	env, out, errOut := testEnv()
	if code := runStatus(env, []string{"--json"}, servingStatusEnv()); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	var decoded Status
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("status --json did not write JSON: %v\n%s", err, out.String())
	}
	if !decoded.Serving {
		t.Errorf("status --json = %s, want the serving state", out)
	}
	if errOut.Len() != 0 {
		t.Errorf("status --json wrote %q to standard error", errOut)
	}
}

// Without the flag the same value is rendered for a person.
func TestStatusWithoutJSONWritesText(t *testing.T) {
	env, out, _ := testEnv()
	if code := runStatus(env, nil, servingStatusEnv()); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		t.Errorf("status wrote JSON without being asked: %q", out)
	}
	if !strings.Contains(out.String(), "serving") {
		t.Errorf("status text = %q, which does not say whether it is serving", out)
	}
}

// A flag this verb does not have is a usage error, not a run: the person who
// typed it meant something, and doing the default thing silently is how a typo
// becomes a surprise.
func TestStatusRefusesAFlagItDoesNotHave(t *testing.T) {
	env, out, errOut := testEnv()
	if code := runStatus(env, []string{"-wibble"}, servingStatusEnv()); code != ExitUsage {
		t.Fatalf("exit = %d, want %d for an unknown flag", code, ExitUsage)
	}
	if out.Len() != 0 {
		t.Errorf("a refused command line still wrote %q to standard output", out)
	}
	if errOut.Len() == 0 {
		t.Error("a refused command line said nothing about why")
	}
}

// A stray word after the verb is refused for the same reason, and the refusal
// names it.
func TestStatusRefusesAStrayArgument(t *testing.T) {
	env, _, errOut := testEnv()
	if code := runStatus(env, []string{"everything"}, servingStatusEnv()); code != ExitUsage {
		t.Fatalf("exit = %d, want %d for a stray argument", code, ExitUsage)
	}
	if !strings.Contains(errOut.String(), "everything") {
		t.Errorf("refusal %q does not name the argument that was not understood", errOut)
	}
}

// decodeInto is the test's decoder, kept here so no verb has to export one.
func decodeInto(b []byte, v any) error { return json.Unmarshal(b, v) }

// A settings file the server could not use as written changes what status is
// looking at — the port among other things — so status says so. On standard
// error, because the JSON on standard output is a contract and a warning is not
// part of it.
func TestStatusStatesASettingsProblemWithoutSpoilingTheJSON(t *testing.T) {
	env, out, errOut := testEnv()
	env.SettingsProblem = "config.json is not a valid configuration — the bind address is locked down to loopback only"
	if code := runStatus(env, []string{"--json"}, servingStatusEnv()); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(errOut.String(), "locked down to loopback") {
		t.Errorf("standard error = %q, which does not state the settings problem", errOut)
	}
	var decoded Status
	if err := decodeInto(out.Bytes(), &decoded); err != nil {
		t.Fatalf("the JSON no longer decodes: %v\n%s", err, out)
	}
}
