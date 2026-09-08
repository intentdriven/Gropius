package archtest_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// adr-2609081118587999 rule 1: what Gropius says about a private network may
// state what it observed and may not state what that network is worth. The
// mark's own vocabulary is guarded where the mark is built — internal/netshape,
// the Network values the gateway emits, and the panel functions that render
// them. This is the same rule over the prose around it: the user-facing
// documentation, the README, the control panel's markup and the panel's own
// script, which are where a sentence explaining the mark would go, and where a
// sentence explaining it wrongly would do the damage the mark exists to
// prevent.
//
// It is deliberately NOT the bare-word scan the other surfaces use. Those scan
// a handful of short fixed strings, where "only" and "safe" have no innocent
// use. Prose is different: docs/ says "files only your account can open" and
// "how long a model is safe from eviction", both honest and neither about a
// network. So this scans for CLAIMS — the shapes a sentence takes when it tells
// an operator what their exposure is — and it scans them only in the passages
// that are about the mark.
//
// That is what lets docs/getting-started.md keep the disclaimer it already
// carries: "says nothing about how safe it is or who else can reach it" uses
// the word and makes the opposite of the claim, and no exemption is needed for
// it because no claim pattern matches it. If one ever did, the fix is a better
// pattern and not a list of blessed sentences.
//
// WHERE THIS SCAN STOPS. It stops in three places, and this list is the whole
// of what the scan is worth — a clean run means "none of these patterns
// appears in a passage this scans", and it does not mean the prose claims
// nothing.
//
//  1. SCOPE. A claim in a passage that never names the mark, and is not beside
//     one or under a heading that does, is not found — and cannot be, because
//     scanning unscoped prose fires on "a model is protected", "files only
//     your account can open" and a README line about being tested end-to-end.
//     TestTheClaimScanCatchesWhatItSaysItCatches plants a claim in every
//     surface and every scope this does cover, so this edge is a measurement.
//  2. VOCABULARY. exposureClaims is a closed list of wordings, and a
//     paraphrase outside it passes. Its comment says which synonyms were added
//     because a review wrote past it, and which one is left off on purpose.
//  3. SUPPRESSION. A negator in the same clause as a claim disarms it, which
//     is what lets an honest denial through and is also how a claim can be
//     dressed as one. cancelledNegations lists the shapes that get past the
//     cancellation.
//
// Each of the three has been narrower than the comment describing it at least
// once. TestANegatorDoesNotDisarmAClaimItDoesNotNegate holds the sentences
// that proved it, so the same wordings cannot come back.

// vendorNames are never right anywhere in any surface, in prose or in markup or
// in a script. The classifier cannot tell one product on the range from
// another, so naming one is a guess presented as a finding — and the reason has
// nothing to do with claims, which is why these stay a bare-word scan over the
// whole file.
var vendorNames = []string{
	"tailscale", "tailnet", "headscale", "zerotier", "wireguard", "nebula",
}

