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
// OpenAI client for no security gain. But a page the user's browser visits also
// connects from loopback, so a bare RemoteAddr check alone would let a
// no-preflight cross-origin POST (a CORS-safelisted Content-Type needs no
// Authorization header, so the browser sends it with no preflight at all) ride
// this exemption straight through — even with a key configured and even with
// Host locked to loopback, since the attack never touches either. When a key is
// configured, a loopback request that does carry an Origin must therefore name a
// loopback one before the exemption applies, the same guard the control plane's
// loopbackOnly uses against the identical CSRF class. A DNS-rebound page is the
// other half of that class: its GET is same-origin from the browser's view, so
// it carries no Origin at all — only a Host naming the attacker's domain. With
// a key configured, the exemption therefore also requires a loopback Host. A
// foreign Host from loopback is not refused outright but falls through to the
// bearer check, so a same-machine proxy or tunnel that preserves the client's
// Host keeps working by sending the key it already holds. An unconfigured key
// (no key, iss-1) leaves loopback exactly as open as it always was: that server
// is already unauthenticated by the user's own explicit choice, key-bypass CSRF
// included, so there is nothing here for either check to protect.
func (g *Gateway) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// One read of the live configuration per request. Everything below,
		// and every handler beyond it, decides on this one reading.
		apiKey := g.cfg().APIKey
		r = withAdmittedKeyed(r, apiKey != "")
		if apiKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		if isLoopback(r.RemoteAddr) {
			if origin := r.Header.Get("Origin"); origin != "" && !isLoopbackOrigin(origin) {
				writeError(w, http.StatusForbidden, "cross-origin request refused")
				return
			}
			if isLoopbackHost(r.Host) {
				next.ServeHTTP(w, r)
				return
			}
			// Loopback connection with a foreign Host: require the key below.
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
	// Residency is reported only on an install that has a key configured. The
	// three-state value is not itself a secret — an unauthenticated client can
	// already learn it by timing a one-token completion, and in doing so it
	// changes residency, which reporting never does — but the in-flight count
	// and the last-used time say who is busy and when, and an open server
	// discloses neither. The condition is the install's, not the request's: a
	// loopback client exempt from the bearer check on a keyed install sees the
	// same picture the control panel already shows it. A nil map means no key
	// and no projection, which an empty one would not.
	//
	// This is withAuth's own admission bit, not a second authorization path:
	// withAuth still decides who may call the listing at all. Reading the key
	// again here would be a second reading of a live value, and a request
	// admitted while no key was configured could then be served as if one had
	// been.
	var residency map[string]runtime.Resident
	if g.admittedKeyed(r) {
		residency = make(map[string]runtime.Resident)
		for _, res := range g.pool.Resident() {
			// Folded on both sides of the join. The registry reports a model's
			// canonical spelling and the pool reports whatever string reached
			// Acquire, and they are not always the same one; joining on the raw
			// strings would report a warm model as cold, which is the swap this
			// listing exists to prevent. Folding cannot mis-attribute: the
			// registry allows at most one model per folded id.
			residency[strings.ToLower(res.RepoID)] = res
		}
	}

	data := make([]any, 0, len(ready))
	for _, m := range ready {
		entry := map[string]any{
			"id":       m.RepoID,
			"object":   "model",
			"created":  m.AddedAt.Unix(),
			"owned_by": "gropius",
		}
		// Gropius's extensions to the OpenAI shape are top-level fields with
		// names already common elsewhere. The context length is given under
		// both spellings on purpose: context_length is what OpenRouter- and
		// Ollama-style listings publish, max_model_len what vLLM-derived
		// clients read. It is the model's architectural maximum, not the
		// window this Mac can hold at once, and nothing here enforces it.
		//
		// An unknown figure is absent rather than 0, which a client that
		// trims its history would read as "no context". The upper bound is
		// applied on every path into the registry, and again here because
		// this is where the figure leaves the machine.
		if m.ContextLength > 0 && m.ContextLength <= registry.MaxContextLength {
			entry["context_length"] = m.ContextLength
			entry["max_model_len"] = m.ContextLength
		}
		if residency != nil {
			addResidency(entry, residency[strings.ToLower(m.RepoID)])
		}
		data = append(data, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// addResidency writes the residency fields onto one models-list entry, from
// the pool's record for that model.
//
// A model the pool is not holding is passed the zero Resident, and every field
// below then reports it correctly without a special case: an empty state is not
// one of the two the pool defines and so projects as not_loaded, nothing is in
// flight, and there is no last-used time to report.
//
// It is deliberately an allow-list of named fields rather than a marshalling of
// runtime.Resident: that struct carries the model server's loopback port and
// its on-disk size, and a field added to it later must not reach the LAN
// because nobody remembered to exclude it. Keeping the backend's internals off
// the wire is the same rule relayRewritingModel exists for.
//
// The values are a snapshot taken while the list is built. Nothing here holds a
// model warm on the client's behalf: by the time the client reads them another
// client's request may have evicted the model.
func addResidency(entry map[string]any, res runtime.Resident) {
	// The value is allow-listed too, not only the field names. Pool is an
	// interface, so the string in Resident.State is not this package's to
	// trust; anything but the two the pool defines means the listing cannot
	// say the model is warm, which is what not_loaded says.
	state := runtime.ResidencyNotLoaded
	switch res.State {
	case runtime.ResidencyLoaded, runtime.ResidencyLoading:
		state = res.State
	}
	entry["state"] = string(state)
	// Zero for a model that is not loaded, which is the true count.
	entry["in_flight"] = res.InFlight
	// The last-used time lives on the pool's entry for the model, so it is
	// there exactly while the pool is holding the model — loading or loaded —
	// and goes when the entry does: eviction, unload, the idle reaper, a crash.
	// Absent rather than zero, which a client would read as 1970 rather than as
	// "unknown". state, not this, is what says whether the model is warm.
	if !res.LastUsed.IsZero() {
		entry["last_used"] = res.LastUsed.Unix()
	}
}

// maxRequestBody caps the size of a completion request. Prompts are text; a
// 32 MiB body is already far beyond any real context window and refusing larger
// ones keeps a hostile or buggy client from exhausting memory.
const maxRequestBody = 32 << 20

// maxResponseBody caps how much of a non-streaming completion response the
// gateway buffers before rewriting and relaying it. max_tokens and logprobs
// are client-controlled and unbounded, so a well-behaved upstream can still
// be driven to a very large single-object response; without a cap the read
// at relayRewritingModel's json branch grows memory without bound, the same
// hazard maxRequestBody exists to prevent on the request side.
const maxResponseBody = 64 << 20

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
		var launchErr *runtime.LaunchError
		if errors.As(err, &launchErr) {
			// The wrapped error can carry absolute local filesystem paths (log
			// file locations, the venv interpreter path, os.PathError from the
			// child process) rooted under the serving account's home directory.
			// Log it server-side; the network response stays generic.
			g.log.Error("model launch failed", "model", model, "err", err)
			writeError(w, http.StatusServiceUnavailable, "the model could not be started")
			return
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

	// The only rewrite of prompt content Gropius performs, and only for a model
	// the operator switched it on for: fold the request's system messages into
	// one leading message, which is the shape a chat template that refuses a
	// system message anywhere but the front will accept
	// (adr-2609061610102325). It happens here, on the body already in hand, so
	// a streamed request takes exactly this path too and the body limit above
	// is the only one there is. Nothing read is logged, kept or counted.
	if r.URL.Path == chatCompletionsPath && g.cfg().PerModel[model].MergeSystemMessages {
		if mergeSystemMessagesInto(payload) == mergeRefused {
			// The operator switched merging on for this model and is not
			// getting it, which is worth saying once, here, rather than
			// leaving them to guess from a template error. Only a refusal is
			// logged: a conversation that was already in order is the ordinary
			// case and needs no line at all. The line names the model and
			// nothing else — the request's content is not the log's business
			// here or anywhere.
			g.log.Debug("relayed a request unmerged: its messages carry something merging cannot rebuild faithfully", "model", model)
		}
	}

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
		// Non-streaming completions are a single JSON object; buffering it is
		// fine as long as it stays within maxResponseBody, and the
		// Content-Length header is already dropped as hop-by-hop, so a length
		// change is invisible to framing. The status line and headers are
		// already written by this point (see the caller), so an oversized
		// body cannot be turned into an error response — reading is simply
		// cut short, same as any other mid-read failure below. A mid-read
		// transport failure or a cap hit still leaves whatever bytes were
		// read in body — write them (rewritten if they happen to parse)
		// rather than dropping them, matching streamCopy's
		// write-then-check-error behavior below.
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
		tooLarge := len(body) > maxResponseBody
		if tooLarge {
			body = body[:maxResponseBody]
		}
		_, _ = w.Write(rewriteModelField(body, modelArg, requested))
		if err != nil || tooLarge {
			return
		}
	default:
		streamCopy(w, resp.Body)
	}
}

// rewriteModelField returns b with a top-level "model" field equal to modelArg
// replaced by requested. modelArg is the backend's absolute --model path,
// which in a per-user install contains the account's home directory and must
// never reach a client; that invariant has to hold even when b cannot be
// parsed as the expected shape (a truncated body — see maxResponseBody in the
// caller — or one cut short by a genuine mid-read transport failure), so
// every fallback below redacts a literal modelArg match instead of returning
// b untouched. It is a no-op on any body that does not actually contain
// modelArg, which is the common case for a real error body.
func rewriteModelField(b []byte, modelArg, requested string) []byte {
	redact := func() []byte { return bytes.ReplaceAll(b, []byte(modelArg), []byte(requested)) }

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(b, &payload); err != nil {
		return redact()
	}
	raw, ok := payload["model"]
	if !ok {
		return redact()
	}
	var m string
	if err := json.Unmarshal(raw, &m); err != nil || m != modelArg {
		return redact()
	}
	rewritten, err := json.Marshal(requested)
	if err != nil {
		return redact()
	}
	payload["model"] = rewritten
	out, err := json.Marshal(payload)
	if err != nil {
		return redact()
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
