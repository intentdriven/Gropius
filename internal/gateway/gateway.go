// Package gateway serves the OpenAI-compatible API on the LAN and the control
// API for the app's own UI.
package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// Pool is the subset of runtime.Pool the gateway needs.
type Pool interface {
	Acquire(ctx context.Context, repoID string) (*runtime.Upstream, func(), error)
	Resident() []runtime.Resident
	Unload(repoID string) error
}

// Models is the subset of the registry the gateway needs.
type Models interface {
	List() []registry.Model
	Ready() []registry.Model
	Get(repoID string) (registry.Model, error)
}

// Options configures a Gateway.
type Options struct {
	Config config.Config
	// ConfigFunc, when set, supplies the live config on every request and takes
	// precedence over Config. The gateway reads the API key through it, so a key
	// set at runtime through the control panel takes effect immediately rather
	// than only after a restart. Without it the gateway would keep serving the
	// LAN unauthenticated while the UI reports the endpoint as protected.
	ConfigFunc func() config.Config
	Pool       Pool
	Models     Models
	Log        *slog.Logger
	// Transport is the HTTP transport used to reach model servers.
	Transport http.RoundTripper
}

// Gateway routes OpenAI requests to model servers.
type Gateway struct {
	cfg    func() config.Config
	pool   Pool
	models Models
	log    *slog.Logger
	tr     http.RoundTripper
}

// New builds a Gateway.
func New(opts Options) *Gateway {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	cfgFn := opts.ConfigFunc
	if cfgFn == nil {
		frozen := opts.Config
		cfgFn = func() config.Config { return frozen }
	}
	if opts.Transport == nil {
		// Model servers are on loopback and a long generation can legitimately
		// run for minutes, so there is no response timeout here. The client's
		// context governs the request's lifetime instead.
		opts.Transport = &http.Transport{
			MaxIdleConnsPerHost: 32,
			// A model that is generating slowly is not a stalled connection.
			ResponseHeaderTimeout: 10 * time.Minute,
		}
	}
	return &Gateway{
		cfg:    cfgFn,
		pool:   opts.Pool,
		models: opts.Models,
		log:    opts.Log,
		tr:     opts.Transport,
	}
}

// Handler returns the OpenAI-compatible routes.
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", g.handleListModels)
	mux.HandleFunc("POST /v1/chat/completions", g.handleCompletions)
	mux.HandleFunc("POST /v1/completions", g.handleCompletions)
	mux.HandleFunc("GET /health", g.handleHealth)
	return g.withAuth(mux)
}

// withAuth enforces the bearer token when one is configured.
//
// Requests from loopback are exempt: they come from this machine, including from
// other macOS user accounts, and requiring a key there would break every local
// OpenAI client for no security gain.
func (g *Gateway) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := g.cfg().APIKey
		if apiKey == "" || isLoopback(r.RemoteAddr) {
			next.ServeHTTP(w, r)
			return
		}
		token := bearerToken(r.Header.Get("Authorization"))
		// Constant-time compare: a byte-wise early return would leak the key.
		if subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) != 1 {
			writeError(w, http.StatusUnauthorized,
				"invalid or missing API key — send it as 'Authorization: Bearer <key>'")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return header[len(prefix):]
	}
	return ""
}

// isLoopback reports whether a RemoteAddr is on this machine.
func isLoopback(remoteAddr string) bool {
	host := remoteAddr
	if i := strings.LastIndex(remoteAddr, ":"); i > 0 {
		host = remoteAddr[:i]
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

func (g *Gateway) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"models": len(g.models.Ready()),
	})
}

