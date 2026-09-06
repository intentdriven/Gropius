package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/intentdriven/Gropius/internal/app"
	"github.com/intentdriven/Gropius/internal/capability"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/hub"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
	"github.com/intentdriven/Gropius/internal/stats"
)

// Control serves the app's own API and the web control panel.
type Control struct {
	App *app.App
	// UI is the embedded web control panel.
	UI http.Handler
	// InstanceToken identifies this server run. It is served on the loopback-only
	// control plane so a future launch can tell this user's server apart from a
	// process squatting on the port (see cmd/gropius singleton coordination).
	InstanceToken string
}

// Handler returns the control plane and web UI, restricted to loopback.
//
// The control plane is administrative: it can delete models and rewrite settings
// (including blanking the API key). It must therefore NEVER be reachable from the
// LAN, even though it shares the same listener as the /v1 API. Binding it to
// loopback means only this machine — including its other user accounts, which
// reach it over 127.0.0.1 — can drive it. The web UI is loopback-only for the
// same reason (and because http://LAN-ip is not a secure browser context).
func (c *Control) Handler() http.Handler {
	mux := http.NewServeMux()
	c.Routes(mux)
	return loopbackOnly(mux)
}

// Routes registers the control-plane endpoints onto a mux. Prefer Handler(),
// which wraps these in the mandatory loopback guard; Routes is exported only so
// tests can exercise handlers directly.
func (c *Control) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/state", c.handleState)
	mux.HandleFunc("GET /api/search", c.handleSearch)
	mux.HandleFunc("POST /api/models/download", c.handleDownload)
	mux.HandleFunc("POST /api/models/cancel", c.handleCancelDownload)
	mux.HandleFunc("POST /api/models/delete", c.handleDelete)
	mux.HandleFunc("POST /api/models/load", c.handleLoad)
	mux.HandleFunc("POST /api/models/unload", c.handleUnload)
	mux.HandleFunc("GET /api/settings", c.handleGetSettings)
	mux.HandleFunc("POST /api/settings", c.handleSetSettings)
	mux.HandleFunc("GET /api/stats", c.handleStats)
	mux.HandleFunc("GET /api/events", c.handleEvents)
	mux.HandleFunc("GET /api/instance", c.handleInstance)
	if c.UI != nil {
		mux.Handle("/", c.UI)
	}
}

// handleInstance serves this server run's identity token. It is loopback-only
// (the whole control plane is), so the token never reaches the LAN; a future
// launch uses it to confirm the process on the port is this user's Gropius.
func (c *Control) handleInstance(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"token": c.InstanceToken})
}

// loopbackOnly rejects any request that did not originate on this machine, and
// additionally defends the control plane against browser-driven cross-origin
// attacks that a source-address check alone cannot see.
//
// A page the victim visits runs in their browser, which connects from 127.0.0.1
// — so RemoteAddr is loopback and a bare check waves the request through. Two
// extra guards close that:
//
//   - Host allow-list: a DNS-rebinding attack points a hostname it controls at
//     127.0.0.1, so the socket is loopback but the Host header is the attacker's.
//     Requiring a genuine loopback Host rejects the rebound request.
//   - Origin allow-list: a cross-site POST from evil.com carries its origin. The
//     real UI is same-origin (a loopback origin), so any other origin is refused.
//     This blocks classic CSRF, which needs no rebinding.
func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.RemoteAddr) {
			writeError(w, http.StatusForbidden,
				"the Gropius control panel is only reachable from the computer it runs on")
			return
		}
		if !isLoopbackHost(r.Host) {
			writeError(w, http.StatusForbidden,
				"unrecognized Host header — the control panel only answers to localhost")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !isLoopbackOrigin(origin) {
			writeError(w, http.StatusForbidden,
				"cross-origin request to the control panel refused")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopbackHost reports whether an HTTP Host header (with or without a port)
// names this machine's loopback interface.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

// isLoopbackOrigin reports whether an Origin header refers to a loopback host.
func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return isLoopbackHost(u.Host)
}

// State is the whole picture the UI renders.
type State struct {
	Models   []registry.Model    `json:"models"`
	Resident []runtime.Resident  `json:"resident"`
	Setup    runtime.SetupStatus `json:"setup"`
	Config   config.Config       `json:"config"`
	// MemoryBudget is the ceiling on the total charged size of resident
	// models, so the panel can say what a pinned set leaves for everything
	// else. The pool resolves it, since the default is a share of this Mac's
	// physical RAM rather than a stored setting.
	MemoryBudget int64 `json:"memory_budget"`
	// Endpoints are the URLs other machines should use.
	Endpoints []string `json:"endpoints"`
	Hostname  string   `json:"hostname"`
	// Warnings surface things the user should know, e.g. an open LAN endpoint.
	Warnings []string `json:"warnings"`
	// Stats is the per-model summary, present only while the operator has
	// recording on. The request rows and the minute buckets are deliberately
	// not here: this snapshot is re-encoded and redrawn on every event and
	// every couple of seconds, and a thousand rows on that path would cost the
	// panel more than the figures are worth. They come from /api/stats
	// instead, fetched while the Statistics view is open.
	Stats []stats.ModelCounters `json:"stats,omitempty"`
}

// snapshot builds the state the UI renders.
//
// Both /api/state and the /api/events stream go through here. They used to build
// the struct separately, and the streaming one quietly omitted Warnings — so the
// "anyone on your network can use this server" notice never reached the UI, which
// is fed exclusively by the stream. One builder, one truth.
func (c *Control) snapshot() State {
	cfg := c.App.Config()
	st := State{
		Models:       c.App.Registry.List(),
		Resident:     c.App.Pool.Resident(),
		Setup:        c.App.Provisioner.Status(),
		Config:       redactConfig(cfg),
		MemoryBudget: c.App.Pool.MemoryBudget(),
		Endpoints:    Endpoints(cfg),
		Hostname:     hostname(),
	}
	if cfg.ExposedToLAN() && cfg.APIKey == "" {
		st.Warnings = append(st.Warnings,
			"This server is reachable by anyone on your network and requires no API key. Set one in Settings to restrict access.")
	}
	if !c.App.Provisioner.Installed() {
		st.Warnings = append(st.Warnings,
			"The MLX runtime is not installed yet — models cannot be served until setup finishes.")
	}
	st.Stats = c.App.Stats.Summary()
	return st
}

// handleStats serves the live view of what this Mac has served: the recent
// requests and the minute buckets, alongside the same per-model counters the
// snapshot carries. It answers with an empty view while recording is off, so
// the panel needs no separate way of asking whether there is anything to show.
//
// It is on the control plane, which is loopback-only: these figures are the
// operator's, and the machine is the boundary (adr-2609061503319212). The
// machine, not the account — the control plane deliberately answers every
// local account (see Handler), so on a shared Mac these figures are readable
// by anyone logged into it. That is what the panel's recording indicator and
// the documentation's "Who can see it" section exist to say out loud.
func (c *Control) handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, c.App.Stats.View())
}

