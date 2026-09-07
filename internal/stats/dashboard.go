package stats

import (
	"context"
	"math"
	"slices"
	"sort"
	"time"
)

// The usage dashboard's arithmetic (itd-2609061521159233).
//
// Everything the panel's four historical tables show is worked out here, in
// one pass over the durable store, and handed to the control plane as sums,
// counts and buckets. The browser never sees a record: it is given the
// aggregate, so the only strings that reach it are the repo ids of models this
// Mac holds.
//
// It records nothing. Reading the store flushes what the writer has buffered
// and publishes the count of lines the read could not use, which are the
// store's own housekeeping; no record is made here and none is changed. The
// dashboard can therefore only show what was already recorded under the opt-in
// adr-2609061503319212 requires, which is what makes "nothing appears that was
// not recorded" a property of the code rather than a promise.

// The bounds one aggregate is held to, so that the panel stays responsive on a
// store at its size cap and a reader can be told what the figures cover.
//
// MaxHistoryRecords is the count that matters: a store at the default 200 MB
// cap holds about 900,000 records (BenchmarkLatestAtTheCap), which one
// sequential pass reads and aggregates in 1.4 s on an Apple M4 Max. A million
// leaves room above that cap while keeping the pass bounded on a store whose
// cap has been raised; past it the aggregate stops and says it stopped.
//
// MaxHistoryDays bounds the window instead of the reading, because the day
// table has a row per day per model: a range of years would be a table nobody
// scrolls, drawn from records the store's retention has mostly dropped.
const (
	MaxHistoryRecords = 1_000_000
	MaxHistoryDays    = 366
)

// historyRecordBound is the record bound the pass actually applies. It is the
// constant above; it is a variable so that a test can lower it and watch the
// pass stop and say it stopped, which a test cannot do against a million
// records without writing a million records.
var historyRecordBound = MaxHistoryRecords

// MaxHistoryRows bounds the tokens-per-day table, and with it everything the
// pass holds in memory that the record bound does not.
//
// The record bound caps the timings, which are one number per record. It does
// not cap the tables, which are one row per day per model: a store whose model
// ids were all different would be a million rows and a response body to match.
// Nothing this Mac writes can produce that — a model id is the name of a
// directory the registry resolved — but a store is a directory of plain files
// that outlives the build that wrote it, and a reader of one must be bounded by
// its own arithmetic rather than by a promise about what wrote it. A year of
// days against fifty-odd models is comfortably inside this.
const MaxHistoryRows = 20_000

// historyRowBound is the row bound the pass actually applies, a variable for
// the same reason historyRecordBound is one: a test that had to reach twenty
// thousand distinct models to watch the bound work would be a test of the
// fixture rather than of the bound.
var historyRowBound = MaxHistoryRows

// There is deliberately no early stop.
//
// A pass that gave up on meeting a record older than the range would be
// reading by an order the store does not have: a record is appended when its
// request finishes and stamped with when it arrived, so the file is in
// completion order and the scan is against arrival times. Bounding the skew
// needs a longest-request figure, and the gateway has none to offer — the read
// deadline is cleared before the model request, so a generation is unbounded by
// construction (internal/gateway/gateway.go), and a clock stepped backwards by
// sleep or by NTP puts a record days in the past whatever the timeouts say.
// Every margin that could be chosen is a guess, and the failure it buys is the
// silent one this design spends most of its care avoiding: a month drawn short
// at whichever record happened to be odd, reading exactly like a quiet month.
//
// So the pass reads to the end of what the bounds allow, and the bounds are
// what hold the cost: at most historyRecordBound records and historyRowBound
// rows, with at most two passes at a time (internal/gateway). One pass over a
// store at its size cap is 1.4 s, which is the whole of the range every time
// rather than only when the range is wide — the price of an answer that is
// either complete or says it is not.

// FirstTokenBucketEdgesMS are the boundaries of the time-to-first-token
// histogram, in milliseconds. There is a bucket below the first edge and one
// above the last, so the table has one column more than there are edges.
//
// They are the tenfold spread a local model actually shows: a small model
// answering from a warm server is under 250 ms, one that had to be loaded is
// seconds, and everything past ten seconds is one column because the
// difference between twenty seconds and forty is not a difference a reader
// acts on.
var FirstTokenBucketEdgesMS = []int64{100, 250, 500, 1000, 2500, 5000, 10000}

