package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// adr-2609081118587999 rule 1: what Gropius says about a private network may
// state what it observed and may not state what that network is worth. The
// mark's own vocabulary is guarded where the mark is built — internal/netshape,
// the Network values the gateway emits, and the three panel functions that
// render them. This is the same rule over the prose around it: the
// user-facing documentation and the control panel's own markup, which are
// where a sentence explaining the mark would go, and where a sentence
// explaining it wrongly would do the damage the mark exists to prevent.
//
// It is deliberately NOT the bare-word scan the other three surfaces use. Those
// scan a handful of short fixed strings, where "only" and "safe" have no
// innocent use. Prose is different: docs/ says "files only your account can
// open" and "how long a model is safe from eviction", both honest and neither
// about a network. So this scans for CLAIMS — the shapes a sentence takes when
// it tells an operator what their exposure is — and it scans them only in the
// passages that are about the mark.
//
// That is what lets docs/getting-started.md keep the disclaimer it already
// carries: "says nothing about how safe it is or who else can reach it" uses
// the word and makes the opposite of the claim, and no exemption is needed for
// it because no claim pattern matches it. If one ever did, the fix is a better
// pattern and not a list of blessed sentences.

// vendorNames are never right anywhere in either surface, in prose or in
// markup. The classifier cannot tell one product on the range from another, so
// naming one is a guess presented as a finding — and the reason has nothing to
// do with claims, which is why these stay a bare-word scan.
var vendorNames = []string{
	"tailscale", "tailnet", "headscale", "zerotier", "wireguard", "nebula",
}

// exposureClaims are the shapes a sentence takes when it tells an operator
// what a network is worth rather than which network an address is on. Each has
// to survive the transitions Gropius cannot see — the network published to the
// internet, shared with machines the operator does not own, logged out from —
// and none of these does.
var exposureClaims = []*regexp.Regexp{
	// "the connection is encrypted", "your traffic stays private". Negated and
	// disclaiming forms ("is not encrypted", "says nothing about how safe it
	// is") do not match, which is the point.
	regexp.MustCompile(`(?i)\b(is|are|stays?|remains?|will be)\s+(safe|secure|private|encrypted|protected)\b`),
	regexp.MustCompile(`(?i)\b(safely|securely|privately)\b`),
	// The exact wording adr-2609081118587999 rule 1 names as refused.
	regexp.MustCompile(`(?i)\bprivate and\b`),
	regexp.MustCompile(`(?i)\bend[\s-]to[\s-]end\b`),
	// "only your devices can reach it", "nobody else can see it".
	regexp.MustCompile(`(?i)\bonly (you|your|the operator)\b`),
	regexp.MustCompile(`(?i)\bno[\s-]?(body|one) else\b`),
	regexp.MustCompile(`(?i)\bonly be (reached|seen|read|used)\b`),
	// An encrypted tunnel, a secure path: a claim without a verb.
	regexp.MustCompile(`(?i)\b(encrypted|secure) (tunnel|path|connection|link|network|channel)\b`),
}

// theMark is what a passage about the mark says, and it is the mark's own
// words. A paragraph containing it is a paragraph explaining what Gropius told
// the operator about their addresses, which is the passage rule 1 governs.
const theMark = "private network"

func TestTheDocumentationAndThePanelClaimNothingAboutAPrivateNetwork(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	surfaces := docSurfaces(t, root)
	if len(surfaces) == 0 {
		t.Fatal("no documentation or panel markup found — the scan is asserting nothing")
	}

	var passages int
	for _, s := range surfaces {
		lower := strings.ToLower(s.text)
		for _, vendor := range vendorNames {
			if strings.Contains(lower, vendor) {
				t.Errorf("%s names %q — the classifier cannot tell one product on that address range from another, so naming one states as a finding what is a guess (adr-2609081118587999 rule 1)", s.name, vendor)
			}
		}
		for _, para := range paragraphs(s.text) {
			if !strings.Contains(strings.ToLower(collapse(para)), theMark) {
				continue
			}
			passages++
			if m := claimIn(para); m != "" {
				t.Errorf("%s says %q about the private-network mark, in:\n\n%s\n\nThe mark says which network an address is on. It cannot say what that network is worth: the network can be published to the internet or shared with machines the operator does not own, and neither transition touches the address (adr-2609081118587999 rule 1)", s.name, m, para)
			}
		}
	}
	if passages == 0 {
		t.Error("nothing in docs/ or the control panel's markup mentions the private-network mark any more — either it stopped being documented, which is its own problem, or this rule is watching the wrong files and is now asserting nothing about claims")
	}
}