func (c *Control) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, c.snapshot())
}

// redactConfig blanks secrets before they go over the wire. The control panel
// is loopback-only, but loopback includes other local user accounts (see the
// Handler comment), so it must never echo the API key or HF token back.
func redactConfig(c config.Config) config.Config {
	if c.APIKey != "" {
		c.APIKey = "********"
	}
	if c.HFToken != "" {
		c.HFToken = "********"
	}
	return c
}

const redacted = "********"

// Endpoints lists the base URLs clients can point at.
func Endpoints(cfg config.Config) []string {
	var out []string
	if cfg.ExposedToLAN() {
		// The .local name resolves to LAN addresses, so a loopback-only bind
		// must not advertise it: the menu bar shows the first entry as the
		// endpoint, and it would be one the server never answers on.
		if h := hostname(); h != "" {
			out = append(out, fmt.Sprintf("http://%s.local:%d/v1", h, cfg.Port))
		}
		for _, ip := range lanIPs() {
			out = append(out, fmt.Sprintf("http://%s:%d/v1", ip, cfg.Port))
		}
	}
	out = append(out, fmt.Sprintf("http://127.0.0.1:%d/v1", cfg.Port))
	return out
}

// hostname is the name other machines use to reach this Mac.
func hostname() string {
	return config.LocalHostName()
}

// lanIPs returns the machine's non-loopback IPv4 addresses.
func lanIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipnet.IP.To4(); ip4 != nil {
			out = append(out, ip4.String())
		}
	}
	return out
}

// maxSearchLimit bounds the "limit" query parameter on /api/search. Without a
// ceiling, a single request turns into an unbounded fan-out of outbound
// RepoSize lookups (internal/hub) against HuggingFace — reachable even from a
// blind, Origin-less cross-origin GET (e.g. <img src>), since loopbackOnly's
// Origin check only ever sees an Origin header on same-site or POST requests.
const maxSearchLimit = 100

