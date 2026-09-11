package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/lifecycle"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// The lifecycle reference is the page a script author reads before they write
// a poller, a teardown step or a CI gate. Everything on it is something the
// code decides, and a page that drifts from any of it is worse than no page: a
// verb that does not exist, a flag that is refused, an exit code a script
// branches on, or a JSON field a menu bar reads.
//
// So the page is held to the code rather than to a reviewer's memory. The verb
// table is read out of cmd/gropius, the flags out of internal/lifecycle's own
// flag registrations, and the exit codes, labels and JSON fields off the
// exported values themselves.
//
// WHAT THIS CANNOT DO, stated so a green run is not over-read. It proves the
// page names what the code carries and names nothing the code does not. It
// says nothing about whether the prose around those names is true — that a
// stage retries, that the models survive an uninstall, that no observed line
// reads as a verdict. Those are held by the tests in internal/lifecycle and by
// the wording test beside them.

const (
	lifecycleHowTo     = "lifecycle.md"
	lifecycleReference = "lifecycle-reference.md"
)

// Every verb this build answers to is on the reference page, under the same
// word a person types.
func TestTheLifecycleReferenceNamesEveryVerb(t *testing.T) {
	page := readDoc(t, lifecycleReference)
	rows := tableRows(page)

	verbs := verbTable(t)
	if len(verbs) < 4 {
		t.Fatalf("cmd/gropius declares %d verbs, so this test is reading the wrong table", len(verbs))
	}
	for verb, kind := range verbs {
		if !rowNaming(rows, "`gropius "+verb+"`") {
			t.Errorf("the reference has no table row for `gropius %s`, which this build answers to", verb)
		}
		if kind == "verbNotYet" {
			// Named rather than omitted, and named as what it is: the person
			// who typed it learns they typed it correctly. The binary says
			// exactly this, and the page must not promise the verb instead.
			if !containsAll(page, "not in this build yet") {
				t.Errorf("the reference lists %q without saying it is not in this build yet", verb)
			}
		}
	}

	// The other direction: a row naming a word this build does not answer to
	// sends a reader to a refusal.
	for _, row := range rows {
		for _, named := range verbCommandRE.FindAllStringSubmatch(row, -1) {
			if _, known := verbs[named[1]]; !known {
				t.Errorf("the reference has a row for `gropius %s`, which is not a verb this build knows", named[1])
			}
		}
	}
}

// Every flag a verb registers is on the page, on a row that also names the verb
// it belongs to. A flag documented against the wrong verb is refused as an
// unknown flag by the one it is typed at.
func TestTheLifecycleReferenceNamesEveryFlag(t *testing.T) {
	page := readDoc(t, lifecycleReference)
	rows := tableRows(page)

	flags := lifecycleFlags(t)
	if len(flags) < 6 {
		t.Fatalf("internal/lifecycle registers %d flags, so this test is reading the wrong calls", len(flags))
	}
	known := map[string]bool{}
	for _, f := range flags {
		known[f.name] = true
		if !rowNaming(rows, "`--"+f.name+"`", f.verb) {
			t.Errorf("the reference has no row naming `--%s` against the verb %q that carries it", f.name, f.verb)
		}
	}

	// The other direction. A flag on the page that no verb registers is a
	// command line that fails with exit 2.
	for _, m := range flagRE.FindAllStringSubmatch(page, -1) {
		if known[m[1]] || firewallFlags[m[1]] {
			continue
		}
		t.Errorf("the reference names --%s, which no lifecycle verb registers", m[1])
	}
}

// firewallFlags are the flags of the system firewall tool, which the page
// prints because the verbs print them: they are the commands that make and
// remove the grant by hand. They belong to /usr/libexec/ApplicationFirewall/
// socketfilterfw and not to any verb, and are recorded here so the scan above
// does not read them as a promise this build makes.
var firewallFlags = map[string]bool{
	"add":        true,
	"unblockapp": true,
	"remove":     true,
}

