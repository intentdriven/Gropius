package lifecycle

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// configShowEnv is a verb environment with captured streams and a settings
// file of its own, so nothing here reads or writes this account's install.
func configShowEnv(t *testing.T, c config.Config) (Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	var out, errOut bytes.Buffer
	return Env{
		Version: "test",
		Paths:   config.NewPaths(root),
		Port:    c.Port,
		Config:  c,
		Out:     &out,
		Err:     &errOut,
		Term:    Terminal{Out: &out},
	}, &out, &errOut
}

// sampleSettings is the configuration the tests below report on: the shipped
// defaults with both secrets set, a setting the panel cannot reach, and one
// model carrying per-model settings — so the report has something of every
// shape in it.
func sampleSettings() config.Config {
	c := config.Default()
	c.APIKey = "bh_not-a-real-key-0123456789"
	c.HFToken = "hf_not-a-real-token-0123456789"
	c.UpstreamHeaderTimeoutSec = 45
	c.Preload = []string{"mlx-community/Qwen3-8B-4bit"}
	temp := 0.7
	c.Models = map[string]config.ModelSettings{
		"mlx-community/Qwen3-8B-4bit": {Pinned: true, ServedContext: 8192, Sampling: config.Sampling{Temperature: &temp}},
	}
	return c
}

// The secrets are never printed, in either form.
//
// The panel is loopback-only and still redacts them, because loopback includes
// the other accounts on this Mac. A terminal verb has a second reason: its
// output is the thing an operator pastes into a bug report, and doctor already
// redacts this account's home directory from its own report for exactly that.
// A key printed here is a key in somebody's issue tracker.
func TestConfigShowRedactsTheSecrets(t *testing.T) {
	settings := sampleSettings()
	for _, form := range []struct {
		name string
		args []string
	}{
		{"the human form", []string{"show"}},
		{"the machine form", []string{"show", "--json"}},
	} {
		t.Run(form.name, func(t *testing.T) {
			env, out, errOut := configShowEnv(t, settings)
			if code := RunConfig(env, form.args); code != ExitOK {
				t.Fatalf("exit = %d (%s)", code, errOut.String())
			}
			printed := out.String()
			for _, secret := range []string{settings.APIKey, settings.HFToken} {
				if strings.Contains(printed, secret) {
					t.Errorf("the report carries a secret's value:\n%s", printed)
				}
			}
			// And the placeholder is there, so a reader can tell "no key" from
			// "a key you are not being shown".
			if strings.Count(printed, Redacted) < 2 {
				t.Errorf("the report does not show both secrets as %s:\n%s", Redacted, printed)
			}
		})
	}
	// A secret that is not set stays empty rather than becoming a placeholder:
	// "no API key" is a fact an operator needs, and a server with no key is the
	// one the exposure warning is about.
	shown := ConfigInForce(config.Default())
	if shown.APIKey != "" || shown.HFToken != "" {
		t.Errorf("an unset secret was reported as set: api_key=%q hf_token=%q", shown.APIKey, shown.HFToken)
	}
}

// Redaction is a property of the rendering rather than a line per field, and
// this is what asks for a new secret to be added to the list. It is a
// heuristic on the key's name and says so: it catches the ordinary way a
// secret arrives — a field called a key, a token, a secret or a password — and
// it would not catch one called something else.
func TestEverySettingThatLooksLikeASecretIsRedacted(t *testing.T) {
	covered := map[string]bool{}
	for _, s := range secretSettings {
		covered[s.Key] = true
	}
	rt := reflect.TypeOf(config.Config{})
	for i := range rt.NumField() {
		key, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if key == "" || key == "-" || covered[key] {
			continue
		}
		for _, word := range []string{"key", "token", "secret", "password"} {
			if strings.Contains(key, word) {
				t.Errorf("config.Config carries %q, which reads like a secret and is not in secretSettings — "+
					"a terminal verb prints what that list does not redact", key)
			}
		}
	}
	// And the other direction: a name in the list that is not a setting any
	// more redacts nothing.
	held := map[string]bool{}
	for i := range rt.NumField() {
		key, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		held[key] = true
	}
	for _, s := range secretSettings {
		if !held[s.Key] {
			t.Errorf("secretSettings names %q, which the configuration no longer holds", s.Key)
		}
	}
}