// searchLimit parses and bounds the "limit" query parameter.
func searchLimit(raw string) int {
	limit, _ := strconv.Atoi(raw)
	if limit <= 0 {
		limit = 40
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	return limit
}

// searchAuthor parses the "author:" prefix out of a search query, returning
// the org to restrict the search to and the remaining search term. Default to
// the mlx-community org: it is where the MLX-converted models live, and
// searching all of HuggingFace returns mostly models that will not load. An
// explicit "author:name" prefix overrides that — but a bare "author:" with
// nothing after the colon left the org filter empty rather than falling back
// to the default, silently turning "override the org" into "search the
// entire Hub."
func searchAuthor(q string) (author, rest string) {
	author = "mlx-community"
	if !strings.HasPrefix(q, "author:") {
		return author, q
	}
	parts := strings.SplitN(strings.TrimPrefix(q, "author:"), " ", 2)
	if parts[0] != "" {
		author = parts[0]
	}
	if len(parts) > 1 {
		rest = parts[1]
	}
	return author, rest
}

func (c *Control) handleSearch(w http.ResponseWriter, r *http.Request) {
	author, q := searchAuthor(r.URL.Query().Get("q"))
	limit := searchLimit(r.URL.Query().Get("limit"))

	models, err := c.App.Hub.Search(r.Context(), hub.SearchQuery{
		Search: q,
		Author: author,
		Limit:  limit,
		Sort:   "downloads",
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Mark what is already local so the UI can show "Downloaded" instead of a
	// download button.
	local := map[string]registry.State{}
	for _, m := range c.App.Registry.List() {
		local[m.RepoID] = m.State
	}

	// Measure this machine so we can show each model's size and hide the ones
	// that will not fit — too big to store, or too big to run in the RAM budget.
	machine := capability.Assess(c.App.Paths.Models)

	// The search payload carries no file sizes, so fetch each repo's download
	// size concurrently (one tree request each, bounded).
	sizes := make([]int64, len(models))
	ctx := r.Context()
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, m := range models {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, repoID string) {
			defer wg.Done()
			defer func() { <-sem }()
			if size, err := c.App.Hub.RepoSize(ctx, repoID); err == nil {
				sizes[i] = size
			}
		}(i, m.ID)
	}
	wg.Wait()

	type result struct {
		hub.Model
		Quantization string `json:"quantization"`
		LocalState   string `json:"local_state,omitempty"`
		SizeBytes    int64  `json:"size_bytes,omitempty"`
	}
	out := make([]result, 0, len(models))
	hidden := 0
	for i, m := range models {
		state := string(local[m.ID])
		// Always show models already on this machine and models we could not
		// measure; otherwise hide ones that do not fit.
		if state == "" && !machine.Fits(sizes[i]) {
			hidden++
			continue
		}
		out = append(out, result{
			Model:        m,
			Quantization: m.Quantization(),
			LocalState:   state,
			SizeBytes:    sizes[i],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results": out,
		"machine": machine,
		"hidden":  hidden,
	})
}

// modelRequest is the body of the model action endpoints.
type modelRequest struct {
	Model string `json:"model"`
}

func decodeModelRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req modelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Model == "" {
		writeError(w, http.StatusBadRequest, `a "model" field is required`)
		return "", false
	}
	return req.Model, true
}

func (c *Control) handleDownload(w http.ResponseWriter, r *http.Request) {
	model, ok := decodeModelRequest(w, r)
	if !ok {
		return
	}
	if err := c.App.Download(model); err != nil {
		writeError(w, modelErrorStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "downloading", "model": model})
}

func (c *Control) handleCancelDownload(w http.ResponseWriter, r *http.Request) {
	model, ok := decodeModelRequest(w, r)
	if !ok {
		return
	}
	if err := c.App.CancelDownload(model); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "cancelled", "model": model})
}

func (c *Control) handleDelete(w http.ResponseWriter, r *http.Request) {
	model, ok := decodeModelRequest(w, r)
	if !ok {
		return
	}
	if err := c.App.Delete(model); err != nil {
		writeError(w, modelErrorStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "model": model})
}

// modelErrorStatus maps a model-action error to the right HTTP status:
// a malformed id is the caller's mistake (400), an absent model is 404, and a
// genuine conflict (already downloading, or busy serving a request) is 409.
func modelErrorStatus(err error) int {
	switch {
	case errors.Is(err, app.ErrInvalidRepoID):
		return http.StatusBadRequest
	case errors.Is(err, registry.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusConflict
	}
}

// handleLoad warms a model so the first real request is not slow.
func (c *Control) handleLoad(w http.ResponseWriter, r *http.Request) {
	model, ok := decodeModelRequest(w, r)
	if !ok {
		return
	}
	// Loading a large model can take minutes; do not hold the HTTP request open
	// for it. The UI watches /api/events for the model to appear as resident.
	go func() {
		ctx, cancel := contextWithTimeout(15 * time.Minute)
		defer cancel()
		_, release, err := c.App.Pool.Acquire(ctx, model)
		if err != nil {
			c.App.Log.Error("preload failed", "model", model, "err", err)
			return
		}
		release()
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "loading", "model": model})
}

func (c *Control) handleUnload(w http.ResponseWriter, r *http.Request) {
	model, ok := decodeModelRequest(w, r)
	if !ok {
		return
	}
	if err := c.App.Pool.Unload(model); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "unloaded", "model": model})
}

func (c *Control) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, redactConfig(c.App.Config()))
}

