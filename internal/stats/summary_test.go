package stats

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testClock is a clock a test moves by hand, so a store can be made to write
// across days and to look at its horizon from a chosen date.
type testClock struct{ at atomic.Int64 }

func (c *testClock) set(t time.Time) { c.at.Store(t.Unix()) }
func (c *testClock) now() time.Time  { return time.Unix(c.at.Load(), 0).UTC() }

// day is a UTC instant, spelled the way a test reads best.
func day(y int, m time.Month, d, hour int) time.Time {
	return time.Date(y, m, d, hour, 0, 0, 0, time.UTC)
}

// summariseTestStore is a store whose local day is UTC, so a test can say
// which day a record falls on without asking where this Mac is.
func summariseTestStore(t *testing.T, clock *testClock, opts StoreOptions) (*FileStore, string) {
	t.Helper()
	opts.Now = clock.now
	opts.loc = time.UTC
	return newTestStore(t, opts)
}

// summaries returns every summary line the store holds, in the order the
// reader gives them.
func summaries(t *testing.T, s *FileStore) []Summary {
	t.Helper()
	var out []Summary
	if err := s.Summaries(0, func(sum Summary) bool { out = append(out, sum); return true }); err != nil {
		t.Fatalf("reading the summaries: %v", err)
	}
	return out
}

// find returns the summary line for one model and day, and whether there was
// exactly one.
func find(sums []Summary, day, model string) (Summary, int) {
	var got Summary
	n := 0
	for _, s := range sums {
		if s.Day == day && s.Model == model {
			got = s
			n++
		}
	}
	return got, n
}

func request(at time.Time, model string) Record {
	return Record{
		Model: model, At: at.Unix(), Class: ClassOK, Streamed: true,
		PromptTokens: 10, CompletionTokens: 20, FirstTokenMS: 30,
		DurationMS: 100, QueueWaitMS: 5, LoadWaitMS: 7,
	}
}

