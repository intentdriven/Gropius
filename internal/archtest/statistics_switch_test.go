package archtest_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// statisticsSwitchReaders are the files allowed to name the statistics switch,
// each with the reason it is allowed to.
//
// There are three states, not two: recording off, recording on, and a separate
// per-model action that raises a model server's own log level. The third must
// never share the switch of the second, because at DEBUG the model server
// writes every request body and every response it produces to its log — prompts
// and completions — while recording statistics is content-free by
// construction. Ollama ships that coupling and has an open security issue about
// it; adr-2609061503319212 is why Gropius does not.
//
// The rule is kept by keeping the switch out of the packages that could act on
// it. It is a scan for the field's names, not a proof: a value passed through
// under another name gets past it. What it catches is the ordinary way the
// coupling appears — someone reaching for the switch in the launcher or the
// pool because it is right there — and it makes the list of readers a thing
// that has to be edited in a diff someone reviews.
var statisticsSwitchReaders = map[string]string{
	"internal/config/config.go":     "declares the switch and reads it from the settings file",
	"internal/gateway/gateway.go":   "reads it per request to decide whether to record",
	"internal/gateway/observe.go":   "takes it as an argument from the line above",
	"internal/app/app.go":           "applies it to the recorder when settings are read or saved",
	"internal/ui/static/app.js":     "draws the switch and the Statistics view",
	"internal/ui/static/index.html": "holds the switch and the view",
}

// statisticsSwitchNames are the ways the switch gets named: the Go field, the
// reads of it, and the key it is written down as in the settings file and in
// the form that posts them. Prose about the Statistics view is not a read of
// the switch, so the field is matched as a declaration or an access rather
// than as a word.
var statisticsSwitchNames = []string{"Statistics bool", ".Statistics", `"statistics"`, "statistics:"}

// The switch reaches the recorder and nothing else. In particular it never
// reaches internal/runtime, which owns the model server's command line.
func TestTheStatisticsSwitchDoesNotReachTheRuntime(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	// walkRepoFiles owns the skip rule — including the dot-directory rule this
	// scan used to carry itself, which keeps a git worktree checked out under
	// .claude/ from being scanned as if it were this checkout. The rest is this
	// scan's own subject: prose about the Statistics view is not a read of the
	// switch, so the surfaces that only describe it are left out.
	subject := walkOptions{AlsoSkip: []string{"client", "build", "docs", "site-src"}}
	walkRepoFiles(t, repoRoot, subject, func(path string, d fs.DirEntry) error {
		name := d.Name()
		if !strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".html") {
			return nil
		}
		if strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		for _, field := range statisticsSwitchNames {
			if !strings.Contains(src, field) {
				continue
			}
			if _, allowed := statisticsSwitchReaders[filepath.ToSlash(rel)]; allowed {
				return nil
			}
			t.Errorf("%s names %s, so the statistics switch reaches it. Recording statistics is "+
				"content-free and the per-model debug action is not, and they must never share a "+
				"switch (adr-2609061503319212). If this is genuinely a new reader, it needs a "+
				"decision record and an entry in statisticsSwitchReaders saying why", rel, field)
			return nil
		}
		return nil
	})
}

// The other half of the same rule, from the launcher's side: a model server is
// started at one level, that level is INFO, and it is written down in one
// place. A second spelling anywhere is how a switch elsewhere would come to
// govern what a model server writes to its log.
func TestTheModelServerIsAlwaysLaunchedAtInfo(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(repoRoot, "internal", "runtime", "launcher.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if n := strings.Count(src, `"--log-level"`); n != 1 {
		t.Errorf("the launcher names --log-level %d times, want exactly 1", n)
	}
	if !strings.Contains(src, `"--log-level", "INFO",`) {
		t.Error("the launcher no longer starts every model server at INFO")
	}
	if strings.Contains(src, `"DEBUG"`) {
		t.Error("the launcher names DEBUG, at which the model server writes every prompt and completion to its log")
	}
}

// The list above is only a boundary while every entry on it is a file that
// exists: a stale entry exempts nothing, and a renamed reader would leave its
// exemption behind for the next file to inherit.
func TestStatisticsSwitchReadersAllExist(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for rel, why := range statisticsSwitchReaders {
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel))); err != nil {
			t.Errorf("statisticsSwitchReaders lists %s (%s), which is not in the tree", rel, why)
		}
	}
}
