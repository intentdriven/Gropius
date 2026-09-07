package archtest_test

import (
	"path/filepath"
	"strconv"
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
	reference := readDoc(t, "statistics-store-reference.md")

	// 1. Every field of a record is named on the reference page, and no field
	//    that does not exist is. Read off the record itself, so a field added
	//    without a word about it fails here rather than shipping
	//    undocumented.
	for _, field := range stats.RecordFields() {
		if !strings.Contains(reference, "`"+field+"`") {
			t.Errorf("the reference page does not name %q, which every record carries", field)
		}
	}
	for _, name := range backticked(reference) {
		if strings.Contains(name, "_") && !recorded(name) && !knownOther(name) {
			t.Errorf("the reference page names %q as if it were recorded; no such field exists", name)
		}
	}
	// And the how-to points at it rather than repeating it.
	if !strings.Contains(page, "statistics-store-reference.md") {
		t.Error("the how-to does not point at the page that says what is recorded, field by field")
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

// recorded reports whether a name is a field that is written down somewhere:
// on a row of the live view, or on a line of the store. The page describes
// both, so both are the page's to name.
func recorded(field string) bool {
	for _, f := range stats.RecordFields() {
		if f == field {
			return true
		}
	}
	for _, f := range stats.StoreFields() {
		if f == field {
			return true
		}
	}
	return false
}

// The same rule for the durable store: every field a line can carry is
// described on the page, and no field the page describes has gone. A store's
// files outlive the build that wrote them and are read with other people's
// tools, so an undocumented field is worse here than anywhere else.
func TestTheStatisticsPageNamesEveryFieldTheStoreWrites(t *testing.T) {
	page := readDoc(t, "statistics-store-reference.md")
	for _, field := range stats.StoreFields() {
		if !strings.Contains(page, "`"+field+"`") {
			t.Errorf("the page does not name %q, which a line of the store can carry", field)
		}
	}
	for _, reason := range stats.RemovalReasons() {
		if !strings.Contains(page, "`"+reason+"`") {
			t.Errorf("the page does not name %q, which a removal line can carry as its reason", reason)
		}
	}
	for _, class := range stats.OutcomeClasses() {
		if !strings.Contains(page, "`"+string(class)+"`") {
			t.Errorf("the page does not name %q, which a request line can carry as its class", class)
		}
	}
	for _, kind := range stats.StoreKinds() {
		if !strings.Contains(page, "`"+kind+"`") {
			t.Errorf("the page does not name %q, which is one of the kinds of line the store writes", kind)
		}
	}
	// And the file naming and the two limits, which are the rest of what
	// someone pointing their own tools at the files has to know.
	for _, phrase := range []string{
		"stats-YYYYMMDD-NNN.jsonl",
		"one JSON object per line",
		"the oldest file goes",
	} {
		if !containsAll(page, phrase) {
			t.Errorf("the page does not say %q", phrase)
		}
	}
}

// knownOther are the backticked names on the page that are not record fields:
// the settings file's own key for the switch, the request field the gateway
// sets on a streamed request, and the reasons a model server can leave memory,
// which are values of a field rather than fields.
func knownOther(name string) bool {
	switch name {
	case "stream_options", "include_usage", "config_json":
		return true
	}
	for _, kind := range stats.StoreKinds() {
		if kind == name {
			return true
		}
	}
	for _, reason := range stats.RemovalReasons() {
		if reason == name {
			return true
		}
	}
	for _, class := range stats.OutcomeClasses() {
		if string(class) == name {
			return true
		}
	}
	return false
}

// docs/ carries one Diataxis type per page. The switch's page is a how-to — a
// procedure a reader follows — and everything a reader consults rather than
// follows is on the reference page beside it.
func TestTheStatisticsPagesAreOneTypeEach(t *testing.T) {
	page := readDoc(t, "request-statistics.md")
	if !strings.Contains(page, "\n1. ") {
		t.Error("the how-to has no numbered procedure, so it is not a how-to")
	}
	if strings.Contains(page, "| Field |") {
		t.Error("the how-to carries a reference table; a how-to is a procedure")
	}
	reference := readDoc(t, "statistics-store-reference.md")
	if !strings.Contains(reference, "| Field |") {
		t.Error("the reference page has no field table; a reference is something a reader consults")
	}
	if strings.Contains(reference, "\n1. Open the control panel") {
		t.Error("the reference page carries a procedure; that belongs on the how-to")
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

// The arithmetic a person's retention settings rest on is one figure, measured
// rather than guessed, and it says the same thing everywhere it appears: on
// the two pages a reader sees and in the comment beside the defaults
// themselves.
func TestTheRecordSizeIsTheSameFigureEverywhere(t *testing.T) {
	want := strconv.Itoa(stats.ApproxRecordBytes)
	for _, doc := range []string{"statistics-store-reference.md", "request-statistics.md"} {
		if !strings.Contains(readDoc(t, doc), want+" bytes") {
			t.Errorf("%s does not say a record measures %s bytes, which is what it does", doc, want)
		}
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readRepoFile(t, repoRoot, "internal/config/config.go"), want+" bytes") {
		t.Error("the comment beside the retention defaults reasons from a different record size")
	}
	// The figure the branch replaced, which the format cannot reach.
	for _, doc := range []string{"statistics-store-reference.md", "request-statistics.md"} {
		if strings.Contains(readDoc(t, doc), "150 bytes") {
			t.Errorf("%s still reasons from 150 bytes a record", doc)
		}
	}
}

// The summary's arithmetic is held to the code the same way a record's is. The
// page tells a reader how far back a summary reaches before they set a limit,
// and a bound changed in one place and not the other would make that a guess.
func TestTheSummarySizeIsTheSameFigureEverywhere(t *testing.T) {
	page := readDoc(t, "statistics-store-reference.md")
	if want := strconv.Itoa(stats.ApproxSummaryBytes); !strings.Contains(page, want+" bytes") {
		t.Errorf("the reference page does not say a summary line is at most %s bytes, which is what it is", want)
	}
	// The default cap the page itself names, a twentieth of it for the
	// summary, one line per model per day, ten models — which is the shape the
	// page's sentence is about.
	const defaultCap = 200 << 20
	const modelsADay = 10
	years := stats.SummaryShareOfCap(defaultCap) / stats.ApproxSummaryBytes / modelsADay / 365
	if years <= 0 {
		t.Fatalf("the summary's share of the default cap holds less than a year of days: %d bytes",
			stats.SummaryShareOfCap(defaultCap))
	}
	if want := strconv.FormatInt(years, 10) + " years"; !strings.Contains(page, want) {
		t.Errorf("the reference page does not say the summary holds %q at the default limit", want)
	}
	// The figure the branch replaced, which the widest line is well past.
	if strings.Contains(page, "decades of days") {
		t.Error("the reference page still promises decades of days, which the measured line size does not reach")
	}
}