// `gropius config show --json` is the contract a script reads, so its shape is
// pinned by a fixture rather than by a reviewer's memory: a key renamed or
// dropped is a break for somebody.
func TestConfigShowJSONIsTheContract(t *testing.T) {
	env, out, errOut := configShowEnv(t, sampleSettings())
	if code := RunConfig(env, []string{"show", "--json"}); code != ExitOK {
		t.Fatalf("exit = %d (%s)", code, errOut.String())
	}

	var got, want any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, out.String())
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "config_show.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(golden, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("`gropius config show --json` does not match testdata/config_show.json:\n%s", out.String())
	}
}

// The terminal reads every setting, including the ones the panel cannot reach.
//
// That is the whole point of this verb. The two settings with no control —
// the preload list and the upstream header timeout — are the ones an operator
// is least able to check, because the panel does not show them either; a read
// that skipped them would leave them reachable from nothing but the file.
func TestConfigShowNamesTheSettingsThePanelCannotReach(t *testing.T) {
	settings := SettingsInForce(sampleSettings())
	for _, key := range []string{
		"preload", "upstream_header_timeout_sec", "advertise",
		"host", "bind_mode", "port", "api_key", "hf_token", "log_level",
		"sampling.temperature", "chat_rule.pipeline_tags",
		"models.mlx-community/Qwen3-8B-4bit.pinned",
		"models.mlx-community/Qwen3-8B-4bit.served_context",
		"models.mlx-community/Qwen3-8B-4bit.sampling.temperature",
	} {
		if _, named := settings[key]; !named {
			t.Errorf("the report does not name %q, so the terminal cannot read it", key)
		}
	}
	// Every setting, not only the ones the file carries: encoding/json omits a
	// value at its zero, and a report of the file's own keys would call a
	// default an absence.
	if raw, named := settings["statistics"]; !named || string(raw) != "false" {
		t.Errorf("statistics = %s (named %v), want false reported rather than omitted", raw, named)
	}
}

// Nothing is written. The control plane is the settings file's only writer,
// and a verb that touched it would be a second one on state that is
// single-writer by record (iss-2609062045106963).
func TestConfigShowWritesNothing(t *testing.T) {
	env, _, errOut := configShowEnv(t, sampleSettings())
	if err := os.MkdirAll(filepath.Dir(env.Paths.Config), 0o755); err != nil {
		t.Fatal(err)
	}
	const stored = `{"port":11535}`
	if err := os.WriteFile(env.Paths.Config, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"show"}, {"show", "--json"}} {
		if code := RunConfig(env, args); code != ExitOK {
			t.Fatalf("exit = %d (%s)", code, errOut.String())
		}
	}
	after, err := os.ReadFile(env.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != stored {
		t.Errorf("config.json was rewritten by a verb that only reads:\n%s", after)
	}
}

// A command line this verb has no meaning for is refused by name and exits 2,
// the code every other refused command line here exits with.
func TestConfigRefusesACommandLineItHasNoMeaningFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		says string
	}{
		{"no subcommand at all", nil, `"show"`},
		{"a subcommand that writes", []string{"set"}, `"set"`},
		{"an argument after show", []string{"show", "port"}, `"port"`},
		{"a flag no verb registers", []string{"show", "--all"}, "all"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, out, errOut := configShowEnv(t, sampleSettings())
			if code := RunConfig(env, tc.args); code != ExitUsage {
				t.Fatalf("exit = %d, want %d", code, ExitUsage)
			}
			if !strings.Contains(errOut.String(), tc.says) {
				t.Errorf("the refusal does not name what went wrong:\n%s", errOut.String())
			}
			if out.Len() != 0 {
				t.Errorf("a refused command line still wrote an answer:\n%s", out.String())
			}
		})
	}
}

// A settings problem is stated on standard error and never in the answer, so a
// caller piping the answer into a decoder does not have to parse around it —
// the rule status already follows. And the verb answers anyway: somebody
// running this is usually trying to find out what is wrong.
func TestConfigShowStatesASettingsProblemBesideTheAnswer(t *testing.T) {
	env, out, errOut := configShowEnv(t, sampleSettings())
	env.SettingsProblem = "config.json is not a valid configuration — the bind address is locked down"
	if code := RunConfig(env, []string{"show", "--json"}); code != ExitOK {
		t.Fatalf("exit = %d: a verb must not refuse to answer over a settings problem", code)
	}
	if !strings.Contains(errOut.String(), "locked down") {
		t.Errorf("the problem was not stated on standard error:\n%s", errOut.String())
	}
	var doc any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Errorf("the problem landed in the middle of the contract: %v\n%s", err, out.String())
	}
}
