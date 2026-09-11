package lifecycle

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/intentdriven/Gropius/internal/config"
)

// `gropius config show` is the terminal's read of the settings in force.
//
// It exists because the terminal could read no setting at all. Go carries the
// whole of the functionality and the panel is the accessible layer, and
// config.json sits beside them as a third surface that is hand-editable by
// design — but somebody at a terminal who wanted to know what the file holds
// had to open the file, which on a Mac they are logged into over SSH means
// reading JSON by eye and, worse, reading the API key and the HuggingFace
// token while they are at it (itd-2609081259493890).
//
// READING ONLY, and that is a decision rather than a stage. A writing verb
// would put a second writer on a file that is single-writer state, and it
// would make the terminal a fourth surface the panel-parity obligation then
// has to cover. The question returns if an operator is found who cannot reach
// the panel at all.

// Redacted is what a secret is shown as. It is the placeholder the control
// plane serves for the same two settings, spelled again here rather than
// shared: nothing on the control plane's path may see this package, so the two
// cannot import one from the other (adr-2609111126115848 condition 3).
const Redacted = "********"

// secretSettings are the settings whose value never leaves the file.
//
// Held as accessors rather than as names so that redaction is one loop over a
// list instead of a line per field somewhere in a renderer: a renderer that
// has to remember a field is a renderer that will one day forget one, and what
// it forgets is printed to a terminal and pasted into a bug report. A setting
// added to config.json with a secret in it needs a line here, and
// TestEverySettingThatLooksLikeASecretIsRedacted is what asks for it.
var secretSettings = []struct {
	Key string
	Get func(config.Config) string
	Set func(*config.Config, string)
}{
	{"api_key", func(c config.Config) string { return c.APIKey }, func(c *config.Config, v string) { c.APIKey = v }},
	{"hf_token", func(c config.Config) string { return c.HFToken }, func(c *config.Config, v string) { c.HFToken = v }},
}

// ConfigInForce is a configuration as this verb may report it: every secret
// replaced by the placeholder, and a secret that is not set left empty so that
// "no key" and "a key you cannot see" are still different answers.
//
// The two settings that carry a default BEHIND a blank are resolved to what is
// actually in force, which is the same thing the control plane does before it
// serves the settings to the panel. An operator asking what the log level is
// wants "sparse", not an empty string they then have to know means sparse; and
// the chat rule has a shipped default that a blank pair of lists does not
// mean. A figure this package cannot resolve without reading the Mac — the
// memory budget's zero, which means the default share of this Mac's memory —
// is left as it is stored, and the reference page says what the zero means.
func ConfigInForce(c config.Config) config.Config {
	for _, s := range secretSettings {
		if s.Get(c) != "" {
			s.Set(&c, Redacted)
		}
	}
	c.LogLevel = c.EffectiveLogLevel()
	c.ChatRule = c.EffectiveChatRule()
	return c
}

// SettingsInForce is every setting the configuration holds, keyed by the path
// config.json spells it at — "port", "sampling.temperature",
// "models.<id>.pinned" — with each value as it is written in the file.
//
// Every setting, not only the ones the file happens to carry. A configuration
// written with encoding/json omits what is at its zero value, so a fresh
// install's file holds eight keys; a verb that printed those and called them
// "the settings in force" would answer a question about thirty-three settings
// with a list of eight. What is in force for the rest is their default, and
// the default is what this reports.
func SettingsInForce(c config.Config) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	collectSettings(reflect.ValueOf(ConfigInForce(c)), "", out, 0)
	return out
}

func collectSettings(rv reflect.Value, prefix string, out map[string]json.RawMessage, depth int) {
	if depth > 8 {
		return // config.Config does not nest this far; a cycle is not a setting
	}
	rt := rv.Type()
	for i := range rt.NumField() {
		f := rt.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		path, fv := prefix+name, indirectValue(rv.Field(i))
		switch fv.Kind() {
		case reflect.Struct:
			collectSettings(fv, path+".", out, depth+1)
		case reflect.Map:
			// The per-model map: each entry is a model's own settings, and the
			// id goes in the path so the reader sees which model a figure
			// belongs to rather than a blob to decode.
			if fv.Type().Elem().Kind() == reflect.Struct {
				for _, key := range fv.MapKeys() {
					collectSettings(indirectValue(fv.MapIndex(key)), path+"."+key.String()+".", out, depth+1)
				}
				continue
			}
			out[path] = encodeSetting(fv)
		default:
			out[path] = encodeSetting(fv)
		}
	}
}