// DayTokens is one model's tokens on one local day.
type DayTokens struct {
	// Day is the local calendar day, as YYYY-MM-DD.
	Day      string `json:"day"`
	Model    string `json:"model"`
	Requests int    `json:"requests"`
	// PromptTokens and CompletionTokens are the model server's own counts.
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	// FromSummary marks a row some or all of whose figures come from the
	// coarse per-model per-day summary the store writes before retention drops
	// a day's own records (itd-2609061602043757), rather than from the records
	// themselves. It is false throughout while nothing writes those summaries.
	//
	// It is a row's property rather than a day's because the fold is additive:
	// on the day where the records run out, a row can carry both the records
	// still held and the summary of the ones that have gone, and a table that
	// could not say which of its rows are partly coarse would quietly mix the
	// two.
	FromSummary bool `json:"from_summary,omitempty"`
	// Partial marks the range's oldest day, when the range starts part-way
	// through it. A reader picks "the last thirty days" at four in the
	// afternoon, so the oldest day holds only the traffic after four — a third
	// of it, drawn beside whole days as though it were one, and read as a quiet
	// day. It is the row's own figure that is short, and the row says so.
	//
	// The newest day is short in the same arithmetic and not marked: it is
	// today, up to now, which is what a reader already takes it for.
	Partial bool `json:"partial,omitempty"`
}

// Tokens is the day's whole traffic for that model, in and out.
func (d DayTokens) Tokens() int64 { return addTokens64(d.PromptTokens, d.CompletionTokens) }