// exposureClaims are the shapes a sentence takes when it tells an operator
// what a network is worth rather than which network an address is on. Each has
// to survive the transitions Gropius cannot see — the network published to the
// internet, shared with machines the operator does not own, logged out from —
// and none of these does.
//
// This is a CLOSED LIST OF WORDINGS, not a test for the idea. It catches the
// phrasings someone has written down; a paraphrase nobody has thought of goes
// straight through, and every widening of it so far has come from a reviewer
// writing a sentence it missed rather than from the list being complete. Six
// of the entries below arrived exactly that way — "is kept private", "is
// unreachable from the internet", "is reachable only by you", "is
// confidential", "is invisible to everyone else", "is tamper-proof" — each of
// which asserted what the first eight patterns refuse, in words none of them
// held. Treat a clean run as "no wording on this list appears", never as "the
// prose claims nothing".
//
// One synonym is deliberately absent. "is authenticated" is a true statement
// about Gropius — it checks a bearer token — so a pattern for it would refuse
// honest prose about the API key written near the mark, and a scan that
// refuses honest prose gets switched off. "A private network connection is
// authenticated." therefore passes; "authenticated and tamper-proof" is caught
// by the second half of the phrase.
var exposureClaims = []*regexp.Regexp{
	// "the connection is encrypted", "your traffic stays private". Negated and
	// disclaiming forms ("is not encrypted", "says nothing about how safe it
	// is") do not match, which is the point.
	regexp.MustCompile(`(?i)\b(is|are|stays?|remains?|will be)\s+(safe|secure|private|encrypted|protected|confidential|invisible|hidden)\b`),
	regexp.MustCompile(`(?i)\b(safely|securely|privately)\b`),
	// The exact wording adr-2609081118587999 rule 1 names as refused.
	regexp.MustCompile(`(?i)\bprivate and\b`),
	regexp.MustCompile(`(?i)\bend[\s-]to[\s-]end\b`),
	// "only your devices can reach it", "nobody else can see it".
	regexp.MustCompile(`(?i)\bonly (you|your|the operator)\b`),
	regexp.MustCompile(`(?i)\b(no[\s-]?(body|one)|nothing) else\b`),
	regexp.MustCompile(`(?i)\bonly be (reached|seen|read|used)\b`),
	// An encrypted tunnel, a secure path: a claim without a verb.
	regexp.MustCompile(`(?i)\b(encrypted|secure) (tunnel|path|connection|link|network|channel)\b`),
	// The paraphrases a review wrote past the eight above. Each is anchored on
	// a verb so it cannot fire on the same word used honestly elsewhere:
	// docs/ says a summary "is kept in their" account and the statistics
	// reference lists an "unreachable" outcome class, and neither matches.
	regexp.MustCompile(`(?i)\b(is|are|stays?|remains?|will be)\s+kept\s+(safe|secure|private|confidential)\b`),
	regexp.MustCompile(`(?i)\b(is|are|stays?|remains?|will be)\s+(unreachable|inaccessible)\b`),
	// "tamper-proof" needs no verb in front of it: it has no honest use in
	// this repository's prose, unlike "kept" or "unreachable".
	regexp.MustCompile(`(?i)\btamper[\s-]?proof\b`),
	regexp.MustCompile(`(?i)\b(reachable|visible|readable|usable)\s+only\b`),
	regexp.MustCompile(`(?i)\bonly\s+by\s+(you|your|machines you)\b`),
}

// theMark is what a passage about the mark says, and it is the mark's own
// words. A passage containing it is a passage explaining what Gropius told the
// operator about their addresses, which is what rule 1 governs.
const theMark = "private network"

// claim is one place a surface says what a network is worth.
type claim struct {
	surface string
	match   string
	passage string
}

func TestTheDocumentationAndThePanelClaimNothingAboutAPrivateNetwork(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	vendors, claims, passages := scanSurfaces(t, root)
	for _, v := range vendors {
		t.Errorf("%s names %q — the classifier cannot tell one product on that address range from another, so naming one states as a finding what is a guess (adr-2609081118587999 rule 1)", v.surface, v.match)
	}
	for _, c := range claims {
		t.Errorf("%s says %q about the private-network mark, in:\n\n%s\n\nThe mark says which network an address is on. It cannot say what that network is worth: the network can be published to the internet or shared with machines the operator does not own, and neither transition touches the address (adr-2609081118587999 rule 1)", c.surface, c.match, c.passage)
	}
	if passages == 0 {
		t.Error("nothing in docs/, the README or the control panel mentions the private-network mark any more — either it stopped being documented, which is its own problem, or this rule is watching the wrong files and is now asserting nothing about claims")
	}
}

// scanSurfaces returns the vendor names and the claims found in a tree, and
// how many passages were in scope. It takes a root so the plant test can point
// it at a copy of the tree with a claim written into it: a rule that is only
// ever run against clean prose is a rule nobody has seen work.
func scanSurfaces(t *testing.T, root string) (vendors, claims []claim, passages int) {
	t.Helper()
	prose := proseSurfaces(t, root)
	scripts := scriptSurfaces(t, root)
	if len(prose) == 0 || len(scripts) == 0 {
		t.Fatalf("found %d prose surfaces and %d script surfaces under %s — the scan is asserting nothing", len(prose), len(scripts), root)
	}
	for _, s := range append(append([]docSurface{}, prose...), scripts...) {
		lower := strings.ToLower(s.text)
		for _, vendor := range vendorNames {
			if strings.Contains(lower, vendor) {
				vendors = append(vendors, claim{surface: s.name, match: vendor})
			}
		}
	}
	for _, s := range prose {
		for _, para := range markPassages(paragraphs(s.text)) {
			passages++
			if m := claimIn(para); m != "" {
				claims = append(claims, claim{surface: s.name, match: m, passage: para})
			}
		}
	}
	for _, s := range scripts {
		for _, lit := range markLiterals(s.text) {
			passages++
			if m := claimIn(lit); m != "" {
				claims = append(claims, claim{surface: s.name, match: m, passage: lit})
			}
		}
	}
	return vendors, claims, passages
}

