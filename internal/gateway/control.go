package gateway

import (
	"context"
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
	"github.com/intentdriven/Gropius/internal/netshape"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
	"github.com/intentdriven/Gropius/internal/stats"
)

// Control serves the app's own API and the web control panel.
type Control struct {
	App *app.App
	// UI is the embedded web control panel.
	UI http.Handler
	// Root is this server's data root. The control plane answers challenges
	// against it so a future launch can tell this user's server apart from a
	// process squatting on the port (see cmd/gropius singleton coordination).
	Root string

	// Repaired names the settings config.Load could not use as written and put
	// into force in a changed form — a trimmed API key, a clamped grace, a
	// statistics figure replaced by its default. Set once before serving, and
	// cleared by a save, which rewrites the file from the values in force.
	//
	// It is here because the panel is the surface the operator is looking at.
	// The startup log says this once, into a stream nobody running the app from
	// the menu bar ever sees, and the panel shows an API key as asterisks
	// whether it was trimmed or not — so without this the one setting where the
	// repair changes what every client must send is invisible.
	Repaired []string

	// loadMu guards loading, the set of models the Load button already has a
	// background load running for, keyed by folded repo id.
	//
	// The button answers before the load finishes — a large model takes
	// minutes — so an operator who sees nothing happen clicks it again. Each
	// click used to start a goroutine of its own, and with eviction grace on
	// each of those occupies one of the places in the queue for memory for the
	// whole maximum wait: enough clicks fill the queue and every cold load,
	// including the ones serving requests from the network, is refused until
	// they drain.
	loadMu  sync.Mutex
	loading map[string]bool

	// repairMu guards Repaired, which the snapshot reads on every state request
	// and a save clears.
	repairMu sync.Mutex

	// settingsMu serialises the whole settings write path: read the settings
	// in force, decode the posted body into a copy of them, hand the result to
	// SetConfig, and work out what the change means for the models already
	// loaded.
	//
	// App.SetConfig has a lock of its own, and it cannot be the one that does
	// this: the snapshot every save starts from is taken before SetConfig is
	// called, so two overlapping saves each write a configuration that never
	// saw the other's change and the second reverts a field it was never asked
	// about — the form posts a whole configuration, so the field need not even
	// appear in the body. The reload_models list has the same staleness: it
	// compares the incoming settings against that snapshot.
	//
	// This handler is the only caller of SetConfig there is, so serialising it
	// here serialises every settings write. It is held across SetConfig, which
	// takes App's own save lock inside it; nothing taken under that lock
	// reaches back into the control plane, so this adds no order anything can
	// invert.
	settingsMu sync.Mutex
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
	mux.HandleFunc("GET /api/stats/history", c.handleStatsHistory)
	mux.HandleFunc("POST /api/stats/clear", c.handleClearStats)
	mux.HandleFunc("GET /api/events", c.handleEvents)
	mux.HandleFunc("GET /api/instance", c.handleInstance)
	if c.UI != nil {
		mux.Handle("/", c.UI)
	}
}

