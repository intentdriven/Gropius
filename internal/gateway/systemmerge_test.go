package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/mlxtest"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
)

const mergeModel = "mlx-community/Qwen3-8B-4bit"

// mergingOn is the settings of a machine where the operator switched merging
// on for the one model these tests serve.
func mergingOn() config.Config {
	cfg := config.Default()
	cfg.PerModel = map[string]config.ModelSettings{
		mergeModel: {MergeSystemMessages: true},
	}
	return cfg
}

// newMergeGateway wires a gateway to a fake mlx server, reading its settings
// live so a test can switch merging off between requests, and logging into a
// recorder at debug level so a test can see everything the gateway wrote.
func newMergeGateway(t *testing.T, cfg func() config.Config) (*httptest.Server, *stubPool, *mlxtest.Server, *logRecorder) {
	t.Helper()

	const modelPath = "/models/mlx-community/Qwen3-8B-4bit"
	fake := mlxtest.Start(mlxtest.Options{ModelArg: modelPath, Reply: "GROPIUS OK"})
	t.Cleanup(fake.Close)

	models := &stubModels{models: []registry.Model{{
		RepoID: mergeModel,
		Path:   modelPath,
		State:  registry.StateReady,
	}}}
	pool := &stubPool{srv: fake}
	rec := &logRecorder{}

	g := New(Options{
		ConfigFunc: cfg,
		Pool:       pool,
		Models:     models,
		Log:        slog.New(rec),
	})
	srv := httptest.NewServer(g.Handler())
	t.Cleanup(srv.Close)
	return srv, pool, fake, rec
}

// logRecorder keeps every log record the gateway writes, message and
// attributes alike, so a test can assert what did not reach the log. It
// accepts every level: a criterion that holds "at any log level" is only
// tested by a handler that refuses to filter.
type logRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (h *logRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (h *logRecorder) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" " + a.Key + "=" + a.Value.String())
		return true
	})
	h.mu.Lock()
	h.lines = append(h.lines, b.String())
	h.mu.Unlock()
	return nil
}

func (h *logRecorder) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *logRecorder) WithGroup(string) slog.Handler            { return h }

func (h *logRecorder) text() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.lines, "\n")
}

// postMerge sends a chat completion and returns the messages the upstream saw.
func postMerge(t *testing.T, srv *httptest.Server, fake *mlxtest.Server, body map[string]any) []any {
	t.Helper()
	resp := post(t, srv, "/v1/chat/completions", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	upstream, _ := fake.LastBody()["messages"].([]any)
	return upstream
}

// decodedMessages is the value a messages array decodes to, which is the
// equality the gateway can promise: it re-marshals every request it relays, so
// byte-identity was never on offer for any field.
func decodedMessages(t *testing.T, messages []any) []any {
	t.Helper()
	b, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	var out []any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The feature itself. A client that re-sends its system prompt mid-conversation
// produces a request a template-strict model refuses; with merging on for that
// model, the model server sees one leading system message holding both texts in
// order, and every other message and field as it was.
func TestMergingFoldsSystemMessagesIntoOneLeadingMessage(t *testing.T) {
	srv, _, fake, _ := newMergeGateway(t, mergingOn)

	got := postMerge(t, srv, fake, map[string]any{
		"model":       mergeModel,
		"temperature": 0.25,
		"messages": []any{
			map[string]any{"role": "system", "content": "You are Alice's assistant."},
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "system", "content": "Answer briefly."},
			map[string]any{"role": "assistant", "content": "hello"},
		},
	})

	want := decodedMessages(t, []any{
		map[string]any{"role": "system", "content": "You are Alice's assistant.\n\nAnswer briefly."},
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{"role": "assistant", "content": "hello"},
	})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("upstream messages =\n %#v\nwant\n %#v", got, want)
	}
	// Merging rewrites the messages and nothing else. The model field is the
	// backend's own path, as it is for every relayed request.
	if temp, _ := fake.LastBody()["temperature"].(float64); temp != 0.25 {
		t.Errorf("temperature reached the model server as %v, want 0.25", temp)
	}
	if got := fake.LastModelField(); got != fake.ModelArg {
		t.Errorf("upstream saw model=%q, want the backend path %q", got, fake.ModelArg)
	}
}

