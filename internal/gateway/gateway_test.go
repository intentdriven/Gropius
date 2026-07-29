package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
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

	mu       sync.Mutex
	acquired []string
	released int
	resident []runtime.Resident
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
		p.mu.Lock()
		p.released++
		p.mu.Unlock()
	}
	return &runtime.Upstream{
		RepoID:   repoID,
		BaseURL:  p.srv.URL(),
		ModelArg: p.srv.ModelArg,
	}, release, nil
}

func (p *stubPool) Resident() []runtime.Resident { return p.resident }
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

	for _, addr := range []string{"127.0.0.1:5555", "[::1]:5555"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		req.RemoteAddr = addr
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("loopback %s got %d, want 200 (loopback must not need a key)", addr, w.Code)
		}
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
