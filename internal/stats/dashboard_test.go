package stats

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// The dashboard's figures are the whole of what it is for, so every one of
// them is checked against a number worked out by hand from the fixture rather
// than against another run of the same code.

// east is two hours ahead of UTC and west is five behind, so a day and an hour
// bucket computed in one of them lands somewhere else in the other. Every test
// here fixes the location rather than taking the machine's, since a figure
// that only comes out right in one time zone is not a figure.
var (
	east = time.FixedZone("east", 2*3600)
	west = time.FixedZone("west", -5*3600)
)

// fixtureStore is a store in a temporary directory, switched on and ready to
// be filled and read back.
func fixtureStore(t *testing.T) *FileStore {
	t.Helper()
	s := NewStore(t.TempDir(), StoreOptions{Months: 1200, MaxBytes: 64 << 20})
	t.Cleanup(func() { s.Close() })
	if err := s.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	return s
}

// at is a wall-clock time in a location, as the whole UTC seconds a record
// carries.
func at(loc *time.Location, y int, m time.Month, d, h, min int) int64 {
	return time.Date(y, m, d, h, min, 0, 0, loc).Unix()
}

// The tokens-per-day view: for each local day, each model's own tokens, and
// beside them that model's share of the whole range.
func TestTokensPerDayAndShareAreTheFixturesOwnSums(t *testing.T) {
	s := fixtureStore(t)
	// Three local days in `east`, two models. The second day's first request
	// is at 01:30 local, which is 23:30 the previous day in UTC — so a reader
	// bucketing by UTC would put it on the wrong day.
	for _, r := range []Record{
		{Model: "org/alpha", At: at(east, 2026, 9, 1, 10, 0), Class: ClassOK, PromptTokens: 100, CompletionTokens: 200},
		{Model: "org/alpha", At: at(east, 2026, 9, 1, 12, 0), Class: ClassOK, PromptTokens: 50, CompletionTokens: 25},
		{Model: "org/beta", At: at(east, 2026, 9, 1, 11, 0), Class: ClassOK, PromptTokens: 10, CompletionTokens: 5},
		{Model: "org/alpha", At: at(east, 2026, 9, 2, 1, 30), Class: ClassOK, PromptTokens: 1, CompletionTokens: 2},
		{Model: "org/beta", At: at(east, 2026, 9, 2, 9, 0), Class: ClassOK, PromptTokens: 200, CompletionTokens: 100},
		{Model: "org/beta", At: at(east, 2026, 9, 3, 8, 0), Class: ClassOK, PromptTokens: 7, CompletionTokens: 3},
	} {
		if err := s.AppendRequest(r); err != nil {
			t.Fatal(err)
		}
	}

	h, err := Aggregate(t.Context(), s, time.Unix(at(east, 2026, 9, 1, 0, 0), 0), time.Unix(at(east, 2026, 9, 4, 0, 0), 0), east)
	if err != nil {
		t.Fatal(err)
	}

	// Newest day first, and within a day the model that served the most
	// tokens first — the order a reader asking "which model does the work"
	// reads down.
	want := []DayTokens{
		{Day: "2026-09-03", Model: "org/beta", Requests: 1, PromptTokens: 7, CompletionTokens: 3},
		{Day: "2026-09-02", Model: "org/beta", Requests: 1, PromptTokens: 200, CompletionTokens: 100},
		{Day: "2026-09-02", Model: "org/alpha", Requests: 1, PromptTokens: 1, CompletionTokens: 2},
		{Day: "2026-09-01", Model: "org/alpha", Requests: 2, PromptTokens: 150, CompletionTokens: 225},
		{Day: "2026-09-01", Model: "org/beta", Requests: 1, PromptTokens: 10, CompletionTokens: 5},
	}
	if !reflect.DeepEqual(h.Days, want) {
		t.Errorf("tokens per day =\n%#v\nwant\n%#v", h.Days, want)
	}

	// The share is the model's tokens over the range's total: alpha 378 of
	// 703, beta 325 of 703.
	wantModels := []ModelShare{
		{Model: "org/alpha", Requests: 3, PromptTokens: 151, CompletionTokens: 227, Tokens: 378, Share: 378.0 / 703.0},
		{Model: "org/beta", Requests: 3, PromptTokens: 217, CompletionTokens: 108, Tokens: 325, Share: 325.0 / 703.0},
	}
	if !reflect.DeepEqual(h.Models, wantModels) {
		t.Errorf("model share =\n%#v\nwant\n%#v", h.Models, wantModels)
	}
	if got := h.Models[0].Share + h.Models[1].Share; got < 0.999999 || got > 1.000001 {
		t.Errorf("the shares add up to %v, want 1", got)
	}
	if h.Records != 6 {
		t.Errorf("the aggregate read %d records, want the fixture's 6", h.Records)
	}
	if h.Truncated {
		t.Error("a six-record fixture is reported as truncated by the record bound")
	}
}