// A single system message that is not first is exactly the shape a
// template-strict model refuses, so it is moved to the front too.
func TestMergingMovesALoneSystemMessageToTheFront(t *testing.T) {
	srv, _, fake, _ := newMergeGateway(t, mergingOn)

	got := postMerge(t, srv, fake, map[string]any{
		"model": mergeModel,
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "system", "content": "Answer briefly."},
		},
	})

	want := decodedMessages(t, []any{
		map[string]any{"role": "system", "content": "Answer briefly."},
		map[string]any{"role": "user", "content": "hi"},
	})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("upstream messages =\n %#v\nwant\n %#v", got, want)
	}
}

// A conversation whose only system message is already the leading one is
// already the shape the template wants, so it is not rebuilt at all. The
// fixture carries nothing but role and content, so the only thing that can
// leave it alone is the guard for that case.
func TestMergingLeavesAnAlreadyLeadingSystemMessageAlone(t *testing.T) {
	srv, _, fake, _ := newMergeGateway(t, mergingOn)

	in := []any{
		map[string]any{"role": "system", "content": "You are Alice's assistant."},
		map[string]any{"role": "user", "content": "hi"},
	}
	got := postMerge(t, srv, fake, map[string]any{"model": mergeModel, "messages": in})

	if want := decodedMessages(t, in); !reflect.DeepEqual(got, want) {
		t.Errorf("upstream messages =\n %#v\nwant them untouched\n %#v", got, want)
	}
}

// The merged message is the conversation's own leading system message with its
// text extended, not a new message written from scratch, so a field the client
// put on that message — a name, a cache directive — stays on the message that
// owned it. Nothing is invented: the later instructions are appended to the
// text of the message that was already there.
func TestMergingKeepsTheLeadingSystemMessagesOwnFields(t *testing.T) {
	srv, _, fake, _ := newMergeGateway(t, mergingOn)

	got := postMerge(t, srv, fake, map[string]any{
		"model": mergeModel,
		"messages": []any{
			map[string]any{"role": "system", "content": "You are Alice's assistant.", "name": "house-rules"},
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "system", "content": "Answer briefly."},
		},
	})

	want := decodedMessages(t, []any{
		map[string]any{
			"role":    "system",
			"content": "You are Alice's assistant.\n\nAnswer briefly.",
			"name":    "house-rules",
		},
		map[string]any{"role": "user", "content": "hi"},
	})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("upstream messages =\n %#v\nwant\n %#v", got, want)
	}
}

// An instruction message that renders to nothing — an unset system-prompt
// setting, a template with nothing in it — must not put a blank line at the
// front of the merged prompt, which is text the client never sent.
func TestMergingDropsEmptyInstructionsFromTheJoin(t *testing.T) {
	srv, _, fake, _ := newMergeGateway(t, mergingOn)

	got := postMerge(t, srv, fake, map[string]any{
		"model": mergeModel,
		"messages": []any{
			map[string]any{"role": "system", "content": ""},
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "system", "content": "Answer briefly."},
		},
	})

	want := decodedMessages(t, []any{
		map[string]any{"role": "system", "content": "Answer briefly."},
		map[string]any{"role": "user", "content": "hi"},
	})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("upstream messages =\n %#v\nwant\n %#v", got, want)
	}
}

// The switch is per model. A model the operator never named is relayed exactly
// as it always was — the gateway does not so much as look at its messages.
func TestWithoutTheSwitchTheMessagesReachTheModelUnchanged(t *testing.T) {
	srv, _, fake, _ := newMergeGateway(t, config.Default)

	in := []any{
		map[string]any{"role": "system", "content": "You are Alice's assistant."},
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{"role": "system", "content": "Answer briefly."},
	}
	got := postMerge(t, srv, fake, map[string]any{"model": mergeModel, "messages": in})

	if want := decodedMessages(t, in); !reflect.DeepEqual(got, want) {
		t.Errorf("upstream messages =\n %#v\nwant them unchanged\n %#v", got, want)
	}
}

// System content sent as a list of parts is not something merging can join
// without inventing a shape the client did not send, so the request is relayed
// as it came — and nothing about it is written down.
func TestMergingLeavesContentPartsAlone(t *testing.T) {
	srv, _, fake, rec := newMergeGateway(t, mergingOn)

	const canary = "zx-content-part-canary"
	in := []any{
		map[string]any{"role": "system", "content": []any{
			map[string]any{"type": "text", "text": canary},
		}},
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{"role": "system", "content": "Answer briefly."},
	}
	got := postMerge(t, srv, fake, map[string]any{"model": mergeModel, "messages": in})

	if want := decodedMessages(t, in); !reflect.DeepEqual(got, want) {
		t.Errorf("upstream messages =\n %#v\nwant them unchanged\n %#v", got, want)
	}
	if strings.Contains(rec.text(), canary) {
		t.Errorf("the log records a request it could not merge:\n%s", rec.text())
	}
}

