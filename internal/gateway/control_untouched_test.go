package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// A cross-field refusal names a field that changed in this save.
//
// A cross-field rule is refused in the words the RULE is about, which are not
// the words the operator touched. Switching the bind from this Mac to the
// whole network, with the eviction grace already on and no API key stored, is
// refused with "eviction grace needs an API key on a LAN-exposed server" — a
// sentence about two settings the operator did not touch, and silent about
// the one they did. They go and look at the grace, which is not what went
// wrong.
//
// This is the third wedge incident's shape (AGENTS.md: "a save must never be
// refused over a setting the operator did not touch"), and the criterion it
// arms is the one that says a refusal names a field that changed in this save
// — or the field whose change made an unchanged one unsafe — and never an
// unchanged field alone.
func TestACrossFieldRefusalNamesAChangedField(t *testing.T) {
	for _, tc := range []struct {
		name string
		// stored is the configuration in force, which is one the load path
		// accepts: every case below is refused by a rule that only fires once
		// the posted change is folded in.
		stored func() config.Config
		posted string
		// changed is the config.json key this save changes. The refusal has to
		// name it.
		changed string
		// rule is a phrase from the cross-field refusal itself, so the test
		// fails loudly if the save is refused by some other rule and the
		// assertion below passes for the wrong reason.
		rule string
	}{
		{
			// The bind is the changed field, and the grace and the key — the
			// two the message is written about — are exactly as they were.
			name: "widening the bind under a grace that was safe on loopback",
			stored: func() config.Config {
				c := config.Default()
				c.Host = "127.0.0.1"
				c.APIKey = ""
				c.EvictionGrace = true
				return c
			},
			posted:  `{"host":"0.0.0.0","bind_mode":"","port":11535,"api_key":"","eviction_grace":true,"decode_concurrency":4,"idle_timeout_sec":0,"stats_months":6,"stats_max_bytes":209715200}`,
			changed: "host",
			rule:    "eviction grace needs an API key",
		},
		{
			name: "switching the grace on with no key on an exposed server",
			stored: func() config.Config {
				c := config.Default()
				c.Host = "0.0.0.0"
				c.APIKey = ""
				c.EvictionGrace = false
				return c
			},
			posted:  `{"host":"0.0.0.0","bind_mode":"","port":11535,"api_key":"","eviction_grace":true,"decode_concurrency":4,"idle_timeout_sec":0,"stats_months":6,"stats_max_bytes":209715200}`,
			changed: "eviction_grace",
			rule:    "eviction grace needs an API key",
		},
		{
			name: "lowering the idle timeout under the grace it protects",
			stored: func() config.Config {
				c := config.Default()
				c.Host = "127.0.0.1"
				c.APIKey = "a-key"
				c.EvictionGrace = true
				c.EvictionGraceSec = 300
				c.EvictionMaxWaitSec = 300
				c.IdleTimeoutSec = 0
				return c
			},
			posted:  `{"host":"127.0.0.1","bind_mode":"","port":11535,"api_key":"a-key","eviction_grace":true,"eviction_grace_sec":300,"eviction_max_wait_sec":300,"decode_concurrency":4,"idle_timeout_sec":60,"stats_months":6,"stats_max_bytes":209715200}`,
			changed: "idle_timeout_sec",
			rule:    "must not be longer than the idle timeout",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestControl(t, tc.stored())
			resp := postJSON(t, srv, "/api/settings", tc.posted)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: this save is refused by a cross-field rule", resp.StatusCode)
			}
			msg := refusalText(t, resp)
			if !strings.Contains(msg, tc.rule) {
				t.Fatalf("the save was refused by some other rule than the one under test:\n%s", msg)
			}
			if !strings.Contains(msg, tc.changed) {
				t.Errorf("the refusal names no field this save changed — %q is what moved, and the "+
					"operator is sent to look at settings they did not touch:\n%s", tc.changed, msg)
			}
		})
	}
}

// refusalText is the message a refused control-plane call reports.
func refusalText(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.Error.Message == "" {
		return string(b)
	}
	return out.Error.Message
}

