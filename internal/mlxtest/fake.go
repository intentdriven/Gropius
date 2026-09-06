// Package mlxtest provides a fake mlx_lm.server for tests.
//
// It is deliberately faithful to the real server's quirks, because those quirks
// are what the rest of Gropius has to work around. Verified against
// mlx-lm 0.31.3 on 2026-07-14:
//
//   - The request's "model" field is a *load instruction*, not a label. If it
//     does not match the value the server was started with, the real server tries
//     to fetch that repo from HuggingFace and (offline) fails with HTTP 404. The
//     gateway must rewrite the field; this fake fails the same way if it doesn't.
//   - /health returns {"status":"ok"} immediately, before the weights load.
//     Readiness therefore cannot be inferred from /health alone.
//   - Streaming responses are SSE "data: {...}" lines ending with "data: [DONE]".
//   - Token counts only reach a streaming client when the request carries
//     "stream_options": {"include_usage": true}. The server then emits one
//     extra event before "[DONE]" whose "choices" array is empty and which
//     carries the usage object.
//   - A "stream_options" object that does not carry the "include_usage" key
//     raises inside the real server, which answers 500. A gateway that merges
//     into a client's own stream_options must therefore always write the key
//     rather than assume it is there.
package mlxtest

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"
)

// Server is a fake mlx_lm.server.
type Server struct {
	// ModelArg is the value the server was "started" with (--model). Requests
	// naming anything else are rejected, exactly like the real thing.
	ModelArg string
	// Reply is the assistant text returned for a completion.
	Reply string
	// FirstTokenDelay holds the first streamed chunk back; see Options.
	FirstTokenDelay time.Duration
	// ChunkDelay paces the chunks after the first; see Options.
	ChunkDelay time.Duration
	// RolePreamble emits a role-only first chunk; see Options.
	RolePreamble bool

	httpSrv   *httptest.Server
	readyAt   time.Time
	completed atomic.Int64

	mu       sync.Mutex
	lastBody map[string]any
	lastAuth string
}

// Options configures a fake server.
type Options struct {
	ModelArg string
	// LoadDelay simulates weight loading. Completions fail until it elapses,
	// while /health returns ok throughout — as the real server does.
	LoadDelay time.Duration
	Reply     string
	// Port binds the fake to a specific loopback port instead of an arbitrary
	// one. A pool hands each model server the port it allocated and then
	// addresses it there, so a fake standing in for a launched process has to
	// answer on that port rather than one of its own.
	Port int
	// FirstTokenDelay holds the first streamed chunk back, standing in for the
	// prefill a real model does before it can emit anything. Without it the
	// whole answer arrives inside a millisecond and a time-to-first-token
	// measurement has nothing to measure.
	FirstTokenDelay time.Duration
	// ChunkDelay paces the chunks after the first, standing in for generation.
	// Without it a whole answer is written before a client could act on any of
	// it, so a test about what happens mid-stream has no mid-stream.
	ChunkDelay time.Duration
	// RolePreamble emits a first chunk carrying only the assistant's role and
	// no text, which is what OpenAI's own streaming API does and what several
	// compatible servers do. Whether the pinned mlx-lm does has not been
	// established here, so a test that cares which chunk a measurement lands
	// on turns this on and says which behaviour it is describing.
	RolePreamble bool
}

// Start launches a fake server. It is closed automatically via t.Cleanup by the
// caller, or explicitly with Close.
func Start(opts Options) *Server {
	s := &Server{
		ModelArg:        opts.ModelArg,
		Reply:           opts.Reply,
		FirstTokenDelay: opts.FirstTokenDelay,
		ChunkDelay:      opts.ChunkDelay,
		RolePreamble:    opts.RolePreamble,
		readyAt:         time.Now().Add(opts.LoadDelay),
	}
	if s.Reply == "" {
		s.Reply = "GROPIUS OK"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/chat/completions", s.handleChat)
	mux.HandleFunc("/v1/completions", s.handleChat)

	if opts.Port == 0 {
		s.httpSrv = httptest.NewServer(mux)
		return s
	}
	// Bind the port the caller was given. The real launcher has the same race
	// between a port being found free and the child binding it, and the same
	// consequence: a failed readiness probe.
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", opts.Port))
	if err != nil {
		panic(fmt.Sprintf("mlxtest: cannot bind port %d: %v", opts.Port, err))
	}
	s.httpSrv = httptest.NewUnstartedServer(mux)
	s.httpSrv.Listener.Close()
	s.httpSrv.Listener = l
	s.httpSrv.Start()
	return s
}

// URL is the base URL of the fake server, e.g. "http://127.0.0.1:54321".
func (s *Server) URL() string { return s.httpSrv.URL }

// Close shuts the server down.
func (s *Server) Close() { s.httpSrv.Close() }

// Completions returns how many completion requests were served.
func (s *Server) Completions() int64 { return s.completed.Load() }

// LastModelField returns the "model" value of the most recent request. The
// gateway is required to rewrite this to the server's ModelArg.
func (s *Server) LastModelField() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastBody == nil {
		return ""
	}
	m, _ := s.lastBody["model"].(string)
	return m
}