// Switching merging off in Settings takes effect on the next request: the
// gateway reads the live settings, so a model that was being merged goes back
// to being relayed unchanged without a restart.
func TestSwitchingMergingOffRelaysTheNextRequestUnchanged(t *testing.T) {
	var mu sync.Mutex
	cfg := mergingOn()
	srv, _, fake, _ := newMergeGateway(t, func() config.Config {
		mu.Lock()
		defer mu.Unlock()
		return cfg
	})

	in := []any{
		map[string]any{"role": "system", "content": "You are Alice's assistant."},
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{"role": "system", "content": "Answer briefly."},
	}
	if got := postMerge(t, srv, fake, map[string]any{"model": mergeModel, "messages": in}); len(got) != 2 {
		t.Fatalf("merging was not on to begin with: upstream saw %#v", got)
	}

	mu.Lock()
	cfg.PerModel = nil
	mu.Unlock()

	got := postMerge(t, srv, fake, map[string]any{"model": mergeModel, "messages": in})
	if want := decodedMessages(t, in); !reflect.DeepEqual(got, want) {
		t.Errorf("upstream messages after the switch went off =\n %#v\nwant them unchanged\n %#v", got, want)
	}
}

// The settings are keyed by the id a request resolves to, not by the name the
// client typed. A client using the short name — which many OpenAI-compatible
// UIs show — must get the same treatment as one using the full repo id.
func TestMergingMatchesTheResolvedModelNotTheRequestedName(t *testing.T) {
	srv, _, fake, _ := newMergeGateway(t, mergingOn)

	got := postMerge(t, srv, fake, map[string]any{
		"model": "Qwen3-8B-4bit",
		"messages": []any{
			map[string]any{"role": "system", "content": "You are Alice's assistant."},
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "system", "content": "Answer briefly."},
		},
	})

	if len(got) != 2 {
		t.Errorf("upstream messages = %#v; the short name did not match the model's settings", got)
	}
}

// The log line is for the operator who switched merging on and is not getting
// it. A conversation that was already in order is the ordinary case, not a
// problem, and must not put a line in the log on every request — which is also
// what keeps the line's own wording true when it does appear.
func TestOnlyARefusalIsLogged(t *testing.T) {
	cases := []struct {
		name     string
		messages []any
		wantLine bool
	}{
		{
			name: "already in order",
			messages: []any{
				map[string]any{"role": "system", "content": "You are Alice's assistant."},
				map[string]any{"role": "user", "content": "hi"},
			},
		},
		{
			name:     "nothing to merge",
			messages: []any{map[string]any{"role": "user", "content": "hi"}},
		},
		{
			name: "merged",
			messages: []any{
				map[string]any{"role": "system", "content": "You are Alice's assistant."},
				map[string]any{"role": "user", "content": "hi"},
				map[string]any{"role": "system", "content": "Answer briefly."},
			},
		},
		{
			name: "refused",
			messages: []any{
				map[string]any{"role": "user", "content": "hi"},
				map[string]any{"role": "system", "content": []any{map[string]any{"type": "text", "text": "a"}}},
				map[string]any{"role": "system", "content": "b"},
			},
			wantLine: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _, fake, rec := newMergeGateway(t, mergingOn)
			postMerge(t, srv, fake, map[string]any{"model": mergeModel, "messages": c.messages})

			logged := strings.Contains(rec.text(), "unmerged")
			if logged != c.wantLine {
				t.Errorf("logged=%v, want %v. Log was:\n%s", logged, c.wantLine, rec.text())
			}
		})
	}
}

// The rule the whole feature is granted under: nothing merging reads is kept.
// The prompt carries a canary, the gateway logs at debug level into a handler
// that records every message and attribute, and the canary must appear
// nowhere — not in the log, and not in anything handed to the pool, which is
// all a model server's launch arguments are ever built from.
func TestMergedRequestLeavesNoPromptContentBehind(t *testing.T) {
	srv, pool, fake, rec := newMergeGateway(t, mergingOn)

	const canary = "zx-prompt-canary-8391"
	postMerge(t, srv, fake, map[string]any{
		"model": mergeModel,
		"messages": []any{
			map[string]any{"role": "system", "content": canary},
			map[string]any{"role": "user", "content": "hi " + canary},
			map[string]any{"role": "system", "content": "Answer briefly."},
		},
	})

	if strings.Contains(rec.text(), canary) {
		t.Errorf("prompt content reached the log:\n%s", rec.text())
	}
	pool.mu.Lock()
	acquired := strings.Join(pool.acquired, ",")
	pool.mu.Unlock()
	if acquired != mergeModel {
		t.Errorf("the pool was handed %q; a launch is built from what the pool is given, and it must be the model id alone", acquired)
	}
}

