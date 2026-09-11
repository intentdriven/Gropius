package archtest_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// The Go configuration and the control panel say the same thing, and this is
// what notices when they stop.
//
// AGENTS.md carries the convention — "the web control panel is the accessible
// layer, carrying the most important settings, and it must be kept in sync
// with what Go can do; a Go capability with no panel equivalent is a gap, not a
// feature tier" — and says the sync obligation is armed the way every other
// cross-surface promise here is: a test, not a habit. This is the test
// (itd-2609081259493890).
//
// It runs in both directions. Every json-tagged setting the configuration
// holds is named by the Settings pane or carries a written exemption below; and
// every settings key the pane's submit body names is a setting the
// configuration holds, so a field removed in Go cannot leave a dead control
// behind.
//
// THE TWO HALVES ARE CHECKED SEPARATELY, and that is not a detail. They were
// once searched as one concatenated blob, which meant either surface alone
// could satisfy the rule: deleting a control's markup while its key survived
// in the script's submit body left the test green, and the setting was then
// posted by a form that no longer had a box for it. So the script must name
// the key, AND every control the script binds to that key must still exist in
// the markup.
//
// WHAT THIS CANNOT DO, stated so a green run is not over-read. Both halves are
// string searches. They prove the key is NAMED in the script and that the
// control ids the script reaches for are present in the document — that nobody
// forgot the panel while adding a field, and that nobody deleted the control
// while leaving the script posting it. They prove nothing about whether the
// control works, is well labelled, or posts the right value, and the markup
// half reaches only the settings whose control id shares a source line with
// their key: a sampling parameter, reached through a table, and a per-model
// setting, reached through a generated row, are checked in the script alone.
// The round-trip tests in internal/ui and the save-path tests in
// internal/gateway are what prove the wiring; this proves nobody forgot. The
// intent says as much in its own mechanism claim: if the enumeration half never
// fails while the round-trip half catches something, the enumeration half was
// the wrong half.

// settingExemption is one setting deliberately left without a panel control.
//
// AN ENTRY HERE COSTS SOMETHING, which is the point. It says a person using
// only the control panel cannot reach this setting at all, so it carries the
// reason, the record that decided it, and the surface an operator reaches the
// setting through instead. The table lives in this file rather than as a
// comment in internal/config because the exemption is ABOUT that file: a reader
// asking "why has this no control" must find the answer where the rule is, not
// where the field is.
type settingExemption struct {
	// Reason is why the panel does not carry this setting.
	Reason string
	// DecidedBy is the record that decided it — an id that resolves to a
	// record under .abcd/, which TestEveryExemptionCitesARecordThatExists
	// holds it to.
	DecidedBy string
	// ReachedThrough is the surface an operator sets this through instead.
	// TestExemptedSettingsAreDocumented holds the documentation to it.
	ReachedThrough string
}

// settingsPaneExemptions is the whole of the exemption table. Two entries, and
// only two (spc-2609111941481833).
var settingsPaneExemptions = map[string]settingExemption{
	"preload": {
		Reason: "A power-tool setting: a list of repo ids loaded at startup, best-effort. " +
			"A panel control would need a model picker duplicating the pinning list for a " +
			"different verb, and pinning is not preloading — the panel would then carry two " +
			"model lists that mean different things.",
		DecidedBy:      "itd-2609081259493890",
		ReachedThrough: "config.json, documented in docs/getting-started.md",
	},
	"upstream_header_timeout_sec": {
		Reason: "An escape hatch over a derivation, not a setting an operator chooses. " +
			"Validate does not bound it, so a control would publish a number with no " +
			"established safe range — and a figure typed into a box invites being typed. " +
			"Establishing a safe range in Validate is the precondition for ever giving it a " +
			"control, and nobody has measured it.",
		DecidedBy:      "itd-2609081259493890",
		ReachedThrough: "config.json, documented in docs/getting-started.md",
	},
}

