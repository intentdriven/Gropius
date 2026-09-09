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
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
	"github.com/intentdriven/Gropius/internal/stats"
)

// Pool is the subset of runtime.Pool the gateway needs.
type Pool interface {
	Acquire(ctx context.Context, repoID string) (*runtime.Upstream, func(), error)
	Resident() []runtime.Resident
	Pinned() []string
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
	// Stats, when set, receives one content-free record per request — but only
	// while the operator has the switch on, which the gateway reads live
	// through ConfigFunc like the API key. Nil means nothing is recorded and
	// nothing is asked of a model server on the recorder's behalf.
	Stats *stats.Recorder
}

// Gateway routes OpenAI requests to model servers.
type Gateway struct {
	cfg    func() config.Config
	pool   Pool
	models Models
	log    *slog.Logger
	tr     http.RoundTripper
	stats  *stats.Recorder
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
		// No ResponseHeaderTimeout here: it is a property of the transport, so
		// it would apply one number to every request regardless of prompt size,
		// which is the defect being fixed. The wait for headers is bounded per
		// request instead, in prefillBudget.
		opts.Transport = &http.Transport{
			MaxIdleConnsPerHost: 32,
		}
	}
	return &Gateway{
		cfg:    cfgFn,
		pool:   opts.Pool,
		models: opts.Models,
		log:    opts.Log,
		tr:     opts.Transport,
		stats:  opts.Stats,
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
		// Tag the caller so the pool can share the load-waiter queue out per
		// key rather than first-come. Only a verified key reaches here, so the
		// tag cannot be spoofed by an unauthenticated caller; everyone else
		// falls through untagged and shares one bucket.
		next.ServeHTTP(w, r.WithContext(runtime.WithSource(r.Context(), token)))
	})
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return header[len(prefix):]
	}
	return ""
}

