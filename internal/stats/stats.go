// Package stats keeps a content-free record of the requests this Mac serves.
//
// It exists under adr-2609061503319212: nothing about usage leaves the
// machine, ever, and what is kept locally is kept only because the operator
// turned it on. The switch is off until they do, and while it is off the
// recorder holds nothing, counts nothing and shows nothing.
//
// The recorder is handed values, never bodies. It has no access to a request,
// its headers, its remote address or its messages — the only string it is ever
// given is a model's repo id, which the registry resolved and which the client
// therefore did not choose. That is what makes "no prompt, no completion, no
// key, no client address" a property of the seam rather than a filter someone
// has to keep correct.
package stats

import (
	"sync"
	"time"
)

// RingSize is how many recent requests the live view holds. At about 150 bytes
// a record that is well under a megabyte, and the panel has more rows than a
// reader will scroll. Keeping more than the recent past is the durable store's
// job (itd-2609061521102742), not the ring's.
const RingSize = 1000

// RollupWindow is how far back the per-minute buckets reach: a day, which is
// 1,440 rows however busy the Mac is.
const RollupWindow = 24 * time.Hour

// NoFirstToken is the time to first token of a request that had none — a
// non-streaming request, or one that failed before a token was produced. It is
// a figure rather than an absence so a record stays a flat row of numbers,
// which is what both the panel and the durable store want.
const NoFirstToken = -1

// Class is how a request ended, named by what was observable from outside: the
// status the client received, whether the client went away, and whether the
// upstream body finished. It is never inferred from anything inside the
// request.
type Class string

const (
	// ClassOK is a request the model server answered in full. It is the only
	// class that carries token counts.
	ClassOK Class = "ok"
	// ClassClientError is one of the gateway's own refusals: a body it could
	// not read or parse, no model field, an unknown model, a body too large.
	ClassClientError Class = "client_error"
	// ClassUpstreamStatus is a status of 300 or more that the model server
	// itself returned, relayed to the client as it stands.
	ClassUpstreamStatus Class = "upstream_status"
	// ClassBusy is a refusal because that model already has as many requests
	// in flight as it will take.
	ClassBusy Class = "busy"
	// ClassRefused is any other refusal from the pool: the model does not fit
	// the memory budget, every loaded model is serving, the pool is closing.
	ClassRefused Class = "refused"
	// ClassLaunchFailed is a model server that could not be started at all.
	ClassLaunchFailed Class = "launch_failed"
	// ClassNotReady is a model server that started but never answered.
	ClassNotReady Class = "not_ready"
	// ClassUnreachable is a model server that was running and did not respond,
	// or that stopped responding part-way through an answer it had begun.
	ClassUnreachable Class = "unreachable"
	// ClassCancelled is a client that went away before the answer was done.
	ClassCancelled Class = "cancelled"
	// ClassGatewayError is Gropius's own failure: it could not re-encode the
	// request or could not build the call to the model server. It is neither
	// the client's fault nor the model server's, and recording it as anything
	// else would put the blame on one of them.
	ClassGatewayError Class = "gateway_error"
)

// Reasons an entry left the pool. Only ReasonEvicted is an eviction; the pool
// removes a model by seven paths and counting them as one would make the load
// and eviction figures disagree with what actually happened.
const (
	ReasonEvicted    = "evicted"
	ReasonIdle       = "idle"
	ReasonUnloaded   = "unloaded"
	ReasonAbandoned  = "abandoned"
	ReasonLoadFailed = "load_failed"
	ReasonCrashed    = "crashed"
	ReasonShutdown   = "shutdown"
)

// OutcomeClasses lists every way a request can be recorded as having ended, so
// that the documentation's list of them is held to this one: a class added
// here without a word about it fails the build.
func OutcomeClasses() []Class {
	return []Class{
		ClassOK, ClassClientError, ClassUpstreamStatus, ClassBusy, ClassRefused,
		ClassLaunchFailed, ClassNotReady, ClassUnreachable, ClassCancelled, ClassGatewayError,
	}
}

// RemovalReasons lists every reason an entry leaves the pool, so the
// documentation's list of them is held to this one rather than to someone's
// memory: a reason added here without a word about it fails the build.
func RemovalReasons() []string {
	return []string{
		ReasonEvicted, ReasonIdle, ReasonUnloaded, ReasonAbandoned,
		ReasonLoadFailed, ReasonCrashed, ReasonShutdown,
	}
}

