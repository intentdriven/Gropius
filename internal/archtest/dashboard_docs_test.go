package archtest_test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/stats"
)

// The four historical views are the whole of the usage dashboard, and each of
// them is drawn from records that stop somewhere. A page that described the
// views without describing where they stop would be worse than no page: a
// reader would take a bounded month for a quiet one.

// Every view the panel draws is described on the page, under the heading the
// panel gives it, so a view renamed or added without a word about it fails
// here rather than shipping undescribed.
func TestTheHistoricalViewsAreEachDescribed(t *testing.T) {
	page := readDoc(t, "statistics-explained.md")
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	markup := readRepoFile(t, repoRoot, "internal/ui/static/index.html")

	for _, view := range []string{
		"Tokens per day",
		"How long requests took",
		"Time to first token, spread",
		"Evictions and reloads by hour",
	} {
		if !strings.Contains(markup, ">"+view+"<") {
			t.Errorf("the panel has no view headed %q, so the page describes one that is not there", view)
		}
		if !strings.Contains(page, view) {
			t.Errorf("the page does not describe the %q view", view)
		}
	}
}

// And what each of them cannot show, which is the half a table cannot say for
// itself.
func TestThePageSaysWhatTheViewsCannotShow(t *testing.T) {
	page := readDoc(t, "statistics-explained.md")
	for _, phrase := range []string{
		// Nothing appears that was not recorded under the opt-in.
		"while the switch was off",
		// The horizon is the store's retention, not a promise of months.
		"size cap",
		// Days past the detail keep only a coarse total, once anything
		// writes one.
		"summary totals",
		// The figures are the model server's counts and Gropius's timings,
		// not an independent measurement of either.
		"the model server's own",
		// Sums by model and by period, never by person.
		"never says who",
	} {
		if !containsAll(page, phrase) {
			t.Errorf("the page does not say %q", phrase)
		}
	}
}

// The two bounds the aggregation is held to are on the page, read off the code
// rather than remembered: a reader deciding whether a figure is the whole
// month has to know what stopped it.
func TestThePageStatesTheBoundsTheFiguresAreDrawnUnder(t *testing.T) {
	page := readDoc(t, "statistics-explained.md")
	if !strings.Contains(page, strconv.Itoa(stats.MaxHistoryDays)+" days") {
		t.Errorf("the page does not say the widest range one view covers is %d days", stats.MaxHistoryDays)
	}
	if want := grouped(stats.MaxHistoryRecords); !strings.Contains(page, want+" records") {
		t.Errorf("the page does not say one pass reads at most %s records", want)
	}
}

// grouped writes a number the way the prose does, in thousands.
func grouped(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// docs/ carries one Diataxis type per page. The statistics pages are now a
// how-to, a reference and an explanation, and each must stay what it is.
func TestTheStatisticsExplanationStaysAnExplanation(t *testing.T) {
	page := readDoc(t, "statistics-explained.md")
	if strings.Contains(page, "\n1. ") {
		t.Error("the explanation carries a numbered procedure; that belongs on the how-to")
	}
	if strings.Contains(page, "| Field |") {
		t.Error("the explanation carries a reference table; that belongs on the reference page")
	}
	// And the reader gets to it from the pages that send them there.
	for _, from := range []string{"request-statistics.md", "getting-started.md"} {
		if !strings.Contains(readDoc(t, from), "statistics-explained.md") {
			t.Errorf("%s does not point at the page that explains the historical views", from)
		}
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readRepoFile(t, repoRoot, "README.md"), "statistics-explained.md") {
		t.Error("the README does not point at the page that explains the historical views")
	}
}

// The views widen what another account on this Mac can read: from the last
// thousand requests the live view holds to the whole of the retained store.
// The store's own files are the account's, but these tables read them back to
// whoever has the panel open, and the pages have to say so — an opt-in is only
// as good as what the person switching it on was told.
func TestThePagesSayTheViewsWidenWhatOtherAccountsCanRead(t *testing.T) {
	explained := readDoc(t, "statistics-explained.md")
	if !containsAll(explained, "The boundary is the Mac, not your account") {
		t.Error("the explanation does not say the boundary is the Mac rather than the account")
	}
	if !strings.Contains(explained, "request-statistics.md#who-can-see-it") {
		t.Error("the explanation does not send the reader to Who can see it")
	}
	// The claim this replaced, which was false on a Mac several people share.
	if strings.Contains(explained, "go no further than the panel you are looking at") {
		t.Error("the explanation claims the figures go no further than the panel")
	}

	howTo := readDoc(t, "request-statistics.md")
	for _, phrase := range []string{
		"as far back as the records do, not merely as far as the live view",
		"not the last thousand requests alone",
	} {
		if !containsAll(howTo, phrase) {
			t.Errorf("Who can see it does not say %q", phrase)
		}
	}
}