// The exit codes, which are the only thing a script can branch on: the
// severity of a finding travels in the report and never in the code.
func TestTheLifecycleReferenceStatesTheExitCodes(t *testing.T) {
	page := readDoc(t, lifecycleReference)
	rows := tableRows(page)

	for _, code := range []struct {
		value int
		what  string
	}{
		{lifecycle.ExitOK, "the verb ran and answered"},
		{lifecycle.ExitFailed, "something verified is wrong"},
		{lifecycle.ExitUsage, "the command line could not be honoured"},
	} {
		if !rowNaming(rows, "`"+strconv.Itoa(code.value)+"`") {
			t.Errorf("the reference has no exit-code row for %d (%s)", code.value, code.what)
		}
	}
	// And the rule that makes the codes readable at all. A reader who takes a
	// warning for a failure writes a CI gate that fails on a fresh install.
	if !containsAll(page, "warnings exit zero") {
		t.Error("the reference does not state that doctor's warnings exit zero")
	}
}

// Every check doctor asks is on the page with the label it carries, and every
// label is defined. The labels are the whole of doctor's honesty: a reader who
// takes an observed line for a verified one has been told the firewall grant is
// healthy by a query that cannot say so.
func TestTheLifecycleReferenceNamesEveryCheckAndItsLabel(t *testing.T) {
	page := readDoc(t, lifecycleReference)
	rows := tableRows(page)

	checks := lifecycle.DefaultChecks()
	if len(checks) < 5 {
		t.Fatalf("doctor asks %d checks, so this test is reading the wrong set", len(checks))
	}
	for _, c := range checks {
		if !rowNaming(rows, c.Name, "`"+string(c.Label)+"`") {
			t.Errorf("the reference has no row naming the %q check with its label %q", c.Name, c.Label)
		}
	}

	// The three labels, each defined in the reader's words. Read off the
	// constants so a fourth label added to the code fails here rather than
	// shipping undefined.
	for _, label := range []lifecycle.Label{lifecycle.Verified, lifecycle.Observed, lifecycle.Undeterminable} {
		if !containsAll(page, "`"+string(label)+"`") {
			t.Errorf("the reference does not define the label %q", label)
		}
	}
	// And the one sentence the observed label exists for. Without it the
	// label is decoration and the reader reads the firewall line as a verdict.
	if !containsAll(page, "never a verdict") {
		t.Error("the reference does not say that an observed finding is never a verdict")
	}
}

// `gropius status --json` is the contract — the menu bar polls it and scripts
// parse it — so every field it emits is named on the page and no field it does
// not emit is.
func TestTheStatusContractIsDocumentedFieldByField(t *testing.T) {
	page := readDoc(t, lifecycleReference)

	documented := map[string]bool{}
	for _, name := range backticked(page) {
		documented[name] = true
	}
	known := map[string]bool{}
	for _, field := range jsonFieldsOf(lifecycle.Status{}, lifecycle.ResidentModel{}) {
		known[field] = true
		if !documented[field] {
			t.Errorf("the reference does not name %q, which `gropius status --json` emits", field)
		}
	}
	for _, field := range jsonFieldsOf(lifecycle.ConfigDocument{}) {
		known[field] = true
		if !documented[field] {
			t.Errorf("the reference does not name %q, which `gropius config show --json` emits", field)
		}
	}
	for _, field := range jsonFieldsOf(lifecycle.Report{}, lifecycle.Finding{}) {
		known[field] = true
		if !documented[field] {
			t.Errorf("the reference does not name %q, which `gropius doctor --json` emits", field)
		}
	}
	// The values of one of those fields are names too, and a reader deciding
	// what to branch on needs them to be this build's.
	for _, state := range residencyStates() {
		known[state] = true
	}

	// A name on the page that no verb emits reads as a field, and a caller
	// that reaches for it gets nothing.
	for name := range documented {
		if strings.Contains(name, "_") && !known[name] {
			t.Errorf("the reference names %q as if a verb emitted it; no such field exists", name)
		}
	}
}

