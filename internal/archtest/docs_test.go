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

// The sampling how-to promises a reader four things, and each of them is a
// thing the code decides. A page that drifts from any of them is worse than
// no page: it is a documented default that does not exist, or a precedence
// rule the server does not follow.
func TestSamplingHowToMatchesTheCode(t *testing.T) {
	page := readDoc(t, "sampling-defaults.md")

	// 1. Which parameters can be defaulted — the table names every field the
	//    configuration holds, and no field it does not.
	table := parameterTable(t, page)
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
	if !containsAll(page, "own value always wins") {
		t.Error("the page does not state that a request's own value wins over the default")
	}

	// 2a. And what "omitted" has to mean. An explicit null is a present key to
	// the model server: the type check fails, the connection closes with no
	// response, and the client sees a 502. A page that says null and omitted
	// are the same thing sends the reader's clients straight into that.
	if !containsAll(page, "An explicit `null` is not the same thing") {
		t.Error("the page does not warn that an explicit null is not the same as omitting the parameter")
	}

	// 3. What a blank field means.
	if !containsAll(page, "Blank means") {
		t.Error("the page does not say what a blank field means")
	}

	// 4. Reproducibility comes from a fixed temperature, because the model
	//    server ignores a seed.
	if !containsAll(page, "seed", "ignores", "fixed temperature") {
		t.Error("the page does not state that the model server ignores a seed and that reproducibility comes from a fixed temperature")
	}
}

// The panel refuses a value outside these ranges, and the page tells the
// reader what they are — so the two must agree.
func TestSamplingHowToStatesTheRealRanges(t *testing.T) {
	page := readDoc(t, "sampling-defaults.md")
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
			t.Errorf("the page does not give %s's ceiling of %s", b.Field, want)
		}
	}
	if !containsAll(page, "temperature\nis at least 0", "between 0 and 1") {
		t.Error("the page does not state the ranges the panel enforces")
	}
	// And why the ceilings Gropius sets are not the model server's.
	if !containsAll(page, "Top-k has an upper limit of") {
		t.Error("the page gives top_k's ceiling without saying it is Gropius' own")
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
