package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// A sampling preference the model server would refuse is dropped rather than
// applied, and dropping it silently would leave a setting that shows as blank
// in the panel with no explanation of where it went.
func TestIgnoredSettingsAreReported(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	warnStartupNotices(log, config.Notices{})
	if buf.Len() != 0 {
		t.Errorf("logged %q with nothing changed", buf.String())
	}

	warnStartupNotices(log, config.Notices{
		Ignored: []string{"sampling.top_p", "model_sampling[org/x].top_k"},
	})
	out := buf.String()
	if !strings.Contains(out, "sampling.top_p") || !strings.Contains(out, "model_sampling[org/x].top_k") {
		t.Errorf("log line %q does not name the ignored fields", out)
	}
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("log line %q is not a warning", out)
	}
}

// A repaired setting is in force in a changed form, and saying it was ignored
// is not a wording quibble: the operator whose API key was trimmed is being
// told the key is not in use, when it is the only key that now works. The two
// are reported as separate lines, each true of what it names.
func TestRepairedSettingsAreReportedAsInForce(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	warnStartupNotices(log, config.Notices{
		Ignored:  []string{"pinned[../../etc]"},
		Repaired: []string{"api_key (trimmed to the 512-byte ceiling)"},
	})

	var ignored, repaired string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		switch {
		case strings.Contains(line, "pinned[../../etc]"):
			ignored = line
		case strings.Contains(line, "api_key"):
			repaired = line
		}
	}
	if ignored == "" {
		t.Fatalf("no line named the ignored setting: %q", buf.String())
	}
	if repaired == "" {
		t.Fatalf("no line named the repaired setting: %q", buf.String())
	}
	if ignored == repaired {
		t.Error("one line reported both, so whichever wording it carries is wrong about the other")
	}
	if strings.Contains(repaired, "ignor") {
		t.Errorf("the repaired line %q says the setting was ignored; it is in force", repaired)
	}
	if strings.Contains(repaired, "model server") {
		t.Errorf("the repaired line %q blames the model server for a limit Gropius chose", repaired)
	}
	if !strings.Contains(repaired, "in force") {
		t.Errorf("the repaired line %q does not say the setting is in force", repaired)
	}
}
