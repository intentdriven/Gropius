package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// The settings body every other client sends. Enough for a valid save and
// nothing more, so a test can add the one field it is about. It repeats the
// fixture's own bind, so a save that changes only the level is a save that
// changes only the level — the restart flag is a disjunction over the fields
// that really need one, and a differing host here would set it for the wrong
// reason.
const levelSettings = `"host":"127.0.0.1","port":11535,"api_key":"","decode_concurrency":4,"idle_timeout_sec":0`

// levelConfig is that body's configuration.
func levelConfig() config.Config {
	cfg := config.Default()
	cfg.Host = "127.0.0.1"
	return cfg
}

// A save is never refused over a setting the operator did not touch, and the
// level is the newest field to be able to break that. A body that names no
// log_level — every client written before this field existed, and a
// hand-rolled curl — must keep the level in force rather than clearing it.
func TestASaveThatNamesNoLogLevelKeepsTheOneInForce(t *testing.T) {
	cfg := levelConfig()
	cfg.LogLevel = config.LogLevelDetailed
	srv, a := newTestControlApp(t, cfg)

	resp := postJSON(t, srv, "/api/settings", `{`+levelSettings+`}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a save that names no log_level = %d, want 200", resp.StatusCode)
	}
	if got := a.Config().EffectiveLogLevel(); got != config.LogLevelDetailed {
		t.Errorf("log_level = %q after a save that did not name it, want %q", got, config.LogLevelDetailed)
	}
}

// And a save that does name it changes it, both ways, without asking for a
// restart: the level is applied to the next line, so a panel that said
// "restart to apply" would be telling the operator to do something that is
// already done.
func TestSavingALogLevelTakesEffectWithoutARestart(t *testing.T) {
	srv, a := newTestControlApp(t, levelConfig())

	for _, level := range []string{config.LogLevelDetailed, config.LogLevelSparse} {
		resp := postJSON(t, srv, "/api/settings", `{`+levelSettings+`,"log_level":"`+level+`"}`)
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("saving log_level=%q = %d, want 200", level, resp.StatusCode)
		}
		var out struct {
			Status  string `json:"status"`
			Restart bool   `json:"restart"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if out.Restart {
			t.Errorf("saving log_level=%q asks for a restart; the level is already in force", level)
		}
		if got := a.Config().EffectiveLogLevel(); got != level {
			t.Errorf("log_level = %q after saving %q", got, level)
		}
	}
}

// A word this build does not write at is refused at the save, where the
// operator is standing, and the refusal says what the two words are. Nothing
// else in the body is applied.
func TestASaveRefusesALogLevelThisBuildDoesNotWriteAt(t *testing.T) {
	srv, a := newTestControlApp(t, levelConfig())

	resp := postJSON(t, srv, "/api/settings",
		`{"host":"127.0.0.1","port":11599,"api_key":"","decode_concurrency":4,"idle_timeout_sec":0,"log_level":"verbose"}`)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("a save with log_level=verbose was accepted")
	}
	if got := a.Config().Port; got == 11599 {
		t.Error("the refused save applied the rest of the body")
	}
}
