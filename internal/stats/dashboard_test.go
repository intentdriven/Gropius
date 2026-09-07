package stats

import (
	"context"
	"errors"
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

// And it does stop: a record older than the margin means everything left to
// read finished earlier still, so there is nothing in the range behind it.
func TestThePassStopsOnceItIsPastTheMargin(t *testing.T) {
	s := fixtureStore(t)
	from := at(east, 2026, 9, 10, 0, 0)
	old := from - int64(historyStopSlack/time.Second) - 3600
	for _, r := range []Record{
		{Model: "org/alpha", At: old - 60, Class: ClassOK, PromptTokens: 5, CompletionTokens: 5},
		{Model: "org/alpha", At: old, Class: ClassOK, PromptTokens: 5, CompletionTokens: 5},
		{Model: "org/alpha", At: from + 60, Class: ClassOK, PromptTokens: 1, CompletionTokens: 1},
	} {
		if err := s.AppendRequest(r); err != nil {
			t.Fatal(err)
		}
	}
	h, err := Aggregate(t.Context(), s, time.Unix(from, 0), time.Unix(from+7200, 0), east)
	if err != nil {
		t.Fatal(err)
	}
	// The in-range record and the first record past the margin are read; the
	// one behind that is not, which is the whole point of stopping.
	if h.Records != 2 {
		t.Errorf("the pass read %d records, want it to stop at the first one past the margin", h.Records)
	}
	if len(h.Days) != 1 || h.Days[0].CompletionTokens != 1 {
		t.Errorf("the range's own record was not the only one counted: %#v", h.Days)
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