// The values `state` can carry. They are the running server's own words rather
// than the status verb's — status decodes the control plane structurally — so
// they are read off the pool that writes them, and a value renamed there fails
// here rather than leaving a reader branching on a word nothing emits.
func TestTheStatusContractNamesTheResidencyStates(t *testing.T) {
	page := readDoc(t, lifecycleReference)
	for _, state := range residencyStates() {
		if !containsAll(page, "`"+state+"`") {
			t.Errorf("the reference does not name %q, which a resident model's state can be", state)
		}
	}
	// And the honest half: the word comes from the server that answered, so a
	// caller must not treat an unrecognised one as an absent model.
	if !containsAll(page, "treat an unknown value as unknown") {
		t.Error("the reference does not say what to do with a residency state this list does not carry")
	}
}

func residencyStates() []string {
	return []string{
		string(runtime.ResidencyLoaded),
		string(runtime.ResidencyLoading),
		string(runtime.ResidencyNotLoaded),
	}
}

// Where the per-user command goes, read off the directory the install actually
// links into. A reader told to look in the wrong directory concludes the
// install failed.
func TestTheLifecyclePagesNameTheDirectoryTheCommandIsLinkedInto(t *testing.T) {
	dir := perUserLinkDir(t)
	want := "~/" + dir
	for _, name := range []string{lifecycleHowTo, lifecycleReference} {
		if !containsAll(readDoc(t, name), want) {
			t.Errorf("docs/%s does not say the gropius command is linked into %s", name, want)
		}
	}
	// And the line that makes it reachable, which is what the install prints
	// when that directory is not on the search path.
	if !containsAll(readDoc(t, lifecycleHowTo), `export PATH="$HOME/`+dir+`:$PATH"`) {
		t.Error("the how-to does not carry the line that puts the command on the search path")
	}
}

// docs/ carries one Diataxis type per page. These two are a how-to and a
// reference, and each must stay what it is.
func TestTheLifecyclePagesKeepTheirDiataxisType(t *testing.T) {
	howTo := readDoc(t, lifecycleHowTo)
	reference := readDoc(t, lifecycleReference)

	if !strings.Contains(howTo, "\n1. ") {
		t.Error("the how-to has no numbered procedure")
	}
	if strings.Contains(howTo, "| Verb |") || strings.Contains(howTo, "| Flag |") {
		t.Error("the how-to carries a reference table; it belongs in the reference page")
	}
	if !strings.Contains(howTo, lifecycleReference) {
		t.Errorf("the how-to does not link to %s", lifecycleReference)
	}
	if !strings.HasPrefix(reference, "# Reference:") {
		t.Errorf("docs/%s does not start with a reference title: %.60q", lifecycleReference, reference)
	}
	if strings.Contains(reference, "\n1. ") {
		t.Errorf("docs/%s carries a numbered procedure; a how-to is a different page", lifecycleReference)
	}
}

// A page nothing links is a page nobody finds. The README indexes the features
// and getting-started is the hub every other page hangs off.
func TestTheLifecyclePagesAreLinked(t *testing.T) {
	root := repoRootDir(t)
	readme := readRepoFile(t, root, "README.md")
	for _, page := range []string{lifecycleHowTo, lifecycleReference} {
		if !strings.Contains(readme, "docs/"+page) {
			t.Errorf("README.md does not link docs/%s", page)
		}
	}
	if !strings.Contains(readDoc(t, "getting-started.md"), lifecycleHowTo) {
		t.Errorf("the getting-started walk-through does not point at docs/%s", lifecycleHowTo)
	}
}

