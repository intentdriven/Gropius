package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/mlxtest"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
	"github.com/intentdriven/Gropius/internal/stats"
)

const testModelID = "mlx-community/Qwen3-8B-4bit"

// statsGateway wires a gateway to a fake model server and a recorder, with
// statistics on or off as the test needs. The fake holds its first chunk back,
// because a time to first token measured against an answer that arrives inside
// a millisecond asserts nothing.
func statsGateway(t *testing.T, on bool, opts mlxtest.Options) (*httptest.Server, *stats.Recorder, *mlxtest.Server, *stubPool) {
	t.Helper()

	const modelPath = "/models/" + testModelID
	opts.ModelArg = modelPath
	if opts.Reply == "" {
		opts.Reply = "GROPIUS OK"
	}
	fake := mlxtest.Start(opts)
	t.Cleanup(fake.Close)

	models := &stubModels{models: []registry.Model{{
		RepoID: testModelID,
		Path:   modelPath,
		State:  registry.StateReady,
	}}}
	pool := &stubPool{srv: fake}

	rec := stats.New(stats.Options{})
	rec.SetEnabled(on)
	cfg := config.Default()
	cfg.Statistics = on

	g := New(Options{Config: cfg, Pool: pool, Models: models, Stats: rec})
	srv := httptest.NewServer(g.Handler())
	t.Cleanup(srv.Close)
	return srv, rec, fake, pool
}

// completion posts a chat completion and returns the raw response body.
func completion(t *testing.T, srv *httptest.Server, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(out)
}

func onlyRecord(t *testing.T, rec *stats.Recorder) stats.Record {
	t.Helper()
	got := rec.View().Requests
	if len(got) != 1 {
		t.Fatalf("the recorder holds %d requests, want exactly 1: %+v", len(got), got)
	}
	return got[0]
}

