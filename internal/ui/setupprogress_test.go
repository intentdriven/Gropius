package ui

import (
	"strings"
	"testing"
)

// The panel shows the same proportion the terminal does.
//
// `gropius install` provisions in the foreground and prints how many of the
// stages are done; the panel reads the same SetupStatus over /api/state. A
// proportion on one surface and a bare spinner on the other would be a Go
// capability with no panel equivalent, which this repository counts as a gap
// rather than a feature tier — so the banner's heading carries the count.
func TestTheSetupBannerNamesHowFarProvisioningHasGot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		want   string
	}{
		{"running, two of three done", `{"stage":"installing MLX","step":2,"steps":3}`, "Setting up: installing MLX… (2 of 3 done)"},
		{"running, nothing done yet", `{"stage":"installing uv","step":0,"steps":3}`, "Setting up: installing uv… (0 of 3 done)"},
		// A failure says so rather than reporting progress: the count belongs
		// to work in progress, and the banner's error line carries the rest.
		{"failed", `{"stage":"failed","step":1,"steps":3}`, "Setup failed"},
		// A server that does not report the count — an older build answering a
		// newer panel — gets the heading it always had rather than "0 of 0".
		{"no count reported", `{"stage":"installing uv"}`, "Setting up: installing uv…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := evalPanel(t, "setupHeading("+tc.status+")", "setupHeading")
			if got != tc.want {
				t.Errorf("setupHeading = %q, want %q", got, tc.want)
			}
		})
	}
}

// And the banner is built from that function rather than assembling the
// heading a second time, which is what keeps the test above worth anything.
func TestTheSetupBannerUsesThatHeading(t *testing.T) {
	body := extractFunction(t, readPanelSource(t), "renderSetup")
	if !strings.Contains(body, "$('setupStage').textContent = setupHeading(s);") {
		t.Error("renderSetup no longer builds its heading with setupHeading, so what the banner says is asserted by nothing")
	}
}