// Record is one request, as counts and timings.
//
// Every field is a number, a fixed class name, or the repo id of a model this
// Mac holds. There is deliberately nothing here that a client can write.
type Record struct {
	// Model is the repo id the request resolved to, empty when it was refused
	// before it resolved to one. It is never the name the client sent.
	Model string `json:"model"`
	// At is when the request arrived, in whole UTC seconds.
	At int64 `json:"at"`
	// Class is how it ended.
	Class Class `json:"class"`
	// Streamed reports whether the client asked for the answer as a stream.
	Streamed bool `json:"streamed"`
	// PromptTokens and CompletionTokens are the model server's own counts,
	// zero for any request that did not finish.
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	// FirstTokenMS is how long the client waited for the first streamed chunk,
	// or NoFirstToken when there was not one.
	FirstTokenMS int64 `json:"first_token_ms"`
	// DurationMS is the whole request, from the moment it arrived.
	DurationMS int64 `json:"duration_ms"`
	// QueueWaitMS is how long it waited for a slot on a model already loaded;
	// LoadWaitMS how long it waited for the model to load.
	QueueWaitMS int64 `json:"queue_wait_ms"`
	LoadWaitMS  int64 `json:"load_wait_ms"`
}

// RecordFields lists a record's fields in the order they are declared, under
// the names they are written down as. The documentation's field table is held
// to this list, so a field added here without a word about it on the page
// fails the build.
func RecordFields() []string { return jsonFields(Record{}) }

// EventKind distinguishes the two things that happen to a model server as
// against a request.
type EventKind string

const (
	EventLoad    EventKind = "load"
	EventRemoved EventKind = "removed"
)

// Event is a model server loading or leaving, which is what makes a slow
// request explicable: a model that had to be loaded first, or one that was
// evicted and had to come back.
type Event struct {
	At    int64     `json:"at"`
	Model string    `json:"model"`
	Kind  EventKind `json:"kind"`
	// Reason is why the entry was removed, one of the reasons above; empty on
	// a load.
	Reason string `json:"reason,omitempty"`
	// DurationMS is how long a load took; zero on a removal.
	DurationMS int64 `json:"duration_ms,omitempty"`
	// Failed reports a load that never became ready.
	Failed bool `json:"failed,omitempty"`
}

// Store is where records go to outlive the process.
//
// Nothing in this package implements it and nothing here opens a file: the
// durable store is itd-2609061521102742, under adr-2609061610107154, and this
// is the seam it plugs into so that there is one collection path rather than
// two. A recorder built without a store keeps everything in memory and writes
// nowhere, which is what this build does.
//
// It carries all three kinds of record that ADR names: the request lines, the
// load and removal events, and a record of the settings in force. The first
// two the recorder produces itself; the third it does not, and only passes on
// (see Recorder.RecordSettings), so that everything reaching a store has been
// through the one switch rather than through two. A lifecycle — a flush, a
// rotation, a close — belongs to the store that has one and is not on this
// interface.
type Store interface {
	AppendRequest(Record) error
	AppendEvent(Event) error
	AppendSettings(Settings) error
}

// ModelCounters is what one model has done since recording was turned on.
type ModelCounters struct {
	Model    string `json:"model"`
	Requests int    `json:"requests"`
	// ByClass counts how those requests ended.
	ByClass          map[Class]int `json:"by_class"`
	PromptTokens     int64         `json:"prompt_tokens"`
	CompletionTokens int64         `json:"completion_tokens"`
	// Loads counts the times the model server started and became ready;
	// FailedLoads the times it did not.
	Loads       int `json:"loads"`
	FailedLoads int `json:"failed_loads"`
	// Evictions counts only the removals that were evictions — a model taken
	// out to make room for another. An idle reap, an operator's unload and a
	// crash are removals, not evictions.
	Evictions int `json:"evictions"`
	// The last request's figures, which is what a reader comparing two
	// quantisations looks at first.
	LastFirstTokenMS     int64 `json:"last_first_token_ms"`
	LastDurationMS       int64 `json:"last_duration_ms"`
	LastCompletionTokens int   `json:"last_completion_tokens"`
	LastLoadMS           int64 `json:"last_load_ms"`
}

