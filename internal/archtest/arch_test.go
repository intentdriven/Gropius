package archtest_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The UI toolkit (systray/AppKit, via cgo) must stay confined to cmd/gropius.
// If it leaks into internal/, the business logic can no longer be tested without
// a display server, and the daemon can no longer run headless under launchd.
func TestNoGUIToolkitInInternalPackages(t *testing.T) {
	// Query the module's own internal packages (excluding this archtest package,
	// which has no non-test files and so is not a build target).
	out, err := exec.Command("go", "list",
		"github.com/intentdriven/Gropius/internal/app",
		"github.com/intentdriven/Gropius/internal/config",
		"github.com/intentdriven/Gropius/internal/discovery",
		"github.com/intentdriven/Gropius/internal/gateway",
		"github.com/intentdriven/Gropius/internal/hub",
		"github.com/intentdriven/Gropius/internal/registry",
		"github.com/intentdriven/Gropius/internal/runtime",
		"github.com/intentdriven/Gropius/internal/ui",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	// Now list every listed package's full dependency set. Rooting the scan on
	// the enumeration above keeps each internal package covered even when — like
	// internal/ui, imported only by cmd/gropius — it is in no other internal
	// package's dependency closure.
	roots := strings.Fields(string(out))
	out, err = exec.Command("go", append([]string{"list", "-deps"}, roots...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.Contains(dep, "fyne.io/systray") {
			t.Errorf("an internal package imports %s — the GUI toolkit must stay in cmd/gropius, "+
				"or internal packages can no longer run headless or be tested without a display", dep)
		}
	}
}