// The criterion that makes the switch worth having: a streamed answer is
// recorded with its model, how it ended, what it cost in tokens, how long the
// client waited for the first chunk and how long the whole thing took.
func TestAStreamedRequestIsRecordedWithItsCountsAndTimings(t *testing.T) {
	srv, rec, _, _ := statsGateway(t, true, mlxtest.Options{FirstTokenDelay: 40 * time.Millisecond})

	status, _ := completion(t, srv, `{"model":"`+testModelID+`","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	got := onlyRecord(t, rec)
	if got.Model != testModelID {
		t.Errorf("the record names model %q, want %q", got.Model, testModelID)
	}
	if got.Class != stats.ClassOK {
		t.Errorf("the record's class is %q, want %q", got.Class, stats.ClassOK)
	}
	if !got.Streamed {
		t.Error("a streamed request was not recorded as streamed")
	}
	if got.PromptTokens != 3 || got.CompletionTokens != 4 {
		t.Errorf("the record counts %d prompt and %d completion tokens, want 3 and 4 from the model server's usage",
			got.PromptTokens, got.CompletionTokens)
	}
	if got.FirstTokenMS <= 0 {
		t.Errorf("time to first token is %d ms; the model server held the first chunk back for 40 ms", got.FirstTokenMS)
	}
	if got.FirstTokenMS > got.DurationMS {
		t.Errorf("time to first token (%d ms) is longer than the whole request (%d ms)", got.FirstTokenMS, got.DurationMS)
	}
	if got.At == 0 {
		t.Error("the record carries no timestamp")
	}
}

// Gropius asks the model server for the token counts on the client's behalf.
// The client did not ask, so the client must not see the answer to that
// question: what reaches it is the stream it would have received anyway, byte
// for byte.
func TestAClientThatDidNotAskForUsageReceivesTheSameBytes(t *testing.T) {
	body := `{"model":"` + testModelID + `","stream":true,"messages":[{"role":"user","content":"hi"}]}`

	off, _, offFake, _ := statsGateway(t, false, mlxtest.Options{})
	_, without := completion(t, off, body)

	on, rec, onFake, _ := statsGateway(t, true, mlxtest.Options{})
	_, with := completion(t, on, body)

	if with != without {
		t.Errorf("recording changed what the client receives:\nwith statistics on:\n%q\nwith them off:\n%q", with, without)
	}
	if strings.Contains(with, `"choices":[]`) {
		t.Errorf("the client received a usage-only chunk it did not ask for:\n%s", with)
	}

	// And the difference is where it belongs: on the request the gateway makes,
	// not on the one it received or the answer it relays.
	if _, asked := onFake.LastBody()["stream_options"]; !asked {
		t.Error("with statistics on, the model server was not asked for the token counts")
	}
	if _, asked := offFake.LastBody()["stream_options"]; asked {
		t.Error("with statistics off, the gateway still merged stream_options into the client's request")
	}
	if got := onlyRecord(t, rec).CompletionTokens; got != 4 {
		t.Errorf("the record counts %d completion tokens, want the 4 the usage chunk carried", got)
	}
}

// A client that asked for the counts itself keeps them: the chunk is its own.
func TestAClientThatAskedForUsageStillReceivesIt(t *testing.T) {
	srv, rec, _, _ := statsGateway(t, true, mlxtest.Options{})

	_, got := completion(t, srv, `{"model":"`+testModelID+`","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	if !strings.Contains(got, `"choices":[]`) {
		t.Errorf("the client asked for the token counts and did not get them:\n%s", got)
	}
	if n := strings.Count(got, `"choices":[]`); n != 1 {
		t.Errorf("the client received %d usage chunks, want 1", n)
	}
	if got := onlyRecord(t, rec).CompletionTokens; got != 4 {
		t.Errorf("the record counts %d completion tokens, want 4", got)
	}
}

// The pinned model server reads stream_options["include_usage"] directly, so a
// client-sent stream_options object without that key raises upstream. Merging
// therefore always writes the key rather than assuming it is there — and
// touches nothing else the client sent.
func TestMergingSetsTheKeyAndLeavesEveryOtherFieldAlone(t *testing.T) {
	srv, _, fake, _ := statsGateway(t, true, mlxtest.Options{})

	sent := `{"model":"` + testModelID + `","stream":true,"stream_options":{},` +
		`"temperature":0.25,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	status, _ := completion(t, srv, sent)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the model server raised on a stream_options without the key", status)
	}

	got := fake.LastBody()
	opts, ok := got["stream_options"].(map[string]any)
	if !ok || opts["include_usage"] != true {
		t.Fatalf("the model server was sent stream_options %#v, want an object carrying include_usage true", got["stream_options"])
	}
	var wanted map[string]any
	if err := json.Unmarshal([]byte(sent), &wanted); err != nil {
		t.Fatal(err)
	}
	for field, want := range wanted {
		switch field {
		case "model", "stream_options":
			continue // the two the gateway is allowed to touch
		}
		if fmt.Sprint(got[field]) != fmt.Sprint(want) {
			t.Errorf("the model server was sent %s = %v, but the client sent %v", field, got[field], want)
		}
	}
}

// A non-streaming answer already carries its counts, so nothing is merged into
// the request — and there is no first token to time.
func TestANonStreamingRequestIsRecordedWithoutATimeToFirstToken(t *testing.T) {
	srv, rec, fake, _ := statsGateway(t, true, mlxtest.Options{})

	completion(t, srv, `{"model":"`+testModelID+`","messages":[{"role":"user","content":"hi"}]}`)

	if _, merged := fake.LastBody()["stream_options"]; merged {
		t.Error("the gateway merged stream_options into a request that was not streaming")
	}
	got := onlyRecord(t, rec)
	if got.Streamed {
		t.Error("a request that did not stream was recorded as streamed")
	}
	if got.FirstTokenMS != stats.NoFirstToken {
		t.Errorf("time to first token is %d, want none for a request that did not stream", got.FirstTokenMS)
	}
	if got.PromptTokens != 3 || got.CompletionTokens != 4 {
		t.Errorf("the record counts %d and %d tokens, want 3 and 4", got.PromptTokens, got.CompletionTokens)
	}
}

// Every way a request can end other than an answer, named by what was
// observable: the status the client received, whether the client went away,
// and whether the upstream body finished. None of them carries token counts.
func TestEveryOtherOutcomeIsRecordedWithItsClass(t *testing.T) {
	cases := []struct {
		name       string
		acquireErr error
		body       string
		want       stats.Class
		wantModel  string
	}{
		{
			name:      "a body that is not JSON",
			body:      `not json at all`,
			want:      stats.ClassClientError,
			wantModel: "",
		},
		{
			name:      "a model this Mac does not have",
			body:      `{"model":"org/nope","messages":[]}`,
			want:      stats.ClassClientError,
			wantModel: "",
		},
		{
			name:       "the model is already as busy as it will get",
			acquireErr: fmt.Errorf("overloaded: %w", runtime.ErrBusy),
			want:       stats.ClassBusy,
			wantModel:  testModelID,
		},
		{
			name:       "the model does not fit the memory budget",
			acquireErr: errors.New("not enough memory to load another model"),
			want:       stats.ClassRefused,
			wantModel:  testModelID,
		},
		{
			name:       "the model server could not be started",
			acquireErr: &runtime.LaunchError{Err: errors.New("no interpreter")},
			want:       stats.ClassLaunchFailed,
			wantModel:  testModelID,
		},
		{
			name:       "the model server started and never answered",
			acquireErr: &runtime.NotReadyError{Err: errors.New("did not become ready within 10m")},
			want:       stats.ClassNotReady,
			wantModel:  testModelID,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, rec, _, pool := statsGateway(t, true, mlxtest.Options{})
			pool.acquireErr = c.acquireErr
			body := c.body
			if body == "" {
				body = `{"model":"` + testModelID + `","messages":[{"role":"user","content":"hi"}]}`
			}

			completion(t, srv, body)

			got := onlyRecord(t, rec)
			if got.Class != c.want {
				t.Errorf("the request was recorded as %q, want %q", got.Class, c.want)
			}
			if got.Model != c.wantModel {
				t.Errorf("the record names model %q, want %q", got.Model, c.wantModel)
			}
			if got.PromptTokens != 0 || got.CompletionTokens != 0 {
				t.Errorf("a request that did not finish carries %d and %d tokens, want none",
					got.PromptTokens, got.CompletionTokens)
			}
			if rec.Summary()[0].ByClass[c.want] != 1 {
				t.Errorf("the per-model counters do not count one %q request: %+v", c.want, rec.Summary())
			}
		})
	}
}

// A non-2xx the model server itself produced is relayed as it stands, and
// recorded as what it was: the model server's answer, not the gateway's.
func TestAnUpstreamStatusIsRecordedAsOne(t *testing.T) {
	srv, rec, _, _ := statsGateway(t, true, mlxtest.Options{LoadDelay: time.Hour})

	status, _ := completion(t, srv, `{"model":"`+testModelID+`","messages":[{"role":"user","content":"hi"}]}`)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want the model server's own 503", status)
	}
	if got := onlyRecord(t, rec).Class; got != stats.ClassUpstreamStatus {
		t.Errorf("the request was recorded as %q, want %q", got, stats.ClassUpstreamStatus)
	}
}

// A model server that is running and does not answer at all is a different
// failure from one that answers with an error.
func TestAnUnreachableModelServerIsRecordedAsOne(t *testing.T) {
	srv, rec, fake, _ := statsGateway(t, true, mlxtest.Options{})
	fake.Close() // the process is gone; nothing is listening on its port

	status, _ := completion(t, srv, `{"model":"`+testModelID+`","messages":[{"role":"user","content":"hi"}]}`)
	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", status)
	}
	if got := onlyRecord(t, rec).Class; got != stats.ClassUnreachable {
		t.Errorf("the request was recorded as %q, want %q", got, stats.ClassUnreachable)
	}
}

// A client that hangs up mid-stream is recorded as having done so, rather than
// as a request that ended well: it is the one outcome the status line cannot
// describe, because the status was sent before the client left.
func TestAClientThatGoesAwayMidStreamIsRecordedAsCancelled(t *testing.T) {
	srv, rec, _, _ := statsGateway(t, true, mlxtest.Options{
		Reply:           "one two three four five six seven eight",
		FirstTokenDelay: 20 * time.Millisecond,
		ChunkDelay:      40 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"`+testModelID+`","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	_, _ = resp.Body.Read(buf)
	cancel()
	resp.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := rec.View().Requests
		if len(got) == 1 {
			if got[0].Class != stats.ClassCancelled {
				t.Fatalf("a client that hung up mid-stream was recorded as %q, want %q", got[0].Class, stats.ClassCancelled)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the abandoned request was never recorded")
}

// The redaction criterion, as a byte scan rather than as a promise. A request
// carrying a sentinel in its prompt, a bearer token in its headers, and a LAN
// address on the connection: none of the three may appear in anything the
// panel shows or the process writes.
func TestNothingFromTheRequestReachesTheRecordOrTheLog(t *testing.T) {
	const (
		sentinel = "SENTINEL-PROMPT-fb0c1d"
		token    = "bh_secrettokenvalue0123"
		lanAddr  = "192.0.2.44:51820"
	)

	const modelPath = "/models/" + testModelID
	fake := mlxtest.Start(mlxtest.Options{ModelArg: modelPath, Reply: "GROPIUS OK"})
	t.Cleanup(fake.Close)
	models := &stubModels{models: []registry.Model{{RepoID: testModelID, Path: modelPath, State: registry.StateReady}}}
	pool := &stubPool{srv: fake}

	rec := stats.New(stats.Options{})
	rec.SetEnabled(true)
	cfg := config.Default()
	cfg.Statistics = true
	cfg.APIKey = token

	var logged bytes.Buffer
	g := New(Options{
		Config: cfg, Pool: pool, Models: models, Stats: rec,
		Log: slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})

	// A crafted RemoteAddr rather than a real LAN listener: the handler is
	// driven directly, which is the only way a test on this machine can present
	// itself as something other than loopback.
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+testModelID+`","stream":true,"messages":[{"role":"user","content":"`+sentinel+`"}]}`))
	req.RemoteAddr = lanAddr
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	view, err := json.Marshal(rec.View())
	if err != nil {
		t.Fatal(err)
	}
	summary, err := json.Marshal(rec.Summary())
	if err != nil {
		t.Fatal(err)
	}
	for _, surface := range []struct {
		name    string
		content string
	}{
		{"the statistics view", string(view)},
		{"the per-model counters", string(summary)},
		{"the process log", logged.String()},
	} {
		for _, secret := range []string{sentinel, token, "192.0.2.44"} {
			if strings.Contains(surface.content, secret) {
				t.Errorf("%s carries %q:\n%s", surface.name, secret, surface.content)
			}
		}
	}
	if len(rec.View().Requests) != 1 {
		t.Fatal("the request was not recorded at all, so the scan above proves nothing")
	}
}

// Off is not "recorded and hidden": nothing is recorded, nothing is asked of
// the model server on the client's behalf, and the answer is the one the
// client would have received from the build before any of this existed.
func TestOffLeavesTheGatewayAsItWas(t *testing.T) {
	srv, rec, fake, _ := statsGateway(t, false, mlxtest.Options{})

	status, body := completion(t, srv, `{"model":"`+testModelID+`","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if view := rec.View(); view.Enabled || len(view.Requests) != 0 || len(view.Models) != 0 {
		t.Errorf("the recorder holds %+v with the switch off, want nothing", view)
	}
	if _, merged := fake.LastBody()["stream_options"]; merged {
		t.Error("with the switch off the gateway still asked the model server for token counts")
	}
	if strings.Contains(body, `"choices":[]`) {
		t.Error("with the switch off the client received a usage chunk")
	}
}

// The switch applies to the next request, not to the next restart: the gateway
// reads it through the same live configuration it reads the API key through.
func TestTheSwitchAppliesToTheNextRequest(t *testing.T) {
	const modelPath = "/models/" + testModelID
	fake := mlxtest.Start(mlxtest.Options{ModelArg: modelPath, Reply: "GROPIUS OK"})
	t.Cleanup(fake.Close)
	models := &stubModels{models: []registry.Model{{RepoID: testModelID, Path: modelPath, State: registry.StateReady}}}
	pool := &stubPool{srv: fake}

	rec := stats.New(stats.Options{})
	cfg := config.Default()
	live := func() config.Config { return cfg }

	g := New(Options{ConfigFunc: live, Pool: pool, Models: models, Stats: rec})
	srv := httptest.NewServer(g.Handler())
	t.Cleanup(srv.Close)

	completion(t, srv, `{"model":"`+testModelID+`","messages":[{"role":"user","content":"hi"}]}`)
	if len(rec.View().Requests) != 0 {
		t.Fatal("a request was recorded before the switch was turned on")
	}

	cfg.Statistics = true
	rec.SetEnabled(true)
	completion(t, srv, `{"model":"`+testModelID+`","messages":[{"role":"user","content":"hi"}]}`)
	if len(rec.View().Requests) != 1 {
		t.Error("the switch did not reach the next request")
	}
}

// A model server that dies mid-generation has already been answered 200, so
// nothing about the status line says the client was short-changed. The relay
// is the only place that can tell, and a request whose answer stopped part-way
// must not be counted as one that went well.
func TestAnAnswerCutShortIsNotRecordedAsOne(t *testing.T) {
	src := io.MultiReader(
		strings.NewReader("data: {\"model\":\"backend\",\"choices\":[{\"delta\":{}}]}\n\n"),
		errReader{errors.New("connection reset")},
	)
	w := httptest.NewRecorder()

	out := streamRewriteSSE(w, src, "backend", "friendly", relayOptions{observing: true})
	if !out.truncated {
		t.Fatal("a stream that ended in an error was reported as having finished")
	}
	if out.firstToken.IsZero() {
		t.Error("the chunk that did arrive was not timed")
	}

	obs := &observation{rec: stats.New(stats.Options{}), started: time.Now(),
		record: stats.Record{Class: stats.ClassOK, FirstTokenMS: stats.NoFirstToken}}
	obs.relayed(out)
	if obs.record.Class != stats.ClassUnreachable {
		t.Errorf("an answer cut short is recorded as %q, want %q", obs.record.Class, stats.ClassUnreachable)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// Which events count as the usage event Gropius asked for, and which do not.
// Removing an event the gateway does not understand is the one mistake here a
// client would see, so anything that is not clearly the counts-only event is
// relayed as it stands.
func TestOnlyTheCountsOnlyEventIsRemoved(t *testing.T) {
	cases := []struct {
		name  string
		event string
		want  bool
	}{
		{"the pinned server's own shape", `{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2}}`, true},
		{"counts with no choices field at all", `{"usage":{"prompt_tokens":1}}`, true},
		{"a chunk of the answer", `{"choices":[{"index":0}]}`, false},
		{"a chunk that also carries counts", `{"choices":[{"index":0}],"usage":{"prompt_tokens":1}}`, false},
		{"a null usage", `{"choices":[],"usage":null}`, false},
		{"choices that are not a list", `{"choices":"none","usage":{"prompt_tokens":1}}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev, ok := decodeEvent([]byte(c.event))
			if !ok {
				t.Fatalf("%s is not an object", c.event)
			}
			if got := isUsageOnly(ev); got != c.want {
				t.Errorf("isUsageOnly(%s) = %v, want %v", c.event, got, c.want)
			}
		})
	}
}