// fromThisMachine reports whether r originated from a client on this machine,
// and is not a page somewhere else driving that client's browser.
//
// This is the one rule, and a bare RemoteAddr check is not it. A page the
// victim visits runs in their browser, which connects from 127.0.0.1, so the
// source address alone waves it through; two guards close what it cannot see.
// A DNS-rebound page points a hostname it controls at 127.0.0.1, so the socket
// is loopback and only the Host header names the attacker. A classic
// cross-site request carries its Origin, and a genuine local client is either
// same-origin on loopback or sends none at all.
//
// loopbackOnly gates the whole control plane on this. withAuth checks the same
// two headers on its bearer-check exemption but cannot fold onto this function,
// and the difference is deliberate rather than drift: a foreign Origin is a 403
// there, while a foreign Host falls through to the bearer check so a
// same-machine proxy that presents the key keeps working. Everything else that
// treats a loopback connection as this machine's own operator answers here, so
// the rule is stated once.
//
// What this does NOT stop, and the condition on that staying harmless: a
// no-cors subresource — <script src>, <img> — that a page anywhere points at
// the loopback URL sends a loopback Host and no Origin at all, so it passes.
// It is not a read primitive today, because the gateway emits no
// Access-Control-Allow-Origin (so the body is opaque to the page) and the JSON
// is a syntax error if parsed as script. The day any CORS header is added to
// these routes, that stops being true and this predicate is no longer enough
// on its own.
func fromThisMachine(r *http.Request) bool {
	if !isLoopback(r.RemoteAddr) {
		return false
	}
	if !isLoopbackHost(r.Host) {
		return false
	}
	origin := r.Header.Get("Origin")
	return origin == "" || isLoopbackOrigin(origin)
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
	// Residency is reported to a client the install has admitted on its key,
	// and to any client on this machine. The three-state value is not itself a
	// secret — an unauthenticated client can already learn it by timing a
	// one-token completion, and in doing so it changes residency, which
	// reporting never does — but the in-flight count and the last-used time say
	// who is busy and when, and an open server discloses neither to the network.
	// A nil map means no projection, which an empty one would not.
	//
	// The two admissions are the two trust classes this server already has, and
	// neither is new here. On a keyed install the condition is the install's,
	// not the request's: a loopback client exempt from the bearer check sees
	// the same picture a keyed LAN client does. On a keyless install the LAN is
	// unauthenticated, so it is told nothing — but a client on this Mac is the
	// class the control panel already shows exactly these facts to, over the
	// same loopback, so withholding them from a program the same person is
	// running on the same machine protects nothing.
	//
	// admittedKeyed is withAuth's own admission bit, not a second authorization
	// path: withAuth still decides who may call the listing at all. Reading the
	// key again here would be a second reading of a live value, and a request
	// admitted while no key was configured could then be served as if one had
	// been.
	//
	// The loopback arm is fromThisMachine, not a bare source-address check.
	// withAuth returns before its own Host and Origin guards when no key is
	// configured, so on a keyless install nothing upstream has looked at either
	// header: a DNS-rebound page would arrive from 127.0.0.1 carrying the
	// attacker's Host and read exactly the activity this handler withholds from
	// the LAN. The guards therefore have to be applied here, and they are the
	// same ones — the same function — the control plane is gated on.
	var residency map[string]runtime.Resident
	var pinned map[string]bool
	if g.admittedKeyed(r) || fromThisMachine(r) {
		// Folded on the same rule as the residency join below. The pinned set
		// is read separately from the residency snapshot because a pin is not
		// a property of a loaded model: a pinned model the pool is not holding
		// is still pinned, and that is exactly the entry a client most wants to
		// tell apart from an ordinary cold one.
		pinned = make(map[string]bool)
		for _, id := range g.pool.Pinned() {
			pinned[config.FoldRepoID(id)] = true
		}
		residency = make(map[string]runtime.Resident)
		for _, res := range g.pool.Resident() {
			// Folded on both sides of the join, through the same rule the
			// registry and the pool key by. The registry reports a model's
			// canonical spelling and the pool reports whatever string reached
			// Acquire, and they are not always the same one; joining on the raw
			// strings would report a warm model as cold, which is the swap this
			// listing exists to prevent. This cannot mis-attribute only because
			// all three key by config.FoldRepoID, so the registry's "at most one
			// model per folded id" is the pool's guarantee and this join's too.
			residency[config.FoldRepoID(res.RepoID)] = res
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
			addResidency(entry, residency[config.FoldRepoID(m.RepoID)], pinned[config.FoldRepoID(m.RepoID)])
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
func addResidency(entry map[string]any, res runtime.Resident, pinned bool) {
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
	// Always present rather than omitted when false: the whole value of the
	// field is telling a pinned model from an unpinned one, and an absent key
	// would be read as an older Gropius that cannot say either way.
	entry["pinned"] = pinned
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

// maxStreamLine caps one line of a streamed answer, which is the unit
// streamRewriteSSE buffers before it can rewrite and relay it. A model server
// that never emits a newline — hung mid-event, or writing something that is
// not SSE at all — would otherwise grow that buffer without limit, the same
// hazard the two caps above exist to prevent, on the third and last body the
// gateway reads.
//
// Set to the whole-answer cap rather than to a figure of its own: one event of
// a streamed answer is a fragment of the answer a non-streamed request returns
// in one object, so a streamed line cannot legitimately be larger than
// maxResponseBody, and anything the two caps share stays a single number to
// change. A real chunk is a few hundred bytes.
const maxStreamLine = maxResponseBody

// bodyReadTimeout bounds how long a client may take to send its request body.
// The server has no WriteTimeout (a generation legitimately streams for minutes),
// which would otherwise leave a slow-uploading client holding a connection and a
// goroutine open indefinitely — a slowloris on the body. The deadline covers only
// the read phase; it is cleared before the model request so generation is unbounded.
const bodyReadTimeout = 30 * time.Second

// handleCompletions proxies a chat/text completion to the right model server.
func (g *Gateway) handleCompletions(w http.ResponseWriter, r *http.Request) {
	// The clock starts before the body is read, so a slow upload counts
	// against the client — which is what every comparable measurement does,
	// and the only definition under which time to first token means what a
	// client thinks it means.
	started := time.Now()
	cfg := g.cfg()
	obs := g.observe(cfg.Statistics, started)
	defer obs.finish(r)

	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(bodyReadTimeout))

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err != nil {
		obs.failed(stats.ClassClientError)
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
		obs.failed(stats.ClassClientError)
		writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		return
	}

	var requested string
	if rawModel, ok := payload["model"]; ok {
		_ = json.Unmarshal(rawModel, &requested)
	}
	if requested == "" {
		obs.failed(stats.ClassClientError)
		writeError(w, http.StatusBadRequest, `the "model" field is required`)
		return
	}

	model, err := g.resolveModel(requested)
	if err != nil {
		// Recorded with no model at all rather than with the name that was
		// asked for: that name is the client's own text, of the client's own
		// length, and the recorder is never handed either.
		obs.failed(stats.ClassClientError)
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	obs.resolved(model)
	obs.streaming(streamRequested(payload))

	up, release, err := g.pool.Acquire(r.Context(), model)
	if err != nil {
		// A refusal that came after a wait is the one a client most needs the
		// figure for: it held the connection open for that long. Set before
		// writeError, which writes the status line immediately.
		var noRoom *runtime.NoRoomError
		if errors.As(err, &noRoom) {
			setWaitHeaders(w.Header(), g.admittedKeyed(r), noRoom.Waited)
			// Recorded as well as reported. A request that held a connection
			// for five minutes and got a 503 is the outcome an operator would
			// go to the statistics to find, and it is exactly the one eviction
			// grace produces when it fails.
			obs.waited(runtime.AcquireStats{QueueWait: noRoom.Waited})
		}
		if errors.Is(err, context.Canceled) {
			obs.failed(stats.ClassCancelled)
			return // the client hung up while the model was loading
		}
		obs.failed(classifyAcquireError(err))
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
	obs.waited(up.Waits)
	// Set here rather than beside WriteHeader below, so that the streamed and
	// the non-streamed path take the same line and so that a later failure on
	// this request still reports the wait it had already paid.
	setWaitHeaders(w.Header(), g.admittedKeyed(r), up.Waits.LoadWait+up.Waits.QueueWait)

	// The load-bearing rewrite. mlx-lm reads "model" as an instruction to *load*
	// that model: anything other than the exact --model value it was started with
	// makes it try to download a repo of that name from HuggingFace, which fails
	// with a 404 when offline.
	rewritten, err := json.Marshal(up.ModelArg)
	if err != nil {
		obs.failed(stats.ClassGatewayError)
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
	if r.URL.Path == chatCompletionsPath && cfg.PerModel[model].MergeSystemMessages {
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

	// A streamed answer carries no token counts unless the request asks for
	// them, so with recording on Gropius asks on the client's behalf and
	// removes the extra event on the way back if the client did not (see
	// mergeIncludeUsage and relayOptions). A non-streamed answer already
	// carries its counts and needs nothing.
	relay := relayOptions{observing: obs.recording()}
	if obs.recording() && streamRequested(payload) {
		relay.dropUsage = !clientWantsUsage(payload)
		mergeIncludeUsage(payload)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		obs.failed(stats.ClassGatewayError)
		writeError(w, http.StatusInternalServerError, "could not re-encode the request")
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		up.BaseURL+r.URL.Path, bytes.NewReader(body))
	if err != nil {
		obs.failed(stats.ClassGatewayError)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(body))
	if accept := r.Header.Get("Accept"); accept != "" {
		req.Header.Set("Accept", accept)
	}
	// The client's bearer token is ours to check, not the model server's to see.

	// Bound the wait for headers — that is, for prefill — rather than the whole
	// exchange: generation legitimately runs for minutes after the first token.
	// The timer is stopped the moment headers arrive, so it never touches the
	// body stream; the context is released when the handler returns.
	budget := prefillBudget(len(body), cfg.UpstreamHeaderTimeoutSec)
	hdrCtx, cancelHdr := context.WithCancel(r.Context())
	defer cancelHdr()
	timedOut := &atomic.Bool{}
	timer := time.AfterFunc(budget, func() { timedOut.Store(true); cancelHdr() })
	req = req.WithContext(hdrCtx)

	resp, err := g.tr.RoundTrip(req)
	timer.Stop()
	if err != nil {
		switch {
		case timedOut.Load():
			// Distinguished from an unreachable server on purpose: the old
			// message blamed the model server for a bound the gateway chose,
			// and a client that retries on it makes things worse — the
			// abandoned request keeps prefilling upstream and its cache stays
			// resident, so the next request runs at less than half speed.
			obs.failed(stats.ClassUnreachable)
			g.log.Error("upstream did not return headers within the prefill budget",
				"model", model, "budget", budget, "request_bytes", len(body))
			writeError(w, http.StatusGatewayTimeout,
				fmt.Sprintf("the model server did not finish reading the prompt within %s; it may still be working on it, and retrying will slow it further. Raise upstream_header_timeout_sec in Settings if this prompt legitimately needs longer.", budget.Round(time.Second)))
			return
		case r.Context().Err() != nil:
			obs.failed(stats.ClassCancelled)
			return // client cancelled
		}
		obs.failed(stats.ClassUnreachable)
		g.log.Error("upstream request failed", "model", model, "err", err)
		writeError(w, http.StatusBadGateway, "the model server did not respond")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		// The model server's own refusal, relayed as it stands. It is a
		// different fact from any of the gateway's own, and counting it as one
		// of those would hide the model server behind the proxy in front of it.
		obs.failed(stats.ClassUpstreamStatus)
	}
	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	out := relayRewritingModel(w, resp, up.ModelArg, requested, relay)
	if out.oversizeLine {
		// Once per answer, and only for the relay's own refusal: the status
		// line has already gone out, so this is the only place the operator
		// can be told why a stream stopped. Nothing of the line is logged —
		// what it carries is the answer being generated.
		g.log.Error("ended a streamed answer: the model server sent a line beyond the relay's limit",
			"model", model, "limit", maxStreamLine)
	}
	obs.relayed(out)
}

// gropiusHeaders are the response headers Gropius writes itself, which an
// upstream may not add to.
var gropiusHeaders = map[string]bool{
	"x-gropius-state":      true,
	"x-gropius-queue-time": true,
}

// setWaitHeaders tells the client what this request spent before its model
// server was asked anything.
//
// Two values and one number. X-Gropius-State is warm or waited;
// X-Gropius-Queue-Time is whole milliseconds, counting the wait for room under
// an eviction grace, the wait for a cold model to load, and the wait for a
// slot on a model already busy. "waited" is exactly "the queue time is not
// zero", so a client never has to reconcile the two, and sub-millisecond
// contention on the pool's own lock — which no client meant by a wait — reads
// as warm.
//
// Written only on an install that has a key configured, which is the models
// list's rule and is here for the models list's reason. These are residency
// facts. "warm" is a server-attested statement that the model was in memory
// with a free slot; "waited" on a model the same client has just seen warm is
// the model's in-flight count at its batch ceiling — other clients are using
// it, right now. That second one is what handleListModels withholds from an
// open server, and unlike bare residency a stopwatch does not give it away: in
// total latency the wait is inseparable from generation time, and this
// separates it to the millisecond on every request, for free. The condition is
// the install's rather than the request's, so a loopback client exempt from
// the bearer check sees what the control panel already shows it.
//
// keyed is withAuth's own admission bit, carried on the request. Reading the
// configured key again here would be a second reading of a live value, and a
// request admitted while no key was configured could then be answered as if
// one had been.
func setWaitHeaders(h http.Header, keyed bool, waited time.Duration) {
	if !keyed {
		return
	}
	ms := waited.Milliseconds()
	state := "warm"
	if ms > 0 {
		state = "waited"
	}
	h.Set("X-Gropius-State", state)
	h.Set("X-Gropius-Queue-Time", strconv.FormatInt(ms, 10))
}

// relayRewritingModel forwards the upstream response body, mapping the
// backend's "model" value — the absolute --model path the request rewrite put
// there — back to the name the client asked for. mlx-lm echoes the request's
// model field into every response and SSE chunk, and the path is a
// backend-internal load instruction that, in a per-user install, contains the
// account's home directory; it must not reach network clients.
func relayRewritingModel(w http.ResponseWriter, resp *http.Response, modelArg, requested string, opts relayOptions) relayOutcome {
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "text/event-stream"):
		return streamRewriteSSE(w, resp.Body, modelArg, requested, opts)
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
		ev, parsed := decodeEvent(body)
		_, _ = w.Write(renderEvent(body, ev, parsed, modelArg, requested))
		if err != nil || tooLarge {
			// The answer the client received is not the answer the model
			// server was going to give, and the 200 has already gone out. A
			// non-streamed answer stops early for exactly the reasons a
			// streamed one does, and is recorded the same way.
			return relayOutcome{upstreamCut: true}
		}
		var out relayOutcome
		if opts.observing && parsed {
			// A non-streamed answer always carries its counts, so nothing was
			// merged into the request to get them. It is parsed once, above.
			out.usage = readUsage(ev)
		}
		return out
	default:
		return streamCopy(w, resp.Body)
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
	payload, ok := decodeEvent(b)
	return renderEvent(b, payload, ok, modelArg, requested)
}

// decodeEvent parses one JSON object — a whole non-streamed answer, or the
// payload of one "data:" event — into its fields, leaving each field's own
// bytes untouched. It is separate from renderEvent below so that a relay which
// is both rewriting and reading an event parses it once rather than twice: the
// gateway is on the critical path of every token.
func decodeEvent(b []byte) (map[string]json.RawMessage, bool) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(b, &payload); err != nil {
		return nil, false
	}
	return payload, true
}

// renderEvent produces the bytes to relay for an event decodeEvent has already
// parsed, falling back to a literal redaction of modelArg for anything that is
// not the expected shape.
func renderEvent(b []byte, payload map[string]json.RawMessage, parsed bool, modelArg, requested string) []byte {
	redact := func() []byte { return bytes.ReplaceAll(b, []byte(modelArg), []byte(requested)) }

	if !parsed {
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

// choicesField is the array of alternatives every completion event carries. A
// usage-only event carries it empty; every event about the generation itself
// carries at least one. Gropius reads whether it is empty and nothing else —
// never what is inside it, which is the answer being generated.
const choicesField = "choices"

// usageField is the model server's own count of what a request cost.
const usageField = "usage"

// isUsageOnly reports whether an event is the one the model server appends
// when a streamed request asked for its token counts: the counts, and nothing
// about the generation.
//
// The shape recognized is exactly the shape asked for: the counts, and a
// choices array that is present and empty. Anything else — no choices field at
// all, a choices field that is not a list, counts riding an event that also
// carries a choice — is relayed as it stands, because removing an event this
// does not understand is the one mistake here that a client would see.
func isUsageOnly(ev map[string]json.RawMessage) bool {
	usage, ok := ev[usageField]
	if !ok || string(usage) == "null" {
		return false
	}
	raw, has := ev[choicesField]
	if !has {
		return false
	}
	choices, ok := decodeChoices(raw)
	return ok && len(choices) == 0
}

// carriesGeneration reports whether an event is about the generation itself,
// which is what "the client has its first chunk" means here. Nothing inside a
// choice is read: what is inside is the answer being generated, and Gropius
// times the answer rather than reading it.
func carriesGeneration(ev map[string]json.RawMessage) bool {
	raw, ok := ev[choicesField]
	if !ok {
		return false
	}
	choices, ok := decodeChoices(raw)
	return ok && len(choices) > 0
}

// decodeChoices parses an event's choices as a list, reporting whether it is
// one at all. Each element's own bytes are left untouched.
func decodeChoices(raw json.RawMessage) ([]json.RawMessage, bool) {
	var choices []json.RawMessage
	if err := json.Unmarshal(raw, &choices); err != nil {
		return nil, false
	}
	return choices, true
}

// readUsage reads the model server's token counts off an event, or returns nil
// when it carries none.
func readUsage(ev map[string]json.RawMessage) *usageCounts {
	raw, ok := ev[usageField]
	if !ok {
		return nil
	}
	var counts struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	}
	if err := json.Unmarshal(raw, &counts); err != nil {
		return nil
	}
	// Clamped, because these are the child process's numbers rather than
	// Gropius's own: a negative count would be summed into the per-model and
	// per-minute totals and drag them below zero, and no count is a truer
	// answer than a wrong one.
	return &usageCounts{
		Prompt:     max(counts.PromptTokens, 0),
		Completion: max(counts.CompletionTokens, 0),
	}
}

// streamRewriteSSE relays an SSE body line by line, rewriting the "model"
// field inside each "data: {...}" event and flushing per line so tokens keep
// streaming. Non-JSON events (notably "data: [DONE]") and non-data lines pass
// through byte-for-byte. Chunk boundaries do not align with event boundaries,
// so a plain streamCopy could not rewrite safely; lines are the unit mlx-lm
// actually emits.
func streamRewriteSSE(w http.ResponseWriter, src io.Reader, modelArg, requested string, opts relayOptions) relayOutcome {
	var out relayOutcome
	rc := http.NewResponseController(w)
	br := bufio.NewReader(src)
	// An SSE event is its data line and the blank line that terminates it. An
	// event that is removed has to take its terminator with it, or the client
	// receives a stray blank line where the event was and the stream it gets is
	// not the stream it would have got.
	dropBlank := false
	for {
		line, err := readBoundedLine(br, maxStreamLine)
		if errors.Is(err, errLineTooLong) {
			// Nothing of this line is relayed and nothing more is read. The
			// answer stops here, which is what upstreamCut says; the caller
			// logs the reason once and its deferred Close on the upstream body
			// ends that connection rather than leaving it to drain.
			out.upstreamCut = true
			out.oversizeLine = true
			return out
		}
		// readBoundedLine returns the bytes it did read alongside the error that
		// stopped it, so a line and the failure that truncated it can arrive
		// together. Every path through the body below therefore falls out to
		// the one error check at the bottom rather than continuing the loop:
		// skipping it on the path that removes an event would lose the very
		// fact this relay exists to report.
		if len(line) > 0 {
			if prefix, payload, ok := cutDataPrefix(line); ok {
				dropBlank = false
				ev, parsed := decodeEvent(payload)
				if parsed && opts.observing {
					// Read from any event that carries counts, not only from
					// the one that is removed: reading and removing are
					// separate decisions, and a server that hangs the counts
					// on a chunk of the answer would otherwise be recorded as
					// a success that cost nothing.
					if u := readUsage(ev); u != nil {
						out.usage = u
					}
					if out.firstToken.IsZero() && carriesGeneration(ev) {
						out.firstToken = time.Now()
					}
				}
				if parsed && opts.dropUsage && isUsageOnly(ev) {
					// Gropius asked for this event, not the client. It is
					// removed here rather than never asked for, because the
					// counts are the whole point of asking.
					dropBlank = true
				} else {
					rewritten := renderEvent(payload, ev, parsed, modelArg, requested)
					if _, werr := fmt.Fprintf(w, "%s%s\n", prefix, rewritten); werr != nil {
						out.clientGone = true
						return out
					}
					_ = rc.Flush()
					// Written and flushed: if that was the terminal event, the
					// client has the whole answer, whatever becomes of the two
					// lines that follow it.
					if isTerminalEvent(payload) {
						out.complete = true
					}
				}
			} else if isBlankLine(line) && dropBlank {
				dropBlank = false
			} else {
				// Anything else relayed ends the removed event's reach: only
				// the blank line immediately after it belongs to it, and a
				// later blank belongs to whatever came between.
				dropBlank = false
				if _, werr := w.Write(line); werr != nil {
					out.clientGone = true
					return out
				}
				// A flush error means the connection does not support
				// flushing; the data is still written, so keep going rather
				// than truncating.
				_ = rc.Flush()
			}
		}
		if err != nil {
			out.upstreamCut = !errors.Is(err, io.EOF)
			return out
		}
	}
}

// errLineTooLong reports a streamed line that reached maxStreamLine without a
// newline in it.
var errLineTooLong = errors.New("the model server sent a line beyond the relay's limit")

// readBoundedLine reads one newline-terminated line, buffering no more than
// limit bytes of it.
//
// bufio.Reader.ReadBytes would grow its buffer for as long as the model server
// keeps writing, so a server hung mid-event — or writing something that is not
// SSE at all — is the whole of what this bounds. What was read before the
// limit is discarded rather than returned: it is half an event, and half an
// event is not one; relaying it would hand a client a fragment of JSON as if
// it were a chunk of the answer.
func readBoundedLine(br *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		// ReadSlice returns a view of the reader's own buffer, valid only
		// until the next read, so each fragment is copied out as it is taken.
		frag, err := br.ReadSlice('\n')
		if len(line)+len(frag) > limit {
			return nil, errLineTooLong
		}
		line = append(line, frag...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}

// cutDataPrefix splits an SSE line into its "data:" prefix and the payload
// after it, reporting whether it is a data line at all.
//
// The single space after the colon is conventional, not required, and the
// prefix is returned rather than re-spelled so a server that omits it gets its
// own framing back byte for byte. Matching only the spelling with the space
// would let a data line through unparsed — and therefore unrewritten, carrying
// the model server's absolute path straight to a client.
func cutDataPrefix(line []byte) (prefix, payload []byte, ok bool) {
	body := bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
	rest, ok := bytes.CutPrefix(body, []byte("data:"))
	if !ok {
		return nil, nil, false
	}
	prefix = []byte("data:")
	if after, hadSpace := bytes.CutPrefix(rest, []byte(" ")); hadSpace {
		prefix = []byte("data: ")
		rest = after
	}
	return prefix, rest, true
}

// isTerminalEvent reports whether an event's payload is the "[DONE]" sentinel
// that ends an OpenAI-compatible stream. It is not JSON and never parses, so
// it is recognized as the text it is, allowing for whitespace a server may
// pad it with.
func isTerminalEvent(payload []byte) bool {
	return bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]"))
}

// isBlankLine reports whether a line read off an SSE body is the empty line
// that terminates an event.
func isBlankLine(line []byte) bool {
	return len(bytes.TrimRight(line, "\r\n")) == 0
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
		// Gropius's own statement about this request, already written. This
		// merges rather than replaces, so a model server emitting either name
		// would otherwise add a second value beside ours and a client reading
		// the first one it finds could be handed the model server's.
		if gropiusHeaders[strings.ToLower(k)] {
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
//
// It reports which side ended it for the same reason the two relays above do:
// a body that stopped part-way is not an answer, and saying nothing about it
// would leave the class to be guessed from a context cancellation delivered
// on another goroutine.
func streamCopy(w http.ResponseWriter, src io.Reader) relayOutcome {
	var out relayOutcome
	rc := http.NewResponseController(w)
	buf := make([]byte, 8<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				out.clientGone = true
				return out
			}
			// A flush error means the connection does not support flushing; the
			// data is still written, so keep going rather than truncating.
			_ = rc.Flush()
		}
		if err != nil {
			out.upstreamCut = !errors.Is(err, io.EOF)
			return out
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

// prefillBudget returns how long to wait for a model server to return response
// headers for a request whose encoded body is bodyBytes long.
//
// Prefill time scales with the prompt, and a single fixed bound cannot serve
// both ends of the range: measured on Apple Silicon, prefill runs at over 1,200
// tokens per second on a small prompt and falls below 200 at the largest
// verified sizes, so a ten-minute bound sized from small prompts killed three
// of four local models mid-prefill and reported it as an upstream failure.
//
// The rate used is deliberately below every measured floor, and a minute is
// added for the fixed costs around prefill. The base is kept as the minimum so
// nothing that works today gets a shorter deadline: prompts under roughly 80K
// tokens are unaffected.
//
// Tokens are estimated from the encoded body at four bytes per token, which is
// the usual ballpark for English text and is deliberately crude — the estimate
// only has to be right to within a factor of about two to keep the bound on the
// correct side, and an exact count would mean tokenising every request on the
// gateway's own hot path.
//
// A positive override replaces the derivation entirely, including the base:
// an operator who says thirty seconds means thirty seconds.
func prefillBudget(bodyBytes, overrideSec int) time.Duration {
	if overrideSec > 0 {
		return time.Duration(overrideSec) * time.Second
	}
	const (
		base            = 10 * time.Minute
		bytesPerToken   = 4
		tokensPerSecond = 150
		fixedOverhead   = time.Minute
	)
	tokens := bodyBytes / bytesPerToken
	derived := time.Duration(tokens/tokensPerSecond)*time.Second + fixedOverhead
	if derived < base {
		return base
	}
	return derived
}