// markPassages is the scope: every paragraph rule 1 governs.
//
// A paragraph is in scope when it names the mark, when the paragraph on either
// side of it does, or when the heading of the section it sits in does. The
// bare "names the mark itself" test was the whole rule, and it missed the
// obvious placement: an explanation runs "Gropius marks a private network
// address." and then, in the next paragraph, "That connection is encrypted."
// The second paragraph is the claim and it never repeats the words.
//
// It stops at the section rather than running to the end of the file for a
// measured reason: unscoped, this scan fires on docs/ saying a model is
// protected and on the README saying Gropius is tested end-to-end, both honest
// and neither about a network, and a scan that refuses honest prose gets
// switched off.
func markPassages(paras []string) []string {
	var out []string
	inSection := false
	for i, p := range paras {
		if isHeading(p) {
			inSection = mentionsTheMark(p)
		}
		near := mentionsTheMark(p) || inSection
		if i > 0 && mentionsTheMark(paras[i-1]) {
			near = true
		}
		if i+1 < len(paras) && mentionsTheMark(paras[i+1]) {
			near = true
		}
		if near {
			out = append(out, p)
		}
	}
	return out
}

var headingLine = regexp.MustCompile(`(?m)\A#{1,6}\s`)

func isHeading(para string) bool { return headingLine.MatchString(strings.TrimSpace(para)) }

func mentionsTheMark(s string) bool {
	return strings.Contains(strings.ToLower(collapse(s)), theMark)
}

// markLiterals is the same scope over the panel's script: every string literal
// in a top-level function whose source mentions the mark, plus any literal that
// mentions it wherever it sits.
//
// The scope used to be three function names written down by hand, which is a
// list that goes stale the moment a fourth function renders anything beside the
// endpoint. Deriving it from the source closes that. What it deliberately does
// not do is scan every literal in the file: app.js says a model "is protected"
// and a computed size "is safe", and neither is about a network.
func markLiterals(src string) []string {
	var out []string
	for _, region := range jsRegions(src) {
		regionAboutTheMark := mentionsTheMark(region)
		for _, lit := range jsLiterals(region) {
			if regionAboutTheMark || mentionsTheMark(lit) {
				out = append(out, lit)
			}
		}
	}
	return out
}

// jsRegions splits a script at its top-level function declarations. The unit is
// the function, comments and all: a claim written in a function that renders
// the mark is in scope whether or not the literal repeats the mark's words.
var topLevelFunc = regexp.MustCompile(`(?m)^function\s`)

func jsRegions(src string) []string {
	starts := topLevelFunc.FindAllStringIndex(src, -1)
	if len(starts) == 0 {
		return []string{src}
	}
	out := []string{src[:starts[0][0]]}
	for i, s := range starts {
		end := len(src)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		out = append(out, src[s[0]:end])
	}
	return out
}

// jsLiterals returns the contents of every string and template literal in a
// piece of JavaScript, skipping comments. What an operator can see is what the
// code quotes, not what its comments say.
func jsLiterals(src string) []string {
	var out []string
	for i := 0; i < len(src); i++ {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			j := strings.Index(src[i:], "\n")
			if j < 0 {
				return out
			}
			i += j
		case strings.HasPrefix(src[i:], "/*"):
			j := strings.Index(src[i+2:], "*/")
			if j < 0 {
				return out
			}
			i += j + 3
		case src[i] == '\'' || src[i] == '"' || src[i] == '`':
			quote := src[i]
			j := i + 1
			for ; j < len(src); j++ {
				if src[j] == '\\' {
					j++
					continue
				}
				if src[j] == quote {
					break
				}
			}
			if j >= len(src) {
				return out
			}
			out = append(out, src[i+1:j])
			i = j
		}
	}
	return out
}