// encoding/json matches an object key to a struct field case-insensitively
// when no key matches exactly, and — decoding an object key by key — a later
// case-variant overwrites what the exact key already set. The model server
// matches exactly, and its answer does not depend on the order the keys
// arrived in. A message whose role is spelled "Role" after its "role" is
// therefore a user turn to the model, and merging must not read it as an
// instruction: folding it in would both obey text the model would not have
// obeyed and delete the turn the client actually sent.
//
// The body is written as raw bytes because the bug depends on that order, and
// marshalling a Go map sorts the keys into the order that hides it.
func TestMergingDoesNotPromoteACaseVariantRoleIntoTheSystemMessage(t *testing.T) {
	srv, _, fake, _ := newMergeGateway(t, mergingOn)

	const disguised = `{"role":"user","content":"hi","Role":"system"}`
	got := postRawMerge(t, srv, fake, `{"model":"`+mergeModel+`","messages":[`+
		`{"role":"system","content":"You are Alice's assistant."},`+
		disguised+`,`+
		`{"role":"system","content":"Answer briefly."}]}`)

	var carried any
	if err := json.Unmarshal([]byte(disguised), &carried); err != nil {
		t.Fatal(err)
	}
	want := decodedMessages(t, []any{
		map[string]any{"role": "system", "content": "You are Alice's assistant.\n\nAnswer briefly."},
		carried,
	})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("upstream messages =\n %#v\nwant\n %#v", got, want)
	}
}

// postRawMerge sends a chat completion whose body is exactly these bytes, for
// the cases where the order of an object's keys is the thing under test.
func postRawMerge(t *testing.T, srv *httptest.Server, fake *mlxtest.Server, body string) []any {
	t.Helper()
	resp, err := srv.Client().Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	upstream, _ := fake.LastBody()["messages"].([]any)
	return upstream
}

// recordingUpstream stands in for the model server and keeps the exact bytes
// of the request it was sent. Exact bytes are the point: the recording fake
// decodes what it receives, and a JSON decoder repairs invalid UTF-8 on the
// way in, so content that must be relayed byte-for-byte can only be seen here.
//
// When release is non-nil it answers a streamed completion by emitting one
// chunk, waiting to be released, then emitting the rest — which is how a test
// sees that the first chunk reached the client before the response completed.
type recordingUpstream struct {
	srv      *httptest.Server
	release  chan struct{}
	modelArg string

	mu   sync.Mutex
	body []byte
}