// And which events are the client's first sight of an answer, which is what a
// time to first token measures.
func TestTheFirstChunkOfAnAnswerIsWhatIsTimed(t *testing.T) {
	cases := []struct {
		event string
		want  bool
	}{
		{`{"choices":[{"index":0}]}`, true},
		{`{"choices":[]}`, false},
		{`{"usage":{"prompt_tokens":1}}`, false},
		{`{"choices":"none"}`, false},
	}
	for _, c := range cases {
		ev, ok := decodeEvent([]byte(c.event))
		if !ok {
			t.Fatalf("%s is not an object", c.event)
		}
		if got := carriesGeneration(ev); got != c.want {
			t.Errorf("carriesGeneration(%s) = %v, want %v", c.event, got, c.want)
		}
	}
}

// Removing an event takes the blank line that terminates it and nothing else.
// A line between the removed event and the next blank one is a line that owns
// that blank, and swallowing it would leave the client's stream one
// terminator short.
func TestRemovingTheCountsEventTakesOnlyItsOwnTerminator(t *testing.T) {
	// The comment line between the removed event and the next blank one is
	// what makes this test bite: the blank belongs to the comment, not to the
	// event that was removed two lines earlier.
	body := "data: {\"model\":\"backend\",\"choices\":[{\"index\":0}]}\n\n" +
		"data: {\"model\":\"backend\",\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2}}\n" +
		": keep-alive\n\n" +
		"data: [DONE]\n\n"
	want := "data: {\"choices\":[{\"index\":0}],\"model\":\"friendly\"}\n\n" +
		": keep-alive\n\n" +
		"data: [DONE]\n\n"

	w := httptest.NewRecorder()
	out := streamRewriteSSE(w, strings.NewReader(body), "backend", "friendly",
		relayOptions{observing: true, keepUsage: false})

	if got := w.Body.String(); got != want {
		t.Errorf("the relayed stream is\n%q\nwant\n%q", got, want)
	}
	if out.usage == nil || out.usage.Completion != 2 {
		t.Errorf("the counts were removed without being read: %+v", out.usage)
	}
	if out.truncated {
		t.Error("a stream that ran to its end was reported as cut short")
	}
}
