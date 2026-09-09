package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// The server half of the settings pane's bind-address round trip.
//
// The panel now renders a stored host the select does not offer as an option
// of its own, so a save posts back the address in force rather than the empty
// string the browser hands out for an unoffered value. That is only worth
// doing while the server accepts what the pane sends back, so each bind
// config.Validate admits — and which the pane used to turn into "" and a 400 —
// is posted here exactly as the fixed form would post it, and must be kept.
func TestSettingsAcceptsAStoredHostThePanelNowPostsBack(t *testing.T) {
	for _, host := range []string{"0.0.0.0", "127.0.0.1", "localhost", "[::1]", "127.0.0.2", "192.0.2.5"} {
		t.Run(host, func(t *testing.T) {
			cfg := config.Default()
			cfg.Host = host
			srv, a := newTestControlApp(t, cfg)

			body := `{"host":"` + host + `","port":11535,"api_key":"","decode_concurrency":4,"idle_timeout_sec":0}`
			resp := postJSON(t, srv, "/api/settings", body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("saving an unchanged bind of %q = %d, want 200", host, resp.StatusCode)
			}
			if got := a.Config().Host; got != host {
				t.Errorf("Host = %q after saving an unchanged bind, want %q", got, host)
			}
		})
	}
}

// The server half of the third choice. The pane posts the mode in its own
// field and leaves host as it was stored, so switching the mode on must not
// disturb the bind the operator had, and switching it off must put that bind
// back (adr-2609091123526871 rule 6).
//
// A save is also never refused over a field the operator did not touch: a body
// that names no bind_mode at all keeps the one that is stored, which is what
// every configuration file written before the field existed relies on.
func TestSettingsCarriesTheBindModeWithoutDisturbingTheHost(t *testing.T) {
	cfg := config.Default()
	cfg.Host = "192.0.2.5"
	srv, a := newTestControlApp(t, cfg)

	const settings = `"port":11535,"api_key":"a-key","decode_concurrency":4,"idle_timeout_sec":0`

	resp := postJSON(t, srv, "/api/settings", `{"host":"192.0.2.5","bind_mode":"private-network",`+settings+`}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("switching the mode on = %d, want 200", resp.StatusCode)
	}
	if got := a.Config().BindMode; got != config.BindModePrivateNetwork {
		t.Errorf("BindMode = %q, want the mode the pane posted", got)
	}
	if got := a.Config().Host; got != "192.0.2.5" {
		t.Errorf("Host = %q — choosing the mode changed a field the operator did not touch, so switching it off would not put their bind back", got)
	}

	resp = postJSON(t, srv, "/api/settings", `{"host":"192.0.2.5","port":11535,"api_key":"a-key","decode_concurrency":4,"idle_timeout_sec":30}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an unrelated save = %d, want 200", resp.StatusCode)
	}
	if got := a.Config().BindMode; got != config.BindModePrivateNetwork {
		t.Errorf("BindMode = %q after a save that never named it — a field the operator did not touch was cleared", got)
	}

	resp = postJSON(t, srv, "/api/settings", `{"host":"192.0.2.5","bind_mode":"",`+settings+`}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("switching the mode off = %d, want 200", resp.StatusCode)
	}
	if got := a.Config(); got.BindMode != config.BindModeHost || got.Host != "192.0.2.5" {
		t.Errorf("after switching the mode off: BindMode = %q, Host = %q — want the bind the operator had", got.BindMode, got.Host)
	}
}

// A save answers "restart" for every field that is consumed at startup, and
// the bind is now two of them. A mode-only save that answered false told an
// API client the bind had changed when the listeners had not moved.
func TestABindModeSaveSaysARestartIsNeeded(t *testing.T) {
	cfg := config.Default()
	srv, _ := newTestControlApp(t, cfg)

	resp := postJSON(t, srv, "/api/settings", `{"host":"0.0.0.0","bind_mode":"private-network","port":11535,"api_key":"a-key","decode_concurrency":4,"idle_timeout_sec":0}`)
	defer resp.Body.Close()
	var body struct {
		Restart bool `json:"restart"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Restart {
		t.Error("restart = false after a save that changed only the bind mode — the addresses are taken at startup, so nothing about the bind changed yet")
	}
}
