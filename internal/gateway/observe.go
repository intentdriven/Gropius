package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/intentdriven/Gropius/internal/runtime"
	"github.com/intentdriven/Gropius/internal/stats"
)

// observation accumulates what is known about one request as it is served, and
// hands it to the recorder when the request ends.
//
// A nil observation is the switched-off case, and every method below tolerates
// one. That is deliberate: it means handleCompletions carries no branch on
// whether recording is on, so the path a request takes with the switch off is
// the path it took before any of this existed.
//
// It holds no part of the request. The model name it carries is the repo id
// the registry resolved, never the string the client sent — a client-chosen
// name would be both unbounded and the client's own text, and the recorder
// takes neither.
type observation struct {
	rec     *stats.Recorder
	started time.Time
	record  stats.Record
	// first is when the first streamed event carrying a choice reached the
	// client, zero when there was none.
	first time.Time
}

// observe starts an observation when the operator has recording on, and
// returns nil when they have not.
func (g *Gateway) observe(on bool, started time.Time) *observation {
	if !on || g.stats == nil {
		return nil
	}
	return &observation{
		rec:     g.stats,
		started: started,
		record:  stats.Record{At: started.UTC().Unix(), Class: stats.ClassOK, FirstTokenMS: stats.NoFirstToken},
	}
}

// recording reports whether anything is being observed, for the few places
// that must not do work when nothing is.
func (o *observation) recording() bool { return o != nil }

// failed names how the request ended. The last word wins, so a handler may
// call it as it goes and the class is whatever the request actually did.
func (o *observation) failed(c stats.Class) {
	if o == nil {
		return
	}
	o.record.Class = c
}

// resolved names the model, once the registry has said which one it is.
func (o *observation) resolved(model string) {
	if o == nil {
		return
	}
	o.record.Model = model
}

// streaming notes that the client asked for the answer as a stream.
func (o *observation) streaming(yes bool) {
	if o == nil {
		return
	}
	o.record.Streamed = yes
}

// waited records what the pool said the request spent getting to a model.
func (o *observation) waited(w runtime.AcquireStats) {
	if o == nil {
		return
	}
	o.record.LoadWaitMS = w.LoadWait.Milliseconds()
	o.record.QueueWaitMS = w.QueueWait.Milliseconds()
}

// relayed folds in what the relay saw: when the first chunk went out and what
// the model server said the request cost.
func (o *observation) relayed(out relayOutcome) {
	if o == nil {
		return
	}
	if !out.firstToken.IsZero() {
		o.first = out.firstToken
	}
	if out.usage != nil {
		o.record.PromptTokens = out.usage.Prompt
		o.record.CompletionTokens = out.usage.Completion
	}
	// A 200 whose body then stopped part-way is not a request that went well,
	// and the status line has already gone out saying it was. Recorded as the
	// model server not answering, which is what happened; a client that went
	// away instead is caught in finish, where cancellation wins over
	// everything.
	if out.truncated && o.record.Class == stats.ClassOK {
		o.record.Class = stats.ClassUnreachable
	}
}

// finish records the request.
//
// A client that went away is recorded as having gone away whatever else
// happened, because that is the observable outcome: the status line was sent
// long before, so nothing else the handler saw describes it. Token counts are
// dropped for anything but a completed answer — a partial count read off an
// abandoned stream is a number that means nothing and would be averaged in as
// if it did.
func (o *observation) finish(r *http.Request) {
	if o == nil {
		return
	}
	if r.Context().Err() != nil {
		o.record.Class = stats.ClassCancelled
	}
	if o.record.Class != stats.ClassOK {
		o.record.PromptTokens = 0
		o.record.CompletionTokens = 0
	}
	if !o.first.IsZero() {
		o.record.FirstTokenMS = o.first.Sub(o.started).Milliseconds()
	}
	o.record.DurationMS = time.Since(o.started).Milliseconds()
	o.rec.Add(o.record)
}

// usageCounts is the model server's own count of what a request cost.
type usageCounts struct {
	Prompt     int
	Completion int
}

// relayOutcome is what relaying the answer revealed, over and above the status
// line: whether the answer was cut short, when the client saw the first chunk,
// and the token counts if the answer carried them.
type relayOutcome struct {
	// truncated is a streamed answer that stopped part-way: the model server
	// went away mid-generation, or the client did. Only the streaming relay
	// reports it, because it is the only one that can tell.
	truncated  bool
	firstToken time.Time
	usage      *usageCounts
}

// relayOptions tell the relay what the observer needs and what the client
// asked for. Both are false with the switch off, which is the relay's original
// behavior exactly.
type relayOptions struct {
	// observing turns on the reading the relay does for the recorder: when the
	// first chunk went out, and the counts in the usage event.
	observing bool
	// keepUsage forwards the usage-only event to the client. It is true only
	// when the client's own request asked for it; otherwise the event is
	// Gropius's business and is removed before the answer is relayed.
	keepUsage bool
}

// streamOptionsField is the request field that decides whether a streamed
// answer ends with the token counts.
const streamOptionsField = "stream_options"

// includeUsageField is the one key inside it that Gropius ever writes.
const includeUsageField = "include_usage"

// clientWantsUsage reports whether the client's own request asked the model
// server for the token counts. What it asked for is what it gets back.
func clientWantsUsage(payload map[string]json.RawMessage) bool {
	raw, ok := payload[streamOptionsField]
	if !ok {
		return false
	}
	var opts struct {
		IncludeUsage bool `json:"include_usage"`
	}
	if err := json.Unmarshal(raw, &opts); err != nil {
		return false
	}
	return opts.IncludeUsage
}

// streamRequested reports whether the client asked for a streamed answer.
func streamRequested(payload map[string]json.RawMessage) bool {
	raw, ok := payload["stream"]
	if !ok {
		return false
	}
	var streamed bool
	if err := json.Unmarshal(raw, &streamed); err != nil {
		return false
	}
	return streamed
}

// mergeIncludeUsage asks the model server for the token counts, by setting the
// one key inside the client's stream_options and leaving everything else in it
// as it was.
//
// The key is always written rather than assumed: the pinned model server reads
// stream_options["include_usage"] directly, so an object that does not carry
// it raises there and the client sees a 500 for a field it wrote itself.
//
// A stream_options that is neither absent, null, nor an object is left exactly
// as the client sent it. That request is already one the model server will
// refuse, and refusing it is the model server's business — repairing a client's
// malformed field to get a figure for the panel is not a trade Gropius makes.
func mergeIncludeUsage(payload map[string]json.RawMessage) {
	opts := map[string]json.RawMessage{}
	if raw, ok := payload[streamOptionsField]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &opts); err != nil {
			return
		}
	}
	opts[includeUsageField] = json.RawMessage("true")
	merged, err := json.Marshal(opts)
	if err != nil {
		return
	}
	payload[streamOptionsField] = merged
}

// classifyAcquireError names the outcome of a pool refusal by what the client
// is about to be told and what actually went wrong.
func classifyAcquireError(err error) stats.Class {
	var launch *runtime.LaunchError
	var notReady *runtime.NotReadyError
	switch {
	case errors.As(err, &launch):
		return stats.ClassLaunchFailed
	case errors.As(err, &notReady):
		return stats.ClassNotReady
	case errors.Is(err, runtime.ErrBusy):
		return stats.ClassBusy
	default:
		return stats.ClassRefused
	}
}
