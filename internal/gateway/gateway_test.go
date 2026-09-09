package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/mlxtest"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// stubModels is a fake registry.
type stubModels struct {
	models []registry.Model
}

func (s *stubModels) List() []registry.Model { return s.models }

func (s *stubModels) Ready() []registry.Model {
	var out []registry.Model
	for _, m := range s.models {
		if m.Ready() {
			out = append(out, m)
		}
	}
	return out
}

func (s *stubModels) Get(repoID string) (registry.Model, error) {
	for _, m := range s.models {
		if m.RepoID == repoID {
			return m, nil
		}
	}
	return registry.Model{}, registry.ErrNotFound
}

// stubPool hands out a fixed upstream backed by a fake mlx server.
type stubPool struct {
	srv *mlxtest.Server
	// acquireErr, if set, is returned by Acquire.
	acquireErr error
	// baseURL, if set, replaces the fake server's own address, so a test can
	// hand the gateway an upstream it cannot even build a request for.
	baseURL string
	// waits is what Acquire reports this acquisition spent getting here, which
	// is what the two wait headers are written from.
	waits runtime.AcquireStats
	// releaseDelay holds the handler up inside release(), which runs before
	// the deferred bookkeeping. It gives a test a window in which a client's
	// disconnect is delivered while the handler is still finishing.
	releaseDelay time.Duration

	mu       sync.Mutex
	acquired []string
	released int
	resident []runtime.Resident
	pinned   []string
	blockFor time.Duration
}

func (p *stubPool) Acquire(ctx context.Context, repoID string) (*runtime.Upstream, func(), error) {
	if p.acquireErr != nil {
		return nil, nil, p.acquireErr
	}
	if p.blockFor > 0 {
		select {
		case <-time.After(p.blockFor):
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	p.mu.Lock()
	p.acquired = append(p.acquired, repoID)
	p.mu.Unlock()

	release := func() {
		if p.releaseDelay > 0 {
			time.Sleep(p.releaseDelay)
		}
		p.mu.Lock()
		p.released++
		p.mu.Unlock()
	}
	base := p.srv.URL()
	if p.baseURL != "" {
		base = p.baseURL
	}
	return &runtime.Upstream{
		RepoID:   repoID,
		BaseURL:  base,
		ModelArg: p.srv.ModelArg,
		Waits:    p.waits,
	}, release, nil
}

func (p *stubPool) Resident() []runtime.Resident { return p.resident }
func (p *stubPool) Pinned() []string             { return p.pinned }
func (p *stubPool) Unload(string) error          { return nil }

func (p *stubPool) releases() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.released
}

// newTestGateway wires a gateway to a fake mlx server holding one model.
func newTestGateway(t *testing.T, cfg config.Config) (*httptest.Server, *stubPool, *mlxtest.Server) {
	t.Helper()

	const modelPath = "/models/mlx-community/Qwen3-8B-4bit"
	fake := mlxtest.Start(mlxtest.Options{ModelArg: modelPath, Reply: "GROPIUS OK"})
	t.Cleanup(fake.Close)

	models := &stubModels{models: []registry.Model{{
		RepoID: "mlx-community/Qwen3-8B-4bit",
		Path:   modelPath,
		State:  registry.StateReady,
	}}}
	pool := &stubPool{srv: fake}

	g := New(Options{Config: cfg, Pool: pool, Models: models})
	srv := httptest.NewServer(g.Handler())
	t.Cleanup(srv.Close)
	return srv, pool, fake
}

// A malformed request body (bad chunked framing) is a client error, not a
// timeout: it must fail 400 immediately, not 408.
func TestMalformedRequestBodyIsBadRequest(t *testing.T) {
	srv, _, _ := newTestGateway(t, config.Default())

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// "ZZZ" is not a valid chunk-size line, so the handler's body read fails
	// with a framing error on the first chunk rather than a deadline.
	fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\n"+
		"Host: gropius.test\r\nContent-Type: application/json\r\n"+
		"Transfer-Encoding: chunked\r\n\r\nZZZ\r\n0\r\n\r\n")

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for malformed chunked framing", resp.StatusCode)
	}
}

func post(t *testing.T, srv *httptest.Server, path string, body any, hdrs map[string]string) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// THE critical test. mlx-lm treats the "model" field as an instruction to load a
// model: if the gateway forwarded the client's friendly name, the backend would
// try to download a repo of that name from HuggingFace and fail with a 404.
func TestGatewayRewritesModelFieldToBackendPath(t *testing.T) {
	srv, _, fake := newTestGateway(t, config.Default())

	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":    "mlx-community/Qwen3-8B-4bit",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	}, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var buf strings.Builder
		io := make([]byte, 512)
		n, _ := resp.Body.Read(io)
		buf.Write(io[:n])
		t.Fatalf("status = %d, body = %s", resp.StatusCode, buf.String())
	}

	// The upstream must have seen its own --model path, not the client's name.
	if got := fake.LastModelField(); got != fake.ModelArg {
		t.Errorf("upstream saw model=%q, want the backend path %q — mlx-lm would try to download %q from HuggingFace",
			got, fake.ModelArg, got)
	}

	var out struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content != "GROPIUS OK" {
		t.Errorf("unexpected completion: %+v", out)
	}
}

// The inverse of the rewrite above: mlx-lm echoes the request's model field —
// post-rewrite, the backend's absolute --model path — into every response, and
// in a per-user install that path contains the account's home directory. The
// gateway must map it back to the name the client asked for.
func TestResponseModelFieldIsNotTheBackendPath(t *testing.T) {
	srv, _, fake := newTestGateway(t, config.Default())

	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":    "mlx-community/Qwen3-8B-4bit",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	}, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Model != "mlx-community/Qwen3-8B-4bit" {
		t.Errorf("response model = %q, want the requested name, not the backend path %q",
			out.Model, fake.ModelArg)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content != "GROPIUS OK" {
		t.Errorf("rewrite mangled the completion: %+v", out)
	}
}

