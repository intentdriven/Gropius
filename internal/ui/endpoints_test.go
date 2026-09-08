package ui

import (
	"strings"
	"testing"
)

// The Connect page is where an operator picks the address they will paste into
// a client, so it is the surface the private-network mark exists for. Two
// things about it have to hold, and neither is visible to a Go test of the
// server: the mark is shown beside the URL rather than inside it, and every
// place that hands the operator something to paste hands them the URL alone.

// The row is the URL and, when Gropius saw one, the network the address is on.
// Nothing else: the mark is a separate element, so no clipboard, example or
// copy of the row can pick it up by accident.
func TestEndpointRowShowsTheMarkBesideTheURL(t *testing.T) {
	cases := []struct {
		name string
		ep   string
		want string
	}{
		{
			"an ordinary address carries no mark",
			`{"url":"http://192.168.1.5:11535/v1","network":""}`,
			`<span>esc(http://192.168.1.5:11535/v1)</span>`,
		},
		{
			"an address on a private network is marked",
			`{"url":"http://100.101.102.103:11535/v1","network":"private network"}`,
			`<span>esc(http://100.101.102.103:11535/v1)</span><span class="pill">esc(private network)</span>`,
		},
		{
			// The panel is fed by a stream it does not control, and an entry
			// with no network at all is the ordinary case on every Mac with no
			// private network.
			"a missing network is no mark",
			`{"url":"http://192.168.1.5:11535/v1"}`,
			`<span>esc(http://192.168.1.5:11535/v1)</span>`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evalPanel(t, "endpointLine(endpointOf("+c.ep+"))", "endpointOf", "endpointLine")
			if got != c.want {
				t.Errorf("endpointLine(%s) = %q, want %q", c.ep, got, c.want)
			}
		})
	}
}

// The mark is rendered through escapeHtml, like every other string the panel
// puts in markup. It comes from the server rather than from a person, but the
// row is built with innerHTML and an unescaped field there is a hole whoever
// opens it will not notice.
func TestTheMarkIsEscaped(t *testing.T) {
	got := evalPanel(t,
		`endpointLine(endpointOf({"url":"u","network":"<b>x</b>"}))`,
		"endpointOf", "endpointLine")
	if !strings.Contains(got, "esc(<b>x</b>)") {
		t.Errorf("endpointLine did not escape the mark: %q", got)
	}
}

// What the operator pastes is the URL. The Copy button and both examples take
// the URL field; none of them takes the rendered row, which carries the mark.
// A base URL with "private network" appended to it is not a base URL.
func TestTheClipboardAndTheExamplesTakeTheURLAlone(t *testing.T) {
	body := extractFunction(t, readPanelSource(t), "renderConnect")
	for _, fragment := range []string{
		"navigator.clipboard.writeText(ep.url)",
		"row.innerHTML = endpointLine(ep);",
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("renderConnect no longer contains %s", fragment)
		}
	}
	if !strings.Contains(body, "eps[0].url") {
		t.Error("renderConnect no longer derives the examples' base URL from the URL field — the curl and Python examples can then carry the mark")
	}
	for _, wrong := range []string{
		"writeText(endpointLine",
		"clipboard.writeText(ep)",
	} {
		if strings.Contains(body, wrong) {
			t.Errorf("renderConnect copies %s — the clipboard takes the URL and nothing else", wrong)
		}
	}
}

// adr-2609081118587999 rule 1: the panel may say which network an address is
// on and may not say what that network is worth. The mark's own text comes
// from the server, so what this guards is the panel's own strings — a label,
// a heading or a tooltip added beside the mark that turns an observation into
// a guarantee, or names a vendor this detection cannot actually tell apart.
func TestTheConnectPageNamesNoVendorAndPromisesNothing(t *testing.T) {
	forbidden := []string{
		"tailscale", "tailnet", "headscale", "zerotier", "wireguard", "nebula",
		"encrypt", "secure", "safe", "only", "vpn", "private and",
	}
	src := readPanelSource(t)
	for _, fn := range []string{"renderConnect", "endpointLine", "endpointOf"} {
		for _, lit := range jsLiterals(extractFunction(t, src, fn)) {
			lower := strings.ToLower(lit)
			for _, word := range forbidden {
				if strings.Contains(lower, word) {
					t.Errorf("%s renders the string %q, which contains %q — the panel states which network an address is on and nothing more", fn, lit, word)
				}
			}
		}
	}
}

// jsLiterals returns the contents of every string and template literal in a
// piece of JavaScript, skipping comments. It is the panel's equivalent of
// reading the string literals out of a Go package: what an operator can see is
// what the code quotes, not what its comments say.
func jsLiterals(src string) []string {
	var out []string
	for i := 0; i < len(src); i++ {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			i += skipTo(src[i:], "\n")
		case strings.HasPrefix(src[i:], "/*"):
			i += skipTo(src[i:], "*/")
		case src[i] == '\'' || src[i] == '"' || src[i] == '`':
			n := skipQuoted(src[i:])
			out = append(out, src[i+1:i+n])
			i += n
		}
	}
	return out
}