// Every setting has a control the Settings pane posts, or an exemption.
func TestEverySettingHasAPanelControlOrAnExemption(t *testing.T) {
	markup, script := settingsPaneMarkupAndScript(t)

	paths := settingPaths(t)
	if len(paths) < 25 {
		t.Fatalf("the walk found %d settings, so it is reading the wrong type", len(paths))
	}
	for _, path := range paths {
		if _, exempt := settingsPaneExemptions[path]; exempt {
			continue
		}
		for _, segment := range pathSegments(path) {
			if !namedBy(script, segment) {
				t.Errorf("the Settings pane's script names no %q, so the setting %q is in the Go "+
					"configuration and nowhere an operator using the panel can reach it. Give it a control "+
					"the form posts, or add it to settingsPaneExemptions with the reason, the record that "+
					"decided it, and the surface it is reached through instead (AGENTS.md, "+
					"\"Three surfaces\")", segment, path)
				continue
			}
			// The other half of the pane. A key the script posts whose control
			// is no longer in the document is a form posting a box nobody can
			// see — which is what a single search over both surfaces at once
			// could not tell from a working control.
			for _, id := range controlIDsBoundTo(script, segment) {
				if !strings.Contains(markup, `id="`+id+`"`) {
					t.Errorf("the panel's script reads %q for the setting %q, and the markup carries no "+
						"control with that id — the form posts a setting whose box is gone", id, path)
				}
			}
		}
	}
}

// An exemption for a field that no longer exists is a rule that matches
// nothing, and worse: a renamed field would inherit another field's exemption
// and be waved through with a reason written about something else. This is the
// liveness rule TestStatisticsSwitchReadersAllExist holds its own list to.
func TestEveryExemptedSettingStillExists(t *testing.T) {
	held := map[string]bool{}
	for _, path := range settingPaths(t) {
		held[path] = true
	}
	for path, ex := range settingsPaneExemptions {
		if !held[path] {
			t.Errorf("settingsPaneExemptions exempts %q, which the configuration no longer holds — remove "+
				"the entry rather than leaving an exemption a renamed field could inherit (decided by %s)",
				path, ex.DecidedBy)
		}
	}
}

// Every exemption carries all three columns, and none of them is a placeholder.
// An exemption whose reason is blank is an undecided gap wearing a decision's
// clothes.
func TestEveryExemptionIsWrittenDown(t *testing.T) {
	for path, ex := range settingsPaneExemptions {
		if strings.TrimSpace(ex.Reason) == "" {
			t.Errorf("the exemption for %q carries no reason", path)
		}
		if strings.TrimSpace(ex.DecidedBy) == "" {
			t.Errorf("the exemption for %q names no record that decided it", path)
		}
		if strings.TrimSpace(ex.ReachedThrough) == "" {
			t.Errorf("the exemption for %q does not say where an operator reaches the setting instead", path)
		}
	}
}

// The record that decided an exemption has to be a record somebody can open. An
// id that resolves to nothing is the same as no reason at all.
func TestEveryExemptionCitesARecordThatExists(t *testing.T) {
	ids := recordIDsInTheLedger(t)
	for path, ex := range settingsPaneExemptions {
		if !ids[ex.DecidedBy] {
			t.Errorf("the exemption for %q cites %s, which resolves to no record under .abcd/ — an "+
				"exemption's reason has to be findable", path, ex.DecidedBy)
		}
	}
}

// The other direction: a settings key the pane posts that the configuration
// does not hold. It decodes into nothing, the save reports success, and the
// control goes on being drawn — which is a field removed in Go leaving a dead
// control behind.
func TestTheSettingsPaneNamesNoSettingTheConfigurationDoesNotHold(t *testing.T) {
	held := map[string]bool{}
	for _, path := range settingPaths(t) {
		for _, segment := range pathSegments(path) {
			held[segment] = true
		}
	}

	posted := postedSettingsKeys(t)
	if len(posted) < 10 {
		t.Fatalf("the scan found %d posted keys, so it is reading the wrong part of the panel", len(posted))
	}
	for _, key := range posted {
		if !held[key] {
			t.Errorf("the Settings form posts %q, which is not a setting config.Config holds — the save "+
				"decodes it into nothing and reports success. Remove the control, or restore the field", key)
		}
	}
}

// ── the configuration side ───────────────────────────────

// settingPaths is every json-tagged leaf setting of config.Config and the
// structs it nests, as a dotted path: "port", "sampling.temperature",
// "models.*.pinned".
//
// Reflection over the type, not a scan of internal/config's source, for two
// reasons. A scan would be one more hand-rolled tree walker in a package that
// already carries an issue about the eight it had (iss-2609081427104462); and a
// scan cannot tell a json tag on a settings struct from one on Notices or
// SetupStatus. Reflection starts from Config and can only reach settings.
func settingPaths(t *testing.T) []string {
	t.Helper()
	var out []string
	collectSettingPaths(t, reflect.TypeOf(config.Config{}), "", &out, 0)
	sort.Strings(out)
	return out
}

