package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// The sampling pages promise a reader four things between them, and each of
// the four is something the code decides. A page that drifts from any of them
// is worse than no page: a documented default that does not exist, or a
// precedence rule the server does not follow.
//
// The four live on the page that owns them, since docs/ carries one Diataxis
// type per page: the parameter inventory and precedence are reference, the
// mechanism is explanation.
func TestSamplingDocsMatchTheCode(t *testing.T) {
	reference := readDoc(t, "sampling-reference.md")
	explanation := readDoc(t, "sampling-explained.md")

	// 1. Which parameters can be defaulted — the table names every field the
	//    configuration holds, and no field it does not.
	table := parameterTable(t, reference)
	want := samplingFieldNames()
	documented := map[string]bool{}
	for _, row := range table {
		for _, name := range backticked(row) {
			documented[name] = true
		}
	}
	for _, name := range want {
		if !documented[name] {
			t.Errorf("the parameter table does not name %q, which Settings can default", name)
		}
	}
	// max_completion_tokens is the other spelling the model server reads for
	// the same budget; anything else in the table is a parameter that does not
	// exist.
	known := map[string]bool{"max_completion_tokens": true}
	for _, name := range want {
		known[name] = true
	}
	for name := range documented {
		if !known[name] {
			t.Errorf("the parameter table names %q, which is not a sampling default Gropius holds", name)
		}
	}

	// 2. A request's own value wins.
	if !containsAll(reference, "carries its own value", "the request's value") {
		t.Error("the reference does not state that a request's own value wins over the default")
	}

	// 2a. And what "omitted" has to mean. An explicit null is a present key to
	// the model server: the type check fails, the connection closes with no
	// response, and the client sees a 502. Documentation that says null and
	// omitted are the same thing sends the reader's clients straight into that.
	if !containsAll(explanation, "An explicit `null` is not the same thing") {
		t.Error("the explanation does not warn that an explicit null is not the same as omitting the parameter")
	}

	// 3. What a blank field means — and the figure, not just the heading. This
	//    is the one clause whose whole point is that the page is honest about
	//    a value the reader cannot otherwise see, so the figures are read off
	//    the recorded bounds rather than trusted.
	if !containsAll(reference, "Blank means") {
		t.Error("the reference does not say what a blank field means")
	}
	blank := map[string]string{}
	for _, row := range table {
		cells := strings.Split(strings.Trim(row, "|"), "|")
		if len(cells) < 3 {
			continue
		}
		for _, name := range backticked(cells[1]) {
			blank[name] = strings.TrimSpace(cells[2])
		}
	}
	for _, b := range config.SamplingBounds() {
		cell, ok := blank[b.Field]
		if !ok {
			t.Errorf("the table has no blank-means cell for %q", b.Field)
			continue
		}
		if !strings.HasPrefix(cell, b.DefaultText()) {
			t.Errorf("%s: the table says a blank field means %q, but the model server's own default is %s",
				b.Field, cell, b.DefaultText())
		}
	}

	// 4. Reproducibility comes from a fixed temperature, because the model
	//    server ignores a seed.
	if !containsAll(explanation, "seed", "ignores", "temperature 0") {
		t.Error("the explanation does not state that the model server ignores a seed and that reproducibility comes from a fixed temperature")
	}
}

// The panel refuses a value outside these ranges, and the reference tells the
// reader what they are — so the two must agree.
func TestSamplingReferenceStatesTheRealRanges(t *testing.T) {
	page := readDoc(t, "sampling-reference.md")
	for _, b := range config.SamplingBounds() {
		if b.Min != 0 {
			t.Fatalf("%s has a lower bound of %v the page does not describe", b.Field, b.Min)
		}
		if !b.HasMax {
			continue
		}
		if b.Max == 1 {
			continue // "between 0 and 1", below
		}
		// Any other ceiling is Gropius' own rather than the model server's, so
		// the page has to give the figure — read off the bound, not repeated
		// here, or the page and the code drift apart while this test is green.
		want := fmt.Sprintf("%d", int(b.Max))
		if !containsAll(page, want) {
			t.Errorf("the reference does not give %s's ceiling of %s", b.Field, want)
		}
	}
	if !containsAll(page, "temperature is at least 0", "between 0 and 1") {
		t.Error("the reference does not state the ranges the panel enforces")
	}
	// And why the ceilings Gropius sets are not the model server's.
	if !containsAll(page, "Top-k has an upper limit of") {
		t.Error("the reference gives top_k's ceiling without saying it is Gropius' own")
	}
}

// docs/ carries one Diataxis type per page. The three sampling pages are a
// how-to, a reference and an explanation, and each must stay what it is: a
// reference table inside the how-to is how a page becomes the catch-all the
// convention exists to prevent.
func TestSamplingPagesKeepTheirDiataxisType(t *testing.T) {
	howTo := readDoc(t, "sampling-defaults.md")
	if !strings.Contains(howTo, "\n1. ") {
		t.Error("the how-to has no numbered procedure")
	}
	if strings.Contains(howTo, "| Field |") {
		t.Error("the how-to carries a reference table; it belongs in sampling-reference.md")
	}
	for _, link := range []string{"sampling-reference.md", "sampling-explained.md"} {
		if !strings.Contains(howTo, link) {
			t.Errorf("the how-to does not link to %s", link)
		}
	}
	if !strings.Contains(readDoc(t, "sampling-reference.md"), "# Reference:") {
		t.Error("the reference page is not titled as one")
	}
}

func TestGettingStartedPointsAtTheSamplingPage(t *testing.T) {
	page := readDoc(t, "getting-started.md")
	if !strings.Contains(page, "sampling-defaults.md") {
		t.Error("the getting-started walk-through does not point at the sampling page")
	}
}

func readDoc(t *testing.T, name string) string {
	t.Helper()
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(repoRoot, "docs", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// parameterTable returns the body rows of the page's parameter table.
func parameterTable(t *testing.T, page string) []string {
	t.Helper()
	idx := strings.Index(page, "| Field |")
	if idx < 0 {
		t.Fatal("the page has no parameter table")
	}
	var rows []string
	for _, line := range strings.Split(page[idx:], "\n") {
		if !strings.HasPrefix(line, "|") {
			break
		}
		rows = append(rows, line)
	}
	if len(rows) < 3 {
		t.Fatalf("the parameter table has %d lines, so it holds no parameters", len(rows))
	}
	return rows[2:] // skip the header and the separator
}

var backtickRE = regexp.MustCompile("`([a-z_]+)`")

func backticked(row string) []string {
	var out []string
	for _, m := range backtickRE.FindAllStringSubmatch(row, -1) {
		out = append(out, m[1])
	}
	return out
}

func samplingFieldNames() []string {
	rt := reflect.TypeOf(config.Sampling{})
	out := make([]string, 0, rt.NumField())
	for i := range rt.NumField() {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		out = append(out, name)
	}
	return out
}

// containsAll reports whether every phrase appears, ignoring the line breaks
// the prose is wrapped at.
func containsAll(page string, phrases ...string) bool {
	flat := strings.Join(strings.Fields(page), " ")
	for _, p := range phrases {
		if !strings.Contains(flat, strings.Join(strings.Fields(p), " ")) {
			return false
		}
	}
	return true
}
