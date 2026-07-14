// Package gateway serves the OpenAI-compatible API on the LAN and the control
// API for the app's own UI.
package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
	Pool   Pool
	Models Models
	Log    *slog.Logger
	// Transport is the HTTP transport used to reach model servers.
	Transport http.RoundTripper
}

// Gateway routes OpenAI requests to model servers.
type Gateway struct {
	cfg    config.Config
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
		cfg:    opts.Config,
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
		if g.cfg.APIKey == "" || isLoopback(r.RemoteAddr) {
			next.ServeHTTP(w, r)
			return
		}
		token := bearerToken(r.Header.Get("Authorization"))
		// Constant-time compare: a byte-wise early return would leak the key.
		if subtle.ConstantTimeCompare([]byte(token), []byte(g.cfg.APIKey)) != 1 {
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

// handleCompletions proxies a chat/text completion to the right model server.
func (g *Gateway) handleCompletions(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		return
	}

	requested, _ := payload["model"].(string)
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
	payload["model"] = up.ModelArg
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

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	streamCopy(w, resp.Body)
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
