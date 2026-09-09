package gateway

import (
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
