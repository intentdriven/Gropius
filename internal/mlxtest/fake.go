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
}

// Start launches a fake server. It is closed automatically via t.Cleanup by the
// caller, or explicitly with Close.
func Start(opts Options) *Server {
	s := &Server{
		ModelArg: opts.ModelArg,
		Reply:    opts.Reply,
		readyAt:  time.Now().Add(opts.LoadDelay),
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
		s.streamReply(w)
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

// streamReply emits one SSE chunk per word, flushing each so a proxy that
// buffers the body instead of streaming it will be caught by the tests.
func (s *Server) streamReply(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	for _, word := range splitWords(s.Reply) {
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