// Rollup is one minute of traffic across every model, which is what the shape
// of a day is drawn from without keeping a day of rows.
type Rollup struct {
	// Minute is the start of the minute, in whole UTC seconds.
	Minute           int64 `json:"minute"`
	Requests         int   `json:"requests"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}

// View is everything the Statistics page draws.
type View struct {
	Enabled  bool            `json:"enabled"`
	Models   []ModelCounters `json:"models"`
	Requests []Record        `json:"requests"`
	Rollups  []Rollup        `json:"rollups"`
	// Store describes the records kept on disk, when there are any to
	// describe. It is filled in by whoever holds the store; the recorder
	// itself neither has one nor knows what is in it.
	Store *StoreStatus `json:"store,omitempty"`
}

// Options configures a Recorder.
type Options struct {
	// Store, when set, is offered every record and event as it is made. Left
	// nil — as it is in this build — nothing is written anywhere.
	Store Store
	// Now is injectable for tests.
	Now func() time.Time
}

// Recorder holds the live view.
//
// Everything it holds is bounded: RingSize requests, one counter set per model
// this Mac has served, and RollupWindow of minute buckets. It is written to
// from every request goroutine and read from the panel's, so every method
// takes the lock and returns.
type Recorder struct {
	now   func() time.Time
	store Store

	mu      sync.Mutex
	enabled bool
	// ring is a fixed slice used as a circular buffer: next is where the
	// following record goes, and full says whether the buffer has wrapped.
	ring    []Record
	next    int
	full    bool
	models  map[string]*ModelCounters
	rollups map[int64]*Rollup
}

// New builds a recorder. It starts switched off, whatever the configuration
// says, and is turned on by SetConfig once the configuration has been read.
func New(opts Options) *Recorder {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	r := &Recorder{now: opts.Now, store: opts.Store}
	r.clearLocked()
	return r
}

// SetEnabled turns recording on or off. Turning it off empties the live view
// as well as stopping new records, so the panel returns to showing nothing
// worked out from a request and "off" is the same state as a fresh start.
// Turning it on when it is already on changes nothing.
func (r *Recorder) SetEnabled(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if on == r.enabled {
		return
	}
	r.enabled = on
	if !on {
		r.clearLocked()
		return
	}
	r.ring = make([]Record, RingSize)
}

// RecordSettings passes a record of the settings in force to the store, when
// there is one and recording is on.
//
// The recorder does not produce this record and keeps nothing from it: the
// settings in force are the composition root's to know. It goes through the
// recorder all the same, so that the switch that decides whether anything is
// recorded is asked exactly once, in one place, about all three kinds of
// record.
func (r *Recorder) RecordSettings(set Settings) {
	if r == nil || r.store == nil || !r.Enabled() {
		return
	}
	_ = r.store.AppendSettings(set)
}

// Clear empties everything the recorder holds without switching it off, which
// is what the panel's Clear does to the live view while the store clears the
// files beside it.
func (r *Recorder) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled {
		return
	}
	r.clearLocked()
	r.ring = make([]Record, RingSize)
}

// Enabled reports whether requests are being recorded.
func (r *Recorder) Enabled() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enabled
}

// clearLocked empties everything the recorder holds, down to the ring itself:
// while recording is off there is nothing to hold, so there is nothing
// allocated to hold it in. Callers must hold r.mu.
func (r *Recorder) clearLocked() {
	r.ring = nil
	r.next = 0
	r.full = false
	r.models = map[string]*ModelCounters{}
	r.rollups = map[int64]*Rollup{}
}

// Add records one finished request. A record that carries no arrival time is
// stamped with the recorder's, so a caller with nothing better to say than
// "now" does not have to invent one.
func (r *Recorder) Add(rec Record) {
	if rec.At == 0 {
		rec.At = r.now().UTC().Unix()
	}
	if !r.addLocked(rec) {
		return
	}
	if r.store != nil {
		// A store that cannot write is a broken store, not a broken request:
		// the durable-store intent owns what to do about it. Nothing is
		// logged here, because a log line per failed write would be a second
		// unbounded record of the traffic this one is meant to bound.
		_ = r.store.AppendRequest(rec)
	}
}

// addLocked folds one record into everything the recorder holds, reporting
// whether it was recorded at all. It is its own function so that the lock is
// released by a defer: a panic anywhere in here would otherwise leave the
// mutex held, and every request that followed would block on it forever with
// nothing in any log to say why.
func (r *Recorder) addLocked(rec Record) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled {
		return false
	}
	r.ring[r.next] = rec
	r.next = (r.next + 1) % RingSize
	if r.next == 0 {
		r.full = true
	}

	m := r.modelLocked(rec.Model)
	m.Requests++
	m.ByClass[rec.Class]++
	m.PromptTokens += int64(rec.PromptTokens)
	m.CompletionTokens += int64(rec.CompletionTokens)
	m.LastFirstTokenMS = rec.FirstTokenMS
	m.LastDurationMS = rec.DurationMS
	m.LastCompletionTokens = rec.CompletionTokens

	// Bucketed by when the request arrived, which is what the row itself is
	// stamped with: a request that spans a minute boundary must not land in a
	// bucket its own row disagrees with.
	bucket := r.bucketLocked(time.Unix(rec.At, 0).UTC())
	bucket.Requests++
	bucket.PromptTokens += int64(rec.PromptTokens)
	bucket.CompletionTokens += int64(rec.CompletionTokens)
	return true
}

// LoadStarted notes that a model server is being started. It is the pool's
// call, made outside the pool's lock.
func (r *Recorder) LoadStarted(model string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled {
		return
	}
	r.modelLocked(model)
}

// LoadFinished notes that a model server finished loading, or failed to.
func (r *Recorder) LoadFinished(model string, took time.Duration, err error) {
	ev := Event{
		At:         r.now().UTC().Unix(),
		Model:      model,
		Kind:       EventLoad,
		DurationMS: took.Milliseconds(),
		Failed:     err != nil,
	}

	if !r.loadFinishedLocked(model, ev) {
		return
	}
	if r.store != nil {
		_ = r.store.AppendEvent(ev)
	}
}

func (r *Recorder) loadFinishedLocked(model string, ev Event) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled {
		return false
	}
	m := r.modelLocked(model)
	if ev.Failed {
		m.FailedLoads++
	} else {
		m.Loads++
		m.LastLoadMS = ev.DurationMS
	}
	return true
}

// Removed notes that a model server left the pool, and why. Only
// ReasonEvicted counts as an eviction.
func (r *Recorder) Removed(model, reason string) {
	ev := Event{At: r.now().UTC().Unix(), Model: model, Kind: EventRemoved, Reason: reason}

	if !r.removedLocked(model, reason) {
		return
	}
	if r.store != nil {
		_ = r.store.AppendEvent(ev)
	}
}

func (r *Recorder) removedLocked(model, reason string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled {
		return false
	}
	if reason == ReasonEvicted {
		r.modelLocked(model).Evictions++
	} else {
		r.modelLocked(model)
	}
	return true
}

// modelLocked returns the counters for a model, creating them on first sight.
// Callers must hold r.mu.
func (r *Recorder) modelLocked(model string) *ModelCounters {
	m, ok := r.models[model]
	if !ok {
		m = &ModelCounters{Model: model, ByClass: map[Class]int{}, LastFirstTokenMS: NoFirstToken}
		r.models[model] = m
	}
	return m
}

// bucketLocked returns the rollup for the minute now falls in, dropping any
// bucket that has fallen out of the window. Callers must hold r.mu.
func (r *Recorder) bucketLocked(now time.Time) *Rollup {
	minute := now.UTC().Truncate(time.Minute).Unix()
	if b, ok := r.rollups[minute]; ok {
		return b
	}
	// Only a new minute is worth sweeping for stale ones: within a minute the
	// set cannot have changed, and sweeping on every request would walk up to
	// 1,440 entries under the lock for nothing.
	oldest := minute - int64(RollupWindow/time.Second)
	for k := range r.rollups {
		if k <= oldest {
			delete(r.rollups, k)
		}
	}
	b := &Rollup{Minute: minute}
	r.rollups[minute] = b
	return b
}

// Summary is the per-model counters, for the state snapshot the panel already
// receives. It is the whole of what recording adds to that snapshot: the ring
// and the rollups are fetched from the Statistics page instead, because the
// snapshot is re-encoded and redrawn every couple of seconds.
func (r *Recorder) Summary() []ModelCounters {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.summaryLocked()
}

func (r *Recorder) summaryLocked() []ModelCounters {
	if !r.enabled {
		return nil
	}
	out := make([]ModelCounters, 0, len(r.models))
	for _, m := range r.models {
		c := *m
		c.ByClass = make(map[Class]int, len(m.ByClass))
		for k, v := range m.ByClass {
			c.ByClass[k] = v
		}
		out = append(out, c)
	}
	sortModels(out)
	return out
}

// View is everything the Statistics page draws, oldest request first.
func (r *Recorder) View() View {
	if r == nil {
		return View{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled {
		return View{}
	}
	return View{
		Enabled:  true,
		Models:   r.summaryLocked(),
		Requests: r.requestsLocked(),
		Rollups:  r.rollupsLocked(),
	}
}

// requestsLocked reads the ring out in the order the requests were recorded.
// Callers must hold r.mu.
func (r *Recorder) requestsLocked() []Record {
	n := r.next
	if !r.full {
		return append(make([]Record, 0, n), r.ring[:n]...)
	}
	out := make([]Record, 0, RingSize)
	out = append(out, r.ring[n:]...)
	return append(out, r.ring[:n]...)
}

// rollupsLocked returns the minute buckets in time order. Callers must hold r.mu.
func (r *Recorder) rollupsLocked() []Rollup {
	out := make([]Rollup, 0, len(r.rollups))
	for _, b := range r.rollups {
		out = append(out, *b)
	}
	sortRollups(out)
	return out
}
