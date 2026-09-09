package archtest_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/applog"
	"github.com/intentdriven/Gropius/internal/config"
)

// The logging page is a reference page, and a reference page that prints a
// figure the code does not use is worse than one that prints none: an operator
// sizing a disk, or waiting for a rotation that is not coming, is acting on
// this page.
func TestTheLoggingPagePrintsTheFiguresTheCodeUses(t *testing.T) {
	page := readDoc(t, "logging.md")

	for _, want := range []string{
		applog.DefaultName,
		fmt.Sprintf("%d MB", applog.DefaultRotateBytes>>20),
		fmt.Sprintf("%d MB", (applog.DefaultRotateBytes>>20)*applog.DefaultKeep),
		"Five files",
	} {
		if !containsAll(page, want) {
			t.Errorf("docs/logging.md does not print %q", want)
		}
	}
	// The rotated names are what a person looks for in the folder.
	if !containsAll(page, "gropius.1.log") {
		t.Error("docs/logging.md does not say what a rotated file is called")
	}
}

// The two words are the settings file's, the panel's and this page's. A page
// that named a third would send an operator to type something the server
// refuses.
func TestTheLoggingPageNamesTheLevelsTheCodeAccepts(t *testing.T) {
	page := readDoc(t, "logging.md")

	for _, level := range []string{config.LogLevelSparse, config.LogLevelDetailed} {
		if !containsAll(page, "`"+level+"`") {
			t.Errorf("docs/logging.md does not name the level %q", level)
		}
	}
	if !containsAll(page, "`log_level`") {
		t.Error("docs/logging.md does not name the setting log_level")
	}
	// A level the code does not accept must not appear as one it does.
	for _, notALevel := range []string{"`verbose`", "`quiet`", "`debug`", "`trace`"} {
		if strings.Contains(page, notALevel) {
			t.Errorf("docs/logging.md offers %s, which this build does not write at", notALevel)
		}
	}
}

// The promise the whole feature rests on, in the place a person goes to check
// it. It is stated here as well as in the panel because the panel is where an
// operator turns the level up and this page is where they decide whether to.
func TestTheLoggingPageStatesWhatIsNeverWritten(t *testing.T) {
	page := readDoc(t, "logging.md")

	for _, want := range []string{
		"No prompt",
		"No answer",
		"No API key",
		"No client address",
		"never written",
	} {
		if !containsAll(page, want) {
			t.Errorf("docs/logging.md does not say %q", want)
		}
	}
	// And the sentence that stops the commonest wrong assumption: that this
	// level reaches the model servers. adr-2609061503319212 turns on it, and
	// internal/archtest/statistics_switch_test.go arms the code side.
	for _, want := range []string{"model servers", "INFO"} {
		if !containsAll(page, want) {
			t.Errorf("docs/logging.md does not distinguish this level from the model servers': missing %q", want)
		}
	}
}

// One Diátaxis type per page. This is a reference: it says what is, it does not
// walk a reader through a task.
func TestTheLoggingPageStaysAReference(t *testing.T) {
	page := readDoc(t, "logging.md")

	if !strings.HasPrefix(page, "# Reference:") {
		t.Errorf("docs/logging.md does not start with a reference title: %.60q", page)
	}
	if strings.Contains(page, "\n1. ") {
		t.Error("docs/logging.md carries a numbered procedure; a how-to is a different page")
	}
}

// A page nothing links is a page nobody finds. README indexes the features and
// getting-started is the hub every other page hangs off.
func TestTheLoggingPageIsLinked(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readRepoFile(t, repoRoot, "README.md"), "docs/logging.md") {
		t.Error("README.md does not link docs/logging.md")
	}
	if !strings.Contains(readDoc(t, "getting-started.md"), "logging.md") {
		t.Error("the getting-started walk-through does not point at the logging page")
	}
}
