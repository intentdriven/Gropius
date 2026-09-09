package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/gateway"
)

// The posture page (itd-2609081718534201) is one view that states, in the
// present tense, who can reach this server, what a request has to carry, what
// is announced and what is recorded. Its one design rule is that it is fed by
// the state snapshot and nothing else: every line is derived from a field the
// control plane already publishes, in a pure function a test can lift out of
// app.js and run against an injected snapshot. These tests hold the page to
// that rule, to the two-key-lines criterion, and to the promise that the page
// reports and never gates.

// A snapshot the tests inject: the wildcard bind in force, a key set,
// statistics off, nothing on a private network. Every case below is an edit
// of it. The bind block is what the control plane says about the RUNNING
// bind, which is what the page reads for reach and for the advert.
const baseSnapshot = `{
  "config": {"host":"0.0.0.0","bind_mode":"","port":11535,"api_key":"********",
             "advertise":true,"statistics":false,"stats_months":6,"stats_max_bytes":52428800},
  "endpoints": [{"url":"http://alices-mac.local:11535/v1"},
                {"url":"http://192.0.2.10:11535/v1"},
                {"url":"http://127.0.0.1:11535/v1"}],
  "bind": {"mode":"","candidates":[],"mode_in_force":"","wildcard":true,"reaches_other_machines":true,
           "port":11535,"advertising":true},
  "hostname": "alices-mac"
}`

// The snapshot of a server that binds this Mac and nothing else.
const loopbackOnlyEdits = `{
  "config": {"host":"127.0.0.1"},
  "endpoints": [{"url":"http://127.0.0.1:11535/v1"}],
  "bind": {"mode":"","candidates":[],"mode_in_force":"","wildcard":false,"reaches_other_machines":false,
           "port":11535,"advertising":false}}`

var postureFunctions = []string{"endpointOf", "bytes", "advertising", "postureLines"}

// privateAddr is an address in the RFC 6598 shared range, which is the shape
// the classifier marks as a private network; the snapshots below write PRIV
// and priv fills it in, so the address is written down once.
const privateAddr = "100.101.102.103" // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range

func priv(s string) string { return strings.ReplaceAll(s, "PRIV", privateAddr) }

func posture(t *testing.T, snapshot string) map[string]map[string]any {
	t.Helper()
	items := evalPanelArray(t, "postureLines("+snapshot+")", postureFunctions...)
	out := map[string]map[string]any{}
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			t.Fatalf("postureLines returned %v, which is not an object", it)
		}
		id, _ := m["id"].(string)
		if id == "" {
			t.Fatalf("postureLines returned an item with no id: %v", m)
		}
		if _, dup := out[id]; dup {
			t.Fatalf("postureLines returned %q twice", id)
		}
		out[id] = m
	}
	return out
}

func text(t *testing.T, lines map[string]map[string]any, id string) string {
	t.Helper()
	l, ok := lines[id]
	if !ok {
		t.Fatalf("the page has no %q line; it has %v", id, keysOf(lines))
	}
	s, _ := l["text"].(string)
	return s
}