// The whole of the intent: what is dropped is counted first, per model and
// per day, and a later drop for a day already summarised extends the line
// rather than writing a second one.
func TestADroppedFileIsSummarisedFirstAndALaterDropExtendsTheDay(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, dir := summariseTestStore(t, clock, StoreOptions{Months: 2})
	on(t, s)

	// One file, three days, two models: a quiet Mac writes across days into
	// the file it has open.
	for d := 10; d <= 12; d++ {
		at := day(2026, time.January, d, 9)
		for range 5 {
			for _, m := range []string{"org/a", "org/b"} {
				if err := s.AppendRequest(request(at, m)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := s.AppendEvent(Event{At: day(2026, time.January, 11, 9).Unix(), Model: "org/a", Kind: EventLoad, DurationMS: 900}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(Event{At: day(2026, time.January, 11, 10).Unix(), Model: "org/a", Kind: EventRemoved, Reason: ReasonEvicted}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// A second file, holding one record for a day already among the first
	// file's and then a recent one, so the horizon keeps it while it drops
	// the first.
	clock.set(day(2026, time.March, 20, 9))
	on(t, s)
	if err := s.AppendRequest(request(day(2026, time.January, 11, 11), "org/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendRequest(request(day(2026, time.March, 20, 9), "org/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := len(names(t, dir)); got != 2 {
		t.Fatalf("the store holds %d files, want the two this test wrote: %v", got, names(t, dir))
	}

	// The horizon drops the first file. Every record in it is summarised
	// before it goes.
	s.SetRetention(2, 1<<20)
	s.SetRetention(1, 1<<20)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}

	sums := summaries(t, s)
	if len(sums) != 6 {
		t.Fatalf("the summary holds %d lines, want one for each of two models on each of three days: %+v", len(sums), sums)
	}
	for _, d := range []string{"2026-01-10", "2026-01-11", "2026-01-12"} {
		for _, m := range []string{"org/a", "org/b"} {
			got, n := find(sums, d, m)
			if n != 1 {
				t.Fatalf("the summary holds %d lines for %s on %s, want one", n, m, d)
			}
			if got.Requests != 5 {
				t.Errorf("%s on %s: %d requests, want the 5 that were dropped", m, d, got.Requests)
			}
			if got.PromptTokens != 50 || got.CompletionTokens != 100 {
				t.Errorf("%s on %s: %d in and %d out, want 50 and 100", m, d, got.PromptTokens, got.CompletionTokens)
			}
			if got.ByClass[ClassOK] != 5 {
				t.Errorf("%s on %s: %d ok, want 5", m, d, got.ByClass[ClassOK])
			}
			if got.DurationMSTotal != 500 || got.QueueWaitMSTotal != 25 || got.LoadWaitMSTotal != 35 {
				t.Errorf("%s on %s: latency totals %+v", m, d, got)
			}
			if got.FirstTokenMSTotal != 150 || got.FirstTokenRequests != 5 {
				t.Errorf("%s on %s: first token %d over %d requests, want 150 over 5", m, d, got.FirstTokenMSTotal, got.FirstTokenRequests)
			}
			if got.TZOffsetMin != 0 {
				t.Errorf("%s on %s: offset %d, want the UTC this test runs in", m, d, got.TZOffsetMin)
			}
		}
	}
	if got, _ := find(sums, "2026-01-11", "org/a"); got.Loads != 1 || got.Removals[ReasonEvicted] != 1 {
		t.Errorf("the load and the eviction were not counted: %+v", got)
	}

	// And the detail is really gone, while the second file is untouched.
	if got := len(names(t, dir)); got != 2 {
		t.Errorf("the directory holds %v, want the surviving file and the summary", names(t, dir))
	}

	// A later drop covering a day already summarised extends that day's line.
	clock.set(day(2026, time.September, 1, 9))
	s.SetRetention(2, 1<<20)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	sums = summaries(t, s)
	got, n := find(sums, "2026-01-11", "org/a")
	if n != 1 {
		t.Fatalf("the summary holds %d lines for org/a on 2026-01-11, want the one it already had", n)
	}
	if got.Requests != 6 {
		t.Errorf("org/a on 2026-01-11: %d requests, want the 5 from the first drop and the 1 from the second", got.Requests)
	}
	if _, n := find(sums, "2026-03-20", "org/a"); n != 1 {
		t.Errorf("the second drop's own day is not in the summary")
	}
}

// The size cap drops files too, and a file dropped for room is summarised
// exactly as one dropped for age.
func TestAFileDroppedForRoomIsSummarisedToo(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, _ := summariseTestStore(t, clock, StoreOptions{MaxBytes: 8 << 10, RotateBytes: 1 << 10})
	on(t, s)
	for i := range 300 {
		if err := s.AppendRequest(request(day(2026, time.January, 10, 9).Add(time.Duration(i)*time.Second), "org/a")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	sums := summaries(t, s)
	got, n := find(sums, "2026-01-10", "org/a")
	if n != 1 {
		t.Fatalf("the summary holds %d lines for the day the cap dropped, want one: %+v", n, sums)
	}
	if got.Requests == 0 {
		t.Fatal("the summary counts no requests at all")
	}
	held := 0
	if err := s.Latest(0, func(l Line) bool {
		if l.Kind == KindRequest {
			held++
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if int(got.Requests)+held != 300 {
		t.Errorf("%d records summarised and %d still held, want 300 between them", got.Requests, held)
	}
}

// The switch governs the summary exactly as it governs everything else: with
// recording off there is no file, and no directory to put one in.
func TestNoSummaryIsWrittenWhileTheSwitchIsOff(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, dir := summariseTestStore(t, clock, StoreOptions{Months: 1})
	for i := range 20 {
		if err := s.AppendRequest(request(day(2025, time.January, 10, 9).Add(time.Duration(i)*time.Second), "org/a")); err != nil {
			t.Fatal(err)
		}
	}
	s.SetRetention(1, 1<<20)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the store directory exists with recording off: %v", names(t, dir))
	}

	// And a store switched off after it has written keeps its summary as it
	// stands: nothing is folded while nothing is recording.
	on(t, s)
	if err := s.AppendRequest(request(day(2025, time.January, 10, 9), "org/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	s.SetRetention(2, 1<<20)
	if slices.Contains(names(t, dir), summaryFileName) {
		t.Error("a summary was written while recording was off")
	}
}

// The summary can hold nothing the detail did not, and far less: counts and
// totals per model per day, and no other key at all.
func TestTheSummaryHoldsOnlyCountsAndTotals(t *testing.T) {
	const sentinel = "org/pineapple-sentinel-7f3a"
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, dir := summariseTestStore(t, clock, StoreOptions{Months: 1})
	on(t, s)
	if err := s.AppendRequest(request(day(2025, time.January, 10, 9), sentinel)); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	s.SetRetention(2, 1<<20)
	s.SetRetention(1, 1<<20)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, summaryFileName))
	if err != nil {
		t.Fatalf("no summary was written: %v", err)
	}
	if !strings.Contains(string(raw), sentinel) {
		t.Fatalf("the summary does not name the model it counts, so this scan proves nothing: %s", raw)
	}
	allowed := map[string]bool{}
	for _, f := range SummaryFields() {
		allowed[f] = true
	}
	for _, f := range summaryIndexFields() {
		allowed[f] = true
	}
	allowed["v"], allowed["kind"] = true, true
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("a summary line is not one JSON object: %q", line)
		}
		for k := range obj {
			if !allowed[k] {
				t.Errorf("the summary line carries %q, which is not one of its documented fields", k)
			}
		}
	}
	// The per-request figures of the record it replaces are not among them.
	for _, gone := range []string{`"streamed"`, `"first_token_ms"`, `"duration_ms"`, `"queue_wait_ms"`} {
		if strings.Contains(string(raw), gone) {
			t.Errorf("the summary carries %s, which is a per-request figure", gone)
		}
	}
}

// A crash between the summary and the drop must cost neither: the records
// must not be counted twice, and the detail must not survive uncounted.
func TestACrashBetweenTheSummaryAndTheDropCountsTheRecordsOnce(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	crash := true
	s, dir := summariseTestStore(t, clock, StoreOptions{Months: 1, afterSummary: func() error {
		if crash {
			return errStoppedForTest
		}
		return nil
	}})
	on(t, s)
	for range 4 {
		if err := s.AppendRequest(request(day(2025, time.January, 10, 9), "org/a")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	s.SetRetention(2, 1<<20)
	s.SetRetention(1, 1<<20)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	// The summary is on disk and the detail is still beside it: this is the
	// state a power cut in the middle of the fold leaves behind.
	if got, _ := find(summaries(t, s), "2025-01-10", "org/a"); got.Requests != 4 {
		t.Fatalf("the summary counts %d requests before the crash, want 4", got.Requests)
	}
	if n := len(names(t, dir)); n != 2 {
		t.Fatalf("the directory holds %v, want the detail file and the summary", names(t, dir))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// The next start reconciles it: the folded file goes, and nothing is
	// counted a second time.
	crash = false
	again := NewStore(dir, StoreOptions{Months: 1, MaxBytes: 1 << 20, RotateBytes: 64 << 10, Now: clock.now, loc: time.UTC})
	defer again.Close()
	on(t, again)
	got, n := find(summaries(t, again), "2025-01-10", "org/a")
	if n != 1 || got.Requests != 4 {
		t.Errorf("after the restart the summary holds %d lines counting %d requests, want one line counting 4", n, got.Requests)
	}
	for _, name := range names(t, dir) {
		if name != summaryFileName {
			t.Errorf("%s is still on disk after it was folded into the summary", name)
		}
	}
}

// The summary is bounded, or a decade of daily lines would crowd out the
// detail it exists to outlive.
func TestTheSummaryStaysWithinItsShareOfTheCap(t *testing.T) {
	clock := &testClock{}
	const cap = 40 << 10
	s, dir := summariseTestStore(t, clock, StoreOptions{Months: 1, MaxBytes: cap, RotateBytes: 2 << 10})
	on(t, s)
	// A year of days, dropped a file at a time.
	for d := range 200 {
		clock.set(day(2026, time.January, 10, 9).AddDate(0, 0, d))
		if err := s.AppendRequest(request(day(2020, time.January, 1, 9).AddDate(0, 0, d), "org/a")); err != nil {
			t.Fatal(err)
		}
		if err := s.Flush(); err != nil {
			t.Fatal(err)
		}
		s.SetRetention(1, cap)
		s.SetRetention(2, cap)
		if err := s.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	fi, err := os.Stat(filepath.Join(dir, summaryFileName))
	if err != nil {
		t.Fatalf("no summary was written at all: %v", err)
	}
	if fi.Size() > cap/summaryShare {
		t.Errorf("the summary holds %d bytes, over its %d-byte share of the cap", fi.Size(), cap/summaryShare)
	}
	sums := summaries(t, s)
	if len(sums) == 0 {
		t.Fatal("the summary holds nothing, so the bound proves nothing")
	}
	// What went is the oldest, so the summary loses its past rather than its
	// present, exactly as the detail does.
	if sums[0].Day < sums[len(sums)-1].Day {
		t.Errorf("the summaries come back oldest first: %s then %s", sums[0].Day, sums[len(sums)-1].Day)
	}
	if sums[len(sums)-1].Day == "2020-01-01" {
		t.Error("nothing was dropped from the summary, so its bound was never reached")
	}
}

// The summary is one of the store's own files in every sense: owner-only,
// and removed by Clear with the rest.
func TestTheSummaryIsOwnerOnlyAndClearRemovesIt(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, dir := summariseTestStore(t, clock, StoreOptions{Months: 1})
	on(t, s)
	if err := s.AppendRequest(request(day(2025, time.January, 10, 9), "org/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	s.SetRetention(2, 1<<20)
	s.SetRetention(1, 1<<20)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(filepath.Join(dir, summaryFileName))
	if err != nil {
		t.Fatalf("no summary was written: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("the summary is mode %04o, want 0600", fi.Mode().Perm())
	}
	if len(summaries(t, s)) == 0 {
		t.Fatal("the summary holds nothing, so clearing it proves nothing")
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(names(t, dir), summaryFileName) {
		t.Error("Clear left the summary behind")
	}
	if got := summaries(t, s); len(got) != 0 {
		t.Errorf("the store still reports %d summary lines after Clear", len(got))
	}
}

// A link planted under the summary's name is refused, as it is under any
// other name the store writes.
func TestALinkPlantedUnderTheSummaryNameIsRefused(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, dir := summariseTestStore(t, clock, StoreOptions{Months: 1})
	on(t, s)
	if err := s.AppendRequest(request(day(2025, time.January, 10, 9), "org/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere.jsonl")
	if err := os.WriteFile(target, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, summaryFileName)); err != nil {
		t.Fatal(err)
	}
	s.SetRetention(2, 1<<20)
	s.SetRetention(1, 1<<20)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "mine\n" {
		t.Errorf("the file the link pointed at was written through: %q", raw)
	}
}

// The reader the dashboard uses: newest day first, and bounded.
func TestSummariesComeBackNewestDayFirst(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, _ := summariseTestStore(t, clock, StoreOptions{Months: 1})
	on(t, s)
	for d := 1; d <= 3; d++ {
		if err := s.AppendRequest(request(day(2025, time.January, d, 9), "org/a")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	s.SetRetention(2, 1<<20)
	s.SetRetention(1, 1<<20)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	got := summaries(t, s)
	want := []string{"2025-01-03", "2025-01-02", "2025-01-01"}
	if len(got) != 3 {
		t.Fatalf("the summary holds %d lines, want three: %+v", len(got), got)
	}
	for i, d := range want {
		if got[i].Day != d {
			t.Fatalf("the summaries came back %v, want newest day first", days(got))
		}
	}
	var bounded []Summary
	if err := s.Summaries(2, func(sum Summary) bool { bounded = append(bounded, sum); return true }); err != nil {
		t.Fatal(err)
	}
	if len(bounded) != 2 || bounded[0].Day != "2025-01-03" {
		t.Errorf("a bound of two gave %v", days(bounded))
	}
}

func days(sums []Summary) []string {
	out := make([]string, 0, len(sums))
	for _, s := range sums {
		out = append(out, s.Day)
	}
	return out
}

// The store's own line about how much room it uses counts the summary, or the
// cap it is judged against would not be the room it takes.
func TestTheSummaryCountsTowardTheRoomTheStoreUses(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, dir := summariseTestStore(t, clock, StoreOptions{Months: 1})
	on(t, s)
	if err := s.AppendRequest(request(day(2025, time.January, 10, 9), "org/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	s.SetRetention(2, 1<<20)
	s.SetRetention(1, 1<<20)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	var onDisk int64
	for _, name := range names(t, dir) {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		onDisk += fi.Size()
	}
	if got := s.Status().Bytes; got != onDisk {
		t.Errorf("the store says it uses %d bytes; the folder holds %d: %v", got, onDisk, names(t, dir))
	}
	if onDisk == 0 {
		t.Fatal("the folder is empty, so this proves nothing")
	}
}

// A summary line is small enough for the arithmetic the page does with it.
func TestASummaryLineIsTheSizeTheDocumentationSays(t *testing.T) {
	b, err := json.Marshal(summaryLine{V: SchemaVersion, Kind: KindSummary, Summary: Summary{
		At: 1767009600, Day: "2026-01-10", Model: "mlx-community/Qwen3-30B-A3B-4bit",
		Requests: 4200, ByClass: map[Class]int64{ClassOK: 4100, ClassCancelled: 100},
		PromptTokens: 4200000, CompletionTokens: 8400000, DurationMSTotal: 42000000,
		QueueWaitMSTotal: 400000, LoadWaitMSTotal: 900000,
		FirstTokenMSTotal: 1200000, FirstTokenRequests: 4100,
		Loads: 12, FailedLoads: 1, Removals: map[string]int64{ReasonEvicted: 8, ReasonIdle: 4},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > ApproxSummaryBytes {
		t.Errorf("a busy day's summary line is %d bytes, over the %d the documentation reasons from: %s", len(b), ApproxSummaryBytes, b)
	}
}

func TestStoreFieldsNamesTheSummarysFieldsToo(t *testing.T) {
	fields := StoreFields()
	for _, f := range append(SummaryFields(), summaryIndexFields()...) {
		if !slices.Contains(fields, f) {
			t.Errorf("StoreFields does not name %q, so the documentation is not held to it", f)
		}
	}
	for _, k := range StoreKinds() {
		if k == "" {
			t.Error("StoreKinds names an empty kind")
		}
	}
	if !slices.Contains(StoreKinds(), KindSummary) || !slices.Contains(StoreKinds(), KindSummaryIndex) {
		t.Errorf("StoreKinds does not name the summary's own kinds: %v", StoreKinds())
	}
}

// The index drives a removal, and it is read off a file. A name in it that is
// not one the store writes is not this code's to delete, whatever the file
// says.
func TestTheIndexCannotAimTheStartupPassAtAnotherFile(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, dir := summariseTestStore(t, clock, StoreOptions{Months: 1})
	on(t, s)
	if err := s.AppendRequest(request(day(2026, time.January, 10, 9), "org/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notes, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(notes)
	if err != nil {
		t.Fatal(err)
	}
	planted := fmt.Sprintf(`{"v":%d,"kind":%q,"folded":[{"name":"notes.txt","bytes":%d,"newest":0}]}`+"\n",
		SchemaVersion, KindSummaryIndex, fi.Size())
	if err := os.WriteFile(filepath.Join(dir, summaryFileName), []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}

	again := NewStore(dir, StoreOptions{Months: 1, MaxBytes: 1 << 20, RotateBytes: 64 << 10, Now: clock.now, loc: time.UTC})
	defer again.Close()
	on(t, again)
	if _, err := os.Stat(notes); err != nil {
		t.Errorf("the startup pass removed a file that is not the store's: %v", err)
	}
}