// A save of an unedited form is accepted, for every configuration the load
// path accepts — including the ones only a hand edit or another Mac's file
// produces.
//
// This is the promise AGENTS.md records as a wedge built three times: "a save
// must never be refused over a setting the operator did not touch". Each of
// those three was the same shape — the operator opened Settings to change one
// thing, and Save answered with a message about a field they had never seen —
// and the stored value that wedged it was one the panel itself could not
// write. So the table below is deliberately made of values a hand edit
// produces: a bind the select never offered, a setting the form does not own,
// a sampling parameter stored at zero.
//
// The body is built the way the form builds it, from the settings the panel is
// served: the redacted secrets it echoes back, the resolved chat rule it shows,
// and only the fields it owns.
func TestASaveOfAnUneditedFormIsAccepted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stored func(config.Config) config.Config
	}{
		{"the shipped defaults", func(c config.Config) config.Config { return c }},
		{
			// iss-2609081742422989's shape: a host config.json accepts and the
			// select never offered.
			name:   "a bind host the select does not offer",
			stored: func(c config.Config) config.Config { c.Host = "localhost"; return c },
		},
		{
			name:   "an IPv6 loopback in its bracketed spelling",
			stored: func(c config.Config) config.Config { c.Host = "[::1]"; return c },
		},
		{
			name: "the private-network mode over a loopback host",
			stored: func(c config.Config) config.Config {
				c.Host, c.BindMode = "127.0.0.1", config.BindModePrivateNetwork
				return c
			},
		},
		{
			// Two settings the form does not own at all, and cannot: the
			// exemptions itd-2609081259493890 writes down.
			name: "settings the form does not own",
			stored: func(c config.Config) config.Config {
				c.UpstreamHeaderTimeoutSec = 45
				c.Preload = []string{"mlx-community/Qwen3-8B-4bit"}
				return c
			},
		},
		{
			name: "both secrets set",
			stored: func(c config.Config) config.Config {
				c.APIKey, c.HFToken = "bh_0123456789abcdef", "hf_0123456789abcdef"
				return c
			},
		},
		{
			// Zero is a real sampling value and not an absence, and every one
			// of these sits exactly on a bound.
			name: "the sampling defaults at their edges",
			stored: func(c config.Config) config.Config {
				zero, one, k, tokens := 0.0, 1.0, 0, 1
				c.Sampling = config.Sampling{Temperature: &zero, TopP: &one, TopK: &k, MinP: &zero, MaxTokens: &tokens}
				return c
			},
		},
		{
			name: "the eviction grace on, with the key it needs",
			stored: func(c config.Config) config.Config {
				c.APIKey = "bh_0123456789abcdef"
				c.EvictionGrace = true
				c.EvictionGraceSec, c.EvictionMaxWaitSec = 300, 600
				c.IdleTimeoutSec = 900
				return c
			},
		},
		{
			name: "the statistics store at its floor",
			stored: func(c config.Config) config.Config {
				c.Statistics = true
				c.StatsMonths, c.StatsMaxBytes = 1, config.MinStatsMaxBytes
				return c
			},
		},
		{
			name:   "the detailed log level",
			stored: func(c config.Config) config.Config { c.LogLevel = config.LogLevelDetailed; return c },
		},
		{
			name:   "a memory budget of the operator's own",
			stored: func(c config.Config) config.Config { c.MaxResidentBytes = 12 << 30; return c },
		},
		{
			// A figure the panel refused to post until its own ceiling was
			// removed, and the server always accepted.
			name:   "a decode concurrency above what the panel used to allow",
			stored: func(c config.Config) config.Config { c.DecodeConcurrency = 128; return c },
		},
		{
			name: "a chat rule of the operator's own",
			stored: func(c config.Config) config.Config {
				c.ChatRule = config.ChatRule{
					PipelineTags: []string{"text-generation"},
					RequiredTags: []string{"conversational"},
				}
				return c
			},
		},
		{
			name: "per-model settings on a model this Mac has not downloaded",
			stored: func(c config.Config) config.Config {
				half := 0.5
				c.Models = map[string]config.ModelSettings{
					"mlx-community/Qwen3-8B-4bit": {
						MergeSystemMessages: true,
						Pinned:              true,
						ServedContext:       8192,
						Sampling:            config.Sampling{Temperature: &half},
					},
				}
				return c
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored := tc.stored(config.Default())
			// The premise, asserted rather than assumed: every row is a
			// configuration the load path accepts. A row the server would
			// refuse at startup would make this test pass for the wrong
			// reason — there would be no promise to keep.
			if err := stored.Validate(); err != nil {
				t.Fatalf("this row is not a configuration the load path accepts: %v", err)
			}

			srv, a := newTestControlApp(t, stored)
			resp := postJSON(t, srv, "/api/settings", uneditedFormBody(t, stored))
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("an unedited form was refused with %d: %s", resp.StatusCode, refusalText(t, resp))
			}
			// And the save must not have moved anything: a form that is
			// accepted and then rewrites a setting nobody touched is the same
			// fault wearing a success.
			if got := a.Config(); got.Host != stored.Host || got.BindMode != stored.BindMode {
				t.Errorf("the bind moved on an unedited save: host %q -> %q, mode %q -> %q",
					stored.Host, got.Host, stored.BindMode, got.BindMode)
			}
		})
	}
}