// LastAuthHeader returns the Authorization header of the most recent request.
// The upstream must never see the client's bearer token.
func (s *Server) LastAuthHeader() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastAuth
}

// LastBody returns the most recent decoded request body.
func (s *Server) LastBody() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastBody
}

func (s *Server) ready() bool { return !time.Now().Before(s.readyAt) }

// handleHealth answers ok even while the model is still loading — this is the
// real behavior, and the reason a readiness probe must do a real completion.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status": "ok"}`)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   []any{map[string]any{"id": s.ModelArg, "object": "model"}},
	})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.lastBody = body
	s.lastAuth = r.Header.Get("Authorization")
	s.mu.Unlock()

	// The defining quirk: an unknown "model" makes the real server attempt a
	// HuggingFace download, which fails with 404 when offline.
	if m, _ := body["model"].(string); m != s.ModelArg {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error": "Cannot find an appropriate cached snapshot folder for the specified revision on the local disk and outgoing traffic has been disabled."}`)
		return
	}

	if !s.ready() {
		http.Error(w, `{"error":"model is still loading"}`, http.StatusServiceUnavailable)
		return
	}

	s.completed.Add(1)

	if stream, _ := body["stream"].(bool); stream {
		includeUsage, err := streamIncludeUsage(body)
		if err != nil {
			// The real server reads stream_options["include_usage"] directly:
			// an object without the key raises, and the client sees a 500.
			http.Error(w, `{"error":"KeyError: 'include_usage'"}`, http.StatusInternalServerError)
			return
		}
		s.streamReply(w, includeUsage)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":     "chatcmpl-fake",
		"object": "chat.completion",
		"model":  s.ModelArg,
		"choices": []any{map[string]any{
			"index":         0,
			"finish_reason": "stop",
			"message":       map[string]any{"role": "assistant", "content": s.Reply},
		}},
		"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 4, "total_tokens": 7},
	})
}

// streamIncludeUsage reads the request's stream_options the way the real
// server does. It reports an error for an object that does not carry the
// include_usage key, which is the shape that raises upstream; an absent or
// null stream_options is simply "no usage", as it is there.
func streamIncludeUsage(body map[string]any) (bool, error) {
	raw, ok := body["stream_options"]
	if !ok || raw == nil {
		return false, nil
	}
	opts, ok := raw.(map[string]any)
	if !ok {
		return false, fmt.Errorf("stream_options is not an object")
	}
	v, ok := opts["include_usage"]
	if !ok {
		return false, fmt.Errorf("stream_options carries no include_usage")
	}
	b, _ := v.(bool)
	return b, nil
}

// streamReply emits one SSE chunk per word, flushing each so a proxy that
// buffers the body instead of streaming it will be caught by the tests. When
// the request asked for usage, one more event follows the words: an empty
// choices array and the token counts, which is the shape the real server
// emits and the only way a streaming client learns what it spent.
func (s *Server) streamReply(w http.ResponseWriter, includeUsage bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	if s.FirstTokenDelay > 0 {
		time.Sleep(s.FirstTokenDelay)
	}
	if s.RolePreamble {
		b, _ := json.Marshal(map[string]any{
			"id":     "chatcmpl-fake",
			"object": "chat.completion.chunk",
			"model":  s.ModelArg,
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{"role": "assistant"},
			}},
		})
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	for i, word := range splitWords(s.Reply) {
		if i > 0 && s.ChunkDelay > 0 {
			time.Sleep(s.ChunkDelay)
		}
		chunk := map[string]any{
			"id":     "chatcmpl-fake",
			"object": "chat.completion.chunk",
			"model":  s.ModelArg,
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{"role": "assistant", "content": word},
			}},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	if includeUsage {
		b, _ := json.Marshal(map[string]any{
			"id":      "chatcmpl-fake",
			"object":  "chat.completion.chunk",
			"model":   s.ModelArg,
			"choices": []any{},
			"usage": map[string]any{
				"prompt_tokens": 3, "completion_tokens": 4, "total_tokens": 7,
			},
		})
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func splitWords(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		cur += string(r)
		if r == ' ' {
			out = append(out, cur)
			cur = ""
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