func newRecordingUpstream(t *testing.T, modelArg string, streaming bool) *recordingUpstream {
	t.Helper()
	u := &recordingUpstream{modelArg: modelArg}
	if streaming {
		u.release = make(chan struct{})
	}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.body = body
		u.mu.Unlock()

		if u.release == nil {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"chatcmpl-fake","object":"chat.completion","model":%q,"choices":[]}`, u.modelArg)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flush := w.(http.Flusher)
		chunk := func(text string) {
			b, _ := json.Marshal(map[string]any{
				"id": "chatcmpl-fake", "object": "chat.completion.chunk", "model": u.modelArg,
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}}},
			})
			w.Write([]byte("data: " + string(b) + "\n\n"))
			flush.Flush()
		}
		chunk("first ")
		select {
		case <-u.release:
		case <-r.Context().Done():
			return
		}
		chunk("second")
		w.Write([]byte("data: [DONE]\n\n"))
		flush.Flush()
	}))
	t.Cleanup(u.srv.Close)
	return u
}

// rawBody is the request the upstream received, byte for byte.
func (u *recordingUpstream) rawBody() []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.body
}

// messages is the relayed messages array as a value.
func (u *recordingUpstream) messages(t *testing.T) []any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(u.rawBody(), &payload); err != nil {
		t.Fatalf("upstream body is not JSON: %v", err)
	}
	out, _ := payload["messages"].([]any)
	return out
}

// newRecordingGateway puts a recording upstream behind the gateway.
func newRecordingGateway(t *testing.T, cfg func() config.Config, streaming bool) (*httptest.Server, *recordingUpstream) {
	t.Helper()
	const modelPath = "/models/mlx-community/Qwen3-8B-4bit"
	upstream := newRecordingUpstream(t, modelPath, streaming)
	models := &stubModels{models: []registry.Model{{
		RepoID: mergeModel, Path: modelPath, State: registry.StateReady,
	}}}
	g := New(Options{
		ConfigFunc: cfg,
		Pool:       &urlPool{baseURL: upstream.srv.URL, modelArg: modelPath},
		Models:     models,
		Log:        slog.New(&logRecorder{}),
	})
	srv := httptest.NewServer(g.Handler())
	t.Cleanup(srv.Close)
	return srv, upstream
}

// urlPool hands out one upstream at a fixed URL, so a test can put a stub
// other than the fake mlx server behind the gateway.
type urlPool struct {
	baseURL  string
	modelArg string
}

func (p *urlPool) Acquire(context.Context, string) (*runtime.Upstream, func(), error) {
	return &runtime.Upstream{RepoID: mergeModel, BaseURL: p.baseURL, ModelArg: p.modelArg}, func() {}, nil
}
func (p *urlPool) Resident() []runtime.Resident { return nil }
func (p *urlPool) Unload(string) error          { return nil }

// Merging is done once, on the body the gateway has already buffered, so a
// streamed completion is merged exactly like any other and still streams: the
// model server receives the folded messages, the first chunk reaches the
// client while the upstream is still generating, and every chunk names the
// model the client asked for rather than the backend's path.
func TestMergedStreamingRequestStillStreams(t *testing.T) {
	const modelPath = "/models/mlx-community/Qwen3-8B-4bit"
	srv, upstream := newRecordingGateway(t, mergingOn, true)

	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":  mergeModel,
		"stream": true,
		"messages": []any{
			map[string]any{"role": "system", "content": "You are Alice's assistant."},
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "system", "content": "Answer briefly."},
		},
	}, nil)
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	first := make(chan string, 1)
	go func() {
		line, err := br.ReadString('\n')
		if err != nil {
			close(first)
			return
		}
		first <- line
	}()

	var firstLine string
	select {
	case line, ok := <-first:
		if !ok {
			t.Fatal("the stream ended before its first chunk")
		}
		firstLine = line
	case <-time.After(5 * time.Second):
		t.Fatal("the first chunk did not reach the client while the upstream was still generating")
	}
	if !strings.HasPrefix(firstLine, "data: ") {
		t.Fatalf("first chunk is not an SSE event: %q", firstLine)
	}

	// The criterion is about a *merged* streaming request, so the relayed body
	// is the half that matters most: a streaming path routed around the merge
	// would still stream perfectly.
	want := decodedMessages(t, []any{
		map[string]any{"role": "system", "content": "You are Alice's assistant.\n\nAnswer briefly."},
		map[string]any{"role": "user", "content": "hi"},
	})
	if got := upstream.messages(t); !reflect.DeepEqual(got, want) {
		t.Errorf("the streamed request reached the model server as\n %#v\nwant\n %#v", got, want)
	}

	close(upstream.release)
	rest, err := io.ReadAll(br)
	if err != nil {
		t.Fatal(err)
	}
	all := firstLine + string(rest)
	if !strings.Contains(all, "data: [DONE]") {
		t.Errorf("stream did not end with [DONE]:\n%s", all)
	}
	if strings.Contains(all, modelPath) {
		t.Errorf("a chunk named the backend path rather than the requested model:\n%s", all)
	}
	for _, line := range strings.Split(all, "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("chunk %q: %v", payload, err)
		}
		if chunk.Model != mergeModel {
			t.Errorf("chunk names model %q, want the requested %q", chunk.Model, mergeModel)
		}
	}
}

// Merging is for chat completions. A plain completion carries a prompt rather
// than a conversation, and the route is the only thing that makes "Gropius
// reads prompt content on exactly one route" true — so a client that posts a
// messages array to /v1/completions has it relayed untouched.
func TestPlainCompletionsAreNeverMerged(t *testing.T) {
	srv, upstream := newRecordingGateway(t, mergingOn, false)

	in := []any{
		map[string]any{"role": "system", "content": "You are Alice's assistant."},
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{"role": "system", "content": "Answer briefly."},
	}
	resp := post(t, srv, "/v1/completions", map[string]any{
		"model": mergeModel, "prompt": "hi", "messages": in,
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if got, want := upstream.messages(t), decodedMessages(t, in); !reflect.DeepEqual(got, want) {
		t.Errorf("a plain completion reached the model server as\n %#v\nwant it unmerged\n %#v", got, want)
	}
}

// Go repairs an invalid UTF-8 byte sequence into U+FFFD when it decodes a JSON
// string, and re-encodes it as valid UTF-8. Merging such a message would hand
// the model characters the client never sent, while relaying it passes the
// same bytes through untouched — so it is relayed. Only raw bytes can show
// this: every JSON decoder between here and the assertion would repair them.
func TestMergingRefusesInvalidUTF8InSystemContent(t *testing.T) {
	srv, upstream := newRecordingGateway(t, mergingOn, false)

	// 0xFF 0xFE is not a valid UTF-8 sequence, and the JSON scanner does not
	// object to it inside a string.
	body := `{"model":"` + mergeModel + `","messages":[` +
		"{\"role\":\"system\",\"content\":\"a\xff\xfeb\"}," +
		`{"role":"user","content":"hi"},` +
		`{"role":"system","content":"Answer briefly."}]}`

	resp, err := srv.Client().Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	raw := upstream.rawBody()
	if !bytes.Contains(raw, []byte{0xff, 0xfe}) {
		t.Errorf("the model server received %q; the invalid bytes were repaired rather than relayed through", raw)
	}
	if bytes.Contains(raw, []byte(`a\ufffd`)) || bytes.Contains(raw, []byte(`\n\nAnswer briefly.`)) {
		t.Errorf("the request was merged despite content merging cannot re-encode: %q", raw)
	}
}

// The unit the criteria above exercise through HTTP, checked directly on the
// shapes a client can send that it must refuse to rewrite.
func TestMergeSystemMessagesRefusesShapesItCannotRebuild(t *testing.T) {
	cases := []struct {
		name     string
		messages string
	}{
		{"not an array", `{"role":"system"}`},
		{"an element that is not an object", `["hello",{"role":"system","content":"a"}]`},
		{"a role that is not a string", `[{"role":5},{"role":"system","content":"a"}]`},
		{"system content that is a list of parts", `[{"role":"system","content":[{"type":"text","text":"a"}]},{"role":"user","content":"hi"},{"role":"system","content":"b"}]`},
		{"a system message with no content", `[{"role":"system"},{"role":"user","content":"hi"},{"role":"system","content":"b"}]`},
		{"a system message whose content is null", `[{"role":"system","content":"a"},{"role":"user","content":"hi"},{"role":"system","content":null}]`},
		{"a non-leading system message carrying another field", `[{"role":"user","content":"hi"},{"role":"system","content":"a","name":"house-rules"},{"role":"system","content":"b"}]`},
		{"a non-leading system message with a case-variant of content", `[{"role":"user","content":"hi"},{"role":"system","content":"a","Content":"b"},{"role":"system","content":"c"}]`},
		{"a leading system message whose content is not a string", `[{"role":"system","content":[{"type":"text","text":"a"}],"name":"x"},{"role":"system","content":"b"}]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, outcome := mergeSystemMessages(json.RawMessage(c.messages)); outcome != mergeRefused {
				t.Errorf("mergeSystemMessages returned %v for %s; it must refuse a shape it cannot rebuild faithfully",
					outcome, c.messages)
			}
		})
	}
}

// The conversations that are already the shape the template wants. These are
// not refusals — there is nothing wrong with them — and telling the two apart
// is what keeps the log quiet on the ordinary request.
func TestMergeSystemMessagesLeavesAConversationThatNeedsNoRewrite(t *testing.T) {
	cases := []struct {
		name     string
		messages string
	}{
		{"one system message, already leading", `[{"role":"system","content":"a"},{"role":"user","content":"hi"}]`},
		{"one leading system message carrying another field", `[{"role":"system","content":"a","name":"house-rules"},{"role":"user","content":"hi"}]`},
		{"no system message at all", `[{"role":"user","content":"hi"}]`},
		{"no messages at all", `[]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, outcome := mergeSystemMessages(json.RawMessage(c.messages)); outcome != mergeNotNeeded {
				t.Errorf("mergeSystemMessages returned %v for %s; it needs no rewrite and is not a refusal",
					outcome, c.messages)
			}
		})
	}
}