// The scan has to fail on a real claim, and it has to fail on one in every
// surface and every scope it says it covers, or the prose passing means
// nothing. Each case below is a claim planted where a review found this scan
// blind, written into a copy of the tree and run through the whole rule.
func TestTheClaimScanCatchesWhatItSaysItCatches(t *testing.T) {
	cases := []struct {
		name string
		// where is the path, relative to the tree root, the plant is written
		// to. An existing file is appended to; a new one is created.
		where string
		plant string
	}{
		{
			"a claim in a page that is already scanned",
			"docs/getting-started.md",
			"\n## More about the mark\n\nAn address on a private network is safe to hand out.\n",
		},
		{
			"a claim in a page in a subdirectory of docs/",
			"docs/guides/mesh-networks.md",
			"# Mesh networks\n\nOn a private network, only your own devices can reach the server.\n",
		},
		{
			"a claim in the README, which was scanned by nothing",
			"README.md",
			"\n## Private networks\n\nA private network endpoint is private and encrypted end-to-end.\n",
		},
		{
			"a claim in a string the panel renders, outside the functions that build the endpoint row",
			"internal/ui/static/app.js",
			"\nfunction renderSomethingElse() {\n  return 'Traffic to a private network address travels securely.';\n}\n",
		},
		{
			"a claim in a literal naming the mark, in a function that renders nothing else about it",
			"internal/ui/static/app.js",
			"\nfunction renderTooltip() {\n  return 'The private network mark means nothing else can see the traffic.';\n}\n",
		},
		{
			"a claim in the paragraph AFTER the one that names the mark",
			"docs/getting-started.md",
			"\nGropius marks an address that sits on a private network.\n\nThat connection is encrypted, so the API key never crosses the cafe's Wi-Fi.\n",
		},
		{
			"a claim in the paragraph BEFORE the one that names the mark",
			"docs/getting-started.md",
			"\nThe endpoint you copy from that row is secure.\n\nGropius marks an address that sits on a private network.\n",
		},
		{
			"a claim further down the section whose heading names the mark",
			"docs/getting-started.md",
			"\n## What the private network mark means\n\nGropius found the address on a tunnel interface.\n\nIt names the network the address belongs to.\n\nOnly your own devices can reach a server at that address.\n",
		},
		{
			"a claim wrapped in a double negative, which the negator check disarmed on",
			"docs/getting-started.md",
			"\nThere is no doubt that an address on a private network is encrypted.\n",
		},
		{
			"a claim about who else can reach it, wrapped the same way",
			"docs/getting-started.md",
			"\nThere is no question that a private network address can only be reached by machines you own.\n",
		},
		{
			"a claim about the tunnel itself, with no verb to negate",
			"docs/getting-started.md",
			"\nA private network address goes over an encrypted tunnel.\n",
		},
		{
			"a claim in the control panel's markup",
			"internal/ui/static/index.html",
			"\n<p>An address on a private network is protected.</p>\n",
		},
		{
			"a vendor named in a subdirectory of docs/",
			"docs/guides/mesh-networks.md",
			"# Mesh networks\n\nGropius marks the address your tailnet gave this Mac.\n",
		},
		{
			"a vendor named in the README",
			"README.md",
			"\nGropius detects Tailscale and marks the address it hands out.\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := plantedTree(t, c.where, c.plant)
			vendors, claims, _ := scanSurfaces(t, root)
			if len(vendors) == 0 && len(claims) == 0 {
				t.Errorf("the scan misses this, planted in %s:\n\n%s\nIt is exactly what adr-2609081118587999 rule 1 refuses, and nothing else in the repository would catch it", c.where, c.plant)
			}
		})
	}
}

