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
