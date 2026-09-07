package stats

import (
	"math"
	"sort"
	"time"
)

// The usage dashboard's arithmetic (itd-2609061521159233).
//
// Everything the panel's three historical tables show is worked out here, in
// one pass over the durable store, and handed to the control plane as sums,
// counts and buckets. The browser never sees a record: it is given the
// aggregate, so the only strings that reach it are the repo ids of models this
// Mac holds.
//
// It reads and writes nothing. The dashboard can therefore only show what was
// already recorded under the opt-in adr-2609061503319212 requires, which is
// what makes "nothing appears that was not recorded" a property of the code
// rather than a promise.

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

// historyStopRun is how many consecutive records older than the range are read
// before the pass gives up on finding any more inside it.
//
// The store is append-only and read newest first, so records arrive in very
// nearly the order they were made, and a run this long past the start of the
// range means the rest of the store is older still. It is a run rather than a
// single record because "very nearly" is not "exactly": a clock stepped
// backwards, or a caller stamping its own arrival time, can put one record out
// of order, and stopping on the first of those would silently drop the rest of
// the range.
const historyStopRun = 4096

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
	// SummaryOnly marks a day whose own records the store's retention has
	// dropped, leaving only the coarse per-model per-day summary written
	// before they went (itd-2609061602043757). It is false throughout while
	// nothing writes those summaries, and it is here rather than added later
	// because a table that cannot say which of its rows are coarse is a table
	// that quietly mixes the two.
	SummaryOnly bool `json:"summary_only,omitempty"`
}

// Tokens is the day's whole traffic for that model, in and out.
func (d DayTokens) Tokens() int64 { return d.PromptTokens + d.CompletionTokens }

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

// History is everything the dashboard's three tables draw.
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
	// Skipped is how many lines it could not use — one is the ordinary cost of
	// a crash, a great many mean the figures rest on a fraction of what was
	// recorded.
	Skipped int64 `json:"skipped"`
	// Truncated reports a pass that stopped at MaxRecords, so the figures
	// cover the newest records rather than the whole range.
	Truncated bool `json:"truncated"`
	// Narrowed reports a range that was wider than MaxDays and was cut to it.
	Narrowed bool `json:"narrowed"`
	// MaxRecords and MaxDays are the bounds themselves, so the panel says what
	// it was held to rather than repeating a number that could drift from it.
	MaxRecords int `json:"max_records"`
	MaxDays    int `json:"max_days"`
}

// RecordSource is the store as the dashboard reads it: newest record first and
// bounded, which is the read the durable store was built to serve.
//
// It is an interface so that the aggregation depends on the reading rather
// than on the FileStore, and so a caller with no store at all — a Mac where
// recording has never been on — passes nil and gets an empty view.
type RecordSource interface {
	Latest(limit int, fn func(Line) bool) error
	Status() StoreStatus
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
func Aggregate(src RecordSource, from, to time.Time, loc *time.Location) (History, error) {
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
		Zone:                    zone,
		Hours:                   make([]HourCounts, 24),
		FirstTokenBucketEdgesMS: FirstTokenBucketEdgesMS,
		MaxRecords:              MaxHistoryRecords,
		MaxDays:                 MaxHistoryDays,
	}
	for i := range h.Hours {
		h.Hours[i].Hour = i
	}
	if widest := to.AddDate(0, 0, -MaxHistoryDays); from.Before(widest) {
		from, h.Narrowed = widest, true
	}
	h.From, h.To = from.Unix(), to.Unix()
	if src == nil {
		return h, nil
	}

	a := newHistoryAgg(h.From, h.To, loc)
	err := src.Latest(0, a.take)
	h.Records, h.Truncated = a.read, a.truncated
	h.Skipped = src.Status().Skipped
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
// a percentile cannot be worked out from a running total. They are bounded by
// the pass itself: at most MaxHistoryRecords timings across every model.
type latencySamples struct {
	firstToken []int64
	rate       []float64
	buckets    []int
}

// historyAgg is the state of one pass.
type historyAgg struct {
	from, to int64
	loc      *time.Location

	read      int
	truncated bool
	// stale counts the consecutive records read from before the range, which
	// is how the pass knows it has walked back past everything it wants.
	stale int

	days     map[dayKey]*DayTokens
	models   map[string]*ModelShare
	latency  map[string]*latencySamples
	hours    []HourCounts
	total    int64
	dayOrder []dayKey
}

func newHistoryAgg(from, to int64, loc *time.Location) *historyAgg {
	a := &historyAgg{
		from: from, to: to, loc: loc,
		days:    map[dayKey]*DayTokens{},
		models:  map[string]*ModelShare{},
		latency: map[string]*latencySamples{},
		hours:   make([]HourCounts, 24),
	}
	return a
}

// take folds one line in, reporting whether the pass should carry on.
func (a *historyAgg) take(l Line) bool {
	if a.read >= MaxHistoryRecords {
		a.truncated = true
		return false
	}
	a.read++
	if l.At > a.to {
		// Newer than the range: nothing to count, and nothing to conclude
		// about how far back the pass has walked.
		return true
	}
	if l.At < a.from {
		a.stale++
		return a.stale < historyStopRun
	}
	a.stale = 0

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

// request folds one request record into the day, model and latency figures.
func (a *historyAgg) request(r Record, when time.Time) {
	key := dayKey{day: when.Format("2006-01-02"), model: r.Model}
	d, ok := a.days[key]
	if !ok {
		d = &DayTokens{Day: key.day, Model: key.model}
		a.days[key] = d
		a.dayOrder = append(a.dayOrder, key)
	}
	d.Requests++
	d.PromptTokens += int64(r.PromptTokens)
	d.CompletionTokens += int64(r.CompletionTokens)

	m, ok := a.models[r.Model]
	if !ok {
		m = &ModelShare{Model: r.Model}
		a.models[r.Model] = m
	}
	m.Requests++
	m.PromptTokens += int64(r.PromptTokens)
	m.CompletionTokens += int64(r.CompletionTokens)
	a.total += int64(r.PromptTokens) + int64(r.CompletionTokens)

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
		h.Days = append(h.Days, *a.days[k])
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
		c.Tokens = c.PromptTokens + c.CompletionTokens
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
		sort.Slice(s.firstToken, func(i, j int) bool { return s.firstToken[i] < s.firstToken[j] })
		sort.Float64s(s.rate)
		first := make([]float64, len(s.firstToken))
		for i, v := range s.firstToken {
			first[i] = float64(v)
		}
		h.Latency = append(h.Latency, ModelLatency{
			Model:             model,
			Requests:          len(s.firstToken),
			FirstTokenMS:      percentilesOf(first),
			Rate:              percentilesOf(s.rate),
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
		P50: nearestRank(sorted, 0.50),
		P90: nearestRank(sorted, 0.90),
		P99: nearestRank(sorted, 0.99),
	}
}

// nearestRank is the value at the given fraction of a sorted slice, counting
// from one: the p90 of ten values is the ninth of them.
func nearestRank(sorted []float64, p float64) float64 {
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