// handleInstance answers a caller's challenge by reading the file the caller
// wrote into the data root and echoing its contents back.
//
// It proves one thing and stores nothing: a process that can read this root is
// on this port. The caller chose the name and the answer, both random and both
// used once, so there is no secret here to harvest, replay, or leave behind at
// shutdown — which is what the identity token this replaces got wrong.
//
// The name is untrusted input turned into a path, so it goes through
// config.ChallengePath, which refuses anything that is not exactly 32 hex
// characters. Without that this handler would read any file the server's uid
// can open. A refusal is deliberately indistinguishable from a missing file:
// both answer 404, so the endpoint reports nothing about what exists.
func (c *Control) handleInstance(w http.ResponseWriter, r *http.Request) {
	path := config.ChallengePath(c.Root, r.URL.Query().Get("challenge"))
	if path == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such challenge"})
		return
	}
	b, err := config.ReadRegular(path, config.MaxChallengeBytes)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such challenge"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"answer": string(b)})
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
//
// The three checks are fromThisMachine, which is where the rule lives: the
// models list admits a keyless install's loopback client on exactly the same
// terms, and two spellings of "came from this machine" would be two things to
// keep right. Only the refusal message is the control plane's own.
func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromThisMachine(r) {
			writeError(w, http.StatusForbidden, loopbackRefusal(r))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loopbackRefusal says which of fromThisMachine's checks refused r, so the
// operator reading a 403 learns what to change. Evaluated only on the refusal
// path, and it enumerates the same checks in the same order.
func loopbackRefusal(r *http.Request) string {
	switch {
	case !isLoopback(r.RemoteAddr):
		return "the Gropius control panel is only reachable from the computer it runs on"
	case !isLoopbackHost(r.Host):
		return "unrecognized Host header — the control panel only answers to localhost"
	default:
		return "cross-origin request to the control panel refused"
	}
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
	// Pinned is the protected set the pool is enforcing, which is what the
	// panel marks its cards and ticks its boxes from. Read from the pool rather
	// than from Config below for the reason /v1/models does: the pool is what
	// actually refuses an eviction, and a pin reconciled with its model after a
	// download reaches the pool before it reaches the stored settings.
	Pinned []string `json:"pinned"`
	// Waiting is how many requests are parked for want of memory under an
	// eviction grace. It is the one thing about that queue nothing else on
	// this snapshot can show: a waiting request holds no model, so it appears
	// nowhere in Resident, and a Mac with nothing loading and nothing being
	// refused looks identical to one with six clients queued behind a model
	// that will not fall idle.
	Waiting int `json:"waiting"`
	// Machine is this Mac's memory, the budget loaded models are held to, and
	// what they are using of it — everything Settings says about the budget,
	// as figures rather than as UI text that nothing can check.
	Machine Machine `json:"machine"`
	// Endpoints are the URLs other machines should use, each with the kind of
	// network its address sits on.
	Endpoints []Endpoint `json:"endpoints"`
	Hostname  string     `json:"hostname"`
	// Warnings surface things the user should know, e.g. an open LAN endpoint.
	Warnings []string `json:"warnings"`
	// Stats is the per-model summary, present only while the operator has
	// recording on. The request rows and the minute buckets are deliberately
	// not here: this snapshot is re-encoded and redrawn on every event and
	// every couple of seconds, and a thousand rows on that path would cost the
	// panel more than the figures are worth. They come from /api/stats
	// instead, fetched while the Statistics view is open.
	Stats []stats.ModelCounters `json:"stats,omitempty"`
	// StatsStore says how far back the records on disk reach and how much room
	// they take, and is present under the same condition: the Settings page
	// draws the two retention figures from this snapshot, and a cap in
	// megabytes means nothing without the date beside it.
	StatsStore *stats.StoreStatus `json:"stats_store,omitempty"`
}

// Machine is what the control panel needs to talk about the memory budget: the
// size of the Mac, the ceiling models are held to, and what is in memory now.
//
// It is a snapshot of figures, not of copy. Every claim the Settings page makes
// — the budget in gigabytes, its share of the machine, whether it is still the
// default, whether the machine is over its budget — is a claim about one of
// these, so each is asserted in Go rather than read out of the panel's text.
type Machine struct {
	// TotalRAM is this Mac's installed memory, or 0 when it cannot be read, in
	// which case the panel shows no percentage and no ceiling is enforced.
	TotalRAM int64 `json:"total_ram"`
	// Budget is the ceiling on the total charged size of resident models, as
	// the pool is enforcing it.
	Budget int64 `json:"budget"`
	// DefaultBudget is the share of this Mac's memory a budget of none would
	// give. The panel needs it to say what clearing the field would do, which
	// is a question only this Mac can answer.
	DefaultBudget int64 `json:"default_budget"`
	// BudgetIsDefault says the budget is the share of this Mac's memory
	// Gropius chose, not a figure the operator set, so the panel can show it as
	// the default rather than as their own number.
	BudgetIsDefault bool `json:"budget_is_default"`
	// WarnAbove is the budget beyond which the panel warns, or 0 when this Mac
	// cannot be measured. Advice, not a limit.
	WarnAbove int64 `json:"warn_above"`
	// ResidentBytes is what the models in memory are charged against the
	// budget, the same 1.2x figure eviction uses — including the servers in
	// ExitingBytes, because that is the figure the pool admits a load against.
	// A panel that counted only the models it lists would report room the pool
	// will not give out.
	ResidentBytes int64 `json:"resident_bytes"`
	// ExitingBytes is the part of that charged to model servers which have left
	// the pool and whose processes have not exited yet. They appear in no
	// models list — they are nobody's model any more — but their memory is not
	// back, so a load can be refused while every model on screen fits.
	ExitingBytes int64 `json:"exiting_bytes"`
	// StuckServers is how many of those are past the point where stopping them
	// should have worked: SIGTERM, then SIGKILL, then nothing. Their memory is
	// held until the kernel lets go, so the budget is smaller than it looks for
	// as long as this is not zero.
	StuckServers int `json:"stuck_servers"`
	// OverBudget says the models in memory cost more than the budget allows.
	// Lowering the budget unloads nothing, so this stands until they unload by
	// the usual rules.
	OverBudget bool `json:"over_budget"`
}

// snapshot builds the state the UI renders.
//
// Both /api/state and the /api/events stream go through here. They used to build
// the struct separately, and the streaming one quietly omitted Warnings — so the
// "anyone on your network can use this server" notice never reached the UI, which
// is fed exclusively by the stream. One builder, one truth.
func (c *Control) snapshot() State {
	cfg := c.App.Config()
	residency := c.App.Pool.Residency()
	st := State{
		Models:    c.App.Registry.List(),
		Resident:  residency.Models,
		Setup:     c.App.Provisioner.Status(),
		Config:    redactConfig(cfg),
		Pinned:    c.App.Pool.Pinned(),
		Waiting:   c.App.Pool.Waiting(),
		Endpoints: Endpoints(cfg),
		Hostname:  hostname(),
	}
	budget := c.App.Pool.MemoryBudget()
	// One reading, not two: a stop landing between a models list and a tally
	// read would count the same server in both, or in neither.
	exiting, stuck := residency.ExitingBytes, residency.StuckServers
	resident := residentCharge(st.Resident) + exiting
	st.Machine = Machine{
		TotalRAM:        c.App.MachineRAM(),
		Budget:          budget,
		DefaultBudget:   capability.DefaultBudget(c.App.MachineRAM()),
		BudgetIsDefault: cfg.MaxResidentBytes == 0,
		WarnAbove:       c.App.BudgetWarnAbove(),
		ResidentBytes:   resident,
		ExitingBytes:    exiting,
		StuckServers:    stuck,
		OverBudget:      resident > budget,
	}
	if stuck > 0 {
		st.Warnings = append(st.Warnings, fmt.Sprintf(
			"%s of memory is held by %d model server(s) that were stopped and have not exited. Until they do, that much of the budget cannot be used.",
			runtime.HumanBytes(exiting), stuck))
	}
	if cfg.ExposedToLAN() && cfg.APIKey == "" {
		st.Warnings = append(st.Warnings,
			"This server is reachable by anyone on your network and requires no API key. Set one in Settings to restrict access.")
	}
	if w := c.App.PinnedFitWarning(); w != "" {
		st.Warnings = append(st.Warnings, w)
	}
	if w := c.App.MemoryBudgetWarning(); w != "" {
		st.Warnings = append(st.Warnings, w)
	}
	if w := c.repairWarning(); w != "" {
		st.Warnings = append(st.Warnings, w)
	}
	if !c.App.Provisioner.Installed() {
		st.Warnings = append(st.Warnings,
			"The MLX runtime is not installed yet — models cannot be served until setup finishes.")
	}
	st.Stats = c.App.Stats.Summary()
	if c.App.Stats.Enabled() {
		status := c.App.StatsStore.Status()
		st.StatsStore = &status
	}
	return st
}

// residentCharge is what the models in memory cost the budget. The figure is
// the pool's own — the charge it admitted each model on, which is its weights,
// their headroom and the cache the window it serves will build — because that
// is the only figure that can be compared with the budget. Working it out here
// from the size on disk is what let the panel show room the pool would not
// give out.
func residentCharge(resident []runtime.Resident) int64 {
	var sum int64
	for _, r := range resident {
		sum += r.Charge
	}
	return sum
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
	view := c.App.Stats.View()
	if view.Enabled {
		// And what is left of the days the store no longer holds in detail. A
		// month whose records retention has dropped still has its per-model
		// totals, and the view says so rather than showing a gap where the
		// traffic was (itd-2609061602043757).
		// The request's own context, so a reader who closes the panel part-way
		// through stops being paid for, exactly as the history endpoint does.
		if _, err := c.App.StatsStore.Summaries(r.Context(),
			stats.SummaryOptions{Days: maxSummaryDays}, func(d stats.SummaryDay) bool {
				view.Summaries = append(view.Summaries, d)
				return true
			}); err != nil {
			switch {
			case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
				// The reader went away. There is no socket left to answer, and
				// logging it at Warn would let anyone who can open this
				// endpoint write an unbounded run of failure lines into the
				// operator's log.
				c.App.Log.Debug("a reading of the request statistics summary was abandoned", "err", err)
				return
			case errors.Is(err, stats.ErrFlushTimedOut):
				// A store that has stopped answering rather than one that is
				// broken. This endpoint is what the panel polls, so it is
				// answered rather than refused — the live figures are in
				// memory and are worth showing — and the store status below
				// carries Stalled, which is what the panel says it out loud
				// from. Debug, not Warn: the store logs the spell once, and a
				// line per poll would be an unbounded run of them.
				c.App.Log.Debug("the request statistics summary was read without a flush the writer answered", "err", err)
			default:
				// Logged, not returned: the live figures are worth showing even
				// when the summary cannot be read, and the reason names the store's
				// directory, which the control plane must not publish.
				c.App.Log.Warn("the request statistics summary could not be read", "err", err)
			}
		}
		// After the reading, not before it: the figures the panel is handed
		// have to describe the reading it was handed them with — the lines it
		// could not use, and a writer it waited on and gave up.
		status := c.App.StatsStore.Status()
		view.Store = &status
	}
	writeJSON(w, http.StatusOK, view)
}

// DefaultHistoryDays is how far back the dashboard looks when a request names
// no range: the last month, which is the span the store's default retention
// comfortably covers and the one a reader asking "which model does the work"
// means.
const DefaultHistoryDays = 30

// historyPasses bounds how many readings of the store run at once.
//
// One pass over a store at its size cap is a second or more of work and
// several hundred megabytes of allocation, and this endpoint is reachable even
// from a blind, Origin-less cross-origin GET (e.g. <img src>) — the same reason
// maxSearchLimit exists — as well as from every other account on this Mac.
// Without a bound, a handful of requests is a way to make the Mac unusable
// from an account that has no other power over it. Two, so that a reader who
// reloads the panel is not refused for having been quick, and no more; a
// refusal is a status the panel can retry, where an unbounded fan-out of
// full-store passes is not something it can recover from.
var historyPasses = make(chan struct{}, 2)

// historyRetryAfter is what the refusal tells a caller to wait, in seconds. One
// pass over a store at its size cap is under two seconds, so a reader refused
// while two are running has an answer by the time they ask again.
const historyRetryAfter = "2"

// historySource is where the aggregates are read from.
//
// It is a variable for the reason stats.historyRecordBound is: the refusal
// above only happens while two passes are actually in flight, and a test that
// could not hold a pass still could not watch the third caller be refused. The
// production value is the app's own store and nothing else sets it.
var historySource = func(a *app.App) stats.RecordSource { return a.StatsStore }

// handleStatsHistory serves the dashboard's four historical tables: tokens
// per day by model with each model's share, request latency by model, and
// evictions and reloads by hour of the local day (itd-2609061521159233).
//
// It takes a range and nothing else — two whole UTC seconds — and answers with
// aggregates: sums, counts, buckets and the repo ids of models this Mac holds.
// No record reaches the browser, so the panel is never in a position to add up
// requests itself, and there is no path, file name or model filter in the
// request that could be turned into one.
//
// It is on the control plane and so behind the loopback guard, for the reason
// /api/stats is: these figures are the operator's and the machine is the
// boundary (adr-2609061503319212). With recording off it answers with an empty
// view, which is what the panel says "nothing is recorded" from — the same
// contract /api/stats has, so the panel needs no separate way of asking.
func (c *Control) handleStatsHistory(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	to, ok := historyTime(r, "to", now)
	if !ok {
		writeError(w, http.StatusBadRequest, "the range's end is not a time in whole seconds")
		return
	}
	from, ok := historyTime(r, "from", to.AddDate(0, 0, -DefaultHistoryDays))
	if !ok {
		writeError(w, http.StatusBadRequest, "the range's start is not a time in whole seconds")
		return
	}
	from, to, ok = historyRange(from, to, now)
	if !ok {
		writeError(w, http.StatusBadRequest,
			"the range must end no later than now, no further back than the records reach, and after it starts")
		return
	}
	if !c.App.Stats.Enabled() {
		writeJSON(w, http.StatusOK, stats.History{})
		return
	}

	select {
	case historyPasses <- struct{}{}:
		defer func() { <-historyPasses }()
	default:
		// A header rather than only a sentence, so the panel can say how long
		// to wait rather than guessing, and so anything else that asks is told
		// in the ordinary way.
		w.Header().Set("Retry-After", historyRetryAfter)
		writeError(w, http.StatusServiceUnavailable,
			"the records are already being read; try again in a moment")
		return
	}

	// The request's own context, so a reader who closes the panel part-way
	// through a pass over months of records stops being paid for.
	history, err := stats.Aggregate(r.Context(), historySource(c.App), from, to, time.Local)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// The reader went away, which is the pass working as it should rather
		// than the store failing. There is no socket left to answer and there
		// is nothing to say: logging it at Warn would let anyone who can open
		// this endpoint write an unbounded run of store-failure lines into the
		// operator's log by opening and abandoning it, which is the very thing
		// the writer logs once per spell to avoid.
		c.App.Log.Debug("a reading of the request statistics was abandoned", "err", err)
		return
	}
	if err != nil {
		// The reason is logged, not returned: these errors name the store's
		// directory, and the control plane answers every account on this Mac.
		c.App.Log.Warn("the request statistics could not be aggregated", "err", err)
		writeError(w, http.StatusInternalServerError, "the records could not be read")
		return
	}
	history.Enabled = true
	writeJSON(w, http.StatusOK, history)
}