// The scan has to fail on a real claim, or the paragraph on it passing means
// nothing. These are the sentences somebody writes when they are being
// helpful, next to the one already in docs/getting-started.md, which has to go
// on passing.
func TestTheClaimScanCatchesAClaimAndLeavesTheDisclaimerAlone(t *testing.T) {
	claims := []string{
		"An address on a private network is safe to hand out.",
		"The private network is encrypted, so the API key never crosses the café's Wi-Fi.",
		"On a private network, only your own devices can reach the server.",
		"A private network endpoint is private and encrypted end-to-end.",
		"Traffic to a private network address travels securely.",
		"An address on a private network can only be reached by machines you own.",
		"Gropius marks the private network so you know the connection is protected.",
		"The private network mark means nobody else can see the traffic.",
		"A private network address goes over an encrypted tunnel.",
	}
	for _, c := range claims {
		if !claimed(c) {
			t.Errorf("the claim scan misses %q — it is exactly what rule 1 refuses", c)
		}
	}

	honest := []string{
		// The sentence docs/getting-started.md carries today.
		"An address in that list that sits on a private network carries a mark saying so: the mark names the network the address belongs to, and says nothing about how safe it is or who else can reach it.",
		"The private network mark is an observation, not a promise.",
		"Gropius serves plain HTTP: nothing it sends is encrypted, on a private network or anywhere else.",
		"A private network address is one Gropius found on a tunnel interface.",
	}
	for _, h := range honest {
		if claimed(h) {
			t.Errorf("the claim scan fires on %q, which claims nothing — a scan that refuses honest prose gets switched off", h)
		}
	}
}

func claimed(s string) bool { return claimIn(s) != "" }

// negators are the words a sentence denying a claim is built from. A doc that
// says "nothing it sends is encrypted" contains "is encrypted" and asserts its
// opposite, and a scan that cannot tell the two apart is a scan somebody
// switches off. The check is sentence-scoped: only what stands between the
// last sentence break and the match counts, so a denial in one sentence does
// not excuse a claim in the next.
var negators = regexp.MustCompile(`(?i)\b(no|not|nothing|never|nor|none|nobody|without|cannot|can't)\b`)

// claimIn returns the first claim a passage makes, or "" when it makes none.
func claimIn(para string) string {
	para = collapse(para)
	for _, claim := range exposureClaims {
		for _, loc := range claim.FindAllStringIndex(para, -1) {
			if negators.MatchString(sentenceBefore(para, loc[0])) {
				continue
			}
			return para[loc[0]:loc[1]]
		}
	}
	return ""
}

// sentenceBefore is the text from the last sentence break up to an offset.
func sentenceBefore(s string, at int) string {
	head := s[:at]
	if i := strings.LastIndexAny(head, ".:;!?"); i >= 0 {
		head = head[i+1:]
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

// docSurfaces is every user-facing page and the control panel's own markup:
// the two places prose about the mark can reach an operator without passing
// through any of the three scans that guard the mark itself.
func docSurfaces(t *testing.T, root string) []docSurface {
	t.Helper()
	var out []docSurface
	entries, err := os.ReadDir(filepath.Join(root, "docs"))
	if err != nil {
		t.Fatalf("reading docs/: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, docSurface{
			name: "docs/" + e.Name(),
			text: readText(t, filepath.Join(root, "docs", e.Name())),
		})
	}
	const panel = "internal/ui/static/index.html"
	out = append(out, docSurface{
		name: panel,
		text: readText(t, filepath.Join(root, filepath.FromSlash(panel))),
	})
	return out
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// paragraphs splits on blank lines. The unit matters: docs/ is hard-wrapped,
// so "sits on a private" and "network carries a mark" are different lines and
// a line-scoped scan would not see the mark named at all.
func paragraphs(text string) []string {
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		if para = strings.TrimSpace(para); para != "" {
			out = append(out, para)
		}
	}
	return out
}
