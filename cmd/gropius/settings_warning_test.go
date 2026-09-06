package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// A sampling preference the model server would refuse is dropped rather than
// applied, and dropping it silently would leave a setting that shows as blank
// in the panel with no explanation of where it went.
func TestDroppedSettingsAreReported(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	warnDroppedSettings(log, nil)
	if buf.Len() != 0 {
		t.Errorf("logged %q with nothing dropped", buf.String())
	}

	warnDroppedSettings(log, []string{"sampling.top_p", "model_sampling[org/x].top_k"})
	out := buf.String()
	if !strings.Contains(out, "sampling.top_p") || !strings.Contains(out, "model_sampling[org/x].top_k") {
		t.Errorf("log line %q does not name the dropped fields", out)
	}
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("log line %q is not a warning", out)
	}
}