// indirectValue follows a pointer to the value it holds, and yields the zero
// value of the pointed-to type for a nil one: a sampling parameter that is not
// set is reported as null, which is what the file would carry.
func indirectValue(rv reflect.Value) reflect.Value {
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return rv
		}
		rv = rv.Elem()
	}
	return rv
}

func encodeSetting(rv reflect.Value) json.RawMessage {
	b, err := json.Marshal(rv.Interface())
	if err != nil {
		return json.RawMessage(`null`)
	}
	return b
}

// RenderConfig writes the settings in force for a person to read, in the
// spelling config.json uses: somebody who reads a figure here and wants to
// change it by hand must be able to search the file for the word they just
// read.
func RenderConfig(t Terminal, settings map[string]json.RawMessage) {
	keys := make([]string, 0, len(settings))
	width := 0
	for key := range settings {
		keys = append(keys, key)
		if len(key) > width {
			width = len(key)
		}
	}
	sort.Strings(keys)

	fmt.Fprintln(t.Out, t.Paint(Dim, "the settings in force, as config.json spells them"))
	fmt.Fprintln(t.Out)
	for _, key := range keys {
		fmt.Fprintf(t.Out, "  %-*s  %s\n", width, key, settings[key])
	}
	fmt.Fprintln(t.Out)
	fmt.Fprintln(t.Out, t.Paint(Dim, "nothing was written. The API key and the HuggingFace token are shown as "+
		Redacted+" and never as their values."))
}

// RunConfig is the config verb. It reads and answers, and it writes nothing —
// the control plane stays the only writer of the settings file.
func RunConfig(env Env, args []string) int {
	if len(args) == 0 {
		writeLine(env.Err, "gropius config: say what to do — this verb reads, and "+Quote("show")+" is what it does")
		return ExitUsage
	}
	sub, rest := args[0], args[1:]
	if sub != "show" {
		writeLine(env.Err, "gropius config: "+Quote(sub)+" is not something this verb does; it reads, and "+
			Quote("show")+" is what it does")
		return ExitUsage
	}

	fs := flags("config", env.Err)
	asJSON := fs.Bool("json", false, "write the machine-readable form, which is the contract")
	if err := fs.Parse(rest); err != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		writeLine(env.Err, "gropius config show: unexpected argument "+Quote(fs.Arg(0)))
		return ExitUsage
	}

	// On Err, and before the answer, for the reason status states it there: a
	// caller piping Out into a decoder must not have to parse around a
	// warning, and the figures below may not be the ones the operator wrote.
	if env.SettingsProblem != "" {
		writeLine(env.Err, "gropius config show: "+env.SettingsProblem)
	}

	settings := SettingsInForce(env.Config)
	if *asJSON {
		if err := writeJSON(env.Out, ConfigDocument{Settings: settings, Problem: env.SettingsProblem}); err != nil {
			writeLine(env.Err, "gropius config show: "+err.Error())
			return ExitFailed
		}
		return ExitOK
	}
	RenderConfig(env.Term, settings)
	return ExitOK
}

// ConfigDocument is what `gropius config show --json` answers, and it is the
// contract: a script reads settings[<key>] by the same name config.json
// carries. Wrapped in a field of its own rather than written as a bare object
// so that a later verb can answer with something beside the settings without
// breaking every caller.
type ConfigDocument struct {
	Settings map[string]json.RawMessage `json:"settings"`
	// Problem is what reading config.json had to do to make it usable, and it
	// is in the document rather than on standard error alone because the
	// document would otherwise lie to the one caller that cannot see standard
	// error. When the file is unusable the settings reported are the
	// FALLBACK — the bind locked down to loopback, advertising off — which is
	// what the server would start from and not what the file holds. A script
	// reading the host out of this without the problem beside it would be told
	// "127.0.0.1" with full confidence about a server that may be answering
	// the whole network. Empty, and omitted, when the file loaded cleanly.
	Problem string `json:"settings_problem,omitempty"`
}