// The latency view: the percentiles and the spread of the requests that
// actually produced an answer, and nothing worked out from one that did not.
func TestTheLatencyDistributionIsTheFixturesOwn(t *testing.T) {
	s := fixtureStore(t)
	// Ten answers whose times to first token are 100 ms to 1,000 ms, each
	// generating a hundred tokens after the first in exactly one second, so
	// every rate is 100 tok/s and the percentiles are readable by eye.
	for i := 1; i <= 10; i++ {
		first := int64(i * 100)
		if err := s.AppendRequest(Record{
			Model: "org/alpha", At: at(east, 2026, 9, 1, 10, i), Class: ClassOK,
			Streamed: true, PromptTokens: 10, CompletionTokens: 101,
			FirstTokenMS: first, DurationMS: first + 1000,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A refusal has no timings to distribute, and a request that never
	// streamed has no first token: neither may move a percentile.
	for _, r := range []Record{
		{Model: "org/alpha", At: at(east, 2026, 9, 1, 10, 20), Class: ClassBusy, FirstTokenMS: NoFirstToken, DurationMS: 3},
		{Model: "org/alpha", At: at(east, 2026, 9, 1, 10, 21), Class: ClassOK, PromptTokens: 5, CompletionTokens: 5, FirstTokenMS: NoFirstToken, DurationMS: 900},
	} {
		if err := s.AppendRequest(r); err != nil {
			t.Fatal(err)
		}
	}

	h, err := Aggregate(t.Context(), s, time.Unix(at(east, 2026, 9, 1, 0, 0), 0), time.Unix(at(east, 2026, 9, 2, 0, 0), 0), east)
	if err != nil {
		t.Fatal(err)
	}
	want := []ModelLatency{{
		Model:        "org/alpha",
		Requests:     10,
		FirstTokenMS: Percentiles{P50: 500, P90: 900, P99: 1000},
		Rate:         Percentiles{P50: 100, P90: 100, P99: 100},
		// Every one of the ten generated more than one token in measurable
		// time, so the rate rests on all ten; a table whose rate count could
		// silently be smaller than its answer count is the thing this field
		// exists to prevent.
		RateRequests: 10,
		// Edges 100, 250, 500, 1,000, 2,500, 5,000 and 10,000 ms: nothing
		// under 100; 100 and 200; 300 and 400; 500 to 900; 1,000.
		FirstTokenBuckets: []int{0, 2, 2, 5, 1, 0, 0, 0},
	}}
	if !reflect.DeepEqual(h.Latency, want) {
		t.Errorf("latency =\n%#v\nwant\n%#v", h.Latency, want)
	}
	if len(FirstTokenBucketEdgesMS) != len(want[0].FirstTokenBuckets)-1 {
		t.Errorf("%d bucket edges make %d buckets, and the view draws %d",
			len(FirstTokenBucketEdgesMS), len(FirstTokenBucketEdgesMS)+1, len(want[0].FirstTokenBuckets))
	}
}

// The eviction and reload view: counted by the hour of the Mac's own day, so
// the same records read in another zone fall in other hours.
func TestEvictionsAndReloadsAreCountedByTheLocalHour(t *testing.T) {
	s := fixtureStore(t)
	evicted := at(time.UTC, 2026, 9, 1, 23, 30)
	loaded := at(time.UTC, 2026, 9, 1, 10, 0)
	for _, e := range []Event{
		{At: evicted, Model: "org/alpha", Kind: EventRemoved, Reason: ReasonEvicted},
		{At: loaded, Model: "org/alpha", Kind: EventLoad, DurationMS: 4000},
		// An idle reap and a shutdown are removals, not evictions. Counting
		// them would make the panel say the memory budget is thrashing when
		// nothing of the kind happened.
		{At: evicted, Model: "org/beta", Kind: EventRemoved, Reason: ReasonIdle},
		{At: evicted, Model: "org/beta", Kind: EventRemoved, Reason: ReasonShutdown},
	} {
		if err := s.AppendEvent(e); err != nil {
			t.Fatal(err)
		}
	}

	from := time.Unix(at(time.UTC, 2026, 9, 1, 0, 0), 0)
	to := time.Unix(at(time.UTC, 2026, 9, 3, 0, 0), 0)
	for _, c := range []struct {
		name         string
		loc          *time.Location
		evictionHour int
		reloadHour   int
	}{
		// 23:30 UTC is 01:30 the next day two hours east, and 18:30 the same
		// day five hours west.
		{"east", east, 1, 12},
		{"west", west, 18, 5},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, err := Aggregate(t.Context(), s, from, to, c.loc)
			if err != nil {
				t.Fatal(err)
			}
			if len(h.Hours) != 24 {
				t.Fatalf("the view has %d hours in its day", len(h.Hours))
			}
			for _, hour := range h.Hours {
				wantEvictions, wantLoads := 0, 0
				if hour.Hour == c.evictionHour {
					wantEvictions = 1
				}
				if hour.Hour == c.reloadHour {
					wantLoads = 1
				}
				if hour.Evictions != wantEvictions || hour.Loads != wantLoads {
					t.Errorf("hour %02d: %d evictions and %d loads, want %d and %d",
						hour.Hour, hour.Evictions, hour.Loads, wantEvictions, wantLoads)
				}
			}
		})
	}
}

// The range is what bounds the figures: a record outside it is not part of
// any sum, and is not part of the total the shares are taken over either.
func TestOnlyTheRangesOwnRecordsAreCounted(t *testing.T) {
	s := fixtureStore(t)
	for _, r := range []Record{
		{Model: "org/alpha", At: at(east, 2026, 9, 1, 12, 0), Class: ClassOK, PromptTokens: 1000, CompletionTokens: 1000},
		{Model: "org/alpha", At: at(east, 2026, 9, 8, 12, 0), Class: ClassOK, PromptTokens: 4, CompletionTokens: 6},
	} {
		if err := s.AppendRequest(r); err != nil {
			t.Fatal(err)
		}
	}

	// The last seven local days ending on the 9th: the 1st falls outside.
	h, err := Aggregate(t.Context(), s, time.Unix(at(east, 2026, 9, 2, 12, 0), 0), time.Unix(at(east, 2026, 9, 9, 12, 0), 0), east)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Days) != 1 || h.Days[0].Day != "2026-09-08" || h.Days[0].CompletionTokens != 6 {
		t.Errorf("the seven-day range covers %#v, want only the 8th's own record", h.Days)
	}
	if len(h.Models) != 1 || h.Models[0].Tokens != 10 || h.Models[0].Share != 1 {
		t.Errorf("the range's totals are %#v, want the 8th's ten tokens as the whole of it", h.Models)
	}
}