func (c *Control) handleSetSettings(w http.ResponseWriter, r *http.Request) {
	current := c.App.Config()

	// Everything saved here is written to config.json, which Load refuses to
	// read above this size — so a larger body could only produce a file the
	// next start cannot read, and a start that cannot read it locks the server
	// down to loopback.
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, config.MaxConfigBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusBadRequest, "settings body is too large")
		} else {
			writeError(w, http.StatusBadRequest, "settings body could not be read")
		}
		return
	}

	// Decode INTO a copy of the current config, not a fresh zero value: the
	// settings form posts only the fields it owns, so any field it omits — e.g.
	// Preload, or Advertise (which has no UI control) — must keep its existing
	// value. Decoding into a zero Config and saving it wholesale silently wiped
	// those, dropping a user's preload list on any unrelated settings change.
	//
	// A deep copy, not a shallow one: encoding/json decodes straight into an
	// existing non-nil pointer's target, so with a plain assignment a posted
	// sampling value would land in the live configuration before Validate could
	// look at it — and stay there when Validate refused the save.
	incoming := current.Clone()
	// "Keep what you did not send" is a rule about fields, not about the
	// members of a collection. encoding/json merges into an existing map, so
	// decoding a posted model_sampling object into the current one reinstates
	// every override the object leaves out — which is every override the user
	// just deleted. Naming the field means "these are the overrides", so start
	// from nothing; omitting it still keeps what is there. The same holds for
	// per_model, where leaving a model out is how Settings switches its
	// merging off.
	if namesModelSampling(raw) {
		incoming.ModelSampling = nil
	}
	if namesPerModel(raw) {
		incoming.PerModel = nil
	}
	if err := json.Unmarshal(raw, &incoming); err != nil {
		writeError(w, http.StatusBadRequest, "settings body is not valid JSON")
		return
	}
	// The UI is served the redacted placeholder; echoing it back must not
	// overwrite the real secret with literal asterisks.
	if incoming.APIKey == redacted {
		incoming.APIKey = current.APIKey
	}
	if incoming.HFToken == redacted {
		incoming.HFToken = current.HFToken
	}

	if err := c.App.SetConfig(incoming); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Host and port bind the server, decode concurrency and idle timeout are
	// pool options — all four are consumed only at startup, and SetConfig
	// cannot apply them live.
	restart := incoming.Port != current.Port ||
		incoming.Host != current.Host ||
		incoming.DecodeConcurrency != current.DecodeConcurrency ||
		incoming.IdleTimeoutSec != current.IdleTimeoutSec
	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "saved",
		"restart":       restart,
		"reload_models": samplingReloads(current, incoming, c.App.Pool.Resident()),
	})
}

// namesModelSampling reports whether the posted body carries a model_sampling
// field at all, however it is spelled — including as null.
func namesModelSampling(body []byte) bool { return namesField(body, "model_sampling") }

// namesPerModel reports the same for per_model, the other collection a save
// replaces rather than merges into.
func namesPerModel(body []byte) bool { return namesField(body, "per_model") }

// namesField reports whether the posted body carries this field at all,
// however it is spelled — including as null.
func namesField(body []byte, field string) bool {
	var named map[string]json.RawMessage
	if err := json.Unmarshal(body, &named); err != nil {
		return false
	}
	// Folded, because the decode this guards matches struct field names
	// case-insensitively: an exact lookup would let one spelling skip the
	// reset and reinstate every entry the caller asked to remove.
	for k := range named {
		if strings.EqualFold(k, field) {
			return true
		}
	}
	return false
}

// samplingReloads names the loaded models whose sampling defaults changed with
// this save.
//
// The defaults are launch flags, so a model that is already running keeps the
// values its process started with until it loads again. Saying which models
// those are is the difference between "the change has not reached these yet"
// and a change that silently appears to have done nothing. A model carrying an
// override that shadows the changed value is not listed: nothing about how it
// is served moved.
func samplingReloads(before, after config.Config, resident []runtime.Resident) []string {
	out := []string{}
	for _, m := range resident {
		if !before.EffectiveSampling(m.RepoID).Equal(after.EffectiveSampling(m.RepoID)) {
			out = append(out, m.RepoID)
		}
	}
	sort.Strings(out)
	return out
}

// handleEvents streams state snapshots to the UI over SSE, so download progress
// appears without polling.
func (c *Control) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	updates, unsub := c.App.Registry.Subscribe()
	defer unsub()

	send := func() bool {
		b, err := json.Marshal(c.snapshot())
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	if !send() {
		return
	}

	// A slow tick alongside the change notifications keeps "resident" and
	// "setup" fresh — neither of those goes through the registry's subscription.
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case _, ok := <-updates:
			if !ok {
				return
			}
			if !send() {
				return
			}
		case <-tick.C:
			if !send() {
				return
			}
		}
	}
}