// Neither page calls a release signed, notarised or trusted.
//
// A build-provenance attestation is not a signature and neither is a checksum:
// the bundles are ad-hoc signed, not notarised, and neither control is visible
// to Gatekeeper. A reader who takes any of those three words for Gatekeeper's
// verdict has been told something no control here can support, and these are
// the pages that describe a download being fetched and placed.
//
// ONE EXEMPTION, and it is the honest statement rather than a claim: "ad-hoc
// signed" is WHY the authorisation panel appears on every update, and the
// sentence that says so is the reason a person needs. It is allowed by exact
// phrase, so "signed" on its own is still a finding.
func TestTheLifecyclePagesNeverCallAReleaseSignedOrNotarised(t *testing.T) {
	const honestPhrase = "ad-hoc signed"
	for _, name := range []string{lifecycleHowTo, lifecycleReference} {
		flat := strings.ToLower(strings.Join(strings.Fields(readDoc(t, name)), " "))
		stripped := strings.ReplaceAll(flat, honestPhrase, "")
		for _, banned := range []string{"signed", "notarised", "notarized", "trusted"} {
			if strings.Contains(stripped, banned) {
				t.Errorf("docs/%s says %q about a release. Say what is true — verified against the checksums "+
					"published with the release — or say nothing", name, banned)
			}
		}
		// And the sentence that IS true has to be there, on both pages, or the
		// rule above is only a prohibition.
		if !containsAll(readDoc(t, name), "checksums published with the release") {
			t.Errorf("docs/%s does not say the download is verified against the checksums published with the release", name)
		}
	}
}

// The report `gropius update` ends on is documented line by line, and the lines
// are read out of the code rather than retyped here.
//
// A reference that describes a different report than the one a person reads is
// worse than none: the whole point of this verb is that the two versions it
// prints are separate facts, and a page that paraphrases them loses exactly the
// distinction it exists to carry.
func TestTheUpdateReportsLinesAreDocumented(t *testing.T) {
	page := readDoc(t, lifecycleReference)
	sentences := updateReportSentences(t)
	if len(sentences) < 8 {
		t.Fatalf("this scan read %d sentences out of the report, so it is reading the wrong file", len(sentences))
	}
	for name, sentence := range sentences {
		if !containsAll(page, sentence) {
			t.Errorf("the reference does not carry the report's %s:\n  %s", name, sentence)
		}
	}
}

// updateReportSentences reads the report's own strings out of the package: the
// three ways a serving version goes unknown, and the sentences the five facts
// are made of.
func updateReportSentences(t *testing.T) map[string]string {
	t.Helper()
	wanted := map[string]bool{
		"checksumSentence":        true,
		"grantReasonSentence":     true,
		"previousReleaseSentence": true,
		"didNotInstallSentence":   true,
		"logOutSentence":          true,
		"cannotQuitSentence":      true,
		"servingReasonNoField":    true,
		"servingReasonUnanswered": true,
		"servingReasonSilent":     true,
		"servingReasonIdle":       true,
	}
	file := parseRepoGoFile(t, filepath.Join("internal", "lifecycle", "updatereport.go"))
	out := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || len(value.Values) != 1 {
				continue
			}
			name := value.Names[0].Name
			if !wanted[name] {
				continue
			}
			if text, ok := concatenatedStringLit(value.Values[0]); ok {
				out[name] = text
			}
		}
	}
	for name := range wanted {
		if out[name] == "" {
			t.Errorf("internal/lifecycle/updatereport.go declares no %s this scan can read; the reference is "+
				"held to the report's own words, so a constant renamed here must be renamed here too", name)
		}
	}
	return out
}

// concatenatedStringLit reads a constant written as several string literals
// joined by +, which is how a sentence long enough to matter is spelled in Go.
func concatenatedStringLit(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return stringLit(e)
	case *ast.BinaryExpr:
		left, okL := concatenatedStringLit(e.X)
		right, okR := concatenatedStringLit(e.Y)
		return left + right, okL && okR
	}
	return "", false
}

// The verb table in cmd/gropius, read out of the source rather than repeated
// here: package main cannot be imported, and a list copied into this file
// would be the thing that goes stale.
func verbTable(t *testing.T) map[string]string {
	t.Helper()
	file := parseRepoGoFile(t, "cmd/gropius/args.go")
	out := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "verbs" || len(value.Values) != 1 {
				continue
			}
			lit, ok := value.Values[0].(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				name, ok := stringLit(kv.Key)
				if !ok {
					continue
				}
				kind, _ := kv.Value.(*ast.Ident)
				if kind == nil {
					t.Fatalf("the verb %q maps to something this scan cannot read", name)
				}
				out[name] = kind.Name
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("cmd/gropius/args.go declares no verbs map, so this scan is reading the wrong file")
	}
	return out
}