// The same inverse mapping must hold for every SSE chunk of a streamed
// completion, without disturbing the stream's framing or its [DONE] sentinel.
func TestStreamingResponseModelFieldIsNotTheBackendPath(t *testing.T) {
	srv, _, fake := newTestGateway(t, config.Default())

	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":    "mlx-community/Qwen3-8B-4bit",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
		"stream":   true,
	}, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw := new(strings.Builder)
	if _, err := io.Copy(raw, resp.Body); err != nil {
		t.Fatal(err)
	}
	body := raw.String()
	if strings.Contains(body, fake.ModelArg) {
		t.Errorf("stream leaks the backend path %q:\n%s", fake.ModelArg, body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("stream did not end with [DONE]:\n%s", body)
	}
	chunks := 0
	for _, line := range strings.Split(body, "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}
		chunks++
		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct{ Content string } `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("chunk is not valid JSON after the rewrite: %v\n%s", err, payload)
		}
		if chunk.Model != "mlx-community/Qwen3-8B-4bit" {
			t.Errorf("chunk model = %q, want the requested name", chunk.Model)
		}
	}
	if chunks == 0 {
		t.Error("no SSE data chunks reached the client")
	}
}

// max_tokens and logprobs are client-controlled and unbounded; a well-behaved
// upstream honoring them can still produce a very large single-object
// response. relayRewritingModel's json branch must not buffer past
// maxResponseBody, mirroring the maxRequestBody cap on the request side.
func TestNonStreamingResponseBodyIsCapped(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(io.LimitReader(fillReader{}, maxResponseBody+1024)),
	}
	rec := httptest.NewRecorder()

	relayRewritingModel(rec, resp, "backend-path", "requested-name", relayOptions{})

	if got := rec.Body.Len(); got > maxResponseBody {
		t.Errorf("relayRewritingModel wrote %d bytes, want capped at maxResponseBody=%d", got, maxResponseBody)
	}
}

// A response cut short by the maxResponseBody cap is no longer valid JSON, so
// rewriteModelField's normal parse-and-replace path can't run — but modelArg,
// the backend's absolute --model path, must still never reach the client.
// Reproduces the leak the maxResponseBody cap introduced on its own: a
// truncated body that still contains a literal, unredacted modelArg.
func TestNonStreamingResponseBodyIsCappedWithoutLeakingBackendPath(t *testing.T) {
	const modelArg = "/Users/alice/Library/Application Support/Gropius/models/mlx-community/Qwen3-8B-4bit"
	const requested = "mlx-community/Qwen3-8B-4bit"

	// mlx-lm echoes "model" near the front of the object, well before a
	// 64 MiB cap would ever cut the body — that's what makes the leak
	// deterministic rather than a rare transport-failure edge case.
	prefix := `{"id":"chatcmpl-fake","object":"chat.completion","model":"` + modelArg + `","choices":[{"index":0,"message":{"role":"assistant","content":"`
	body := io.MultiReader(strings.NewReader(prefix), io.LimitReader(fillReader{}, maxResponseBody))
	resp := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(body),
	}
	rec := httptest.NewRecorder()

	relayRewritingModel(rec, resp, modelArg, requested, relayOptions{})

	if strings.Contains(rec.Body.String(), modelArg) {
		t.Fatalf("capped, truncated response still contains the backend path %q", modelArg)
	}
}

// rewriteModelField's one hard invariant — modelArg must never reach the
// client — has to hold even when b cannot be parsed as the expected shape at
// all (not just when it merely lacks a "model" field), which is exactly what
// a truncated or mid-read-interrupted body looks like.
func TestRewriteModelFieldRedactsBackendPathEvenWhenTruncated(t *testing.T) {
	const modelArg = "/Users/alice/Library/Application Support/Gropius/models/mlx-community/Qwen3-8B-4bit"
	const requested = "mlx-community/Qwen3-8B-4bit"

	truncated := []byte(`{"id":"chatcmpl-fake","model":"` + modelArg + `","choices":[{"index":0,"message":{"role":"assistant","content":"partial tex`)

	out := rewriteModelField(truncated, modelArg, requested)

	if strings.Contains(string(out), modelArg) {
		t.Fatalf("truncated body still contains the backend path %q:\n%s", modelArg, out)
	}
	if !strings.Contains(string(out), requested) {
		t.Errorf("redaction dropped the requested model name entirely:\n%s", out)
	}
}

// fillReader is an unbounded source of 'x' bytes.
type fillReader struct{}

func (fillReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

// Clients (and many OpenAI-compatible UIs) often use the short model name.
func TestGatewayAcceptsShortModelName(t *testing.T) {
	srv, pool, _ := newTestGateway(t, config.Default())

	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":    "Qwen3-8B-4bit", // no org prefix
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	}, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("short model name was rejected: status %d", resp.StatusCode)
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if len(pool.acquired) != 1 || pool.acquired[0] != "mlx-community/Qwen3-8B-4bit" {
		t.Errorf("short name resolved to %v, want the full repo id", pool.acquired)
	}
}

func TestUnknownModelReturns404(t *testing.T) {
	srv, _, _ := newTestGateway(t, config.Default())

	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":    "nobody/not-downloaded",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	}, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	var e struct {
		Error struct{ Message string } `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&e)
	if !strings.Contains(e.Error.Message, "download") {
		t.Errorf("error should tell the user to download the model, got %q", e.Error.Message)
	}
}

func TestMissingModelFieldIsRejected(t *testing.T) {
	srv, _, _ := newTestGateway(t, config.Default())
	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestMalformedJSONIsRejected(t *testing.T) {
	srv, _, _ := newTestGateway(t, config.Default())
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// /v1/models must come from our registry. mlx_lm.server's own /v1/models scans
// the HF cache dir and blows up when it is missing.
func TestListModelsComesFromRegistry(t *testing.T) {
	srv, _, _ := newTestGateway(t, config.Default())

	resp, err := srv.Client().Get(srv.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Object != "list" {
		t.Errorf("object = %q, want list", out.Object)
	}
	if len(out.Data) != 1 || out.Data[0].ID != "mlx-community/Qwen3-8B-4bit" {
		t.Fatalf("models = %+v", out.Data)
	}
}

func TestListModelsHidesUnreadyModels(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	models := &stubModels{models: []registry.Model{
		{RepoID: "org/ready", State: registry.StateReady},
		{RepoID: "org/downloading", State: registry.StateDownloading},
	}}
	g := New(Options{Config: config.Default(), Pool: &stubPool{srv: fake}, Models: models})
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Data) != 1 || out.Data[0].ID != "org/ready" {
		t.Errorf("a still-downloading model was advertised as servable: %+v", out.Data)
	}
}

// Tokens must reach the client as they are generated. A proxy that buffers the
// body would make streaming useless — the user would wait for the whole answer.
func TestStreamingIsNotBuffered(t *testing.T) {
	srv, _, _ := newTestGateway(t, config.Default())

	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":    "mlx-community/Qwen3-8B-4bit",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
		"stream":   true,
	}, nil)
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	first := string(body[:n])
	if !strings.HasPrefix(first, "data: ") {
		t.Errorf("first chunk is not an SSE event: %q", first)
	}

	// Drain and confirm the stream terminates properly.
	rest := make([]byte, 8192)
	total := first
	for {
		n, err := resp.Body.Read(rest)
		total += string(rest[:n])
		if err != nil {
			break
		}
	}
	if !strings.Contains(total, "data: [DONE]") {
		t.Errorf("stream did not end with [DONE]:\n%s", total)
	}
}

// Hop-by-hop headers from the upstream must not reach the client: the gateway
// re-frames the streamed body itself, so forwarding Transfer-Encoding or
// Content-Length would risk a response with conflicting framing.
func TestHopByHopHeadersAreNotForwarded(t *testing.T) {
	upstream := http.Header{
		"Content-Type":      {"application/json"},
		"Transfer-Encoding": {"chunked"},
		"Content-Length":    {"1234"},
		"Connection":        {"keep-alive"},
		"X-Model-Server":    {"mlx"},
	}
	dst := http.Header{}
	copyResponseHeaders(dst, upstream)

	for _, drop := range []string{"Transfer-Encoding", "Content-Length", "Connection"} {
		if dst.Get(drop) != "" {
			t.Errorf("hop-by-hop header %q was forwarded to the client", drop)
		}
	}
	if dst.Get("Content-Type") != "application/json" {
		t.Error("end-to-end header Content-Type was dropped")
	}
	if dst.Get("X-Model-Server") != "mlx" {
		t.Error("end-to-end header X-Model-Server was dropped")
	}
}

// The upstream is released even when the client disconnects mid-stream;
// otherwise the model would be pinned forever and could never be evicted.
func TestUpstreamIsReleasedAfterRequest(t *testing.T) {
	srv, pool, _ := newTestGateway(t, config.Default())

	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":    "mlx-community/Qwen3-8B-4bit",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	}, nil)
	resp.Body.Close()

	if got := pool.releases(); got != 1 {
		t.Errorf("release called %d times, want 1 — a leaked reference pins the model forever", got)
	}
}

func TestPoolErrorBecomes503(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	models := &stubModels{models: []registry.Model{
		{RepoID: "org/m", State: registry.StateReady},
	}}
	pool := &stubPool{srv: fake, acquireErr: fmt.Errorf("not enough memory to load another model")}
	g := New(Options{Config: config.Default(), Pool: pool, Models: models})
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()

	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":    "org/m",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	}, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	var e struct {
		Error struct{ Message string } `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&e)
	if !strings.Contains(e.Error.Message, "memory") {
		t.Errorf("the memory-pressure reason should reach the client, got %q", e.Error.Message)
	}
}

// A Launcher.Launch failure can embed absolute local filesystem paths (the
// venv interpreter, the model directory, a log file) rooted under the
// serving account's home directory. Because the default config ships with no
// API key, that error would otherwise reach any unauthenticated LAN caller.
func TestLaunchErrorDoesNotLeakLocalPaths(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	models := &stubModels{models: []registry.Model{
		{RepoID: "org/m", State: registry.StateReady},
	}}
	leaky := fmt.Errorf("python runtime is not installed (/Users/carol/Library/Application Support/Gropius/venv/bin/python): file does not exist")
	pool := &stubPool{srv: fake, acquireErr: fmt.Errorf("start model server for org/m: %w", &runtime.LaunchError{Err: leaky})}
	g := New(Options{Config: config.Default(), Pool: pool, Models: models})
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()

	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":    "org/m",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	}, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	var e struct {
		Error struct{ Message string } `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&e)
	if strings.Contains(e.Error.Message, "/Users/") || strings.Contains(e.Error.Message, "carol") {
		t.Errorf("launch error leaked a local path to the network caller: %q", e.Error.Message)
	}
	if e.Error.Message == "" {
		t.Error("client should still get a non-empty error message")
	}
}

// ---- auth ----

func TestNoAuthByDefault(t *testing.T) {
	srv, _, _ := newTestGateway(t, config.Default())
	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":    "mlx-community/Qwen3-8B-4bit",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the default config should not require a key, got status %d", resp.StatusCode)
	}
}

// httptest serves on loopback, and loopback is exempt from auth, so to test the
// key we drive the middleware directly with a non-loopback RemoteAddr.
func gatewayWithKey(t *testing.T, key string) http.Handler {
	t.Helper()
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	t.Cleanup(fake.Close)

	cfg := config.Default()
	cfg.APIKey = key
	models := &stubModels{models: []registry.Model{{RepoID: "org/m", State: registry.StateReady}}}
	g := New(Options{Config: cfg, Pool: &stubPool{srv: fake}, Models: models})
	return g.Handler()
}

func TestAPIKeyRequiredForLANRequests(t *testing.T) {
	h := gatewayWithKey(t, "bh_secret")

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "192.168.1.50:9999" // off-machine
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("a LAN request with no key returned %d, want 401", w.Code)
	}
}

func TestCorrectAPIKeyIsAccepted(t *testing.T) {
	h := gatewayWithKey(t, "bh_secret")

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "192.168.1.50:9999"
	req.Header.Set("Authorization", "Bearer bh_secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("a LAN request with the right key returned %d, want 200", w.Code)
	}
}

func TestWrongAPIKeyIsRejected(t *testing.T) {
	h := gatewayWithKey(t, "bh_secret")

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "192.168.1.50:9999"
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("a wrong key returned %d, want 401", w.Code)
	}
}

// A key set at runtime (through the control panel) must take effect on the
// already-running gateway without a restart. Reading a frozen config snapshot
// left the LAN endpoint unauthenticated while the UI reported it as protected.
func TestAPIKeyChangeTakesEffectLive(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	t.Cleanup(fake.Close)

	live := config.Default() // starts with no key
	models := &stubModels{models: []registry.Model{{RepoID: "org/m", State: registry.StateReady}}}
	g := New(Options{
		ConfigFunc: func() config.Config { return live },
		Pool:       &stubPool{srv: fake},
		Models:     models,
	})
	h := g.Handler()

	// With no key, a LAN request is served.
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "192.168.1.50:9999"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("with no key, LAN request got %d, want 200", w.Code)
	}

	// Set a key at runtime; the same handler must now demand it.
	live.APIKey = "bh_live"
	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "192.168.1.50:9999"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("after setting a key at runtime, LAN request got %d, want 401", w.Code)
	}
}

// Other macOS user accounts reach the server over loopback. Forcing them to
// configure a key would break every local OpenAI client for no security gain.
func TestLoopbackIsExemptFromAuth(t *testing.T) {
	h := gatewayWithKey(t, "bh_secret")

	for _, tc := range []struct{ addr, host string }{
		{"127.0.0.1:5555", "localhost:11535"},
		{"[::1]:5555", "[::1]:11535"},
		{"127.0.0.1:5555", "127.0.0.1:11535"},
	} {
		addr := tc.addr
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		req.RemoteAddr = addr
		req.Host = tc.host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("loopback %s got %d, want 200 (loopback must not need a key)", addr, w.Code)
		}
	}
}

// A page the victim's browser visits also connects from loopback: without an
// Origin check, a no-preflight cross-origin POST (a CORS-safelisted
// Content-Type needs no Authorization header, so browsers send it with no
// preflight) would ride the loopback exemption straight through, even with a
// key configured — the same CSRF class the control plane's loopbackOnly
// already blocks.
func TestLoopbackCrossOriginRequestIsRejected(t *testing.T) {
	h := gatewayWithKey(t, "bh_secret")

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("loopback request with a foreign Origin got %d, want 403", w.Code)
	}
}

// A same-origin loopback request (what the app's own local clients send) must
// keep working — only a foreign Origin should be refused.
func TestLoopbackSameOriginRequestIsAllowed(t *testing.T) {
	h := gatewayWithKey(t, "bh_secret")

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Host = "localhost:11535"
	req.Header.Set("Origin", "http://127.0.0.1:11535")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("loopback request with a loopback Origin got %d, want 200", w.Code)
	}
}

// With no key configured, the server is already unauthenticated by the user's
// own explicit choice (iss-1) — the Origin check exists to protect a configured
// key, so it must not restrict anything when there is no key to protect.
func TestLoopbackWithNoKeyIgnoresForeignOrigin(t *testing.T) {
	h := gatewayWithKey(t, "")

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("loopback request with no key configured got %d, want 200 (no key means nothing to protect)", w.Code)
	}
}

// The client's token is Gropius' business; the model server has no use for it.
func TestClientTokenIsNotForwardedUpstream(t *testing.T) {
	cfg := config.Default()
	cfg.APIKey = "bh_secret"

	const modelPath = "/models/org/m"
	fake := mlxtest.Start(mlxtest.Options{ModelArg: modelPath})
	defer fake.Close()

	models := &stubModels{models: []registry.Model{{RepoID: "org/m", Path: modelPath, State: registry.StateReady}}}
	g := New(Options{Config: cfg, Pool: &stubPool{srv: fake}, Models: models})
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()

	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":    "org/m",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	}, map[string]string{"Authorization": "Bearer bh_secret"})
	defer resp.Body.Close()

	if got := fake.LastAuthHeader(); got != "" {
		t.Errorf("the client's Authorization header was forwarded to the model server: %q", got)
	}
}

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:1234", true},
		{"[::1]:1234", true},
		{"192.168.1.10:1234", false},
		{"10.0.0.5:80", false},
	}
	for _, tt := range tests {
		if got := isLoopback(tt.addr); got != tt.want {
			t.Errorf("isLoopback(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}

func TestHealthEndpoint(t *testing.T) {
	srv, _, _ := newTestGateway(t, config.Default())
	resp, err := srv.Client().Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if out["status"] != "ok" {
		t.Errorf("health = %v", out)
	}
}

// A DNS-rebound page reaches the gateway from loopback with a same-origin GET:
// no Origin header, and a Host naming the attacker's domain. The loopback
// exemption must therefore also require a loopback Host when a key is set —
// the same guard the control plane's loopbackOnly applies. A foreign Host
// falls through to the bearer check rather than a flat refusal, so a
// same-machine proxy that preserves Host keeps working by sending the key.
func TestLoopbackRebindingHostRequiresKey(t *testing.T) {
	h := gatewayWithKey(t, "bh_secret")

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Host = "attacker.example:11535"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("loopback GET with a foreign Host and no key got %d, want 401", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Host = "attacker.example:11535"
	req.Header.Set("Authorization", "Bearer bh_secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("loopback GET with a foreign Host but the right key got %d, want 200", w.Code)
	}

	open := gatewayWithKey(t, "")
	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Host = "attacker.example:11535"
	w = httptest.NewRecorder()
	open.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("with no key configured loopback must stay open regardless of Host, got %d", w.Code)
	}
}

// Gropius's extensions to the OpenAI-shaped models list are top-level fields
// with names already common elsewhere: context_length is what OpenRouter- and
// Ollama-style listings publish, max_model_len what vLLM-derived clients read.
// Both carry the same figure, and the four fields the list already served are
// untouched.
func TestListModelsPublishesContextLengthUnderBothNames(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	models := &stubModels{models: []registry.Model{
		{RepoID: "org/wide", State: registry.StateReady, ContextLength: 262144},
	}}
	g := New(Options{Config: config.Default(), Pool: &stubPool{srv: fake}, Models: models})
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()

	entry := firstModelEntry(t, srv)
	for _, name := range []string{"context_length", "max_model_len"} {
		n, ok := entry[name].(float64)
		if !ok || int64(n) != 262144 {
			t.Errorf("%s = %v, want 262144", name, entry[name])
		}
	}
	want := map[string]bool{"id": true, "object": true, "created": true, "owned_by": true,
		"context_length": true, "max_model_len": true}
	for k := range entry {
		if !want[k] {
			t.Errorf("unexpected field %q on the models list", k)
		}
	}
	for k := range want {
		if _, ok := entry[k]; !ok {
			t.Errorf("field %q missing from the models list", k)
		}
	}
}

// A model whose configuration declares no positional range is listed exactly
// as it is without one: no figure, and still ready to serve.
func TestListModelsOmitsAnUnknownContextLength(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	models := &stubModels{models: []registry.Model{
		{RepoID: "org/quiet", State: registry.StateReady},
	}}
	g := New(Options{Config: config.Default(), Pool: &stubPool{srv: fake}, Models: models})
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()

	entry := firstModelEntry(t, srv)
	if entry["id"] != "org/quiet" {
		t.Fatalf("the model was not listed: %+v", entry)
	}
	for _, name := range []string{"context_length", "max_model_len"} {
		if v, ok := entry[name]; ok {
			t.Errorf("%s = %v, want the field to be absent", name, v)
		}
	}
}

// The registry bounds the figure on every path into it, but the models list
// is the LAN-facing edge: a figure that somehow got past those bounds must
// not be published from here either.
func TestListModelsRefusesAnAbsurdContextLength(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	models := &stubModels{models: []registry.Model{
		{RepoID: "org/absurd", State: registry.StateReady, ContextLength: registry.MaxContextLength + 1},
	}}
	g := New(Options{Config: config.Default(), Pool: &stubPool{srv: fake}, Models: models})
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()

	entry := firstModelEntry(t, srv)
	if _, ok := entry["context_length"]; ok {
		t.Errorf("an out-of-range figure was published: %v", entry["context_length"])
	}
}

// firstModelEntry decodes GET /v1/models and returns the single entry, as a
// raw map so a test can see exactly which fields are on the wire.
func firstModelEntry(t *testing.T, srv *httptest.Server) map[string]any {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 1 {
		t.Fatalf("data = %+v, want exactly one model", out.Data)
	}
	return out.Data[0]
}

// The models list is a documented interface, and the reference page is where
// a reader looks it up. This pins the page to the handler: every field served
// is described there, and nothing is described that is not served.
func TestModelsListReferenceDocumentsEveryFieldServed(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	models := &stubModels{models: []registry.Model{
		{RepoID: "org/m", State: registry.StateReady, ContextLength: 131072},
	}}
	g := New(Options{Config: config.Default(), Pool: &stubPool{srv: fake}, Models: models})
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()

	served := map[string]bool{}
	for k := range firstModelEntry(t, srv) {
		served[k] = true
	}
	// The residency fields are served only on a keyed install, so the set the
	// page is held to is the union of both listings — otherwise documenting
	// them would read here as documenting a field that does not exist.
	keyed := residencyGateway(t, "bh_secret", runtime.Resident{
		RepoID:   "org/warm",
		State:    runtime.ResidencyLoaded,
		LastUsed: time.Unix(1757145600, 0),
		InFlight: 1,
	})
	entries, _ := listModelsEntries(t, keyed, "bh_secret")
	for _, entry := range entries {
		for k := range entry {
			served[k] = true
		}
	}

	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "models-list.md"))
	if err != nil {
		t.Fatalf("the models-list reference page is missing: %v", err)
	}
	// Only the "## Fields" section is the field table; another table
	// elsewhere on the page must not be read as phantom fields.
	_, fields, ok := strings.Cut(string(page), "\n## Fields\n")
	if !ok {
		t.Fatal("the reference page has no `## Fields` section")
	}
	if next := strings.Index(fields, "\n## "); next >= 0 {
		fields = fields[:next]
	}
	documented := map[string]bool{}
	for _, line := range strings.Split(fields, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		field, _, ok := strings.Cut(strings.TrimPrefix(line, "| `"), "`")
		if ok {
			documented[field] = true
		}
	}

	for field := range served {
		if !documented[field] {
			t.Errorf("the models list serves %q, which the reference page does not describe", field)
		}
	}
	for field := range documented {
		if !served[field] {
			t.Errorf("the reference page describes %q, which the models list does not serve", field)
		}
	}

	// The things a reader must not have to infer: what the figure is, that it
	// is not what this Mac can necessarily serve, and the ceiling above which
	// a declared figure is refused — which the acceptance criterion calls the
	// documented ceiling, so it has to be a number on a user-facing page and
	// has to be the number the code enforces.
	// The same for residency: what the three values mean, that the fields
	// need a key, and that reading one reserves nothing — a client that took
	// the snapshot for a promise would be the failure this feature invites.
	for _, phrase := range []string{
		"architectural maximum",
		"may be smaller",
		withThousands(registry.MaxContextLength),
		"not_loaded",
		"API key",
		"snapshot",
	} {
		if !strings.Contains(string(page), phrase) {
			t.Errorf("the reference page never says %q", phrase)
		}
	}
}

// The acceptance criterion is written against the composed path, and every
// test above stubs out one half of it. This one carries a real model
// directory through a real Registry.Rescan to the bytes on the wire.
func TestContextLengthReachesTheWireFromAModelDirectory(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	root := t.TempDir()
	dir := filepath.Join(root, "models", "mlx-community", "Qwen3-Coder-Next-4bit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A config.json of the shape mlx-community publishes.
	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"model_type":"qwen3_next","max_position_embeddings":262144,"rope_scaling":null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), make([]byte, 64), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := registry.Open(filepath.Join(root, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Rescan(filepath.Join(root, "models")); err != nil {
		t.Fatalf("Rescan: %v", err)
	}

	g := New(Options{Config: config.Default(), Pool: &stubPool{srv: fake}, Models: reg})
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()

	entry := firstModelEntry(t, srv)
	if entry["id"] != "mlx-community/Qwen3-Coder-Next-4bit" {
		t.Fatalf("the scanned model was not listed: %+v", entry)
	}
	for _, name := range []string{"context_length", "max_model_len"} {
		if n, ok := entry[name].(float64); !ok || int64(n) != 262144 {
			t.Errorf("%s = %v, want 262144", name, entry[name])
		}
	}
}

// withThousands renders n the way the documentation writes a large number,
// so the ceiling on the reference page is checked against the constant the
// code enforces rather than a copy that can drift.
func withThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// listModelsEntries drives the handler as a LAN client would — an off-machine
// address, the key in a bearer header when there is one — and returns the
// entries as raw maps beside the exact bytes served, so a test can assert both
// what is on the wire and what is not.
func listModelsEntries(t *testing.T, h http.Handler, key string) ([]map[string]any, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "203.0.113.50:9999" // off-machine (RFC 5737 documentation range)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/models = %d, want 200: %s", w.Code, w.Body.String())
	}
	var out struct {
		Data []map[string]any `json:"data"`
	}
	body := w.Body.String()
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out.Data, body
}

// residencyGateway wires a gateway whose registry holds two ready models and
// whose pool is holding whichever of them the caller names as resident.
func residencyGateway(t *testing.T, key string, resident ...runtime.Resident) http.Handler {
	t.Helper()
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	t.Cleanup(fake.Close)

	cfg := config.Default()
	cfg.APIKey = key
	added := time.Unix(1757145600, 0)
	models := &stubModels{models: []registry.Model{
		{RepoID: "org/warm", State: registry.StateReady, Path: "/models/org/warm", AddedAt: added},
		{RepoID: "org/cold", State: registry.StateReady, Path: "/models/org/cold", AddedAt: added},
	}}
	g := New(Options{Config: cfg, Pool: &stubPool{srv: fake, resident: resident}, Models: models})
	return g.Handler()
}

// entryByID picks one model's entry out of a listing.
func entryByID(t *testing.T, entries []map[string]any, id string) map[string]any {
	t.Helper()
	for _, e := range entries {
		if e["id"] == id {
			return e
		}
	}
	t.Fatalf("%q is not in the listing: %+v", id, entries)
	return nil
}

// On a keyed install the listing says which models are loaded, how busy each
// one is and when it was last used, so a client can send its work to a warm
// model instead of triggering the load it never knew about. The
// fields the list already served are untouched.
func TestListModelsReportsResidencyOnAKeyedInstall(t *testing.T) {
	lastUsed := time.Unix(1757145600, 0)
	h := residencyGateway(t, "bh_secret", runtime.Resident{
		RepoID:   "org/warm",
		State:    runtime.ResidencyLoaded,
		Port:     51234,
		LastUsed: lastUsed,
		InFlight: 3,
	})

	entries, _ := listModelsEntries(t, h, "bh_secret")

	warm := entryByID(t, entries, "org/warm")
	if warm["state"] != "loaded" {
		t.Errorf("state = %v for a model whose probe answered, want %q", warm["state"], "loaded")
	}
	if n, ok := warm["in_flight"].(float64); !ok || int(n) != 3 {
		t.Errorf("in_flight = %v, want 3", warm["in_flight"])
	}
	if n, ok := warm["last_used"].(float64); !ok || int64(n) != lastUsed.Unix() {
		t.Errorf("last_used = %v, want %d (Unix seconds)", warm["last_used"], lastUsed.Unix())
	}

	cold := entryByID(t, entries, "org/cold")
	if cold["state"] != "not_loaded" {
		t.Errorf("state = %v for a model the pool is not holding, want %q", cold["state"], "not_loaded")
	}
	if n, ok := cold["in_flight"].(float64); !ok || int(n) != 0 {
		t.Errorf("in_flight = %v for a model that is not loaded, want 0", cold["in_flight"])
	}
	// A model that has never been used in this process has no last-used time,
	// and the field is absent rather than sent as a zero a client would read
	// as 1970.
	if v, ok := cold["last_used"]; ok {
		t.Errorf("last_used = %v for a model never used, want the field to be absent", v)
	}
	// The four fields the list served before this one are unchanged.
	if cold["object"] != "model" || cold["owned_by"] != "gropius" {
		t.Errorf("the pre-existing fields changed: %+v", cold)
	}
	if n, ok := cold["created"].(float64); !ok || int64(n) != 1757145600 {
		t.Errorf("created = %v, want the model's own added-at time", cold["created"])
	}
}

// A model whose server is up but has not answered its readiness probe is
// neither warm nor cold, and the listing must not round it to either: a client
// told "loaded" waits for the load anyway, one told "not loaded" may start a
// second, competing load.
func TestListModelsReportsALoadingModel(t *testing.T) {
	// The pool stamps lastUsed at launch, so a model that is still loading
	// already carries one — the moment its load was asked for.
	launched := time.Unix(1757145600, 0)
	h := residencyGateway(t, "bh_secret", runtime.Resident{
		RepoID:   "org/warm",
		State:    runtime.ResidencyLoading,
		LastUsed: launched,
	})

	entries, _ := listModelsEntries(t, h, "bh_secret")
	warm := entryByID(t, entries, "org/warm")
	if got := warm["state"]; got != "loading" {
		t.Errorf("state = %v for a model still loading, want %q", got, "loading")
	}
	// last_used is the pool's record for a model it is holding, and it is
	// holding this one. Suppressing it here would make an absent last_used
	// mean two different things; state is what says whether the model is warm.
	if n, ok := warm["last_used"].(float64); !ok || int64(n) != launched.Unix() {
		t.Errorf("last_used = %v for a loading model, want %d", warm["last_used"], launched.Unix())
	}
}

// With no key configured the server is open to the whole LAN, and the listing
// stays exactly what it is without this feature: nobody learns from it what
// this Mac is running. The key set is asserted exactly, so a field added later
// fails here rather than shipping onto an open server.
func TestListModelsCarriesNoResidencyWithoutAnAPIKey(t *testing.T) {
	h := residencyGateway(t, "", runtime.Resident{
		RepoID:   "org/warm",
		State:    runtime.ResidencyLoaded,
		Port:     51234,
		LastUsed: time.Unix(1757145600, 0),
		InFlight: 3,
	})

	entries, body := listModelsEntries(t, h, "")
	for _, entry := range entries {
		want := map[string]bool{"id": true, "object": true, "created": true, "owned_by": true}
		for k := range entry {
			if !want[k] {
				t.Errorf("an unkeyed listing carries %q; it must be exactly today's list", k)
			}
		}
		for k := range want {
			if _, ok := entry[k]; !ok {
				t.Errorf("field %q missing from an unkeyed listing", k)
			}
		}
	}
	// Byte-for-byte: not one of the residency field names reaches an open
	// server. The names only — a value like "loaded" is a substring of an id a
	// future fixture could carry, and the exact key-set assertion above already
	// covers the value side.
	for _, name := range []string{"state", "in_flight", "last_used", "pinned"} {
		if strings.Contains(body, name) {
			t.Errorf("an unkeyed listing mentions %q: %s", name, body)
		}
	}
}

// The residency projection is an allow-list of named fields, never the
// runtime.Resident struct marshalled whole: that struct carries the model
// server's loopback port, and the entry is built beside a model path that in a
// per-user install names the serving account's home directory. Neither may
// reach the network, and this is the standing guard on that staying true as
// fields are added to Resident.
func TestListModelsPublishesNoPortOrPath(t *testing.T) {
	h := residencyGateway(t, "bh_secret", runtime.Resident{
		RepoID:   "org/warm",
		State:    runtime.ResidencyLoaded,
		Port:     51234,
		Bytes:    8 << 30,
		LoadedAt: time.Unix(1757145600, 0),
		LastUsed: time.Unix(1757145600, 0),
	})

	_, body := listModelsEntries(t, h, "bh_secret")
	for _, leak := range []string{"127.0.0.1", "51234", "/models/org/warm", "bytes", "loaded_at", "port"} {
		if strings.Contains(body, leak) {
			t.Errorf("the models list published %q: %s", leak, body)
		}
	}
}

// The key is read once per request, by the middleware, and the handler decides
// on that same reading.
//
// Reading it twice would let the owner's click on Save land between the two: a
// LAN request admitted while no key was configured — and so admitted with no
// credential at all — would be served residency because a key existed a moment
// later. The window is tiny and the fields are small, but it is a request
// reaching something withAuth never sanctioned, so the handler must decide on
// the configuration the request was admitted under.
func TestListModelsDecidesOnTheConfigItWasAdmittedUnder(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	// The first read (withAuth's) sees no key, so the request is admitted
	// unauthenticated; every later read sees one, as if Save landed in between.
	var reads int
	cfgFn := func() config.Config {
		cfg := config.Default()
		if reads > 0 {
			cfg.APIKey = "bh_secret"
		}
		reads++
		return cfg
	}
	models := &stubModels{models: []registry.Model{
		{RepoID: "org/warm", State: registry.StateReady, AddedAt: time.Unix(1757145600, 0)},
	}}
	pool := &stubPool{srv: fake, resident: []runtime.Resident{{
		RepoID:   "org/warm",
		State:    runtime.ResidencyLoaded,
		InFlight: 3,
		LastUsed: time.Unix(1757145600, 0),
	}}}
	g := New(Options{ConfigFunc: cfgFn, Pool: pool, Models: models})

	entries, body := listModelsEntries(t, g.Handler(), "")
	entry := entryByID(t, entries, "org/warm")
	for _, name := range []string{"state", "in_flight", "last_used"} {
		if _, ok := entry[name]; ok {
			t.Errorf("a request admitted with no key configured was served %q: %s", name, body)
		}
	}
}

// The projection allow-lists the state's *value*, not only the field names.
// Pool is an interface, so the string in Resident.State is not this package's
// to trust: an implementation that leaves it unset would otherwise publish
// "state": "", a fourth value the reference page does not define and no client
// can act on. An unrecognized state means the listing cannot say the model is
// warm, which is exactly what not_loaded says.
func TestListModelsRefusesAnUnknownResidencyState(t *testing.T) {
	h := residencyGateway(t, "bh_secret", runtime.Resident{
		RepoID: "org/warm",
		State:  runtime.ResidencyState("wedged"),
	})

	entries, _ := listModelsEntries(t, h, "bh_secret")
	if got := entryByID(t, entries, "org/warm")["state"]; got != "not_loaded" {
		t.Errorf("state = %v for an unrecognized pool state, want %q", got, "not_loaded")
	}
}

// last_used is the pool's record for a model it is holding, so it is there
// exactly while the model is loaded and goes when the model does. A model that
// was hammered a minute ago and has since been evicted carries no last-used
// time, and a client must not be able to read that as "loaded and idle".
func TestListModelsOmitsLastUsedForAnEvictedModel(t *testing.T) {
	// The pool is holding nothing: org/warm has been evicted since its last
	// request, which is the ordinary state of a model on a busy Mac.
	h := residencyGateway(t, "bh_secret")

	entries, _ := listModelsEntries(t, h, "bh_secret")
	entry := entryByID(t, entries, "org/warm")
	if entry["state"] != "not_loaded" {
		t.Fatalf("state = %v, want %q", entry["state"], "not_loaded")
	}
	if v, ok := entry["last_used"]; ok {
		t.Errorf("last_used = %v for a model the pool is no longer holding, want the field to be absent", v)
	}
}

// The listing joins the registry's models to the pool's entries, and the two
// name a model with different spellings: the registry reports its canonical
// one, the pool whatever string reached Acquire. A model warmed under a
// hand-typed id must still be reported as loaded, or the client is told to go
// cold on a model that is warm — the exact swap this feature exists to avoid.
func TestListModelsJoinsResidencyWhateverTheSpelling(t *testing.T) {
	h := residencyGateway(t, "bh_secret", runtime.Resident{
		RepoID:   "ORG/WARM",
		State:    runtime.ResidencyLoaded,
		InFlight: 2,
		LastUsed: time.Unix(1757145600, 0),
	})

	entries, _ := listModelsEntries(t, h, "bh_secret")
	warm := entryByID(t, entries, "org/warm")
	if warm["state"] != "loaded" {
		t.Errorf("state = %v for a model the pool holds under another spelling, want %q",
			warm["state"], "loaded")
	}
	if n, ok := warm["in_flight"].(float64); !ok || int(n) != 2 {
		t.Errorf("in_flight = %v, want 2", warm["in_flight"])
	}
	// The other model must not have picked anything up from the join.
	if cold := entryByID(t, entries, "org/cold"); cold["state"] != "not_loaded" {
		t.Errorf("state = %v for the model that is not loaded, want %q", cold["state"], "not_loaded")
	}
}

// The condition is the install's, not the request's.
//
// A same-machine client is exempt from the bearer check, and the docs and the
// README both promise it never needs a key — so on a keyed install it sees the
// residency fields with no Authorization header at all, the same picture the
// control panel already shows it on loopback. Narrowing the gate to "this
// request presented a bearer" is a plausible misreading of "key-gated" that
// every other test in this package would survive; this is the one that does
// not.
func TestListModelsReportsResidencyToAnExemptLoopbackClient(t *testing.T) {
	h := residencyGateway(t, "bh_secret", runtime.Resident{
		RepoID:   "org/warm",
		State:    runtime.ResidencyLoaded,
		InFlight: 1,
		LastUsed: time.Unix(1757145600, 0),
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Host = "localhost:11535"
	// No Authorization header: this client is exempt and holds no key.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("an exempt loopback request got %d, want 200: %s", w.Code, w.Body.String())
	}
	var out struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	warm := entryByID(t, out.Data, "org/warm")
	for name, want := range map[string]any{"state": "loaded", "in_flight": float64(1)} {
		if warm[name] != want {
			t.Errorf("%s = %v for an exempt loopback client, want %v", name, warm[name], want)
		}
	}
	if _, ok := warm["last_used"]; !ok {
		t.Errorf("last_used missing for an exempt loopback client: %+v", warm)
	}
}

// The exemption is narrow, and residency does not widen it. A loopback
// connection carrying a foreign Host is the DNS-rebinding shape withAuth
// deliberately drops through to the bearer check, so with no key presented it
// is refused outright — and a refused request is served no fields at all.
func TestForeignHostLoopbackRequestGetsNoResidency(t *testing.T) {
	h := residencyGateway(t, "bh_secret", runtime.Resident{
		RepoID: "org/warm",
		State:  runtime.ResidencyLoaded,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Host = "attacker.example"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("a loopback request with a foreign Host and no key got %d, want 401", w.Code)
	}
	for _, name := range []string{"state", "in_flight", "last_used"} {
		if strings.Contains(w.Body.String(), name) {
			t.Errorf("a refused request was served %q: %s", name, w.Body.String())
		}
	}
}

// composedLauncher starts nothing: the pool's readiness probe is redirected to
// a fake mlx server by composedTransport, so a Process that merely exists is
// enough to carry the entry through startLocked.
type composedLauncher struct{ launched int }

func (l *composedLauncher) Precheck(runtime.Spec) error { return nil }

func (l *composedLauncher) Launch(context.Context, runtime.Spec) (runtime.Process, error) {
	l.launched++
	return composedProc{done: make(chan struct{})}, nil
}

type composedProc struct{ done chan struct{} }

func (p composedProc) Stop(context.Context) error { close(p.done); return nil }
func (p composedProc) Done() <-chan struct{}      { return p.done }
func (p composedProc) Err() error                 { return nil }
func (p composedProc) Pid() int                   { return 0 }

// composedTransport sends the pool's readiness probe to the fake server, since
// the pool addresses model servers by a port it allocated itself.
type composedTransport struct{ target string }

func (t composedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	out, err := http.NewRequestWithContext(req.Context(), req.Method, t.target+req.URL.Path, req.Body)
	if err != nil {
		return nil, err
	}
	out.Header = req.Header
	return http.DefaultTransport.RoundTrip(out)
}

// registrySource adapts the registry to runtime.ModelSource, as the app does.
type registrySource struct{ reg *registry.Registry }

func (s registrySource) Resolve(repoID string) (string, int64, error) {
	m, err := s.reg.Get(repoID)
	if err != nil {
		return "", 0, err
	}
	return m.Path, m.Bytes, nil
}

// Every other residency test stubs out one half of the path: the gateway tests
// hand the projection a hand-written runtime.Resident, and the pool test never
// reaches an HTTP handler. The seam between them — the pool's key space and the
// registry's, joined in handleListModels — is then checked by nothing but the
// compiler, and that seam is exactly where the spelling defect lived.
//
// This carries a real model directory through a real Registry.Rescan and a real
// runtime.Pool to the bytes on the wire, the same shape as
// TestContextLengthReachesTheWireFromAModelDirectory.
func TestResidencyReachesTheWireFromARealPool(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "models", "mlx-community", "Qwen3-8B-4bit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"model_type":"qwen3","max_position_embeddings":40960}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), make([]byte, 64), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Open(filepath.Join(root, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Rescan(filepath.Join(root, "models")); err != nil {
		t.Fatalf("Rescan: %v", err)
	}

	// The pool's readiness probe names the backend's --model value, which is
	// the registry's own path for the model, so the fake must answer to it.
	scanned, err := reg.Get("mlx-community/Qwen3-8B-4bit")
	if err != nil {
		t.Fatalf("the scanned model is not in the registry: %v", err)
	}
	fake := mlxtest.Start(mlxtest.Options{ModelArg: scanned.Path})
	defer fake.Close()

	launcher := &composedLauncher{}
	pool := runtime.NewPool(runtime.PoolOptions{
		Launcher:         launcher,
		Models:           registrySource{reg},
		MaxResidentBytes: 1 << 30,
		ReadyTimeout:     10 * time.Second,
		HTTP:             &http.Client{Timeout: 5 * time.Second, Transport: composedTransport{fake.URL()}},
	})
	defer pool.Close()

	cfg := config.Default()
	cfg.APIKey = "bh_secret"
	g := New(Options{Config: cfg, Pool: pool, Models: reg})

	// Cold first: the registry lists the model, the pool holds nothing.
	cold := entryByID(t, mustList(t, g.Handler()), "mlx-community/Qwen3-8B-4bit")
	if cold["state"] != "not_loaded" {
		t.Errorf("state = %v before the load, want %q", cold["state"], "not_loaded")
	}

	// Warm it through the pool, under a spelling the registry does not use —
	// the id an operator hand-types into preload.
	_, release, err := pool.Acquire(context.Background(), "MLX-Community/Qwen3-8B-4bit")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()

	warm := entryByID(t, mustList(t, g.Handler()), "mlx-community/Qwen3-8B-4bit")
	if warm["state"] != "loaded" {
		t.Errorf("state = %v after the load, want %q", warm["state"], "loaded")
	}
	if _, ok := warm["last_used"].(float64); !ok {
		t.Errorf("last_used = %v after the load, want a Unix time", warm["last_used"])
	}
	if launcher.launched != 1 {
		t.Errorf("launched %d model servers, want 1", launcher.launched)
	}
	// The pre-existing fields still come from the registry, unchanged.
	if warm["id"] != "mlx-community/Qwen3-8B-4bit" || warm["owned_by"] != "gropius" {
		t.Errorf("the pre-existing fields changed: %+v", warm)
	}
	if n, ok := warm["context_length"].(float64); !ok || int(n) != 40960 {
		t.Errorf("context_length = %v, want 40960", warm["context_length"])
	}
}

// mustList lists the models as an exempt loopback client and returns the entries.
func mustList(t *testing.T, h http.Handler) []map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Host = "localhost:11535"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/models = %d, want 200: %s", w.Code, w.Body.String())
	}
	var out struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Data
}

// The registry's index, the pool's entries and the listing's join between them
// are three key spaces that must be one. This drives a corpus of spellings
// through all three at once: the registry must resolve each to the same model,
// the pool must still hold exactly one entry however many spellings have asked
// for it, and the listing must report that model loaded under the registry's
// own spelling every time.
//
// The archtest keeps the fold in one place; this asserts that one place is
// enough — that no site has drifted into keying by something else.
func TestRegistryPoolAndListingShareOneKeySpace(t *testing.T) {
	const canonical = "mlx-community/Qwen3-8B-4bit"

	root := t.TempDir()
	dir := filepath.Join(root, "models", "mlx-community", "Qwen3-8B-4bit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"model_type":"qwen3","max_position_embeddings":40960}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), make([]byte, 64), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Open(filepath.Join(root, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Rescan(filepath.Join(root, "models")); err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	scanned, err := reg.Get(canonical)
	if err != nil {
		t.Fatalf("the scanned model is not in the registry: %v", err)
	}
	fake := mlxtest.Start(mlxtest.Options{ModelArg: scanned.Path})
	defer fake.Close()

	launcher := &composedLauncher{}
	pool := runtime.NewPool(runtime.PoolOptions{
		Launcher:         launcher,
		Models:           registrySource{reg},
		MaxResidentBytes: 1 << 30,
		ReadyTimeout:     10 * time.Second,
		HTTP:             &http.Client{Timeout: 5 * time.Second, Transport: composedTransport{fake.URL()}},
	})
	defer pool.Close()

	cfg := config.Default()
	cfg.APIKey = "bh_secret"
	g := New(Options{Config: cfg, Pool: pool, Models: reg})

	// Every spelling an operator or a script can reach the pool with.
	for _, spelling := range []string{
		canonical,
		"MLX-Community/Qwen3-8B-4bit",
		"mlx-community/qwen3-8b-4bit",
		"MLX-COMMUNITY/QWEN3-8B-4BIT",
	} {
		if got := config.FoldRepoID(spelling); got != config.FoldRepoID(canonical) {
			t.Errorf("FoldRepoID(%q) = %q, want the same key as %q", spelling, got, canonical)
			continue
		}
		if m, err := reg.Get(spelling); err != nil || m.RepoID != canonical {
			t.Errorf("registry.Get(%q) = %+v, %v; want the model spelled %q", spelling, m, err, canonical)
			continue
		}

		_, release, err := pool.Acquire(context.Background(), spelling)
		if err != nil {
			t.Errorf("Acquire(%q): %v", spelling, err)
			continue
		}
		release()

		if res := pool.Resident(); len(res) != 1 {
			t.Errorf("after acquiring %q the pool holds %d entries, want 1: %+v", spelling, len(res), res)
		}
		entry := entryByID(t, mustList(t, g.Handler()), canonical)
		if entry["state"] != "loaded" {
			t.Errorf("after acquiring %q the listing reports state = %v, want %q",
				spelling, entry["state"], "loaded")
		}
	}

	if launcher.launched != 1 {
		t.Errorf("launched %d model servers for one model, want 1", launcher.launched)
	}
}

// The projection reads withAuth's admission bit, so a handler reached without
// withAuth in front of it has no admission to read — and must report nothing
// rather than fall back to the live configuration, which is the second read
// this design exists to remove. Mounting handleListModels on a mux of its own
// is exactly the refactor that would do it silently.
func TestListModelsWithoutTheAuthMiddlewareReportsNoResidency(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	cfg := config.Default()
	cfg.APIKey = "bh_secret"
	models := &stubModels{models: []registry.Model{
		{RepoID: "org/warm", State: registry.StateReady, AddedAt: time.Unix(1757145600, 0)},
	}}
	pool := &stubPool{srv: fake, resident: []runtime.Resident{{
		RepoID:   "org/warm",
		State:    runtime.ResidencyLoaded,
		InFlight: 3,
		LastUsed: time.Unix(1757145600, 0),
	}}}
	g := New(Options{Config: cfg, Pool: pool, Models: models})

	// The handler directly: no withAuth, so no admission decision was made.
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	g.handleListModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/models = %d, want 200", w.Code)
	}
	for _, name := range []string{"state", "in_flight", "last_used"} {
		if strings.Contains(w.Body.String(), name) {
			t.Errorf("a handler reached without withAuth served %q: %s", name, w.Body.String())
		}
	}
}

// A pinned model is one the operator has protected from eviction, and a client
// choosing where to send its work wants that as much as it wants residency:
// a pinned model is the one that will still be warm on the next turn. The
// field follows residency's rule exactly, because it discloses the same thing
// — what the operator of this Mac cares about — and it is true for a pinned
// model the pool is not holding, which is the case a residency record cannot
// describe at all.
func TestModelsListReportsPinnedOnlyOnAKeyedInstall(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	added := time.Unix(1757145600, 0)
	models := &stubModels{models: []registry.Model{
		{RepoID: "org/warm", State: registry.StateReady, AddedAt: added},
		{RepoID: "org/cold", State: registry.StateReady, AddedAt: added},
		{RepoID: "org/plain", State: registry.StateReady, AddedAt: added},
	}}
	// org/warm is pinned and loaded; org/cold is pinned and not loaded, under a
	// spelling the registry does not use; org/plain is neither.
	pool := &stubPool{
		srv:      fake,
		pinned:   []string{"org/warm", "ORG/Cold"},
		resident: []runtime.Resident{{RepoID: "org/warm", State: runtime.ResidencyLoaded}},
	}

	keyed := config.Default()
	keyed.APIKey = "bh_secret"
	entries, _ := listModelsEntries(t,
		New(Options{Config: keyed, Pool: pool, Models: models}).Handler(), "bh_secret")
	for id, want := range map[string]bool{"org/warm": true, "org/cold": true, "org/plain": false} {
		if got := entryByID(t, entries, id)["pinned"]; got != want {
			t.Errorf("%s: pinned = %v, want %v", id, got, want)
		}
	}

	// Unkeyed: the listing is the OpenAI fields and the context figure, and
	// says nothing about what this Mac is protecting.
	open, _ := listModelsEntries(t,
		New(Options{Config: config.Default(), Pool: pool, Models: models}).Handler(), "")
	for _, entry := range open {
		if _, ok := entry["pinned"]; ok {
			t.Errorf("an unkeyed listing carries pinned: %v", entry)
		}
	}
}

// The refusal a network client gets when the memory is all spoken for must not
// say which models the operator protected. This runs the whole path: a real
// pool with a real pinned model in memory, and the gateway's own 503 body.
func TestRefusalToANetworkClientNamesNoPinnedModel(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Open(paths.State)
	if err != nil {
		t.Fatal(err)
	}
	// 200 bytes is charged 240, so a 250-byte budget holds exactly one of them.
	for _, id := range []string{"org/protected", "org/wanted"} {
		if err := reg.Put(registry.Model{
			RepoID: id, Path: paths.ModelDir(id), Bytes: 200,
			State: registry.StateReady, Progress: 100,
		}); err != nil {
			t.Fatal(err)
		}
	}

	l := &recordingLauncher{specs: map[string]runtime.Spec{}}
	pool := runtime.NewPool(runtime.PoolOptions{
		Launcher:         l,
		Models:           registrySource{reg},
		MaxResidentBytes: 250,
		Pinned:           []string{"org/protected"},
		ReadyTimeout:     10 * time.Second,
		HTTP:             &http.Client{Timeout: 5 * time.Second},
	})
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, release, err := pool.Acquire(ctx, "org/protected")
	if err != nil {
		t.Fatalf("Acquire(org/protected): %v", err)
	}
	release() // idle, so only the pin protects it

	cfg := config.Default()
	g := New(Options{Config: cfg, Pool: pool, Models: reg})
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"org/wanted","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "org/protected") {
		t.Errorf("the 503 body names the protected model: %s", body)
	}
	if !strings.Contains(string(body), "memory") {
		t.Errorf("the 503 body does not explain the memory pressure: %s", body)
	}
	if got := len(pool.Resident()); got != 1 {
		t.Errorf("pool holds %d models after the refusal, want the pinned one still there", got)
	}
}

// The prefill bound must not shorten anything that works today, must grow with
// the prompt, and must honour an operator override exactly.
func TestPrefillBudget(t *testing.T) {
	const base = 10 * time.Minute
	for _, tc := range []struct {
		name      string
		bodyBytes int
		override  int
		want      time.Duration
		atLeast   time.Duration
	}{
		{name: "empty request keeps the base", want: base},
		{name: "small prompt keeps the base", bodyBytes: 40_000, want: base},
		{name: "80K tokens still keeps the base", bodyBytes: 80_000 * 4, want: base},
		{name: "256K tokens gets far longer", bodyBytes: 256_000 * 4, atLeast: 28 * time.Minute},
		{name: "override wins over the base", bodyBytes: 0, override: 30, want: 30 * time.Second},
		{name: "override wins over a huge prompt", bodyBytes: 256_000 * 4, override: 60, want: time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := prefillBudget(tc.bodyBytes, tc.override)
			if tc.want != 0 && got != tc.want {
				t.Errorf("prefillBudget(%d, %d) = %s, want %s", tc.bodyBytes, tc.override, got, tc.want)
			}
			if tc.atLeast != 0 && got < tc.atLeast {
				t.Errorf("prefillBudget(%d, %d) = %s, want at least %s", tc.bodyBytes, tc.override, got, tc.atLeast)
			}
			if tc.override == 0 && got < base {
				t.Errorf("derived bound %s is shorter than the base %s", got, base)
			}
		})
	}
}

// A model server that never emits a newline must not make the streamed relay
// buffer without limit. The request side is capped at maxRequestBody and the
// buffered JSON response at maxResponseBody; the streamed side is capped at
// maxStreamLine, and the relay ends the answer rather than growing one line
// forever.
func TestStreamedLineIsCapped(t *testing.T) {
	// Bounded a little past the cap rather than endless, so a build without
	// the cap fails this test instead of allocating until the machine gives
	// up — the same shape as TestNonStreamingResponseBodyIsCapped above.
	src := io.LimitReader(fillReader{}, maxStreamLine+1024)
	rec := httptest.NewRecorder()

	out := streamRewriteSSE(rec, src, "backend-path", "requested-name", relayOptions{})

	if got := rec.Body.Len(); got != 0 {
		t.Errorf("relayed %d bytes of an unterminated line, want none: half an event is not an event", got)
	}
	if !out.upstreamCut {
		t.Error("an answer ended by the line cap was not reported as cut short")
	}
	if !out.oversizeLine {
		t.Error("the relay did not report the oversized line, so nothing could be logged about it")
	}
}