func keysOf(m map[string]map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// edited overlays one level of edits on the base snapshot: a top-level object
// is merged field by field, anything else replaces the base value.
func edited(t *testing.T, edits string) string {
	t.Helper()
	var base map[string]any
	if err := json.Unmarshal([]byte(baseSnapshot), &base); err != nil {
		t.Fatal(err)
	}
	var over map[string]any
	if err := json.Unmarshal([]byte(edits), &over); err != nil {
		t.Fatal(err)
	}
	for k, v := range over {
		if sub, ok := v.(map[string]any); ok {
			if cur, ok := base[k].(map[string]any); ok {
				for kk, vv := range sub {
					cur[kk] = vv
				}
				continue
			}
		}
		base[k] = v
	}
	b, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func wants(t *testing.T, lines map[string]map[string]any, id string, fragments ...string) {
	t.Helper()
	got := text(t, lines, id)
	for _, want := range fragments {
		if !strings.Contains(got, want) {
			t.Errorf("the %s line reads %q, which does not say %q", id, got, want)
		}
	}
}

func refuses(t *testing.T, lines map[string]map[string]any, id string, fragments ...string) {
	t.Helper()
	got := text(t, lines, id)
	for _, wrong := range fragments {
		if strings.Contains(got, wrong) {
			t.Errorf("the %s line reads %q, which must not say %q", id, got, wrong)
		}
	}
}

// Criterion 1: the page states who can reach the server and by which
// addresses, the two key answers, the announcement, the request log and what
// is recorded — each as a fact.
func TestThePostureLinesStateWhatIsOn(t *testing.T) {
	lines := posture(t, baseSnapshot)
	for _, want := range []string{
		"reach", "transport", "panel", "key-network", "key-local", "announce", "log", "stats",
	} {
		if _, ok := lines[want]; !ok {
			t.Errorf("the page has no %q line; it has %v", want, keysOf(lines))
		}
	}
	wants(t, lines, "reach",
		"This server answers on every address this Mac holds.",
		"The ones Gropius can name are http://alices-mac.local:11535/v1, http://192.0.2.10:11535/v1 and http://127.0.0.1:11535/v1;",
		"a name ending in .local is this Mac's name on the local network and not an address.",
		"Which machines can reach an address is decided by the network it is on, and Gropius does not see that.")
	wants(t, lines, "key-network", "A request arriving from another machine has to carry the API key. A key is set.")
	wants(t, lines, "key-local", "A request from this Mac to a loopback address is served without the key")
	wants(t, lines, "announce",
		"Gropius is announcing this server to every machine on the local network, as a Bonjour service named after this Mac's name, alices-mac,",
		"this Mac's addresses", "port 11535", "no model names and no key")
	wants(t, lines, "log", "Each request to the API's endpoints", "method, path, status and duration", "no client address",
		"at the sparse level", "logs folder")
	wants(t, posture(t, edited(t, `{"config":{"log_level":"detailed"}}`)), "log", "at the detailed level")
	wants(t, lines, "stats", "Request statistics are off: no request is recorded.")
	wants(t, lines, "transport", "plain HTTP")
	wants(t, lines, "panel", "answer on this Mac alone")
}

// Criterion 2: with a key set, the two key lines have different answers.
// withAuth admits a loopback connection with a loopback Host without the key,
// so a second account on this Mac reaches the API unauthenticated while every
// machine on the network is refused. One sentence saying "a key is required"
// is false of half of that.
func TestTheTwoKeyLinesHaveDifferentAnswers(t *testing.T) {
	withKey := posture(t, baseSnapshot)
	wants(t, withKey, "key-network", "has to carry the API key")
	wants(t, withKey, "key-local", "without the key", "another account")
	noKey := posture(t, edited(t, `{"config":{"api_key":""}}`))
	wants(t, noKey, "key-network", "No API key is set", "served without one")
	wants(t, noKey, "key-local", "without a key")
}

// A loopback-only bind: the server answers on this Mac, the announcement
// reaches nobody, and no request arrives from another machine to carry a key
// or not — the page says all three as facts rather than leaving the
// wildcard's sentences in place to contradict each other.
func TestALoopbackOnlyBindIsStatedAsSuch(t *testing.T) {
	lines := posture(t, edited(t, loopbackOnlyEdits))
	wants(t, lines, "reach", "answers on this Mac and on no other address: http://127.0.0.1:11535/v1.", "reaches nothing")
	wants(t, lines, "announce", "not announcing", "reaches no other machine")
	wants(t, lines, "key-network", "A key is set.", "reaches no other machine")
	refuses(t, lines, "key-network", "arriving from another machine")
	noKey := posture(t, edited(t, `{"config":{"host":"127.0.0.1","api_key":""},
		"endpoints":[{"url":"http://127.0.0.1:11535/v1"}],
		"bind":{"mode":"","candidates":[],"mode_in_force":"","wildcard":false,"reaches_other_machines":false,"port":11535,"advertising":false}}`))
	wants(t, noKey, "key-network", "No API key is set, and the bind reaches no other machine.")
}

// A bind to one address names it, from the bind state and not from the
// endpoint list, which drops a departed IPv4 address and never lists an IPv6
// one. The list is what clients can use, and it holds the bound address too.
func TestABindToOneAddressNamesTheAddress(t *testing.T) {
	lines := posture(t, edited(t, `{
		"endpoints": [{"url":"http://192.0.2.10:11535/v1"}, {"url":"http://127.0.0.1:11535/v1"}],
		"bind": {"mode":"","candidates":[],"mode_in_force":"","wildcard":false,"bound":"192.0.2.10","reaches_other_machines":true,"port":11535,"advertising":true}}`))
	wants(t, lines, "reach", "This server answers on 192.0.2.10 and on this Mac; the addresses clients can use are http://192.0.2.10:11535/v1 and http://127.0.0.1:11535/v1.")
	refuses(t, lines, "reach", "no other address")

	// A zoned IPv6 bind is acquired and reaches other machines, and neither
	// the endpoint list nor the URL spelling can carry it: the page says an
	// address it cannot name is answered on rather than printing a blank.
	zoned := posture(t, edited(t, `{
		"endpoints": [{"url":"http://127.0.0.1:11535/v1"}],
		"bind": {"mode":"","candidates":[],"mode_in_force":"","wildcard":false,"bound":"","reaches_other_machines":true,"port":11535,"advertising":true}}`))
	wants(t, zoned, "reach", "one more address, which Gropius cannot write as a URL", "the addresses it can name are http://127.0.0.1:11535/v1.")
	refuses(t, zoned, "reach", "answers on  and", "no other address")
}

// The page reads the RUNNING bind, never the endpoint list, for whether the
// server reaches another machine: on an IPv6-only Mac the list names loopback
// alone while the wildcard sockets answer on every address and the advert is
// up. Reporting that Mac as loopback-only would understate its exposure,
// which is the one direction adr-2609081118587999 rule 4 exists to prevent.
func TestReachIsReadFromTheBindAndNotFromTheEndpointList(t *testing.T) {
	lines := posture(t, edited(t, `{"endpoints": [{"url":"http://127.0.0.1:11535/v1"}]}`))
	wants(t, lines, "reach", "every address this Mac holds", "The ones Gropius can name are http://127.0.0.1:11535/v1;")
	refuses(t, lines, "reach", "reaches nothing")
	wants(t, lines, "announce", "is announcing")
}

// A mode saved and not yet in force has changed nothing: the sockets, the
// advert and the addresses are the running bind's until the next start, and
// the page says the choice is saved rather than reporting it as running.
func TestASavedModeIsNotReportedAsRunning(t *testing.T) {
	lines := posture(t, edited(t, priv(`{
		"config": {"bind_mode":"private-network"},
		"bind": {"mode":"private-network","candidates":["PRIV"],"mode_in_force":"","wildcard":true,"reaches_other_machines":true,"port":11535,"advertising":true}}`)))
	wants(t, lines, "announce", "is announcing")
	wants(t, lines, "reach", "every address this Mac holds")
	wants(t, lines, "private", "The private-network choice is saved and is not in force until Gropius next starts.")
	refuses(t, lines, "private", "selected")
}

// Criterion 4: on a private network the page states that sharing and public
// tunnelling change who reaches the address, that Gropius cannot observe
// either, and — under the mode — which address the mode selected (the
// amendment to adr-2609081118587999 requires the selection to be shown here).
func TestThePrivateNetworkLineStatesTheLimits(t *testing.T) {
	marked := posture(t, edited(t, priv(`{
		"endpoints": [{"url":"http://192.0.2.10:11535/v1"},
		              {"url":"http://PRIV:11535/v1","network":"private network"},
		              {"url":"http://127.0.0.1:11535/v1"}]}`)))
	wants(t, marked, "private",
		"http://"+privateAddr+":11535/v1 is on a private network",
		"shared with machines", "publish", "Gropius cannot see")

	mode := posture(t, edited(t, priv(`{
		"config": {"bind_mode":"private-network"},
		"endpoints": [{"url":"http://PRIV:11535/v1","network":"private network"},
		              {"url":"http://127.0.0.1:11535/v1"}],
		"bind": {"mode":"private-network","selected":"PRIV","candidates":["PRIV"],
		         "mode_in_force":"private-network","bound":"PRIV","wildcard":false,"reaches_other_machines":true,"port":11535,"advertising":false}}`)))
	wants(t, mode, "private", "The private-network choice selected "+privateAddr+".")
	wants(t, mode, "announce", "not announcing", "private-network choice excludes")
	wants(t, mode, "reach", "This server answers on "+privateAddr+" and on this Mac;")

	if _, present := posture(t, baseSnapshot)["private"]; present {
		t.Error("with nothing on a private network, the page still carries a private-network line")
	}
}

// A bind that narrowed to this Mac says so in the resolver's own words, which
// is the fact behind a Connect tab that suddenly lists loopback alone.
func TestANarrowedBindIsStatedInTheResolversWords(t *testing.T) {
	lines := posture(t, edited(t, `{
		"endpoints": [{"url":"http://127.0.0.1:11535/v1"}],
		"bind": {"mode":"","candidates":[],"mode_in_force":"","wildcard":false,"reaches_other_machines":false,"refusal":"could not listen on 192.0.2.10","port":11535,"advertising":false}}`))
	wants(t, lines, "reach", "no other address", "The bind narrowed to this Mac: could not listen on 192.0.2.10.")
}

// What is recorded, where, and for how long — and "off" when it is off, which
// is a fact and costs one line. The store's figures ride the snapshot under the
// same condition, so absence is the observation; a store that could not be
// opened says so before any figure, because a refused store reported as an
// empty one would have the operator believing records were accumulating.
func TestTheStatisticsLineSaysWhatIsRecordedAndForHowLong(t *testing.T) {
	on := posture(t, edited(t, `{
		"config": {"statistics":true,"stats_months":3,"stats_max_bytes":10485760},
		"stats_store": {"files":2,"bytes":1572864,"oldest":1788696000}}`))
	wants(t, on, "stats",
		"being recorded on this Mac",
		"when a model was loaded or evicted, and the settings in force",
		"no prompt, no answer, no key and no client address",
		"3 months", "10.0 MB",
		"records from 2026-09-06 onwards, 1.5 MB in 2 files",
		"every account on this Mac")
	empty := posture(t, edited(t, `{
		"config": {"statistics":true},
		"stats_store": {"files":0,"bytes":0,"oldest":0}}`))
	wants(t, empty, "stats", "none kept yet")
	refused := posture(t, edited(t, `{
		"config": {"statistics":true},
		"stats_store": {"refused":true,"files":0,"bytes":0,"oldest":0}}`))
	wants(t, refused, "stats", "could not open the store", "nothing is on disk")
	refuses(t, refused, "stats", "none kept yet")
	stalled := posture(t, edited(t, `{
		"config": {"statistics":true},
		"stats_store": {"files":1,"bytes":2048,"oldest":1788696000,"stalled":true}}`))
	wants(t, stalled, "stats", "the disk did not answer in time")
	stalledEmpty := posture(t, edited(t, `{
		"config": {"statistics":true},
		"stats_store": {"files":0,"bytes":0,"oldest":0,"stalled":true}}`))
	wants(t, stalledEmpty, "stats", "none kept yet", "the disk did not answer in time")
}

// The announcement line reads the decision the process made at start, which
// the bind state carries as `advertising`, and names which of the rule's
// three reasons (the setting, the mode in force, the reach) is keeping it
// off. The stored setting is not consulted: a save changes it at once and the
// advert, started once at launch, runs on regardless until the next start —
// so reading the setting would report "not announcing" while the network
// went on hearing the advert. A failure to start is the one thing the
// snapshot cannot carry, and the line says so. The service is named after
// this Mac, and a Mac with no name to read is announced as gropius, which is
// what internal/discovery publishes.
func TestTheAnnouncementLineNamesWhyItIsOff(t *testing.T) {
	cases := []struct{ name, edits, want string }{
		{"switched off at start", `{"bind":{"advertising":false}}`, "switched off when Gropius started"},
		{"the private-network choice", `{"bind":{"advertising":false,"mode_in_force":"private-network"}}`, "private-network choice excludes"},
		{"a bind that reaches nobody", `{"bind":{"advertising":false,"reaches_other_machines":false}}`, "reaches no other machine"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wants(t, posture(t, edited(t, c.edits)), "announce", "not announcing", c.want)
		})
	}
	// The stored mode and the stored setting are what a save changes; neither
	// stops the advert that is running.
	wants(t, posture(t, edited(t, `{"config":{"bind_mode":"private-network","advertise":false}}`)), "announce", "is announcing")
	// The port announced is the one the listeners took, not a port saved since.
	wants(t, posture(t, edited(t, `{"config":{"port":12000}}`)), "announce", "port 11535")
	wants(t, posture(t, baseSnapshot), "announce", "failed to start", "log", "takes effect at the next start")
	wants(t, posture(t, edited(t, `{"hostname":""}`)), "announce", "name, gropius,")
}

// Criterion 3, mechanised as far as it can be: every line names the snapshot
// fields it read, each of those fields exists on the Go type the control
// plane publishes, each is actually read by the code that builds the line,
// and the lines that read nothing are exactly the two constants — the
// transport and the panel's own bind — which are facts about the binary that
// no snapshot field could carry. A line that read a field the
// snapshot does not have would render a blank or "undefined" as if it were an
// observation.
func TestEveryPostureLineTracesToTheSnapshot(t *testing.T) {
	constants := map[string]bool{"transport": true, "panel": true}
	// The most fully populated snapshot: every line present.
	lines := posture(t, edited(t, priv(`{
		"config": {"statistics":true,"bind_mode":"private-network"},
		"endpoints": [{"url":"http://PRIV:11535/v1","network":"private network"}],
		"bind": {"mode":"private-network","selected":"PRIV","candidates":["PRIV"],"refusal":"",
		         "mode_in_force":"private-network","bound":"PRIV","wildcard":false,"reaches_other_machines":true,"port":11535,"advertising":false},
		"stats_store": {"files":1,"bytes":10,"oldest":1}}`)))
	state := reflect.TypeOf(gateway.State{})
	src := readPanelSource(t)
	code := extractFunction(t, src, "postureLines") + extractFunction(t, src, "advertising")
	for id, l := range lines {
		reads, _ := l["reads"].([]any)
		if constants[id] {
			if len(reads) != 0 {
				t.Errorf("the %s line is a constant and claims to read %v", id, reads)
			}
			continue
		}
		if len(reads) == 0 {
			t.Errorf("the %s line names no snapshot field it reads", id)
		}
		for _, r := range reads {
			path, _ := r.(string)
			if !jsonPathExists(state, path) {
				t.Errorf("the %s line reads %q, which the snapshot does not carry", id, path)
			}
			leaf := path[strings.LastIndex(path, ".")+1:]
			if !strings.Contains(code, "."+leaf) {
				t.Errorf("the %s line declares that it reads %q, and nothing in postureLines or advertising reads .%s", id, path, leaf)
			}
		}
	}
}

// jsonPathExists walks a dotted JSON path through the struct's json tags,
// descending into pointers and slices, so "endpoints.url" and
// "stats_store.oldest" both resolve. A field tagged "-" is not on the wire
// and does not count.
func jsonPathExists(typ reflect.Type, path string) bool {
	for _, seg := range strings.Split(path, ".") {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct {
			return false
		}
		found := false
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == seg && name != "-" {
				typ = f.Type
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// The config fields the page reads are read under their stored names, which is
// what binds the JavaScript to config.Config rather than to a copy of it.
func TestThePostureReadsConfigFieldsUnderTheirStoredNames(t *testing.T) {
	cfg := reflect.TypeOf(config.Config{})
	src := readPanelSource(t)
	code := extractFunction(t, src, "postureLines") + extractFunction(t, src, "advertising")
	for _, field := range []string{"api_key", "statistics", "stats_months", "stats_max_bytes", "log_level"} {
		if !jsonPathExists(cfg, field) {
			t.Errorf("config.Config has no json field %q", field)
		}
		if !strings.Contains(code, "."+field) {
			t.Errorf("the page does not read config.%s, which its lines are meant to be derived from", field)
		}
	}
}

// The page must never read a stored, startup-only setting for anything about
// the running server. config.bind_mode, config.port and config.advertise are
// what a save writes and a restart applies; what is running is on state.bind.
// A page that read the stored ones would report the advert off, the port
// moved, and the wildcard's addresses gone, the moment the operator saved a
// change in the pane next door.
func TestThePostureNeverReadsAStartupOnlySettingForTheRunningServer(t *testing.T) {
	src := readPanelSource(t)
	for _, fn := range []string{"postureLines", "advertising"} {
		body := extractFunction(t, src, fn)
		for _, stored := range []string{"bind_mode", "c.port", "c.advertise", "config.port", "config.advertise"} {
			if strings.Contains(body, stored) {
				t.Errorf("%s reads %s — the running bind is on state.bind, and the stored setting is only what the next start will use", fn, stored)
			}
		}
	}
}

// Criterion 5: no interruption. The view is reached by navigation and by
// nothing else — no code path switches to it — and its renderer writes into
// its own container and into no other view. It also leaves the container
// alone when nothing changed: the snapshot arrives every couple of seconds,
// and prose being read must not be rebuilt under the reader.
func TestThePostureViewIsReachedByNavigationAlone(t *testing.T) {
	markup := readPanelMarkup(t)
	if !strings.Contains(markup, `data-tab="posture"`) {
		t.Error("the panel has no Posture tab")
	}
	if !strings.Contains(markup, `<section id="tab-posture" class="panel">`) {
		t.Error("the panel has no tab-posture section, or it is not an ordinary hidden panel")
	}
	src := readPanelSource(t)
	if strings.Contains(src, "showTab('posture')") || strings.Contains(src, `showTab("posture")`) {
		t.Error("app.js navigates to the posture view itself — the page is somewhere the operator goes, never somewhere they are taken")
	}
	body := extractFunction(t, src, "renderPosture")
	for _, target := range dollarTargets(body) {
		if target != "posture" {
			t.Errorf("renderPosture writes into #%s — the view renders nothing into any other view", target)
		}
	}
	if !strings.Contains(body, "if (html === postureShown) return;") {
		t.Error("renderPosture rebuilds the page on every snapshot, tearing down prose the operator may be reading or selecting")
	}
	if !strings.Contains(extractFunction(t, src, "render"), "renderPosture();") {
		t.Error("render() no longer calls renderPosture, so the view is never drawn")
	}
}

// dollarTargets is every id passed to the panel's $() helper in a piece of
// its source.
func dollarTargets(src string) []string {
	var out []string
	for i := 0; ; {
		at := strings.Index(src[i:], "$('")
		if at < 0 {
			return out
		}
		start := i + at + 3
		end := strings.Index(src[start:], "'")
		if end < 0 {
			return out
		}
		out = append(out, src[start:start+end])
		i = start + end
	}
}

// Criteria 6 and 7: the page reports and gates nothing, and it is an input
// to nothing. On the panel's side that means its functions never call the
// control plane: no api(), no fetch, no post. The Go side of the same rule is
// internal/archtest's enforcement-detection scan, which does not know this
// view exists because the view sends the server nothing it could read.
func TestThePostureViewReadsAndNeverWrites(t *testing.T) {
	src := readPanelSource(t)
	for _, fn := range []string{"postureLines", "advertising", "renderPosture"} {
		body := extractFunction(t, src, fn)
		for _, call := range []string{"api(", "fetch(", "postModel(", "EventSource", "XMLHttpRequest", "location."} {
			if strings.Contains(body, call) {
				t.Errorf("%s contains %s — the posture view reads the snapshot and sends nothing", fn, call)
			}
		}
	}
}

// Criterion 8: the page's own strings state which network an address is on and
// nothing about what that network is worth, and name no vendor. This is the
// same closed list the Connect page is held to; internal/archtest's claim scan
// covers the same functions from the other side, on the shapes a sentence
// takes when it promises exposure.
func TestThePostureStringsNameNoVendorAndPromiseNothing(t *testing.T) {
	forbidden := []string{
		"tailscale", "tailnet", "headscale", "zerotier", "wireguard", "nebula",
		"encrypt", "secure", "safe", "only", "vpn", "private and",
	}
	src := readPanelSource(t)
	for _, fn := range []string{"postureLines", "advertising", "renderPosture"} {
		for _, lit := range jsLiterals(extractFunction(t, src, fn)) {
			lower := strings.ToLower(lit)
			for _, word := range forbidden {
				if strings.Contains(lower, word) {
					t.Errorf("%s renders the string %q, which contains %q — the page states what it observed and nothing more", fn, lit, word)
				}
			}
		}
	}
	markup := readPanelMarkup(t)
	start := strings.Index(markup, `<section id="tab-posture"`)
	if start < 0 {
		t.Fatal("no posture section in the markup")
	}
	end := strings.Index(markup[start:], "</section>")
	if end < 0 {
		t.Fatal("the posture section is not closed")
	}
	lower := strings.ToLower(markup[start : start+end])
	for _, word := range forbidden {
		if strings.Contains(lower, word) {
			t.Errorf("the posture section's markup contains %q", word)
		}
	}
}

// Every line is escaped on its way into the markup: the strings come from the
// server rather than from a person, but the row is built with innerHTML and a
// hostname or a refusal is text the panel did not write.
func TestThePostureLinesAreEscaped(t *testing.T) {
	body := extractFunction(t, readPanelSource(t), "renderPosture")
	for _, want := range []string{"escapeHtml(l.heading)", "escapeHtml(l.text)"} {
		if !strings.Contains(body, want) {
			t.Errorf("renderPosture does not contain %s", want)
		}
	}
}

// The reference page documents every line the panel renders, under the
// heading the panel uses, and no line the panel does not render. The page is
// user-facing and nothing else holds it to the code.
func TestTheReferencePageDocumentsEveryLine(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "posture-reference.md"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(doc)
	lines := posture(t, edited(t, priv(`{
		"config": {"statistics":true},
		"endpoints": [{"url":"http://PRIV:11535/v1","network":"private network"}],
		"stats_store": {"files":0,"bytes":0,"oldest":0}}`)))
	documented := 0
	for id, l := range lines {
		heading, _ := l["heading"].(string)
		if !strings.Contains(page, "| **"+heading+"** |") {
			t.Errorf("docs/posture-reference.md has no row for the %s line, whose heading is %q", id, heading)
		}
		documented++
	}
	if rows := strings.Count(page, "\n| **"); rows != documented {
		t.Errorf("docs/posture-reference.md has %d rows and the panel renders %d lines", rows, documented)
	}
}