// A negator suppresses a claim, so a negator is the way to smuggle one past.
// Each sentence below names the mark, sits in scope, makes a claim in the
// scan's own vocabulary, and was measured to pass — the whole set was watched
// to return "" before the patterns and the scope of the suppression were
// changed.
//
// The first four are double negatives the cancellation list did not spell:
// "nobody"/"none" are one word rather than "no" plus a head, and a negator can
// stand three words away from the head it negates. The fifth is the general
// case and the one that mattered: the suppression was scoped to the SENTENCE,
// so a negator anywhere earlier in it disarmed an independent later clause,
// and "…, and is encrypted" is a claim standing on its own.
//
// The last six are the other half — the claim vocabulary is a closed list, and
// a closed list is evaded by synonym. These are the paraphrases that were
// found; the ones that have not been thought of still pass, which is what the
// comment on exposureClaims now says instead of implying otherwise.
func TestANegatorDoesNotDisarmAClaimItDoesNotNegate(t *testing.T) {
	for _, s := range []string{
		"Nobody doubts that a private network connection is encrypted.",
		"None dispute that a private network address is secure.",
		"No sane person doubts that a private network address is secure.",
		"It is not in doubt that a private network connection is encrypted.",
		"A private network address can never be reached by anyone but you, and is encrypted.",

		"A private network address is kept private.",
		"A private network address is unreachable from the internet.",
		"A private network address is reachable only by you.",
		"A private network connection is confidential.",
		"A private network address is invisible to everyone else.",
		"A private network connection is authenticated and tamper-proof.",
	} {
		if claimIn(s) == "" {
			t.Errorf("the claim scan misses %q — it states what the network is worth, which is what adr-2609081118587999 rule 1 refuses", s)
		}
	}
}

// And the other half: the scan must leave honest prose alone. A rule that
// refuses the disclaimer docs/ already carries is a rule somebody switches off.
//
// These are sentences the PATTERNS have to leave alone, which is not the same
// set as the sentences the RULE leaves alone. docs/ also says "a model is
// protected from eviction" and "files only your account can open"; those match
// a pattern and are honest, and what saves them is the scope — they are nowhere
// near the mark. Scope is measured by the test above running over the real
// docs/ and finding nothing, not asserted here.
func TestTheClaimScanLeavesHonestProseAlone(t *testing.T) {
	honest := []string{
		// The sentence docs/getting-started.md carries today.
		"An address in that list that sits on a private network carries a mark saying so: the mark names the network the address belongs to, and says nothing about how safe it is or who else can reach it.",
		"The private network mark is an observation, not a promise.",
		"Gropius serves plain HTTP: nothing it sends is encrypted, on a private network or anywhere else.",
		"A private network address is one Gropius found on a tunnel interface.",
		// The double-negative cancellation must not swallow a plain denial
		// that happens to contain the same words.
		"Gropius does not claim that a private network connection is encrypted.",
		"Nothing here says the traffic is safe.",
	}
	for _, h := range honest {
		if m := claimIn(h); m != "" {
			t.Errorf("the claim scan fires on %q (matching %q), which claims nothing — a scan that refuses honest prose gets switched off", h, m)
		}
	}
}