// historyTime reads one end of the range out of the query, falling back to a
// default when it is absent. A value that is not a whole non-negative number
// of seconds is a refusal rather than a silent fallback: a panel sending a
// broken range should hear about it, and so should anything else that asks.
func historyTime(r *http.Request, name string, fallback time.Time) (time.Time, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, true
	}
	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || secs < 0 {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}

// historyRange holds the range to one the store can answer for, reporting
// whether anything is left of it.
//
// One rule, applied once, after the defaults have been filled in: the range
// ends no later than now, and it has to have something in it. Clamping the end
// is what keeps a caller from naming a time so far ahead that the aggregation's
// own narrowing has nothing to narrow, and it makes the range the panel draws
// the range it asked for. The start is left alone, because narrowing it is the
// aggregation's to do and to report — a range cut to the widest one view covers
// must say so, and a handler that had quietly cut it first would leave nothing
// to say.
//
// The end is also held to the horizon, and that one is a refusal rather than a
// clamp. The store is read newest first, so a range ending long ago is a walk
// back over everything newer than it before the figures even begin; a range
// ending further back than a view can reach has nothing to show for that walk,
// and answering it would be a full pass over the store that any caller could
// ask for by naming a date near the epoch.
//
// What none of this does is make every range cheap. A range that ends within
// the horizon but well before now still costs the walk back to it; that walk is
// bounded by the aggregation's record bound and by historyPasses, which is what
// holds the cost, rather than the shape of the range.
func historyRange(from, to, now time.Time) (time.Time, time.Time, bool) {
	if to.After(now) {
		to = now
	}
	if to.Before(now.AddDate(0, 0, -stats.MaxHistoryDays)) {
		return time.Time{}, time.Time{}, false
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

// maxSummaryDays bounds what one answer carries, in days rather than in lines.
// A day is one line per model that served on it, so a bound on lines would hand
// a Mac running ten models a tenth of the span it handed a Mac running one. The
// figure is past what the summary's own share of the default size limit holds
// — three years of days for ten models, and more for fewer — so on a store
// Gropius wrote this cuts nothing off; it is here so that a summary grown by a
// build with a larger limit cannot make this answer unbounded.
const maxSummaryDays = 1500

// handleClearStats throws away everything recording has produced: the files
// and the live view both. It is the only thing that removes a record —
// switching recording off stops new ones and leaves the old ones alone — and
// it takes no path from the request: it acts on the store's resolved
// directory, and only on the file names the store itself writes.
func (c *Control) handleClearStats(w http.ResponseWriter, r *http.Request) {
	if err := c.App.ClearStats(); err != nil {
		// The reason is logged, not returned: these errors name the store's
		// directory, and the control plane answers every account on this Mac.
		c.App.Log.Warn("the request statistics could not be cleared", "err", err)
		writeError(w, http.StatusInternalServerError, "the records could not be cleared")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "cleared"})
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

// Endpoint is one base URL clients can point at, and the kind of network the
// address sits on.
//
// Network is an observation and never a promise: it says which network Gropius
// found the address on, not what that network is worth. Gropius cannot see
// whether that network has since been published to the internet, shared with
// machines the operator does not own, or logged out from, and a word like
// "encrypted" survives every one of those and then lies
// (adr-2609081118587999, rule 1). It is empty for an ordinary address, which
// is every address on a machine with no private network.
type Endpoint struct {
	URL string `json:"url"`
	// Network names the kind of network — never the vendor, which this
	// detection cannot tell apart anyway.
	Network string `json:"network,omitempty"`
}

// Endpoints lists the base URLs clients can point at.
//
// It lists only what this server answers on. With a wildcard bind that is
// every address the machine holds; with a specific bind it is that address
// alone, because the rest refuse the connection — and an endpoint list that
// offers a dead address, still worse a marked dead address, is worse than one
// that offers nothing.
//
// Nothing here is memoized. The private network can appear, disappear or
// change address while Gropius runs, and the panel rebuilds this list on every
// snapshot; the classification behind it is interface inspection with no
// network call and no subprocess, so it can stay on that path.
func Endpoints(cfg config.Config) []Endpoint {
	var out []Endpoint
	bound, wildcard := boundAddr(cfg.Host)
	if cfg.ExposedToLAN() {
		addrs := netshape.Addrs()
		switch {
		case wildcard:
			// The wildcard: every address answers, so the list is as it was.
			// A loopback-only bind must still not advertise the .local name,
			// which resolves to LAN addresses — the menu bar shows the first
			// entry as the endpoint, and it would be one the server never
			// answers on. Neither may a machine that holds no address on a
			// local network: with only a private-network address up, the name
			// resolves to nothing, and it would be the entry the menu bar
			// hands out.
			if hasLocalNetworkAddr(addrs) {
				out = appendLocalName(out, cfg.Port)
			}
			for _, a := range addrs {
				out = appendEndpoint(out, a.IP, cfg.Port, a.Network)
			}
		case bound != "":
			// A specific bind. The .local name resolves to the addresses this
			// machine holds on the local network, so it answers only when the
			// bind covers the sole one of those; with others on the machine it
			// may resolve to one of them, and a name that might land elsewhere
			// is exactly what this list is dropping. An address on a private
			// network is not one the name resolves to at all, so a machine
			// holding only that one does not get the name either.
			if len(addrs) == 1 && addrs[0].IP == bound && addrs[0].Network == "" {
				out = appendLocalName(out, cfg.Port)
			}
			out = appendEndpoint(out, bound, cfg.Port, networkOf(addrs, bound))
		default:
			// A Host that is neither a wildcard nor anything a client can be
			// pointed at. config.Validate refuses it, so reaching here means
			// the configuration was not loaded through Load; nothing is listed
			// for it either way. An address the server cannot even bind is the
			// dead address this list exists to stop offering, and a Host
			// carrying control characters is worse than dead — it is a base
			// URL the panel, the menu bar and the clipboard hand out.
		}
	}
	out = appendEndpoint(out, loopbackHost(bound, wildcard), cfg.Port, "")
	return out
}

// loopbackHost is the loopback address this server answers on.
//
// It is 127.0.0.1 everywhere except under a bind that took IPv6 loopback and
// nothing else: "[::1]" listens on ::1, refuses 127.0.0.1, and listing the one
// it refuses while omitting the one it answers on is the dead-address fault in
// both directions at once. A name — "localhost" — stays 127.0.0.1, because
// that is what the name resolves to for a client on this Mac.
//
// This is not the whole of the loopback entry's honesty. Under a specific
// non-loopback bind the server does not answer on loopback at all and the
// entry is listed anyway; that divergence from the intent's fourth criterion is
// recorded in TestLoopbackIsListedUnderEveryBindIncludingOneItDoesNotAnswerOn
// and belongs to iss-7.
func loopbackHost(bound string, wildcard bool) string {
	if !wildcard && bound != "" {
		if ip := net.ParseIP(bound); ip != nil && ip.IsLoopback() && ip.To4() == nil {
			return bound
		}
	}
	return "127.0.0.1"
}

// boundAddr is the one address this server answers on, and whether the bind is
// a wildcard on which every address answers. A host that is neither — one no
// listener could take — is "", false, and nothing is listed for it.
//
// A Host that is a name is a specific bind too: it resolves to whatever it
// resolves to, and the addresses beside it are no more reachable for that.
func boundAddr(host string) (string, bool) {
	if host == "" {
		return "", true
	}
	// The bind spelling and the URL spelling of an IPv6 address differ, and
	// this is where the two meet: config.Host carries the brackets the listener
	// needs, and everything downstream of here works with the bare address.
	bare, ok := config.URLHost(host)
	if !ok {
		return "", false
	}
	if ip := net.ParseIP(bare); ip != nil {
		if ip.IsUnspecified() {
			return "", true
		}
		return ip.String(), false
	}
	return bare, false
}

// networkOf is the kind of network the bound address sits on, read out of the
// addresses this machine already reported rather than by enumerating them a
// second time. An address the machine does not hold is on no network this can
// name, which is the same answer a fresh look would give.
func networkOf(addrs []netshape.Addr, bound string) string {
	for _, a := range addrs {
		if a.IP == bound {
			return a.Network
		}
	}
	return ""
}

// hasLocalNetworkAddr reports whether this machine holds an address the .local
// name could resolve to. An address on a private network is not one: that name
// is answered over the local network, not through a tunnel.
func hasLocalNetworkAddr(addrs []netshape.Addr) bool {
	for _, a := range addrs {
		if a.Network == "" {
			return true
		}
	}
	return false
}

// appendLocalName adds this Mac's .local endpoint, when it has a name to give.
func appendLocalName(out []Endpoint, port int) []Endpoint {
	if h := hostname(); h != "" {
		out = appendEndpoint(out, h+".local", port, "")
	}
	return out
}

// appendEndpoint adds one entry, and adds nothing when the host will not make
// a URL. Every entry goes through here, including the machine's own name,
// which is read from scutil and is no more trusted for this than a
// hand-edited Host is.
func appendEndpoint(out []Endpoint, host string, port int, network string) []Endpoint {
	if u := endpointURL(host, port); u != "" {
		out = append(out, Endpoint{URL: u, Network: network})
	}
	return out
}

// endpointURL is the base URL for one host, or "" when the host is not
// something a URL can carry.
//
// It goes through JoinHostPort so an IPv6 literal is bracketed rather than run
// together with the port into something no client can parse — and through
// config.URLHost first, because JoinHostPort brackets unconditionally and the
// bind spelling of an IPv6 address is already bracketed. Doubling them
// produced "http://[[::1]]:11535/v1" as the menu-bar title, the clipboard's
// contents and the panel's curl and Python base URL.
func endpointURL(host string, port int) string {
	h, ok := config.URLHost(host)
	if !ok {
		return ""
	}
	return "http://" + net.JoinHostPort(h, strconv.Itoa(port)) + "/v1"
}

// hostname is the name other machines use to reach this Mac.
func hostname() string {
	return config.LocalHostName()
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
	// that will not fit — too big to store, or too big to run in the memory
	// budget. The budget is the one the pool is enforcing, read on every
	// search, so a figure the operator changes in Settings changes what this
	// tab shows without a restart and without a second answer to the question
	// of what fits.
	machine := capability.Assess(c.App.Paths.Models, c.App.MachineRAM(), c.App.Pool.MemoryBudget())

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
// a malformed id is the caller's mistake (400), an absent model is 404, a
// server that is going away is 503, and a genuine conflict (already
// downloading, being deleted, or busy serving a request) is 409.
func modelErrorStatus(err error) int {
	switch {
	case errors.Is(err, app.ErrInvalidRepoID):
		return http.StatusBadRequest
	case errors.Is(err, registry.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, app.ErrShuttingDown):
		// Named rather than left to the default arm: a conflict says the state
		// of this model is the problem and asking again about a different one
		// would work, and neither is true here. The server is stopping, and
		// 503 is what says so.
		return http.StatusServiceUnavailable
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
	//
	// One background load per model, however many times the button is pressed:
	// a second one would ask the pool for a model the first is already loading
	// and hold a second place in the queue for memory to do it. A click that
	// finds a load already running is answered with the same status, because
	// it is the same true answer — this model is loading.
	if c.beginLoad(model) {
		go func() {
			defer c.endLoad(model)
			ctx, cancel := contextWithTimeout(15 * time.Minute)
			defer cancel()
			_, release, err := c.App.Pool.Acquire(ctx, model)
			if err != nil {
				c.App.Log.Error("preload failed", "model", model, "err", err)
				return
			}
			release()
		}()
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "loading", "model": model})
}

// beginLoad claims the background load of a model, reporting whether this
// caller is the one that has to run it.
//
// Keyed on the folded repo id, which is how the pool itself looks a model up:
// two spellings are two strings and one model, and keying on the spelling
// would let a second click through under a different case.
func (c *Control) beginLoad(model string) bool {
	key := config.FoldRepoID(model)
	c.loadMu.Lock()
	defer c.loadMu.Unlock()
	if c.loading[key] {
		return false
	}
	if c.loading == nil {
		c.loading = make(map[string]bool)
	}
	c.loading[key] = true
	return true
}

// endLoad releases the claim, whether the load succeeded or failed. The next
// click starts a fresh one — a load that failed is worth retrying, and a model
// that is now resident costs the pool nothing to acquire again.
func (c *Control) endLoad(model string) {
	c.loadMu.Lock()
	defer c.loadMu.Unlock()
	delete(c.loading, config.FoldRepoID(model))
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

	// The body is read before the lock is taken and the answer written after
	// it is released: a client that uploads or reads slowly is not something
	// the next save should have to wait behind.
	out, err := c.applySettings(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// repairWarning is what the panel says about the settings the file could not
// carry as written.
//
// It says they are in force, because they are, and it names them: an operator
// who reads "your API key was shortened" can check the key their clients send,
// which is the only thing they can usefully do about it. Saying "ignored" would
// send them to set a key that is already working, and blaming the model server
// would send them to the wrong software entirely.
func (c *Control) repairWarning() string {
	c.repairMu.Lock()
	defer c.repairMu.Unlock()
	if len(c.Repaired) == 0 {
		return ""
	}
	return "Some settings in config.json could not be used as written and are in force in a changed form: " +
		strings.Join(c.Repaired, ", ") +
		". Check them here and save to write the values now in force back to the file."
}

// clearRepairs drops the notice once a save has rewritten config.json from the
// values in force: there is nothing left in the file that needed repairing, and
// a warning that outlives what it warned about is the same untruth from the
// other side.
func (c *Control) clearRepairs() {
	c.repairMu.Lock()
	defer c.repairMu.Unlock()
	c.Repaired = nil
}

// applySettings is the settings write path, from the settings in force to what
// the save is answered with, run start to finish under settingsMu. It returns
// what the panel is told, or the refusal to report to the caller — every one of
// which is the caller's own mistake, and so a 400.
func (c *Control) applySettings(raw []byte) (map[string]any, error) {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()

	current := c.App.Config()

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
		return nil, errors.New("settings body is not valid JSON")
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
		return nil, err
	}
	// The file has just been written from the settings in force, repairs and
	// all, so there is nothing left in it to repair.
	c.clearRepairs()
	// Host and port bind the server, decode concurrency and idle timeout are
	// pool options — all four are consumed only at startup, and SetConfig
	// cannot apply them live.
	restart := incoming.Port != current.Port ||
		incoming.Host != current.Host ||
		incoming.DecodeConcurrency != current.DecodeConcurrency ||
		incoming.IdleTimeoutSec != current.IdleTimeoutSec
	out := map[string]any{
		"status":        "saved",
		"restart":       restart,
		"reload_models": samplingReloads(current, incoming, c.App.Pool.Resident()),
	}
	// A budget that claims most of the Mac is saved and answered with advice.
	// What a Mac can actually carry is not a figure Gropius knows, so this is
	// the one thing a save says without refusing anything.
	if warn := c.App.MemoryBudgetWarning(); warn != "" {
		out["warning"] = warn
	}
	return out, nil
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
