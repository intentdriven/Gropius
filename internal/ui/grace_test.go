package ui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// The form owns three fields, and a save that left any of them out would
// switch the feature off — the settings endpoint replaces what the form sends.
func TestSettingsFormPostsTheEvictionGraceFields(t *testing.T) {
	src := readPanelSource(t)
	for _, field := range []string{"eviction_grace:", "eviction_grace_sec:", "eviction_max_wait_sec:"} {
		if !strings.Contains(src, field) {
			t.Errorf("the settings form does not post %s", field)
		}
	}
	for _, id := range []string{"setGrace", "setGraceSec", "setGraceWait"} {
		if !strings.Contains(src, "'"+id+"'") {
			t.Errorf("the panel never reads %s, so the field cannot reach a save", id)
		}
	}
}

// The browser refuses a form whose number is outside a field's own min/max
// before the submit listener runs, so a bound here that is tighter than the
// server's would make a figure saved by any other client unsavable from the
// panel — the wedge step="0.1" was on the budget field. Bound the two fields
// to the ceiling the server actually enforces, read from the server's own
// constant so the two cannot drift.
func TestTheEvictionGraceFieldsAreBoundedByTheServersOwnCeiling(t *testing.T) {
	page, err := assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"setGraceSec", "setGraceWait"} {
		field := fieldTag(t, string(page), id)
		if got := attr(field, "min"); got != "0" {
			t.Errorf("%s has min=%q, want 0 — zero is how the panel says \"the default\"", id, got)
		}
		want := strconv.Itoa(config.MaxEvictionWaitSec)
		if got := attr(field, "max"); got != want {
			t.Errorf("%s has max=%q, want the server's ceiling %s", id, got, want)
		}
	}
}

// A queue of requests waiting for room is live state, not a stored setting, so
// it is drawn where the models are and not behind the form's editing guard.
func TestThePanelSaysHowManyRequestsAreWaitingForRoom(t *testing.T) {
	for _, tc := range []struct {
		waiting int
		want    string
	}{
		{0, ""},
		{1, "1 request is waiting for memory to free up."},
		{4, "4 requests are waiting for memory to free up."},
	} {
		t.Run(fmt.Sprintf("%d waiting", tc.waiting), func(t *testing.T) {
			got := evalPanel(t, fmt.Sprintf("waitingLine(%d)", tc.waiting), "waitingLine")
			if got != tc.want {
				t.Errorf("waitingLine(%d) = %q, want %q", tc.waiting, got, tc.want)
			}
		})
	}
}

// fieldTag returns the whole <input> tag carrying the given id.
func fieldTag(t *testing.T, page, id string) string {
	t.Helper()
	re := regexp.MustCompile(`<input id="` + regexp.QuoteMeta(id) + `"[^>]*>`)
	m := re.FindString(page)
	if m == "" {
		t.Fatalf("no input with id %q in the control panel", id)
	}
	return m
}

// attr reads one attribute out of a tag.
func attr(tag, name string) string {
	re := regexp.MustCompile(name + `="([^"]*)"`)
	m := re.FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	return m[1]
}