// plantedTree copies the surfaces this rule scans into a temporary root and
// writes one plant into it. Copying rather than editing the repository is what
// lets the plants be checked in and run on every commit.
func plantedTree(t *testing.T, where, plant string) string {
	t.Helper()
	src, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	for _, rel := range []string{"docs", "README.md", filepath.FromSlash("internal/ui/static")} {
		copyTree(t, filepath.Join(src, rel), filepath.Join(dst, rel))
	}
	target := filepath.Join(dst, filepath.FromSlash(where))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(plant); err != nil {
		t.Fatal(err)
	}
	return dst
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// cancelledNegations are the phrases in which a negator negates nothing. "There
// is no doubt that an address on a private network is encrypted" contains "no",
// asserts the claim, and walked past the negator check — a review planted it
// and it passed.
//
// WHAT THIS DOES NOT CATCH, stated as measured rather than as intended. Three
// earlier versions of this comment each named a narrower limit than the one
// that held, so this one names the shape rather than an instance:
//
//   - The negative-polarity HEADS are a closed list — doubt, question, deny,
//     dispute, and "not un-". A double negative built on a head that is not
//     here ("it is beyond argument that…", "few would deny…") is not
//     cancelled. When the head is missing the negator is also usually missing,
//     so the claim tends to fire anyway; "few", "hardly" and "scarcely" are
//     the cases where it does not, and they get through.
//   - The negator has to stand within three words of its head. "No engineer
//     who has looked at the protocol doubts that…" is further than that and is
//     not cancelled, so the claim it carries is suppressed.
//   - A negator in the SAME CLAUSE as the claim still suppresses it, and that
//     is deliberate: it is what lets "Gropius does not claim that a private
//     network connection is encrypted" through. "It is not true that a private
//     network address is encrypted" reads the same way to this scan, and so
//     does the pathological "is never not encrypted".
//
// What it must not do is break a plain denial — "does not claim that the
// connection is encrypted" has the same shape and asserts the opposite — which
// is why the fix is to cancel these phrases rather than to stop looking behind
// a subordinating "that".
var cancelledNegations = regexp.MustCompile(`(?i)\b(no|not|never|nobody|none|nothing|cannot|can't)\b(\s+\w+){0,3}?\s+(doubt|doubts|doubted|question|questions|questioned|questioning|deny|denies|denied|denying|dispute|disputes|disputed|disputing)\b|\bnot un`)

// negators are the words a sentence denying a claim is built from. A doc that
// says "nothing it sends is encrypted" contains "is encrypted" and asserts its
// opposite, and a scan that cannot tell the two apart is a scan somebody
// switches off.
//
// The check is CLAUSE-scoped, not sentence-scoped, and the difference is a
// hole a review walked through: "A private network address can never be
// reached by anyone but you, and is encrypted." denies one thing and asserts
// another, and while the scope was the sentence the "never" in the first
// clause disarmed the claim in the second. Only what stands between the last
// clause boundary and the match counts now.
var negators = regexp.MustCompile(`(?i)\b(no|not|nothing|never|nor|none|nobody|without|cannot|can't)\b`)

// claimIn returns the first claim a passage makes, or "" when it makes none.
func claimIn(para string) string {
	para = collapse(para)
	for _, claim := range exposureClaims {
		for _, loc := range claim.FindAllStringIndex(para, -1) {
			clause := cancelledNegations.ReplaceAllString(clauseBefore(para, loc[0]), " ")
			if negators.MatchString(clause) {
				continue
			}
			return para[loc[0]:loc[1]]
		}
	}
	return ""
}

// clauseBoundary is what ends the span a negator reaches across: sentence
// punctuation, a comma, or a coordinating conjunction. A subordinating "that"
// is deliberately not here — "does not claim that X is encrypted" has to stay
// suppressed, and it is one clause for this purpose.
var clauseBoundary = regexp.MustCompile(`(?i)[.:;!?,]|\s(and|but|or|nor|yet|while|whereas|although|though)\s`)

// clauseBefore is the text from the last clause boundary up to an offset.
func clauseBefore(s string, at int) string {
	head := s[:at]
	if all := clauseBoundary.FindAllStringIndex(head, -1); len(all) > 0 {
		head = head[all[len(all)-1][1]:]
	}
	return head
}

// collapse turns every run of whitespace into one space. docs/ is hard-wrapped
// and the panel's markup is indented, so without this "private" and "network"
// on two lines are not the mark, and "only your" split across a line break is
// not a claim.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

type docSurface struct {
	name string
	text string
}

// proseSurfaces is every user-facing page and the control panel's own markup:
// the places prose about the mark can reach an operator without passing through
// any of the scans that guard the mark itself.
//
// docs/ is walked rather than listed, because it was read one directory deep
// and a page in docs/guides/ was scanned by nothing at all. README.md is here
// because it is the first page anybody reads and it was scanned by nothing
// either.
func proseSurfaces(t *testing.T, root string) []docSurface {
	t.Helper()
	var out []docSurface
	docs := filepath.Join(root, "docs")
	err := filepath.WalkDir(docs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, docSurface{name: filepath.ToSlash(rel), text: readText(t, path)})
		return nil
	})
	if err != nil {
		t.Fatalf("walking docs/: %v", err)
	}
	for _, rel := range []string{"README.md", "internal/ui/static/index.html"} {
		out = append(out, docSurface{name: rel, text: readText(t, filepath.Join(root, filepath.FromSlash(rel)))})
	}
	return out
}

// scriptSurfaces is the control panel's script: prose reaches an operator from
// a string literal in it as surely as from a page.
func scriptSurfaces(t *testing.T, root string) []docSurface {
	t.Helper()
	const panel = "internal/ui/static/app.js"
	return []docSurface{{name: panel, text: readText(t, filepath.Join(root, filepath.FromSlash(panel)))}}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// paragraphs splits on blank lines. The unit matters: docs/ is hard-wrapped, so
// "sits on a private" and "network carries a mark" are different lines and a
// line-scoped scan would not see the mark named at all.
func paragraphs(text string) []string {
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		if para = strings.TrimSpace(para); para != "" {
			out = append(out, para)
		}
	}
	return out
}
