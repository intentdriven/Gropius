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
//
// Walked the way the RENDERER walks it, which is the whole point: the report
// recurses into the nested settings structs and into every per-model entry, so
// a guard that looked only at config.Config's own fields would be blind to
// exactly the place a new secret is most likely to arrive — a token on a
// model's settings, say. The two walks have to cover the same ground, or the
// list of things to redact is checked against a smaller world than the thing
// doing the redacting.
func TestEverySettingThatLooksLikeASecretIsRedacted(t *testing.T) {
	covered := map[string]bool{}
	for _, s := range secretSettings {
		covered[s.Key] = true
	}

	reported := SettingsInForce(sampleSettings())
	if len(reported) < 25 {
		t.Fatalf("the report names %d settings, so this guard is reading the wrong walk", len(reported))
	}
	held := map[string]bool{}
	for key := range reported {
		leaf := key
		if i := strings.LastIndex(key, "."); i >= 0 {
			leaf = key[i+1:]
		}
		held[leaf] = true
		if covered[leaf] {
			continue
		}
		// Matched on whole snake_case segments rather than as substrings,
		// because "max_tokens" is a sampling parameter and not a credential.
		// That is the cost of a heuristic and it runs both ways: a secret
		// called "auth_tokens" would slip past this, which is why the list
		// itself is the guard and this only asks for the obvious ones.
		for _, segment := range strings.Split(leaf, "_") {
			switch segment {
			case "key", "token", "secret", "password":
				t.Errorf("`gropius config show` reports %q, which reads like a secret and is not in "+
					"secretSettings — the verb prints what that list does not redact, to a terminal and "+
					"into a bug report", key)
			}
		}
	}
	// And the other direction: a name in the list that is not a setting any
	// more redacts nothing.
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

// The machine contract carries the settings problem too.
//
// Standard error is not visible to the one caller the contract exists for. A
// script that runs `gropius config show --json 2>/dev/null` and reads the bind
// address out of it would be told "127.0.0.1" about a server that may be
// answering the whole network: when config.json is unusable the settings
// reported are the fail-closed FALLBACK the server would start from, not what
// the file holds. The problem has to travel in the document as well.
func TestConfigShowJSONCarriesTheSettingsProblem(t *testing.T) {
	env, out, _ := configShowEnv(t, sampleSettings())
	env.SettingsProblem = "config.json is not a valid configuration — the bind address is locked down"
	if code := RunConfig(env, []string{"show", "--json"}); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	var doc struct {
		Problem string `json:"settings_problem"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Problem, "locked down") {
		t.Errorf("the contract does not carry the settings problem, so a caller reading it with standard "+
			"error closed is told the fallback is the truth: %q", doc.Problem)
	}

	// And it is absent when there is nothing to say, so its presence means
	// something rather than being a field every caller has to test.
	env, clean, _ := configShowEnv(t, sampleSettings())
	if code := RunConfig(env, []string{"show", "--json"}); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(clean.String(), "settings_problem") {
		t.Errorf("a clean read still carries a settings_problem field:\n%s", clean.String())
	}
}

// The guard above walks a VALUE, so it can only see the fields that value
// populates — and indirectValue stops at a nil pointer rather than recursing
// into the type behind it. A pointer-to-struct field left nil in the fixture
// would therefore emit one null key, its inner fields never reaching the
// secret check, while an operator who set it would have those fields printed.
//
// No such field exists in config.Config today, so rather than build a
// type-driven second walk for a shape nothing has, this fails the day one
// arrives — with the reason, and pointing at the guard that would go quiet.
func TestNoSettingHidesItsFieldsBehindAPointer(t *testing.T) {
	var walk func(rt reflect.Type, path string, depth int)
	walk = func(rt reflect.Type, path string, depth int) {
		if depth > 8 {
			return
		}
		for i := range rt.NumField() {
			f := rt.Field(i)
			if f.PkgPath != "" {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			here := path + name
			if f.Type.Kind() == reflect.Pointer && f.Type.Elem().Kind() == reflect.Struct {
				t.Errorf("config.Config reaches %q through a pointer to a struct. SettingsInForce reports a "+
					"nil one as a single null and never walks inside it, so TestEverySettingThatLooksLikeASecret"+
					"IsRedacted would stop seeing its fields — give the walk a type-driven half before adding "+
					"this shape", here)
				continue
			}
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			switch {
			case ft.Kind() == reflect.Map && ft.Elem().Kind() == reflect.Struct:
				walk(ft.Elem(), here+".*.", depth+1)
			case ft.Kind() == reflect.Struct:
				walk(ft, here+".", depth+1)
			}
		}
	}
	walk(reflect.TypeOf(config.Config{}), "", 0)
}