func collectSettingPaths(t *testing.T, rt reflect.Type, prefix string, out *[]string, depth int) {
	t.Helper()
	if depth > 8 {
		t.Fatalf("the settings walk reached depth %d at %q; config.Config does not nest that far", depth, prefix)
	}
	for i := range rt.NumField() {
		f := rt.Field(i)
		if f.PkgPath != "" {
			continue // unexported: not a setting, and not in the file
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue // carried in the struct and never written to config.json
		}
		path := prefix + name
		switch ft := indirect(f.Type); ft.Kind() {
		case reflect.Struct:
			collectSettingPaths(t, ft, path+".", out, depth+1)
		case reflect.Map:
			// The one per-model map. Its entries are keyed by model id, so the
			// path carries "*" where the id goes and the settings below it are
			// per-model leaves of their own.
			if elem := indirect(ft.Elem()); elem.Kind() == reflect.Struct {
				collectSettingPaths(t, elem, path+".*.", out, depth+1)
				continue
			}
			*out = append(*out, path)
		default:
			*out = append(*out, path)
		}
	}
}

func indirect(rt reflect.Type) reflect.Type {
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	return rt
}

// pathSegments is the names a path is made of, with the per-model wildcard
// dropped. Every one of them has to be named by the pane: a control for
// "served_context" under a "models" object the pane never names is a control
// posting into nothing.
func pathSegments(path string) []string {
	var out []string
	for _, segment := range strings.Split(path, ".") {
		if segment != "*" {
			out = append(out, segment)
		}
	}
	return out
}

// ── the panel side ───────────────────────────────────────

// settingsPaneMarkupAndScript is the pane's two surfaces, kept apart so that
// neither can satisfy a rule about the other.
//
// The markup is the WHOLE document rather than the Settings section: a control
// id the script reaches for has to exist somewhere a browser can find it, and
// scoping this to one section would fail on the ids that settings code shares
// with the views beside it. What keeps a read-only line elsewhere from passing
// as a control is the other half — the script has to post the key — and the
// scope condition that says so (itd-2609081259493890).
//
// The script's comments are removed because they are prose about the panel
// rather than the panel. app.js carries a comment naming the settings the form
// does NOT own — the comment explaining why they are preserved server-side —
// and a search that read it would report those settings as reached from the
// panel on the strength of a sentence saying that they are not.
func settingsPaneMarkupAndScript(t *testing.T) (markup, script string) {
	t.Helper()
	root := repoRootDir(t)
	return readRepoFile(t, root, filepath.Join("internal", "ui", "static", "index.html")),
		stripJSComments(readRepoFile(t, root, filepath.Join("internal", "ui", "static", "app.js")))
}

