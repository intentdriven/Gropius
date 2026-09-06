package archtest_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/stats"
)

// The page beside the switch promises a reader four things, and each of the
// four is something the code decides. A page that drifts from any of them is
// worse than no page: recording is an opt-in, and an opt-in is only as good as
// what the person switching it on was told.
func TestTheStatisticsPageNamesEveryFieldThatIsRecorded(t *testing.T) {
	page := readDoc(t, "request-statistics.md")

	// 1. Every field of a record is named on the page, and no field that does
	//    not exist is. Read off the record itself, so a field added without a
	//    word about it fails here rather than shipping undocumented.
	for _, field := range stats.RecordFields() {
		if !strings.Contains(page, "`"+field+"`") {
			t.Errorf("the page does not name %q, which every record carries", field)
		}
	}
	for _, name := range backticked(page) {
		if strings.Contains(name, "_") && !recorded(name) && !knownOther(name) {
			t.Errorf("the page names %q as if it were recorded; no such field exists", name)
		}
	}

	// 2. What is never recorded, in the reader's words rather than the code's.
	if !containsAll(page, "never", "prompt", "answer", "API key", "address") {
		t.Error("the page does not state that prompts, answers, keys and client addresses are never recorded")
	}

	// 3. That nothing recorded leaves the Mac.
	if !containsAll(page, "leaves this Mac") {
		t.Error("the page does not state that nothing recorded leaves this Mac")
	}

	// 4. What the process still writes while the switch is off — which is the
	//    honest half of "off is identical to today", and is exactly the line
	//    cmd/gropius writes.
	if !containsAll(page, "method, path, status and duration") {
		t.Error("the page does not say what the process logs while the switch is off")
	}
	if !containsAll(page, "off until you turn it on") {
		t.Error("the page does not state that recording is off until it is turned on")
	}
}

func recorded(field string) bool {
	for _, f := range stats.RecordFields() {
		if f == field {
			return true
		}
	}
	return false
}

// knownOther are the backticked names on the page that are not record fields:
// the settings file's own key for the switch, and the request field the
// gateway sets on a streamed request.
func knownOther(name string) bool {
	switch name {
	case "stream_options", "include_usage", "config_json":
		return true
	}
	return false
}

// docs/ carries one Diataxis type per page, and this one is a how-to: a
// procedure a reader follows, not a table they consult.
func TestTheStatisticsPageIsAHowTo(t *testing.T) {
	page := readDoc(t, "request-statistics.md")
	if !strings.Contains(page, "\n1. ") {
		t.Error("the page has no numbered procedure, so it is not a how-to")
	}
	if strings.Contains(page, "| Field |") {
		t.Error("the page carries a reference table; a how-to is a procedure")
	}
}

func TestGettingStartedPointsAtTheStatisticsPage(t *testing.T) {
	if !strings.Contains(readDoc(t, "getting-started.md"), "request-statistics.md") {
		t.Error("the getting-started walk-through does not point at the statistics page")
	}
}

// The page names all three states and keeps them apart, because the reason
// they are three is the whole reason recording could be made an opt-in without
// also handing the operator a prompt transcript.
func TestTheStatisticsPageNamesTheThreeStates(t *testing.T) {
	page := readDoc(t, "request-statistics.md")
	for _, phrase := range []string{
		"three states",
		"model server's own log level",
		"this switch can never make it",
	} {
		if !containsAll(page, phrase) {
			t.Errorf("the page does not say %q", phrase)
		}
	}
}

// And it says who else on this Mac can turn it on and read it. The control
// plane asks nobody for a password and answers every local account by design,
// so "nothing leaves this Mac" is true and, on a shared Mac, not the whole
// answer.
func TestTheStatisticsPageSaysWhoCanSeeIt(t *testing.T) {
	page := readDoc(t, "request-statistics.md")
	for _, phrase := range []string{
		"Who can see it",
		"any of them can open the control panel",
		"The boundary is the Mac, not your account",
	} {
		if !containsAll(page, phrase) {
			t.Errorf("the page does not say %q", phrase)
		}
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(readRepoFile(t, repoRoot, "README.md"), "The boundary is the Mac rather than your account") {
		t.Error("the README claims a record for one account alone")
	}
}
