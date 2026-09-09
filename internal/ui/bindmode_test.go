package ui

import (
	"encoding/json"
	"strings"
	"testing"
)

// The third choice in Settings. It is not a host, so it cannot be an address in
// the same select without something turning it into one: the pane shows a mode,
// and a save posts the mode in its own field and leaves Host as the operator
// last set it (adr-2609091123526871 rule 6).
//
// Leaving Host alone is not tidiness. A save must never be refused, or
// silently change, over a field the operator did not touch — and switching the
// mode off has to put back the bind they had.
func TestTheBindSelectCarriesTheModeInItsOwnField(t *testing.T) {
	cases := []struct {
		name   string
		chosen string
		stored string
		want   string
	}{
		{"the wildcard", "0.0.0.0", "0.0.0.0", `{"host":"0.0.0.0","bind_mode":""}`},
		{"this Mac", "127.0.0.1", "0.0.0.0", `{"host":"127.0.0.1","bind_mode":""}`},
		{"the private network keeps the stored host", "private-network", "192.0.2.5", `{"host":"192.0.2.5","bind_mode":"private-network"}`},
		{"and keeps the wildcard when that is what was stored", "private-network", "0.0.0.0", `{"host":"0.0.0.0","bind_mode":"private-network"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evalBindMode(t, "report(bindSelectBody("+quote(c.chosen)+", "+quote(c.stored)+"));")
			if !sameJSON(t, got, c.want) {
				t.Errorf("bindSelectBody(%q, %q) = %s, want %s", c.chosen, c.stored, got, c.want)
			}
		})
	}
}

// What the select shows for a configuration. Under the mode it shows the mode;
// otherwise it shows the bind address, which is what it always did.
func TestTheBindSelectShowsTheModeWhenTheModeIsInForce(t *testing.T) {
	cases := []struct{ config, want string }{
		{`{"host":"0.0.0.0","bind_mode":""}`, `"0.0.0.0"`},
		{`{"host":"192.0.2.5","bind_mode":""}`, `"192.0.2.5"`},
		{`{"host":"192.0.2.5","bind_mode":"private-network"}`, `"private-network"`},
	}
	for _, c := range cases {
		got := evalBindMode(t, "report(bindSelectValue("+c.config+"));")
		if got != c.want {
			t.Errorf("bindSelectValue(%s) = %s, want %s", c.config, got, c.want)
		}
	}
}

// The mode can be chosen only where there is an address to choose, and the
// pane says which address that is — the amendment's second condition, on the
// surface the operator chooses from.
//
// The exception is the one that matters most: a mode already in force stays
// selectable even with nothing to select, because a select cannot show a value
// it does not offer. Disabling it there would blank the control and post the
// mode away on the next save, which is the fault the bind select was just
// fixed for.
func TestThePrivateChoiceIsOfferedOnlyWhereThereIsAnAddressToChoose(t *testing.T) {
	cases := []struct {
		name       string
		bind       string
		wantOff    bool
		wantInText string
	}{
		{
			name:       "one address matches: it is named",
			bind:       `{"mode":"","candidates":["100.101.102.103"]}`,
			wantInText: "100.101.102.103",
		},
		{
			name:       "nothing matches: the choice is not offered",
			bind:       `{"mode":"","candidates":[]}`,
			wantOff:    true,
			wantInText: "no",
		},
		{
			name:       "the mode is in force with nothing to select: still offered, and says so",
			bind:       `{"mode":"private-network","candidates":[],"refusal":"no address on this Mac is on a private network"}`,
			wantInText: "no",
		},
		{
			name:       "several match: offered, and names them rather than counting them",
			bind:       `{"mode":"","candidates":["100.101.102.103","100.64.7.7"]}`,
			wantInText: "100.64.7.7",
		},
		{
			// What the running mode bound, not what it would bind now. The
			// address a private network hands out changes, and the pane has to
			// name the one the server is answering on — which is the whole of
			// the amendment's second condition.
			name:       "the mode is running: the address it bound, not the one that matches now",
			bind:       `{"mode":"private-network","selected":"100.101.102.103","candidates":["100.64.7.7"]}`,
			wantInText: "100.101.102.103",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evalBindMode(t, "report([privateBindDisabled("+c.bind+"), privateBindLabel("+c.bind+")]);")
			var pair []json.RawMessage
			if err := json.Unmarshal([]byte(got), &pair); err != nil || len(pair) != 2 {
				t.Fatalf("the panel returned %q: %v", got, err)
			}
			var off bool
			var label string
			if err := json.Unmarshal(pair[0], &off); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(pair[1], &label); err != nil {
				t.Fatal(err)
			}
			if off != c.wantOff {
				t.Errorf("disabled = %v, want %v", off, c.wantOff)
			}
			if !strings.Contains(strings.ToLower(label), c.wantInText) {
				t.Errorf("the label is %q, which does not say %q — the operator has to be able to see what the mode would bind", label, c.wantInText)
			}
		})
	}
}

// adr-2609081118587999 rule 1, on this surface: the pane may say which network
// an address is on and may not say what that network is worth. A vendor is
// refused for a second reason — this detection cannot tell one product on the
// range from another, so a name would be a guess.
//
// "only" is deliberately absent from the list, unlike the endpoint list's copy
// of it: this pane's existing options say "this Mac only", which is a statement
// about which addresses are bound and not a claim about a network.
func TestTheBindModeLabelsNameNoVendorAndPromiseNothing(t *testing.T) {
	forbidden := []string{
		"tailscale", "tailnet", "headscale", "zerotier", "wireguard", "nebula",
		"encrypt", "secure", "safe", "vpn", "private and",
	}
	src := readPanelSource(t)
	for _, fn := range []string{"privateBindLabel"} {
		for _, lit := range jsLiterals(extractFunction(t, src, fn)) {
			for _, word := range forbidden {
				if strings.Contains(strings.ToLower(lit), word) {
					t.Errorf("%s renders %q, which contains %q — the pane says which network an address is on and nothing more", fn, lit, word)
				}
			}
		}
	}
}

// The pure functions above are worth testing only while renderSettings and the
// save actually use them.
func TestTheSettingsPaneUsesTheBindModeFunctions(t *testing.T) {
	src := readPanelSource(t)
	render := extractFunction(t, src, "renderSettings")
	for _, want := range []string{"renderBindMode(", "bindSelectValue("} {
		if !strings.Contains(render, want) {
			t.Errorf("renderSettings no longer calls %s — the pane would show the mode as a blank control", want)
		}
	}
	if !strings.Contains(src, "bindSelectBody(") {
		t.Error("nothing calls bindSelectBody — the save would post a mode as a host, which config.Validate refuses")
	}
}

// The option has to exist in the markup for the select to carry it at all.
func TestTheMarkupOffersTheThirdChoice(t *testing.T) {
	page, err := assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), `value="private-network"`) {
		t.Error("the bind select has no private-network option — a Go capability with no panel equivalent is a gap")
	}
	// Nothing re-binds while the server runs, and the Connect tab now lists
	// what was acquired rather than what is stored — so a bind saved and not
	// yet in force is a difference the operator can see and has to be able to
	// explain.
	if !strings.Contains(string(page), "when Gropius next starts") {
		t.Error("the pane does not say that a bind change applies at the next start")
	}
}

// evalBindMode runs a snippet against the bind-mode functions lifted out of
// app.js. report() is how a snippet answers.
func evalBindMode(t *testing.T, snippet string) string {
	t.Helper()
	src := readPanelSource(t)
	var b strings.Builder
	b.WriteString("const report = (v) => process.stdout.write(JSON.stringify(v));\n")
	b.WriteString(constDecl(t, src, "PRIVATE_BIND"))
	for _, name := range []string{"bindSelectValue", "bindSelectBody", "privateBindLabel", "privateBindDisabled", "bindNoticeText"} {
		b.WriteString(extractFunction(t, src, name))
		b.WriteString("\n")
	}
	b.WriteString(snippet)
	return evalJS(t, b.String())
}

// constDecl lifts a single top-level `const NAME = ...;` line out of the panel
// source, so a snippet can run against the same value the panel uses rather
// than a copy of it.
func constDecl(t *testing.T, src, name string) string {
	t.Helper()
	at := strings.Index(src, "const "+name+" =")
	if at < 0 {
		t.Fatalf("the panel has no top-level const %s", name)
	}
	end := strings.Index(src[at:], "\n")
	if end < 0 {
		t.Fatalf("const %s is not terminated", name)
	}
	return src[at:at+end] + "\n"
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func sameJSON(t *testing.T, got, want string) bool {
	t.Helper()
	var a, b map[string]any
	if err := json.Unmarshal([]byte(got), &a); err != nil {
		t.Fatalf("the panel returned %q, which is not an object: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		return false
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

// A bind that narrowed says so where the operator changes it. The log is read
// by whoever runs the server headless; the pane is read by everyone else, and
// without this the only difference between a mode that is running and one that
// found nothing to bind is an endpoint that is missing from another tab.
func TestThePaneSaysWhenTheBindNarrowed(t *testing.T) {
	cases := []struct{ bind, want string }{
		{`{"mode":"private-network","candidates":[],"refusal":"no address on this Mac is on a private network"}`, `"no address on this Mac is on a private network"`},
		{`{"mode":"private-network","candidates":["100.101.102.103"],"selected":"100.101.102.103"}`, `""`},
		{`{"mode":"","candidates":[]}`, `""`},
	}
	for _, c := range cases {
		if got := evalBindMode(t, "report(bindNoticeText("+c.bind+"));"); got != c.want {
			t.Errorf("bindNoticeText(%s) = %s, want %s", c.bind, got, c.want)
		}
	}
	if !strings.Contains(extractFunction(t, readPanelSource(t), "renderSettings"), "bindNotice") {
		t.Error("renderSettings does not render the notice — a bind that narrowed would be silent in the pane")
	}
	page, err := assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), `id="bindNotice"`) {
		t.Error("the pane has no element for the notice")
	}
}