// handleListModels reports the models Gropius can serve.
//
// It deliberately does not proxy to mlx_lm.server's own /v1/models, which
// enumerates the HuggingFace cache directory rather than the loaded model (and
// throws CacheNotFound when that directory is absent).
func (g *Gateway) handleListModels(w http.ResponseWriter, r *http.Request) {
	ready := g.models.Ready()
	data := make([]any, 0, len(ready))
	for _, m := range ready {
		data = append(data, map[string]any{
			"id":       m.RepoID,
			"object":   "model",
			"created":  m.AddedAt.Unix(),
			"owned_by": "gropius",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// maxRequestBody caps the size of a completion request. Prompts are text; a
// 32 MiB body is already far beyond any real context window and refusing larger
// ones keeps a hostile or buggy client from exhausting memory.
const maxRequestBody = 32 << 20

// bodyReadTimeout bounds how long a client may take to send its request body.
// The server has no WriteTimeout (a generation legitimately streams for minutes),
// which would otherwise leave a slow-uploading client holding a connection and a
// goroutine open indefinitely — a slowloris on the body. The deadline covers only
// the read phase; it is cleared before the model request so generation is unbounded.
const bodyReadTimeout = 30 * time.Second

// handleCompletions proxies a chat/text completion to the right model server.
func (g *Gateway) handleCompletions(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(bodyReadTimeout))

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		case errors.Is(err, os.ErrDeadlineExceeded):
			writeError(w, http.StatusRequestTimeout, "timed out reading the request body")
		default:
			// Malformed framing (bad chunked encoding), an aborted upload, and
			// the like are client errors, not timeouts.
			writeError(w, http.StatusBadRequest, "failed to read the request body")
		}
		return
	}
	// Body is in hand; the multi-minute generation phase must not be bounded.
	_ = rc.SetReadDeadline(time.Time{})

	// Decode into raw messages, not a fully-materialized map: the gateway only
	// rewrites the "model" field, so parsing the entire prompt (the messages array
	// can be hundreds of KB) into Go values and re-serializing it is wasted CPU and
	// garbage on the request's critical path. RawMessage keeps every other field as
	// the original bytes, copied through once.
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		return
	}

	var requested string
	if rawModel, ok := payload["model"]; ok {
		_ = json.Unmarshal(rawModel, &requested)
	}
	if requested == "" {
		writeError(w, http.StatusBadRequest, `the "model" field is required`)
		return
	}

	model, err := g.resolveModel(requested)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	up, release, err := g.pool.Acquire(r.Context(), model)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return // the client hung up while the model was loading
		}
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	defer release()

	// The load-bearing rewrite. mlx-lm reads "model" as an instruction to *load*
	// that model: anything other than the exact --model value it was started with
	// makes it try to download a repo of that name from HuggingFace, which fails
	// with a 404 when offline.
	rewritten, err := json.Marshal(up.ModelArg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not re-encode the request")
		return
	}
	payload["model"] = rewritten
	body, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not re-encode the request")
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		up.BaseURL+r.URL.Path, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(body))
	if accept := r.Header.Get("Accept"); accept != "" {
		req.Header.Set("Accept", accept)
	}
	// The client's bearer token is ours to check, not the model server's to see.

	resp, err := g.tr.RoundTrip(req)
	if err != nil {
		if r.Context().Err() != nil {
			return // client cancelled
		}
		g.log.Error("upstream request failed", "model", model, "err", err)
		writeError(w, http.StatusBadGateway, "the model server did not respond")
		return
	}
	defer resp.Body.Close()

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	relayRewritingModel(w, resp, up.ModelArg, requested)
}

// relayRewritingModel forwards the upstream response body, mapping the
// backend's "model" value — the absolute --model path the request rewrite put
// there — back to the name the client asked for. mlx-lm echoes the request's
// model field into every response and SSE chunk, and the path is a
// backend-internal load instruction that, in a per-user install, contains the
// account's home directory; it must not reach network clients.
func relayRewritingModel(w http.ResponseWriter, resp *http.Response, modelArg, requested string) {
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "text/event-stream"):
		streamRewriteSSE(w, resp.Body, modelArg, requested)
	case strings.HasPrefix(ct, "application/json"):
		// Non-streaming completions are a single bounded JSON object; buffering
		// it is fine, and the Content-Length header is already dropped as
		// hop-by-hop, so the length change is invisible to framing.
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return // relay what was written; the connection is already committed
		}
		_, _ = w.Write(rewriteModelField(body, modelArg, requested))
	default:
		streamCopy(w, resp.Body)
	}
}

