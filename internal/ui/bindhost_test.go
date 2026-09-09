package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The bind-address control is a <select> offering two addresses, and an
// HTMLSelectElement has no notion of a value it does not carry: assigning one
// sets selectedIndex to -1 and leaves .value the empty string. Every save from
// a pane whose stored host was anything else therefore posted host:"" — a save
// that changed nothing about the bind included — and the server refused it,
// naming a field the pane never showed the operator as wrong.
//
// The fix is to render the stored host as an option of its own before it is
// assigned. extraBindOption is the pure half of that: which address the select
// has to be given, or null when it already offers it.
func TestExtraBindOptionNamesAHostTheSelectDoesNotOffer(t *testing.T) {
	const offered = `["0.0.0.0","127.0.0.1"]`
	cases := []struct {
		name string
		host string
		want string
	}{
		{"the wildcard the select already offers", "0.0.0.0", `null`},
		{"the loopback the select already offers", "127.0.0.1", `null`},
		// The four binds config.Validate accepts and this select never
		// offered. Each of them is a bind Gropius starts on.
		{"loopback by name", "localhost", `"localhost"`},
		{"IPv6 loopback", "[::1]", `"[::1]"`},
		{"another loopback address", "127.0.0.2", `"127.0.0.2"`},
		{"one specific LAN address", "192.0.2.5", `"192.0.2.5"`},
		// Nothing stored yet is not a bind: the select keeps the option its
		// markup already selects rather than growing an empty one.
		{"no host at all", "", `null`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expr := fmt.Sprintf("extraBindOption(%q, %s)", c.host, offered)
			if got := evalPanelExpr(t, expr, "extraBindOption"); got != c.want {
				t.Errorf("%s = %s, want %s", expr, got, c.want)
			}
		})
	}
}

// The acceptance test for the fix: the value the form would post, taken from a
// select that behaves as the browser's does — assigning a value it does not
// offer leaves it empty. Without the extra option every case below posts "",
// which config.Validate refuses.
func TestTheBindSelectRoundTripsTheStoredHost(t *testing.T) {
	for _, host := range []string{"0.0.0.0", "127.0.0.1", "localhost", "[::1]", "127.0.0.2", "192.0.2.5"} {
		t.Run(host, func(t *testing.T) {
			got := evalBindSelect(t, fmt.Sprintf(`
				const sel = new FakeSelect(["0.0.0.0", "127.0.0.1"]);
				renderBindOptions(sel, %q);
				sel.value = %q;
				report([sel.value, sel.options.map((o) => o.textContent)]);
			`, host, host))
			var out struct {
				Value  string
				Labels []string
			}
			decodeBindSelect(t, got, &out.Value, &out.Labels)
			if out.Value != host {
				t.Errorf("the form would post host %q for a bind of %q", out.Value, host)
			}
			// The added option is labeled with the address itself, so the
			// pane shows the bind in force rather than a blank control.
			if !contains(out.Labels, host) && host != "" {
				t.Errorf("the select does not show %q at all: %v", host, out.Labels)
			}
		})
	}
}

// renderSettings runs on every live update, so the option it adds has to be
// the same one option each time: a pane left open while the bind changed would
// otherwise collect one dead address per bind it has held, and an operator
// would be offered addresses this Mac no longer serves on.
func TestTheBindSelectDoesNotAccumulateOptions(t *testing.T) {
	got := evalBindSelect(t, `
		const sel = new FakeSelect(["0.0.0.0", "127.0.0.1"]);
		renderBindOptions(sel, "localhost");
		renderBindOptions(sel, "localhost");
		renderBindOptions(sel, "192.0.2.5");
		renderBindOptions(sel, "127.0.0.1");
		sel.value = "127.0.0.1";
		report([sel.value, sel.options.map((o) => o.value)]);
	`)
	var value string
	var values []string
	decodeBindSelect(t, got, &value, &values)
	if value != "127.0.0.1" {
		t.Errorf("the form would post host %q after the bind went back to one the select offers", value)
	}
	want := []string{"0.0.0.0", "127.0.0.1"}
	if len(values) != len(want) {
		t.Fatalf("the select offers %v, want exactly %v", values, want)
	}
	for i := range want {
		if values[i] != want[i] {
			t.Fatalf("the select offers %v, want exactly %v", values, want)
		}
	}
}

// The two functions above are only worth testing while renderSettings both
// calls them and does so before it assigns the host. Assigning first and
// adding the option after would leave .value empty exactly as it is today, and
// the tests above would still pass.
func TestRenderSettingsAddsTheOptionBeforeAssigningTheHost(t *testing.T) {
	body := extractFunction(t, readPanelSource(t), "renderSettings")
	const (
		render = `renderBindOptions($('setHost'), c.host);`
		assign = `$('setHost').value = c.host;`
	)
	at := strings.Index(body, render)
	if at < 0 {
		t.Fatalf("renderSettings no longer contains %s — a stored host the select does not offer is then posted back empty", render)
	}
	to := strings.Index(body, assign)
	if to < 0 {
		t.Fatalf("renderSettings no longer contains %s", assign)
	}
	if at > to {
		t.Error("renderSettings assigns the host before it offers it; an unoffered value assigns as the empty string")
	}
}

// evalBindSelect runs a snippet against renderBindOptions and its helper,
// lifted out of app.js, with just enough of a DOM for a <select> to behave as
// the browser's does: assigning a value the element does not offer leaves it
// empty, which is the whole of the bug. report() is how a snippet answers.
func evalBindSelect(t *testing.T, snippet string) string {
	t.Helper()
	const harness = `
		class FakeOption {
			constructor() { this.value = ''; this.textContent = ''; this.dataset = {}; this.parent = null; }
			remove() {
				const i = this.parent.options.indexOf(this);
				if (i >= 0) this.parent.options.splice(i, 1);
			}
		}
		class FakeSelect {
			constructor(values) {
				this._value = '';
				this.options = [];
				for (const v of values) {
					const o = new FakeOption();
					o.value = v;
					o.textContent = v;
					this.appendChild(o);
				}
			}
			appendChild(o) { o.parent = this; this.options.push(o); }
			set value(v) { this._value = this.options.some((o) => o.value === v) ? v : ''; }
			get value() { return this._value; }
		}
		const document = { createElement: () => new FakeOption() };
		const report = (v) => process.stdout.write(JSON.stringify(v));
	`
	src := readPanelSource(t)
	var b strings.Builder
	b.WriteString(harness)
	for _, name := range []string{"extraBindOption", "renderBindOptions"} {
		b.WriteString(extractFunction(t, src, name))
		b.WriteString("\n")
	}
	b.WriteString(snippet)
	return evalJS(t, b.String())
}

// decodeBindSelect unpacks the [value, list] pair a bind-select snippet reports.
func decodeBindSelect(t *testing.T, out string, value *string, list *[]string) {
	t.Helper()
	var pair []json.RawMessage
	if err := json.Unmarshal([]byte(out), &pair); err != nil || len(pair) != 2 {
		t.Fatalf("the panel returned %q, which is not a [value, list] pair: %v", out, err)
	}
	if err := json.Unmarshal(pair[0], value); err != nil {
		t.Fatalf("the panel returned %q as the select's value: %v", pair[0], err)
	}
	if err := json.Unmarshal(pair[1], list); err != nil {
		t.Fatalf("the panel returned %q as the select's options: %v", pair[1], err)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
