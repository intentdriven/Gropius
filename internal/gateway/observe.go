package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
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
	// first is when the first streamed event carrying a choice was read off
	// the model server, zero when there was none. It is read rather than
	// written because a write that then fails is not a first token the client
	// ever saw, and the difference between the two moments is a memcpy.
	first time.Time
	// delivered records that the relay ran and reported neither side cutting
	// the answer short — that is, the whole answer was written and the model
	// server's body was read to its end.
	delivered bool
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
	// and the status line has already gone out saying it was. Which of the two
	// it was is read off the side that actually failed, not off a cancellation
	// that may or may not have been delivered yet.
	o.delivered = !out.clientGone && !out.upstreamCut
	if o.record.Class != stats.ClassOK {
		return
	}
	switch {
	case out.clientGone:
		o.record.Class = stats.ClassCancelled
	case out.oversizeLine:
		// Ahead of upstreamCut, which is set with it: the model server was
		// reachable and was answering, and the answer ended because Gropius
		// stopped reading at a limit Gropius chose. Filing that as
		// "unreachable" would send an operator reading the statistics after
		// the wrong piece of software.
		o.record.Class = stats.ClassGatewayError
	case out.upstreamCut:
		o.record.Class = stats.ClassUnreachable
	}
}

// finish records the request.
//
// The cancelled class is decided by what was observed, in that order: the
// relay's own verdict first, and the request's context only where the relay
// has nothing to say. A client that reads to the end of the answer and closes
// its socket without draining makes Go cancel this handler's context while
// this very function is running, so a context error on its own does not mean
// the client missed anything — and a request whose whole answer was delivered
// must not be filed as abandoned with its token counts thrown away, which
// would leave its model's totals short by exactly the requests that went best.
//
// Token counts are dropped for anything but a completed answer: a partial
// count read off an abandoned stream is a number that means nothing and would
// be averaged in as if it did.
func (o *observation) finish(r *http.Request) {
	if o == nil {
		return
	}
	if !o.delivered && r.Context().Err() != nil {
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
// line: whether the answer was cut short and by which side, when the first
// chunk was relayed, and the token counts if the answer carried them.
//
// The two ways an answer stops early are kept apart because they are opposite
// facts about the same request, and getting them the wrong way round is the
// one error that would make this feature actively misleading. The relay knows
// which side failed — a failed read is the model server, a failed write is the
// client — so it says, rather than leaving the class to be inferred from a
// cancellation that arrives on another goroutine whenever it is scheduled.
type relayOutcome struct {
	// upstreamCut is a read from the model server that failed part-way.
	upstreamCut bool
	// clientGone is a write to the client that failed part-way.
	clientGone bool
	// oversizeLine is an upstream line that reached maxStreamLine without a
	// newline, which is the one way the relay itself ends an answer. It is a
	// fact about this Gropius rather than about either side: it is logged once
	// by the caller, and it is what the answer is recorded under, because
	// upstreamCut — which is set with it, the answer having stopped part-way —
	// would file Gropius's own limit as the model server failing.
	oversizeLine bool
	firstToken   time.Time
	usage        *usageCounts
}

// relayOptions tell the relay what the observer needs and what the client
// asked for.
//
// Both fields are false in the zero value, and the zero value has to be the
// harmless one: removing an event is the destructive act here, so it is the
// one that has to be asked for by name. A caller that forgets these entirely
// relays exactly what the model server sent.
type relayOptions struct {
	// observing turns on the reading the relay does for the recorder: when the
	// first chunk went out, and the counts in the usage event.
	observing bool
	// dropUsage removes the usage-only event from the answer. It is set only
	// when Gropius asked the model server for that event on a client's behalf
	// and the client did not ask for it itself.
	dropUsage bool
}

// streamOptionsField is the request field that decides whether a streamed
// answer ends with the token counts.
const streamOptionsField = "stream_options"

// includeUsageField is the one key inside it that Gropius ever writes.
const includeUsageField = "include_usage"

// clientWantsUsage reports whether the client's own request asked the model
// server for the token counts. What it asked for is what it gets back.
//
// The question is decided the way the model server decides it, not the way Go
// would: the pinned server reads the value for its truth in Python, where 1
// and "true" are as true as true is. Reading it as a Go bool would answer
// "the client did not ask" for a request the model server is about to honour,
// and the client would then lose the event it wrote that field to get. Every
// value that is not plainly a refusal therefore counts as asking, because
// keeping an event the client may not want is a smaller wrong than removing
// one it does.
func clientWantsUsage(payload map[string]json.RawMessage) bool {
	raw, ok := payload[streamOptionsField]
	if !ok {
		return false
	}
	var opts map[string]json.RawMessage
	if err := json.Unmarshal(raw, &opts); err != nil {
		// Not an object: the model server will refuse this request, and
		// nothing was merged into it. Nothing to remove either way.
		return true
	}
	value, ok := opts[includeUsageField]
	if !ok {
		return false
	}
	return truthy(value)
}

// truthy reports whether a JSON value is one the pinned model server would
// act on, following Python's own rule: false, null, zero and the empty string
// or collection are the refusals, and everything else is a yes.
//
// A number is decided by its value, not by how it was written down: 0, 0.0,
// -0.0, 0e0 and 1e-400 all reach Python as zero and are all refusals there,
// and matching them as text would have called four of the five a yes.
func truthy(raw json.RawMessage) bool {
	trimmed := string(bytes.TrimSpace(raw))
	if n, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return n != 0
	}
	switch trimmed {
	case "false", "null", `""`, "[]", "{}", "":
		return false
	}
	return true
}

// streamRequested reports whether the client asked for a streamed answer.
//
// Decided by the model server's rule, exactly as include_usage is: the pinned
// server tests the field's truth in Python, so a client that sent 1 or "true"
// is streamed and several OpenAI SDK wrappers send precisely that. Reading it
// as a Go bool called those requests unstreamed, so nothing asked for their
// token counts and every one of them was recorded as costing nothing.
func streamRequested(payload map[string]json.RawMessage) bool {
	raw, ok := payload["stream"]
	if !ok {
		return false
	}
	return truthy(raw)
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