// rewriteModelField returns b with a top-level "model" field equal to modelArg
// replaced by requested. Anything that does not parse, or a model value other
// than modelArg, passes through untouched — error bodies stay verbatim.
func rewriteModelField(b []byte, modelArg, requested string) []byte {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(b, &payload); err != nil {
		return b
	}
	raw, ok := payload["model"]
	if !ok {
		return b
	}
	var m string
	if err := json.Unmarshal(raw, &m); err != nil || m != modelArg {
		return b
	}
	rewritten, err := json.Marshal(requested)
	if err != nil {
		return b
	}
	payload["model"] = rewritten
	out, err := json.Marshal(payload)
	if err != nil {
		return b
	}
	return out
}

// streamRewriteSSE relays an SSE body line by line, rewriting the "model"
// field inside each "data: {...}" event and flushing per line so tokens keep
// streaming. Non-JSON events (notably "data: [DONE]") and non-data lines pass
// through byte-for-byte. Chunk boundaries do not align with event boundaries,
// so a plain streamCopy could not rewrite safely; lines are the unit mlx-lm
// actually emits.
func streamRewriteSSE(w http.ResponseWriter, src io.Reader, modelArg, requested string) {
	rc := http.NewResponseController(w)
	br := bufio.NewReader(src)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if payload, ok := bytes.CutPrefix(bytes.TrimSuffix(line, []byte("\n")), []byte("data: ")); ok {
				out := rewriteModelField(payload, modelArg, requested)
				if _, werr := fmt.Fprintf(w, "data: %s\n", out); werr != nil {
					return // client went away
				}
			} else if _, werr := w.Write(line); werr != nil {
				return
			}
			// A flush error means the connection does not support flushing; the
			// data is still written, so keep going rather than truncating.
			_ = rc.Flush()
		}
		if err != nil {
			return
		}
	}
}

// hopByHopHeaders are connection-scoped headers that belong to a single
// transport hop and must not be forwarded to the client (RFC 7230 §6.1). The
// gateway re-frames the streamed body itself, so forwarding the upstream's
// Content-Length or Transfer-Encoding would risk a response with conflicting or
// duplicated framing.
var hopByHopHeaders = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
	"content-length":      true,
}

// copyResponseHeaders forwards the upstream headers to the client, dropping the
// hop-by-hop ones so Go's own response framing stays authoritative.
func copyResponseHeaders(dst, src http.Header) {
	for k, vs := range src {
		if hopByHopHeaders[strings.ToLower(k)] {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// resolveModel maps a client's model name onto a downloaded model.
//
// Clients may use the full repo id ("mlx-community/Qwen3-8B-4bit") or the short
// name ("Qwen3-8B-4bit"); many OpenAI-compatible UIs show only the latter.
func (g *Gateway) resolveModel(requested string) (string, error) {
	if m, err := g.models.Get(requested); err == nil {
		if !m.Ready() {
			return "", fmt.Errorf("model %q is not ready (%s)", requested, m.State)
		}
		return m.RepoID, nil
	}

	var matches []string
	for _, m := range g.models.Ready() {
		if strings.EqualFold(m.Name(), requested) {
			matches = append(matches, m.RepoID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("model %q is not available — download it first", requested)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("model %q is ambiguous: %s", requested, strings.Join(matches, ", "))
	}
}

// streamCopy relays the upstream body, flushing each chunk so SSE tokens reach
// the client as they are generated rather than in one lump at the end.
func streamCopy(w http.ResponseWriter, src io.Reader) {
	rc := http.NewResponseController(w)
	buf := make([]byte, 8<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // client went away
			}
			// A flush error means the connection does not support flushing; the
			// data is still written, so keep going rather than truncating.
			_ = rc.Flush()
		}
		if err != nil {
			return
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError renders an OpenAI-shaped error, which is what clients parse.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    "invalid_request_error",
			"code":    status,
		},
	})
}
