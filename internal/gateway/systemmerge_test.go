package gateway

import (
	"bufio"
	"context"
	"encoding/json"
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

// A conversation whose system message is already the leading one needs no
// rewrite, and must not get one: rebuilding it would re-encode a message from
// the two fields merging knows and drop anything else it carries.
func TestMergingLeavesAnAlreadyLeadingSystemMessageAlone(t *testing.T) {
	srv, _, fake, _ := newMergeGateway(t, mergingOn)

	in := []any{
		map[string]any{"role": "system", "content": "You are Alice's assistant.", "name": "house-rules"},
		map[string]any{"role": "user", "content": "hi"},
	}
	got := postMerge(t, srv, fake, map[string]any{"model": mergeModel, "messages": in})

	if want := decodedMessages(t, in); !reflect.DeepEqual(got, want) {
		t.Errorf("upstream messages =\n %#v\nwant them untouched\n %#v", got, want)
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

// blockingUpstream answers a chat completion with an SSE stream that emits one
// chunk, waits to be released, then emits the rest — so a test can prove the
// first chunk reached the client before the upstream response completed.
type blockingUpstream struct {
	release chan struct{}
	srv     *httptest.Server
}

func newBlockingUpstream(t *testing.T, modelArg string) *blockingUpstream {
	t.Helper()
	u := &blockingUpstream{release: make(chan struct{})}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flush := w.(http.Flusher)
		chunk := func(text string) {
			b, _ := json.Marshal(map[string]any{
				"id": "chatcmpl-fake", "object": "chat.completion.chunk", "model": modelArg,
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
// streamed completion still streams: the first chunk reaches the client while
// the upstream is still generating, and every chunk names the model the client
// asked for rather than the backend's path.
func TestMergedStreamingRequestStillStreams(t *testing.T) {
	const modelPath = "/models/mlx-community/Qwen3-8B-4bit"
	upstream := newBlockingUpstream(t, modelPath)

	models := &stubModels{models: []registry.Model{{
		RepoID: mergeModel, Path: modelPath, State: registry.StateReady,
	}}}
	g := New(Options{
		ConfigFunc: mergingOn,
		Pool:       &urlPool{baseURL: upstream.srv.URL, modelArg: modelPath},
		Models:     models,
		Log:        slog.New(&logRecorder{}),
	})
	srv := httptest.NewServer(g.Handler())
	t.Cleanup(srv.Close)

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
		{"no system message at all", `[{"role":"user","content":"hi"}]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := mergeSystemMessages(json.RawMessage(c.messages)); ok {
				t.Errorf("mergeSystemMessages rewrote %s; it must relay a shape it cannot rebuild faithfully", c.messages)
			}
		})
	}
}