// controlIDsBoundTo is the element ids the script reaches for on a line that
// also names this setting — which is how the panel binds a key to its control:
// "advertise: $('setAdvertise').checked" going one way and
// "$('setAdvertise').checked = !!c.advertise" coming back.
//
// Line-scoped, so it finds the bindings written as one expression and none of
// the bindings that travel through a table or a generated row. That is the
// limit stated at the top of this file: it is a check that catches the
// ordinary way a control is lost, not a proof that every key has one.
func controlIDsBoundTo(script, key string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(script, "\n") {
		if !namedBy(line, key) {
			continue
		}
		for _, m := range panelControlIDRE.FindAllStringSubmatch(line, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	return out
}

// panelControlIDRE matches the panel's element lookup, $('someId'), and only
// the literal form: an id the script computes is not one this can check.
var panelControlIDRE = regexp.MustCompile(`\$\('([A-Za-z0-9_]+)'\)`)

// settingsPaneMarkup is the Settings pane's section of the panel's markup, and
// only it. Another tab's markup is not the Settings pane, and a setting named
// on the posture page is an account of what is on rather than a control
// (itd-2609081259493890's scope conditions).
func settingsPaneMarkup(t *testing.T, page string) string {
	t.Helper()
	const opening = `<section id="tab-settings"`
	start := strings.Index(page, opening)
	if start < 0 {
		t.Fatal("the control panel has no Settings section, so this scan is reading the wrong markup")
	}
	end := strings.Index(page[start:], "</section>")
	if end < 0 {
		t.Fatal("the Settings section is never closed")
	}
	return page[start : start+end]
}

// namedBy reports whether the pane names this key, matched on whole tokens so
// that "preload" is not found inside "preloading".
func namedBy(pane, key string) bool {
	return regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(key) + `([^A-Za-z0-9_]|$)`).MatchString(pane)
}

// postedSettingsKeys is every settings key the Settings form's submit body
// names: the body literal itself, and the two helpers it spreads and calls to
// build the pairs of fields one control owns.
//
// WHAT IT DOES NOT COVER, so the green is read for what it is: the sampling
// keys, which the body posts through a table of its own, and the per-model
// keys, which it posts through modelSettings. Those are covered in the other
// direction — the pane has to name them — and a dead control for one of them
// would be caught there the moment the field went.
func postedSettingsKeys(t *testing.T) []string {
	t.Helper()
	script := stripJSComments(readRepoFile(t, repoRootDir(t), filepath.Join("internal", "ui", "static", "app.js")))

	seen := map[string]bool{}
	var out []string
	for _, marker := range []string{"const body = {", "function bindSelectBody(", "function chatRule("} {
		for _, m := range objectKeyRE.FindAllStringSubmatch(jsBlock(t, script, marker), -1) {
			if key := m[1]; !seen[key] {
				seen[key] = true
				out = append(out, key)
			}
		}
	}
	sort.Strings(out)
	return out
}

// objectKeyRE matches an object literal's key. Settings keys are lower case
// with underscores, which is what config.json spells them as, so a camelCase
// identifier followed by a colon is not read as one.
var objectKeyRE = regexp.MustCompile(`([a-z][a-z0-9_]*)\s*:`)

// jsBlock returns the brace-delimited block opening at the first "{" at or
// after marker, braces inside strings and comments skipped.
func jsBlock(t *testing.T, src, marker string) string {
	t.Helper()
	start := strings.Index(src, marker)
	if start < 0 {
		t.Fatalf("the panel's script carries no %q, so this scan is reading the wrong file", marker)
	}
	open := strings.Index(src[start:], "{")
	if open < 0 {
		t.Fatalf("%q opens no block", marker)
	}
	depth := 0
	for i := start + open; i < len(src); i++ {
		switch c := src[i]; {
		case c == '\'' || c == '"' || c == '`':
			i += jsQuotedLen(src[i:]) - 1
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return src[start+open : i+1]
			}
		}
	}
	t.Fatalf("the block opened by %q is never closed", marker)
	return ""
}

// stripJSComments removes JavaScript comments and leaves everything else,
// string literals included: the per-model keys the form posts are string
// literals, so stripping those would hide the settings this scan is looking
// for.
func stripJSComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			j := strings.IndexByte(src[i:], '\n')
			if j < 0 {
				return b.String()
			}
			i += j // the newline itself is kept
		case strings.HasPrefix(src[i:], "/*"):
			j := strings.Index(src[i+2:], "*/")
			if j < 0 {
				return b.String()
			}
			i += 2 + j + 2
		case src[i] == '\'' || src[i] == '"' || src[i] == '`':
			n := jsQuotedLen(src[i:])
			b.WriteString(src[i : i+n])
			i += n
		default:
			b.WriteByte(src[i])
			i++
		}
	}
	return b.String()
}

// jsQuotedLen is the length of the quoted run starting at s[0], including both
// quotes, honoring backslash escapes.
func jsQuotedLen(s string) int {
	quote := s[0]
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case quote:
			return i + 1
		}
	}
	return len(s)
}

// ── the record side ──────────────────────────────────────

// recordIDsInTheLedger is every record id that resolves to a file under
// .abcd/. Ids are carried in the file names, so the set is the names rather
// than a parse of each record.
func recordIDsInTheLedger(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	// Rooted at .abcd itself: walkRepoFiles walks the root whatever it is
	// called, and rooting here rather than naming it back in keeps the
	// dot-directory rule — and the worktree hazard it exists for — untouched.
	walkRepoFiles(t, filepath.Join(repoRootDir(t), ".abcd"), walkOptions{}, func(path string, d fs.DirEntry) error {
		for _, m := range recordIDRE.FindAllStringSubmatch(d.Name(), -1) {
			out[m[1]] = true
		}
		return nil
	})
	if len(out) == 0 {
		t.Fatal("no record ids found under .abcd/, so this scan is reading the wrong directory")
	}
	return out
}

var recordIDRE = regexp.MustCompile(`\b((?:iss|itd|spc|adr|cond)-\d+)`)

// A guard on the guard: the walk above must actually be reading the ledger, and
// os is imported for the check.
func TestTheLedgerScanFindsTheDecidingRecords(t *testing.T) {
	if _, err := os.Stat(filepath.Join(repoRootDir(t), ".abcd")); err != nil {
		t.Fatalf("this repository has no .abcd ledger: %v", err)
	}
}