// A store that was never written to is not an error and not a figure: it is
// an empty view, which is what the panel says "nothing is recorded" from.
func TestAnEmptyStoreAggregatesToNothing(t *testing.T) {
	s := NewStore(t.TempDir(), StoreOptions{})
	t.Cleanup(func() { s.Close() })
	h, err := Aggregate(t.Context(), s, time.Unix(0, 0), time.Now(), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Days) != 0 || len(h.Models) != 0 || len(h.Latency) != 0 || h.Records != 0 {
		t.Errorf("an empty store aggregated to %#v", h)
	}
	// The hourly view is always a whole day, so the table has its 24 rows
	// whether or not anything happened in them.
	if len(h.Hours) != 24 {
		t.Errorf("the empty view has %d hours in its day, want 24", len(h.Hours))
	}
	if h.Truncated {
		t.Error("an empty store is reported as truncated")
	}

	// And a store that is not there at all reads the same way, which is the
	// state a Mac that has never had recording on is in.
	none, err := Aggregate(t.Context(), (*FileStore)(nil), time.Unix(0, 0), time.Now(), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Days) != 0 || len(none.Hours) != 24 {
		t.Errorf("no store at all aggregated to %#v", none)
	}
}

// The bounds are the promise the panel makes about staying responsive, so
// they are reported rather than assumed: a reader has to be able to tell a
// quiet month from a month the aggregate stopped short of.
func TestTheAggregateReportsTheBoundsItWasHeldTo(t *testing.T) {
	s := fixtureStore(t)
	if err := s.AppendRequest(Record{Model: "org/alpha", At: at(east, 2026, 9, 1, 12, 0), Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	h, err := Aggregate(t.Context(), s, time.Unix(at(east, 2026, 9, 1, 0, 0), 0), time.Unix(at(east, 2026, 9, 2, 0, 0), 0), east)
	if err != nil {
		t.Fatal(err)
	}
	if h.MaxRecords != MaxHistoryRecords || h.MaxDays != MaxHistoryDays {
		t.Errorf("the aggregate reports bounds of %d records and %d days, want %d and %d",
			h.MaxRecords, h.MaxDays, MaxHistoryRecords, MaxHistoryDays)
	}
	if h.From != at(east, 2026, 9, 1, 0, 0) || h.To != at(east, 2026, 9, 2, 0, 0) {
		t.Errorf("the aggregate reports the range %d..%d, want the one it was asked for", h.From, h.To)
	}
	if h.Zone == "" {
		t.Error("the aggregate does not say which day it counted by")
	}
}

// A range wider than the bound is narrowed rather than refused, and says so:
// the panel's "all" is whatever the store holds, and the store's own cap can
// be set high enough to hold more than a year.
func TestARangeWiderThanTheBoundIsNarrowedToIt(t *testing.T) {
	s := fixtureStore(t)
	to := time.Unix(at(east, 2026, 9, 1, 0, 0), 0)
	from := to.AddDate(-5, 0, 0)
	h, err := Aggregate(t.Context(), s, from, to, east)
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(h.To-h.From) * time.Second; got > time.Duration(MaxHistoryDays)*24*time.Hour {
		t.Errorf("a five-year range was aggregated over %v, want it narrowed to %d days", got, MaxHistoryDays)
	}
	if !h.Narrowed {
		t.Error("the range was narrowed and the view does not say so")
	}
}

// The pass walks back past the range's start by a margin, because the store's
// order is not the order it reads by: a record is appended when its request
// finishes and stamped with when the request arrived, so a slow request is
// appended after requests that arrived later than it did. Stopping at the
// first record older than the range would pass over the slow one.
func TestARecordThatArrivedInRangeButFinishedLateIsStillCounted(t *testing.T) {
	s := fixtureStore(t)
	from := at(east, 2026, 9, 2, 0, 0)
	// Appended in completion order, which is the order the store is written
	// in. The first line arrived an hour inside the range and answered at
	// once; the second arrived an hour before it and took hours to answer, so
	// it is written last and, read newest first, comes out first.
	for _, r := range []Record{
		{Model: "org/alpha", At: from + 3600, Class: ClassOK, PromptTokens: 40, CompletionTokens: 60},
		{Model: "org/alpha", At: from - 3600, Class: ClassOK, PromptTokens: 1, CompletionTokens: 1},
	} {
		if err := s.AppendRequest(r); err != nil {
			t.Fatal(err)
		}
	}
	// Read in the order the store holds them, to be sure the fixture is the
	// awkward one this test is about rather than the easy one.
	var order []int64
	if err := s.Latest(0, func(l Line) bool { order = append(order, l.At); return true }); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != from-3600 {
		t.Fatalf("the store reads back %v; this test needs the out-of-range record read first", order)
	}

	h, err := Aggregate(t.Context(), s, time.Unix(from, 0), time.Unix(from+7200, 0), east)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Days) != 1 || h.Days[0].CompletionTokens != 60 {
		t.Errorf("the late-finishing request was passed over: days = %#v", h.Days)
	}
}

// However far out of order a record is, it is still found. There is no early
// stop to pass it over, which is the whole of why: the store is in completion
// order and the scan is against arrival times, so any margin would be a guess,
// and the failure a wrong guess buys is a month silently drawn short.
func TestAStragglerFarOlderThanTheRangeDoesNotEndThePass(t *testing.T) {
	s := fixtureStore(t)
	from := at(east, 2026, 9, 10, 0, 0)
	// Written in completion order. The middle line is stamped a fortnight
	// before the range — a clock stepped backwards, or a generation that ran
	// for days — and the line after it is inside the range.
	for _, r := range []Record{
		{Model: "org/alpha", At: from + 60, Class: ClassOK, PromptTokens: 1, CompletionTokens: 1},
		{Model: "org/alpha", At: from - 14*24*3600, Class: ClassOK, PromptTokens: 5, CompletionTokens: 5},
		{Model: "org/alpha", At: from + 120, Class: ClassOK, PromptTokens: 2, CompletionTokens: 2},
	} {
		if err := s.AppendRequest(r); err != nil {
			t.Fatal(err)
		}
	}
	h, err := Aggregate(t.Context(), s, time.Unix(from, 0), time.Unix(from+7200, 0), east)
	if err != nil {
		t.Fatal(err)
	}
	if h.Records != 3 {
		t.Errorf("the pass read %d of the fixture's 3 records", h.Records)
	}
	// Both in-range records, not just the one before the straggler.
	if len(h.Days) != 1 || h.Days[0].CompletionTokens != 3 || h.Days[0].Requests != 2 {
		t.Errorf("a record behind the straggler was passed over: %#v", h.Days)
	}
	// And the pass says it got back past the range's start, which is what the
	// panel needs to tell a covered range from one the store does not reach.
	if !h.ReachedStart {
		t.Error("the pass read past the range's start and does not say so")
	}
	if h.Truncated {
		t.Error("a three-record fixture is reported as stopped by a bound")
	}
}

// A store that does not reach back as far as the range says so, and says it
// differently from a pass a bound stopped: one is a quiet history, the other is
// a figure drawn short.
func TestAStoreThatDoesNotReachTheRangesStartSaysSo(t *testing.T) {
	s := fixtureStore(t)
	from := at(east, 2026, 9, 10, 0, 0)
	if err := s.AppendRequest(Record{Model: "org/alpha", At: from + 60, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	h, err := Aggregate(t.Context(), s, time.Unix(from, 0), time.Unix(from+7200, 0), east)
	if err != nil {
		t.Fatal(err)
	}
	if h.ReachedStart {
		t.Error("a store holding nothing older than the range claims to have read past its start")
	}
	if h.Truncated {
		t.Error("running out of store is reported as a bound stopping the pass")
	}
}

// A pass that stops at its record bound says so, because a table that quietly
// covered the newest records only would be read as covering the range.
func TestAPassThatStopsAtTheRecordBoundSaysSo(t *testing.T) {
	s := fixtureStore(t)
	day := at(east, 2026, 9, 1, 12, 0)
	for i := range 6 {
		if err := s.AppendRequest(Record{
			Model: "org/alpha", At: day + int64(i), Class: ClassOK, PromptTokens: 1, CompletionTokens: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Lowered rather than reached: reaching the real bound would mean writing
	// a million records to assert one boolean.
	was := historyRecordBound
	historyRecordBound = 4
	t.Cleanup(func() { historyRecordBound = was })

	h, err := Aggregate(t.Context(), s, time.Unix(day-3600, 0), time.Unix(day+3600, 0), east)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Truncated {
		t.Error("the pass stopped at its record bound and does not say so")
	}
	if h.Records != 4 || h.MaxRecords != 4 {
		t.Errorf("the pass read %d records under a bound reported as %d, want 4 and 4", h.Records, h.MaxRecords)
	}
	// And the figures are the ones it did read, not a refusal.
	if len(h.Days) != 1 || h.Days[0].Requests != 4 {
		t.Errorf("a truncated pass drew %#v, want the four records it read", h.Days)
	}
}

// A range far wider than the bound is narrowed whatever end date it names. An
// end so far in the future that date arithmetic on it wraps would otherwise
// narrow nothing while reporting that it had.
func TestAnAbsurdEndDateStillNarrowsTheRange(t *testing.T) {
	s := fixtureStore(t)
	widest := time.Duration(MaxHistoryDays) * 24 * time.Hour
	for _, to := range []time.Time{
		time.Unix(1<<38, 0),
		time.Unix(1<<50, 0),
		time.Unix(1<<62, 0),
	} {
		h, err := Aggregate(t.Context(), s, time.Unix(0, 0), to, east)
		if err != nil {
			t.Fatal(err)
		}
		if !h.Narrowed {
			t.Errorf("a range ending at %d was not narrowed and says it was not", to.Unix())
		}
		if got := time.Duration(h.To-h.From) * time.Second; got != widest {
			t.Errorf("a range ending at %d was aggregated over %v, want %v", to.Unix(), got, widest)
		}
	}
}

// A reader who closes the panel part-way through a pass over months of records
// stops being paid for.
func TestAPassStopsWhenTheReaderGoesAway(t *testing.T) {
	s := fixtureStore(t)
	day := at(east, 2026, 9, 1, 12, 0)
	if err := s.AppendRequest(Record{Model: "org/alpha", At: day, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Aggregate(ctx, s, time.Unix(day-3600, 0), time.Unix(day+3600, 0), east); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled reading returned %v, want context.Canceled", err)
	}
}

// The bucket edges the aggregate hands out are its own copy: a caller that
// changed the slice it was given would change every aggregation that followed.
func TestTheBucketEdgesHandedOutAreACopy(t *testing.T) {
	h, err := Aggregate(t.Context(), nil, time.Unix(0, 0), time.Now(), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.FirstTokenBucketEdgesMS) == 0 {
		t.Fatal("the aggregate carries no bucket edges, so the table has no headings")
	}
	h.FirstTokenBucketEdgesMS[0] = 999999
	if FirstTokenBucketEdgesMS[0] == 999999 {
		t.Error("the aggregate handed out the package's own slice")
	}
}

// A pass stopped by the row bound says so too. The record bound caps the
// timings; this one caps the tables, which are a row per day per model and are
// what a store full of unrecognisable model ids would grow without bound.
func TestAPassThatStopsAtTheRowBoundSaysSo(t *testing.T) {
	s := fixtureStore(t)
	day := at(east, 2026, 9, 1, 12, 0)
	for i := range 8 {
		if err := s.AppendRequest(Record{
			Model:        "org/model-" + string(rune('a'+i)),
			At:           day + int64(i),
			Class:        ClassOK,
			PromptTokens: 1, CompletionTokens: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	was := historyRowBound
	historyRowBound = 3
	t.Cleanup(func() { historyRowBound = was })

	h, err := Aggregate(t.Context(), s, time.Unix(day-3600, 0), time.Unix(day+3600, 0), east)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Truncated {
		t.Error("the pass stopped at its row bound and does not say so")
	}
	if len(h.Days) > historyRowBound {
		t.Errorf("the table has %d rows under a bound of %d", len(h.Days), historyRowBound)
	}
}

// A range that starts part-way through a day — which is every range the panel
// sends — has an oldest day holding only the part of it inside the range. That
// row says so, because drawn beside whole days it reads as a quiet one.
func TestTheRangesOldestDayIsMarkedWhenTheRangeClipsIt(t *testing.T) {
	s := fixtureStore(t)
	for _, r := range []Record{
		// Before the range starts on the 1st: never counted.
		{Model: "org/alpha", At: at(east, 2026, 9, 1, 9, 0), Class: ClassOK, PromptTokens: 500, CompletionTokens: 500},
		// After it, on the same day: counted, and the row is short by the above.
		{Model: "org/alpha", At: at(east, 2026, 9, 1, 18, 0), Class: ClassOK, PromptTokens: 10, CompletionTokens: 20},
		// A whole day inside the range.
		{Model: "org/alpha", At: at(east, 2026, 9, 2, 12, 0), Class: ClassOK, PromptTokens: 1, CompletionTokens: 2},
	} {
		if err := s.AppendRequest(r); err != nil {
			t.Fatal(err)
		}
	}

	h, err := Aggregate(t.Context(), s,
		time.Unix(at(east, 2026, 9, 1, 16, 0), 0), time.Unix(at(east, 2026, 9, 3, 0, 0), 0), east)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Days) != 2 {
		t.Fatalf("the range covers %d day rows, want 2: %#v", len(h.Days), h.Days)
	}
	whole, clipped := h.Days[0], h.Days[1]
	if whole.Day != "2026-09-02" || whole.Partial {
		t.Errorf("the whole day is %#v, want the 2nd unmarked", whole)
	}
	if clipped.Day != "2026-09-01" || !clipped.Partial {
		t.Errorf("the clipped day is %#v, want the 1st marked partial", clipped)
	}
	// And the mark is about the clipping, not about the figures: the row still
	// holds exactly what fell inside the range.
	if clipped.CompletionTokens != 20 {
		t.Errorf("the clipped row holds %d completion tokens, want the 20 inside the range", clipped.CompletionTokens)
	}

	// A range that starts at a local midnight clips nothing, and nothing is
	// marked.
	aligned, err := Aggregate(t.Context(), s,
		time.Unix(at(east, 2026, 9, 1, 0, 0), 0), time.Unix(at(east, 2026, 9, 3, 0, 0), 0), east)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range aligned.Days {
		if d.Partial {
			t.Errorf("a day-aligned range marks %s partial", d.Day)
		}
	}
}

// The fold the per-model per-day summaries will arrive through, exercised on
// its own: the seam is what itd-2609061602043757 plugs into, and a fold with a
// stated correctness property and no test is a property nobody is holding.
func TestTheSummaryFoldAddsToTheDayAndMarksIt(t *testing.T) {
	a := newHistoryAgg(t.Context(), 0, 1<<40, time.UTC)
	// A day's surviving records, then the summary of the ones retention took.
	a.addTokens("2026-09-01", "org/alpha", 2, 10, 20, false)
	a.addTokens("2026-09-01", "org/alpha", 8, 100, 200, true)
	// And a day whose records have gone entirely.
	a.addTokens("2026-08-01", "org/alpha", 40, 400, 800, true)

	var h History
	a.fill(&h)
	if len(h.Days) != 2 {
		t.Fatalf("the fold made %d rows, want 2: %#v", len(h.Days), h.Days)
	}
	boundary, gone := h.Days[0], h.Days[1]
	// Additive, never subtracting: the boundary day carries both halves.
	if boundary.Day != "2026-09-01" || boundary.Requests != 10 ||
		boundary.PromptTokens != 110 || boundary.CompletionTokens != 220 {
		t.Errorf("the boundary day is %#v, want the records and the summary added", boundary)
	}
	if !boundary.FromSummary {
		t.Error("the boundary day carries summary figures and is not marked")
	}
	if gone.Day != "2026-08-01" || !gone.FromSummary || gone.Requests != 40 {
		t.Errorf("the summary-only day is %#v", gone)
	}
	// The range's total, which the share column divides by, takes the summary
	// figures too — a share drawn over the records alone would not add to one.
	if len(h.Models) != 1 || h.Models[0].Tokens != 110+220+400+800 {
		t.Errorf("the range's total is %#v, want every folded token", h.Models)
	}
	if h.Models[0].Share != 1 {
		t.Errorf("the only model's share is %v, want 1", h.Models[0].Share)
	}
}

// unreadableStore fills a directory with lines this build cannot read: a
// schema version newer than its own, which is exactly what a store written by
// a later Gropius looks like. Nothing in it ever reaches the aggregation, so
// every bound that lives there bounds nothing.
func unreadableStore(t *testing.T, dir string, files int) {
	t.Helper()
	line := []byte(`{"v":99,"kind":"request","model":"mlx-community/Qwen3-8B-4bit",` +
		`"at":1788696030,"class":"ok","streamed":true,"prompt_tokens":1234,` +
		`"completion_tokens":567,"first_token_ms":210,"duration_ms":4200,` +
		`"queue_wait_ms":12,"load_wait_ms":0}` + "\n")
	var buf []byte
	for int64(len(buf)) < defaultRotateBytes {
		buf = append(buf, line...)
	}
	for i := 1; i <= files; i++ {
		name := fmt.Sprintf("stats-20260901-%03d.jsonl", i)
		if err := os.WriteFile(filepath.Join(dir, name), buf, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// A store whose lines this build cannot read is still bounded and still
// stoppable. Both used to live in the aggregation's own callback, which the
// store never calls for a line that does not parse — so a store a newer
// Gropius wrote was read from end to end, on every request, and a reader who
// had gone away was paid for to the last byte of it.
func TestAStoreOfUnreadableLinesIsStillBoundedAndStillStoppable(t *testing.T) {
	dir := t.TempDir()
	unreadableStore(t, dir, 8) // 40 MB, about 300,000 lines
	s := NewStore(dir, StoreOptions{Months: 1200, MaxBytes: defaultMaxBytes})
	t.Cleanup(func() { s.Close() })

	was := historyRecordBound
	historyRecordBound = 1
	t.Cleanup(func() { historyRecordBound = was })

	t.Run("the bound holds", func(t *testing.T) {
		started := time.Now()
		h, err := Aggregate(t.Context(), s, time.Unix(0, 0), time.Now(), time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		if h.Skipped > 1 {
			t.Errorf("the pass read %d unreadable lines under a bound of 1", h.Skipped)
		}
		if !h.Truncated {
			t.Error("a pass a bound stopped does not say so")
		}
		if took := time.Since(started); took > 5*time.Second {
			t.Errorf("a bounded pass over an unreadable store took %v", took)
		}
	})

	t.Run("the reader who went away is let go of", func(t *testing.T) {
		historyRecordBound = was // the bound must not be what stops this one
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		started := time.Now()
		_, err := Aggregate(ctx, s, time.Unix(0, 0), time.Now(), time.UTC)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("a cancelled pass over an unreadable store returned %v, want context.Canceled", err)
		}
		if took := time.Since(started); took > 5*time.Second {
			t.Errorf("a cancelled pass over an unreadable store took %v to come back", took)
		}
	})
}

// The byte budget is the other half: a store of enormous lines yields few of
// them and costs every byte, so what bounds the reading is not the count.
func TestTheReadIsBoundedInBytesAsWellAsInLines(t *testing.T) {
	dir := t.TempDir()
	unreadableStore(t, dir, 4)
	s := NewStore(dir, StoreOptions{Months: 1200, MaxBytes: defaultMaxBytes})
	t.Cleanup(func() { s.Close() })

	got, err := s.Read(t.Context(), ReadOptions{MaxBytes: 1}, func(Line) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if !got.Bounded {
		t.Error("a read the byte budget stopped does not say so")
	}
	// One file's worth, and not the four: the budget is checked between files,
	// because a file is read whole before any line of it is looked at, so the
	// first one is always paid for and none after it is.
	if got.Bytes > 2*defaultRotateBytes {
		t.Errorf("a read under a one-byte budget read %d bytes, want one file's worth", got.Bytes)
	}
}

// Two readings at once each report their own skipped count. The store keeps a
// running one for the panel's store line; an aggregate that took that figure
// would show the other reading's.
func TestEachReadReportsItsOwnSkippedCount(t *testing.T) {
	dir := t.TempDir()
	unreadableStore(t, dir, 1)
	s := NewStore(dir, StoreOptions{Months: 1200, MaxBytes: defaultMaxBytes})
	t.Cleanup(func() { s.Close() })

	small, err := s.Read(t.Context(), ReadOptions{MaxLines: 10}, func(Line) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if small.Skipped != 10 {
		t.Errorf("a read of ten lines reports %d skipped, want 10", small.Skipped)
	}
	big, err := s.Read(t.Context(), ReadOptions{MaxLines: 100}, func(Line) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if big.Skipped != 100 {
		t.Errorf("a read of a hundred lines reports %d skipped, want 100", big.Skipped)
	}
}

// A count off a file is a count nobody promised is sane. A hand edit or a
// corrupt line must not turn into a table of negative tokens and a share of
// nothing, which reads as a fault in the arithmetic rather than as a bad line.
func TestHostileTokenCountsDoNotWrapTheTotals(t *testing.T) {
	s := fixtureStore(t)
	day := at(east, 2026, 9, 1, 12, 0)
	for _, r := range []Record{
		{Model: "org/alpha", At: day, Class: ClassOK, PromptTokens: math.MaxInt64, CompletionTokens: math.MaxInt64},
		{Model: "org/alpha", At: day + 1, Class: ClassOK, PromptTokens: -5, CompletionTokens: -5},
		{Model: "org/beta", At: day + 2, Class: ClassOK, PromptTokens: 10, CompletionTokens: 10},
	} {
		if err := s.AppendRequest(r); err != nil {
			t.Fatal(err)
		}
	}
	h, err := Aggregate(t.Context(), s, time.Unix(day-3600, 0), time.Unix(day+3600, 0), east)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range h.Days {
		if d.PromptTokens < 0 || d.CompletionTokens < 0 || d.Tokens() < 0 {
			t.Errorf("a day's tokens went negative: %#v", d)
		}
	}
	for _, m := range h.Models {
		if m.Tokens < 0 || m.Share < 0 || m.Share > 1 {
			t.Errorf("a model's totals went out of range: %#v", m)
		}
	}
}