// A setting the form does not own survives any save it makes.
//
// The two exempted settings have no control by decision, so the ONLY thing
// standing between them and an unrelated save is the write path keeping what
// the body left out. It has been wrong before: decoding a posted body into a
// zero configuration wiped a preload list on every settings change.
func TestASettingTheFormDoesNotOwnSurvivesASave(t *testing.T) {
	stored := config.Default()
	stored.UpstreamHeaderTimeoutSec = 45
	stored.Preload = []string{"mlx-community/Qwen3-8B-4bit", "mlx-community/Qwen3-4B-4bit"}

	srv, a := newTestControlApp(t, stored)
	// A save that changes something the form DOES own, which is the ordinary
	// way an operator reaches this path.
	body := strings.Replace(uneditedFormBody(t, stored), `"port":11535`, `"port":11536`, 1)
	resp := postJSON(t, srv, "/api/settings", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, refusalText(t, resp))
	}

	got := a.Config()
	if got.UpstreamHeaderTimeoutSec != stored.UpstreamHeaderTimeoutSec {
		t.Errorf("upstream_header_timeout_sec = %d after a save that never named it, want %d",
			got.UpstreamHeaderTimeoutSec, stored.UpstreamHeaderTimeoutSec)
	}
	if strings.Join(got.Preload, ",") != strings.Join(stored.Preload, ",") {
		t.Errorf("preload = %v after a save that never named it, want %v", got.Preload, stored.Preload)
	}
	if got.Port != 11536 {
		t.Errorf("port = %d; the save that carried these through changed nothing at all", got.Port)
	}
}

// uneditedFormBody is what the Settings form posts for a stored configuration
// with no field edited.
//
// It is the panel's submit body written in Go, and it models the three things
// the round trip does to a value on its way through the form: the secrets
// arrive redacted and are echoed back as the placeholder, the chat rule
// arrives resolved because the form posts back what it shows, and the fields
// the form does not own are not named at all.
//
// A mirror of another surface is a thing that can drift, and this one is held
// in place from both ends: internal/archtest fails when the pane stops naming
// a setting, and TestTheSettingsPaneNamesNoSettingTheConfigurationDoesNotHold
// fails when it names one that does not exist.
func uneditedFormBody(t *testing.T, stored config.Config) string {
	t.Helper()
	shown := redactConfig(stored)

	host, mode := shown.Host, ""
	if shown.BindMode == config.BindModePrivateNetwork {
		mode = config.BindModePrivateNetwork
	}
	body := map[string]any{
		"host":                  host,
		"bind_mode":             mode,
		"port":                  shown.Port,
		"advertise":             shown.Advertise,
		"api_key":               shown.APIKey,
		"idle_timeout_sec":      shown.IdleTimeoutSec,
		"decode_concurrency":    shown.DecodeConcurrency,
		"hf_token":              shown.HFToken,
		"max_resident_bytes":    shown.MaxResidentBytes,
		"eviction_grace":        shown.EvictionGrace,
		"eviction_grace_sec":    shown.EvictionGraceSec,
		"eviction_max_wait_sec": shown.EvictionMaxWaitSec,
		"chat_rule":             shown.ChatRule,
		"statistics":            shown.Statistics,
		"stats_months":          shown.StatsMonths,
		// Typed in megabytes and stored in bytes, so a figure that is not a
		// whole number of megabytes comes back rounded — which is itself a
		// value the save has to accept.
		"stats_max_bytes": ((shown.StatsMaxBytes + (1 << 19)) >> 20) << 20,
		"log_level":       shown.EffectiveLogLevel(),
		"sampling":        shown.Sampling,
		"models":          modelsOrEmpty(shown.Models),
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// modelsOrEmpty is the per-model map as the form posts it: the whole map, and
// an empty object rather than null when there is nothing in it.
func modelsOrEmpty(m map[string]config.ModelSettings) map[string]config.ModelSettings {
	if m == nil {
		return map[string]config.ModelSettings{}
	}
	return m
}