// ModelShare is one model's part of the whole range: what it served, and what
// fraction of the range's tokens that came to.
type ModelShare struct {
	Model            string `json:"model"`
	Requests         int    `json:"requests"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	Tokens           int64  `json:"tokens"`
	// Share is Tokens over the range's total tokens, between 0 and 1. It is a
	// fraction rather than a rounded percentage because rounding is the
	// panel's to do, and a table of percentages that does not add up to a
	// hundred is read as an error in the figures.
	Share float64 `json:"share"`
}

// Percentiles are the three points of a distribution the table shows. They are
// nearest-rank over the requests in the range: the p50 of ten values is the
// fifth of them in order, not an interpolation between the fifth and the
// sixth, so every figure shown is a figure some request actually produced.
type Percentiles struct {
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P99 float64 `json:"p99"`
}

// ModelLatency is how long one model's requests took over the range.
type ModelLatency struct {
	Model string `json:"model"`
	// Requests is how many requests the figures rest on: answers that streamed
	// a first token. A refusal has no latency to distribute and a request that
	// never streamed has no first token, so neither is here — which is why
	// this count is usually smaller than the model's own request count.
	Requests     int         `json:"requests"`
	FirstTokenMS Percentiles `json:"first_token_ms"`
	// Rate is generated tokens a second, counted the way the panel's live view
	// counts it: the tokens after the first over the time spent generating
	// them. Unweighted, so a model's median request is the one in the middle
	// of its requests rather than of its tokens.
	Rate Percentiles `json:"rate"`
	// RateRequests is how many answers the rate figures rest on, which is not
	// Requests: an answer of a single token has no rate, and neither has one
	// that spent no measurable time generating. A model mostly asked for
	// one-word answers would otherwise show a rate over a handful of requests
	// under a count of hundreds.
	RateRequests int `json:"rate_requests"`
	// FirstTokenBuckets counts the requests falling between
	// FirstTokenBucketEdgesMS, with one bucket below the first edge and one
	// above the last.
	FirstTokenBuckets []int `json:"first_token_buckets"`
}

// HourCounts is one hour of the local day, across every model.
type HourCounts struct {
	Hour int `json:"hour"`
	// Evictions counts only the removals that were evictions — a model taken
	// out to make room for another. An idle reap, an operator's unload, a
	// crash and a shutdown are removals, and counting them here would say the
	// memory budget is thrashing when nothing of the kind happened.
	Evictions int `json:"evictions"`
	// Loads counts every model server started in the hour, whether or not it
	// became ready: the cost a reader is looking for is the loading, which a
	// server that failed still spent.
	Loads int `json:"loads"`
}

// History is everything the dashboard's four tables draw.
type History struct {
	// Enabled is filled in by whoever serves this: the aggregation itself has
	// no opinion about the switch, and a store holds what it holds.
	Enabled bool `json:"enabled"`
	// From and To are the range actually aggregated, in whole UTC seconds —
	// which is the range asked for, narrowed to MaxHistoryDays if it was
	// wider.
	From int64 `json:"from"`
	To   int64 `json:"to"`
	// Zone names the location the days and hours were counted in, so a reader
	// can tell what "Tuesday" meant.
	Zone string `json:"zone"`

	// Days is newest day first, and within a day the model that served the
	// most tokens first.
	Days []DayTokens `json:"days"`
	// Models is the range's totals, largest share first.
	Models []ModelShare `json:"models"`
	// Latency is one row per model that answered, largest number of timed
	// requests first.
	Latency []ModelLatency `json:"latency"`
	// Hours is always the whole 24, in order, so the table has its rows
	// whether or not anything happened in them.
	Hours []HourCounts `json:"hours"`
	// FirstTokenBucketEdgesMS are the histogram's own edges, sent with the
	// counts so the table's column headings are drawn from the figures rather
	// than from a copy of the edges kept in the panel that could drift from
	// them.
	FirstTokenBucketEdgesMS []int64 `json:"first_token_bucket_edges_ms"`

	// Records is how many records the pass read, in the range or out of it.
	Records int `json:"records"`
	// Skipped is how many lines could not be used — one is the ordinary cost of
	// a crash, a great many mean the figures rest on a fraction of what was
	// recorded.
	//
	// It is this pass's own count, not the store's running one: two readings at
	// once would otherwise each report the other's figure, and a reader shown
	// "so many lines could not be read" deserves the number from the reading in
	// front of them.
	Skipped int64 `json:"skipped"`
	// Truncated reports a pass that a bound stopped: the record bound or the
	// row bound. On its own it does not mean the figures are short of the
	// range — a pass reading back past the range's start can meet a bound
	// afterwards — which is what ReachedStart is for.
	Truncated bool `json:"truncated"`
	// ReachedStart reports a pass that read a record older than the range, and
	// so covered the whole of it. False means the store simply does not go back
	// that far, or that a bound stopped the pass before it got there; the two
	// are told apart by Truncated, and only the second is a figure drawn short.
	ReachedStart bool `json:"reached_start"`
	// Narrowed reports a range that was wider than MaxDays and was cut to it.
	Narrowed bool `json:"narrowed"`
	// MaxRecords and MaxDays are the bounds themselves, so the panel says what
	// it was held to rather than repeating a number that could drift from it.
	MaxRecords int `json:"max_records"`
	MaxDays    int `json:"max_days"`
}

// MaxHistoryBytes is the most file content one pass reads.
//
// The line bound below already bounds the decoding, which is what the time goes
// on. This bounds the reading itself, for the store that is mostly one enormous
// line: a file is read whole before any line of it is looked at, so without a
// budget across files a pass over a store whose size cap has been raised is a
// pass over all of it. Half a gigabyte is twice the default cap and comfortably
// past anything a pass of a million lines can want.
const MaxHistoryBytes = 512 << 20

// RecordSource is the store as the dashboard reads it: newest record first,
// bounded in what it costs rather than only in what it yields, and stoppable.
//
// It is an interface so that the aggregation depends on the reading rather
// than on the FileStore, and so a caller with no store at all — a Mac where
// recording has never been on — passes nil and gets an empty view.
type RecordSource interface {
	Read(ctx context.Context, opts ReadOptions, fn func(Line) bool) (ReadStats, error)
}

// Aggregate walks the store once and returns everything the dashboard draws.
//
// One pass, because the three views are sums and counts grouped by model, by
// local day and by local hour, and a second pass would read the same bytes
// again. Newest first, so a range that is a fraction of the store costs a
// fraction of the reading: the pass stops once it is past the range's start.
//
// The location is the Mac's own, not UTC: "most evictions fall in one hour of
// the day" is a claim about the hours a person keeps, and a day boundary drawn
// in UTC would put a late-evening request on tomorrow.
func Aggregate(ctx context.Context, src RecordSource, from, to time.Time, loc *time.Location) (History, error) {
	if loc == nil {
		loc = time.UTC
	}
	// The zone's own abbreviation at the end of the range, rather than the
	// location's name: a Mac's location is "Local", which tells a reader
	// nothing about which day a row is on.
	zone, _ := to.In(loc).Zone()
	if zone == "" {
		zone = loc.String()
	}
	h := History{
		Zone:  zone,
		Hours: make([]HourCounts, 24),
		// Cloned: the package's own slice is what bucketOf reads, and a caller
		// that mutated the one it was handed would change every aggregation
		// that followed.
		FirstTokenBucketEdgesMS: slices.Clone(FirstTokenBucketEdgesMS),
		MaxRecords:              historyRecordBound,
		MaxDays:                 MaxHistoryDays,
	}
	for i := range h.Hours {
		h.Hours[i].Hour = i
	}
	// By subtraction rather than by date arithmetic on `to`: a caller naming
	// an absurd end date would make `to.AddDate(...)` wrap, and a comparison
	// against a wrapped value narrows nothing while reporting that it did.
	if widest := time.Duration(MaxHistoryDays) * 24 * time.Hour; to.Sub(from) > widest {
		from, h.Narrowed = to.Add(-widest), true
	}
	h.From, h.To = from.Unix(), to.Unix()
	if src == nil {
		return h, nil
	}

	a := newHistoryAgg(ctx, h.From, h.To, loc)
	a.partialDay = clippedDay(from, loc)
	// Bounded in lines rather than in records, because those are different
	// numbers the moment a line does not parse — a store a newer Gropius wrote,
	// or one a crash tore, yields nothing while costing every byte of itself —
	// and the context goes to the reader for the same reason: the check in take
	// never runs on a store this build cannot read a line of.
	read, err := src.Read(ctx, ReadOptions{
		MaxLines: historyRecordBound,
		MaxBytes: MaxHistoryBytes,
	}, a.take)
	if a.err != nil {
		return h, a.err
	}
	h.Records, h.ReachedStart = a.read, a.reachedStart
	h.Truncated = a.truncated || read.Bounded
	h.Skipped = read.Skipped
	if err != nil {
		return h, err
	}
	a.fill(&h)
	return h, nil
}

// dayKey identifies one cell of the tokens-per-day table.
type dayKey struct {
	day   string
	model string
}

// latencySamples are one model's timings, kept until the pass is over because
// a percentile cannot be worked out from a running total.
//
// The timings are bounded by the record bound: at most one first-token figure
// and one rate for each record the pass reads, however they are spread across
// models. What the record bound does not bound is how many models there are to
// spread them across, which MaxHistoryRows does.
type latencySamples struct {
	firstToken []int64
	rate       []float64
	buckets    []int
}

// historyAgg is the state of one pass.
type historyAgg struct {
	ctx      context.Context
	from, to int64
	loc      *time.Location
	// partialDay is the local day the range starts part-way through, or empty
	// when it starts at a midnight.
	partialDay string
	// reachedStart records that a record older than the range has been read,
	// which is how the pass knows it has covered the whole of it rather than
	// run out of store.
	reachedStart bool

	read      int
	truncated bool
	// err is the reason the pass gave up, when it was not the record bound:
	// the caller going away.
	err error

	days     map[dayKey]*DayTokens
	models   map[string]*ModelShare
	latency  map[string]*latencySamples
	hours    []HourCounts
	total    int64
	dayOrder []dayKey
}

func newHistoryAgg(ctx context.Context, from, to int64, loc *time.Location) *historyAgg {
	a := &historyAgg{
		ctx: ctx, from: from, to: to, loc: loc,
		days:    map[dayKey]*DayTokens{},
		models:  map[string]*ModelShare{},
		latency: map[string]*latencySamples{},
		hours:   make([]HourCounts, 24),
	}
	return a
}

// take folds one line in, reporting whether the pass should carry on.
func (a *historyAgg) take(l Line) bool {
	if a.read >= historyRecordBound || len(a.days) >= historyRowBound {
		a.truncated = true
		return false
	}
	// Checked now and then rather than per record: a pass over a store at its
	// size cap is over a second of work, and a client that has gone away
	// should not be paid for to the end of it. The cost of asking is a channel
	// read, which is why it is not asked a million times.
	if a.read%4096 == 0 && a.ctx != nil {
		if err := a.ctx.Err(); err != nil {
			a.err = err
			return false
		}
	}
	a.read++
	if l.At > a.to {
		// Newer than the range: nothing to count, and nothing to conclude
		// about how far back the pass has walked.
		return true
	}
	if l.At < a.from {
		// Older than the range, and read rather than stopped at: see the note
		// above about why there is no early stop. Meeting one does say the
		// walk has got back past the range's start.
		a.reachedStart = true
		return true
	}

	when := time.Unix(l.At, 0).In(a.loc)
	switch l.Kind {
	case KindRequest:
		a.request(l.Request, when)
	case KindRemoved:
		if l.Event.Reason == ReasonEvicted {
			a.hours[when.Hour()].Evictions++
		}
	case KindLoad:
		a.hours[when.Hour()].Loads++
	}
	return true
}

// addTokens is the one place a day's figures are added up.
//
// Everything the tokens-per-day table and the share column rest on goes
// through here — the day's row, the model's total for the range, and the
// range's own total — so there is one fold rather than three that have to stay
// in step. It is additive and never subtracts, which is what lets a second
// source be folded in on top of the records.
//
// That second source is the coarse per-model per-day summary the store writes
// before retention drops a day's records (itd-2609061602043757). When its
// reader exists, walking it newest day first and calling this with fromSummary
// true is the whole of the change: a day held only as a summary gains its row,
// and the row is marked so a reader is never shown a coarse figure as an exact
// one.
//
// The condition the fold rests on, stated because adding is only right under
// it: a summary covers the records that have gone and no others. Retention
// removes a file rather than a day, so the day where the store's oldest file
// ended has some of its records still held and the rest dropped; a summary
// written over that whole day, rather than over the part of it that was
// dropped, would be added to records this pass has already counted and the day
// would read double — and so would every share, since the range's total takes
// the same figures. That is the summary writer's contract to keep, and this is
// where a change to it would land.
//
// The latency table is deliberately not fed from summaries at all: a summary
// carries mergeable sums and counts, and a percentile cannot be recovered from
// those.
func (a *historyAgg) addTokens(day, model string, requests int, in, out int64, fromSummary bool) {
	key := dayKey{day: day, model: model}
	d, ok := a.days[key]
	if !ok {
		d = &DayTokens{Day: key.day, Model: key.model}
		a.days[key] = d
		a.dayOrder = append(a.dayOrder, key)
	}
	d.Requests += requests
	d.PromptTokens = addTokens64(d.PromptTokens, in)
	d.CompletionTokens = addTokens64(d.CompletionTokens, out)
	d.FromSummary = d.FromSummary || fromSummary

	m, ok := a.models[model]
	if !ok {
		m = &ModelShare{Model: model}
		a.models[model] = m
	}
	m.Requests += requests
	m.PromptTokens = addTokens64(m.PromptTokens, in)
	m.CompletionTokens = addTokens64(m.CompletionTokens, out)
	a.total = addTokens64(a.total, addTokens64(in, out))
}

// addTokens64 adds two token counts without letting them wrap.
//
// The counts come off a file, and only the account that owns the store can
// write that file — so this is not a defence against anyone. It is a defence
// against a table of negative tokens and a share of zero, which is what a hand
// edit or a corrupt line otherwise produces, and which reads as a bug in the
// arithmetic rather than as a bad line in the store. A negative count is
// nothing, and a sum that would overflow stops at the largest number there is.
func addTokens64(a, b int64) int64 {
	if a < 0 {
		a = 0
	}
	if b < 0 {
		b = 0
	}
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// request folds one request record into the day, model and latency figures.
func (a *historyAgg) request(r Record, when time.Time) {
	a.addTokens(when.Format("2006-01-02"), r.Model, 1,
		int64(r.PromptTokens), int64(r.CompletionTokens), false)

	if r.Class != ClassOK || r.FirstTokenMS < 0 {
		return
	}
	s, ok := a.latency[r.Model]
	if !ok {
		s = &latencySamples{buckets: make([]int, len(FirstTokenBucketEdgesMS)+1)}
		a.latency[r.Model] = s
	}
	s.firstToken = append(s.firstToken, r.FirstTokenMS)
	s.buckets[bucketOf(r.FirstTokenMS)]++
	if rate, ok := generationRate(r); ok {
		s.rate = append(s.rate, rate)
	}
}

// fill turns the pass's maps into the ordered tables the panel draws.
func (a *historyAgg) fill(h *History) {
	h.Hours = a.hours
	for i := range h.Hours {
		h.Hours[i].Hour = i
	}

	h.Days = make([]DayTokens, 0, len(a.dayOrder))
	for _, k := range a.dayOrder {
		d := *a.days[k]
		d.Partial = d.Day == a.partialDay
		h.Days = append(h.Days, d)
	}
	// Newest day first, and the day's biggest model first: the order a reader
	// asking "which model does the work" reads down.
	sort.SliceStable(h.Days, func(i, j int) bool {
		if h.Days[i].Day != h.Days[j].Day {
			return h.Days[i].Day > h.Days[j].Day
		}
		if h.Days[i].Tokens() != h.Days[j].Tokens() {
			return h.Days[i].Tokens() > h.Days[j].Tokens()
		}
		return h.Days[i].Model < h.Days[j].Model
	})

	h.Models = make([]ModelShare, 0, len(a.models))
	for _, m := range a.models {
		c := *m
		c.Tokens = addTokens64(c.PromptTokens, c.CompletionTokens)
		if a.total > 0 {
			c.Share = float64(c.Tokens) / float64(a.total)
		}
		h.Models = append(h.Models, c)
	}
	sort.SliceStable(h.Models, func(i, j int) bool {
		if h.Models[i].Tokens != h.Models[j].Tokens {
			return h.Models[i].Tokens > h.Models[j].Tokens
		}
		return h.Models[i].Model < h.Models[j].Model
	})

	h.Latency = make([]ModelLatency, 0, len(a.latency))
	for model, s := range a.latency {
		slices.Sort(s.firstToken)
		slices.Sort(s.rate)
		h.Latency = append(h.Latency, ModelLatency{
			Model:             model,
			Requests:          len(s.firstToken),
			FirstTokenMS:      percentilesOfInts(s.firstToken),
			Rate:              percentilesOf(s.rate),
			RateRequests:      len(s.rate),
			FirstTokenBuckets: s.buckets,
		})
	}
	sort.SliceStable(h.Latency, func(i, j int) bool {
		if h.Latency[i].Requests != h.Latency[j].Requests {
			return h.Latency[i].Requests > h.Latency[j].Requests
		}
		return h.Latency[i].Model < h.Latency[j].Model
	})
}

// clippedDay names the local day a range starts part-way through, or is empty
// when the range starts at a local midnight and every day it covers is whole.
func clippedDay(from time.Time, loc *time.Location) string {
	f := from.In(loc)
	midnight := time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, loc)
	if f.Equal(midnight) {
		return ""
	}
	return f.Format("2006-01-02")
}

// bucketOf is which histogram column a time to first token falls in: the first
// edge it is under, or the column past the last edge.
func bucketOf(ms int64) int {
	for i, edge := range FirstTokenBucketEdgesMS {
		if ms < edge {
			return i
		}
	}
	return len(FirstTokenBucketEdgesMS)
}

// generationRate is the tokens after the first over the time spent generating
// them, which is what the panel's live view means by tokens a second. A
// request with one token, or one that spent no measurable time generating, has
// no rate at all rather than a figure that means something else.
func generationRate(r Record) (float64, bool) {
	if r.CompletionTokens < 2 || r.FirstTokenMS < 0 {
		return 0, false
	}
	generating := float64(r.DurationMS-r.FirstTokenMS) / 1000
	if generating <= 0 {
		return 0, false
	}
	return float64(r.CompletionTokens-1) / generating, true
}

// percentilesOf reads the three points off a sorted slice by nearest rank.
func percentilesOf(sorted []float64) Percentiles {
	return Percentiles{
		P50: nearestRankOf(sorted, 0.50),
		P90: nearestRankOf(sorted, 0.90),
		P99: nearestRankOf(sorted, 0.99),
	}
}

// percentilesOfInts is percentilesOf over the timings, which are whole
// milliseconds. It reads the sorted slice in place: copying a million int64s
// into a million float64s to reuse one function would be eight megabytes spent
// on nothing.
func percentilesOfInts(sorted []int64) Percentiles {
	return Percentiles{
		P50: float64(nearestRankOf(sorted, 0.50)),
		P90: float64(nearestRankOf(sorted, 0.90)),
		P99: float64(nearestRankOf(sorted, 0.99)),
	}
}

// nearestRankOf is the value at the given fraction of a sorted slice, counting
// from one: the p90 of ten values is the ninth of them.
func nearestRankOf[T int64 | float64](sorted []T, p float64) T {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}