// verbFlag is one flag, and the verb whose flag set registers it.
type verbFlag struct{ verb, name string }

// lifecycleFlags is every flag the package registers, paired with its verb.
//
// The verb comes from the flags() call in the same function body, which is
// where the flag set is named — so a flag moved to another verb moves here
// too, and the page is held to the pairing rather than to the spelling.
func lifecycleFlags(t *testing.T) []verbFlag {
	t.Helper()
	root := repoRootDir(t)
	dir := filepath.Join(root, "internal", "lifecycle")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []verbFlag
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file := parseRepoGoFile(t, filepath.Join("internal", "lifecycle", name))
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			verb, names := flagsIn(fn.Body)
			if verb == "" {
				continue
			}
			for _, flag := range names {
				out = append(out, verbFlag{verb: verb, name: flag})
			}
		}
	}
	return out
}

// flagRegistrars are the flag-set methods that name a flag. A flag registered
// through one this list does not know is a flag this scan would miss, so the
// list is the thing to extend rather than the page.
var flagRegistrars = map[string]bool{"Bool": true, "String": true, "Int": true, "Duration": true, "Float64": true}

// flagsIn reads one function body: the verb its flag set is built for, and
// every flag registered on it.
func flagsIn(body *ast.BlockStmt) (verb string, flags []string) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if fn.Name == "flags" && len(call.Args) > 0 {
				if name, ok := stringLit(call.Args[0]); ok {
					verb = name
				}
			}
		case *ast.SelectorExpr:
			// A registration is three arguments — name, default, usage — and
			// the name is a literal. Anything else is a method that merely
			// shares a name with one, such as a builder's String().
			if flagRegistrars[fn.Sel.Name] && len(call.Args) == 3 {
				if name, ok := stringLit(call.Args[0]); ok {
					flags = append(flags, name)
				}
			}
		}
		return true
	})
	return verb, flags
}

// perUserLinkDir is the directory the install links the command into, as the
// package spells it: the parts of linkDirName joined the way filepath.Join
// joins them.
func perUserLinkDir(t *testing.T) string {
	t.Helper()
	file := parseRepoGoFile(t, filepath.Join("internal", "lifecycle", "binlink.go"))
	var parts []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "linkDirName" || len(value.Values) != 1 {
				continue
			}
			call, ok := value.Values[0].(*ast.CallExpr)
			if !ok {
				continue
			}
			for _, arg := range call.Args {
				if part, ok := stringLit(arg); ok {
					parts = append(parts, part)
				}
			}
		}
	}
	if len(parts) == 0 {
		t.Fatal("internal/lifecycle/binlink.go declares no linkDirName this scan can read")
	}
	return strings.Join(parts, "/")
}

func parseRepoGoFile(t *testing.T, rel string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(repoRootDir(t), rel), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func stringLit(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// jsonFieldsOf is every field name the given values marshal to, at one level
// and through the nested types named beside them.
func jsonFieldsOf(values ...any) []string {
	var out []string
	for _, v := range values {
		rt := reflect.TypeOf(v)
		for i := range rt.NumField() {
			name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			out = append(out, name)
		}
	}
	return out
}

// tableRows is every table row on a page, flattened so a row wrapped by the
// formatter still reads as one line.
func tableRows(page string) []string {
	var rows []string
	for _, line := range strings.Split(page, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			rows = append(rows, line)
		}
	}
	return rows
}

// rowNaming reports whether one row names all of the given phrases.
func rowNaming(rows []string, phrases ...string) bool {
	for _, row := range rows {
		if containsAll(row, phrases...) {
			return true
		}
	}
	return false
}

var (
	verbCommandRE = regexp.MustCompile("`gropius ([a-z]+)`")
	flagRE        = regexp.MustCompile(`--([a-z][a-z-]*)`)
)
