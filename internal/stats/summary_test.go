package stats

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// errStoppedForTest is what the fold's afterSummary seam returns to stand for
// a crash between writing the summary and dropping the detail it counts.
var errStoppedForTest = errors.New("stopped for test")

// testClock is a clock a test moves by hand, so a store can be made to write
// across days and to look at its horizon from a chosen date.
type testClock struct{ at atomic.Int64 }

func (c *testClock) set(t time.Time) { c.at.Store(t.Unix()) }
func (c *testClock) now() time.Time  { return time.Unix(c.at.Load(), 0).UTC() }

// day is a UTC instant, spelled the way a test reads best.
func day(y int, m time.Month, d, hour int) time.Time {
	return time.Date(y, m, d, hour, 0, 0, 0, time.UTC)
}

// summarizeTestStore is a store whose local day is UTC, so a test can say
// which day a record falls on without asking where this Mac is.
func summarizeTestStore(t *testing.T, clock *testClock, opts StoreOptions) (*FileStore, string) {
	t.Helper()
	opts.Now = clock.now
	opts.loc = time.UTC
	return newTestStore(t, opts)
}

// summaries returns every summary line the store holds, in the order the
// reader gives them.
func summaries(t *testing.T, s *FileStore) []SummaryDay {
	t.Helper()
	var out []SummaryDay
	if err := s.Summaries(0, func(sum SummaryDay) bool { out = append(out, sum); return true }); err != nil {
		t.Fatalf("reading the summaries: %v", err)
	}
	return out
}

// find returns the summary line for one model and day, and whether there was
// exactly one.
func find(sums []SummaryDay, day, model string) (SummaryDay, int) {
	var got SummaryDay
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
// per day, and a later drop for a day already summarized extends the line
// rather than writing a second one.
func TestADroppedFileIsSummarizedFirstAndALaterDropExtendsTheDay(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, dir := summarizeTestStore(t, clock, StoreOptions{Months: 2})
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

	// The horizon drops the first file. Every record in it is summarized
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

	// A later drop covering a day already summarized extends that day's line.
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

// The size cap drops files too, and a file dropped for room is summarized
// exactly as one dropped for age.
func TestAFileDroppedForRoomIsSummarizedToo(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, _ := summarizeTestStore(t, clock, StoreOptions{MaxBytes: 8 << 10, RotateBytes: 1 << 10})
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
		t.Errorf("%d records summarized and %d still held, want 300 between them", got.Requests, held)
	}
}

// The switch governs the summary exactly as it governs everything else: with
// recording off there is no file, and no directory to put one in.
func TestNoSummaryIsWrittenWhileTheSwitchIsOff(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, dir := summarizeTestStore(t, clock, StoreOptions{Months: 1})
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
	s, dir := summarizeTestStore(t, clock, StoreOptions{Months: 1})
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
	s, dir := summarizeTestStore(t, clock, StoreOptions{Months: 1, afterSummary: func() error {
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
	s, dir := summarizeTestStore(t, clock, StoreOptions{Months: 1, MaxBytes: cap, RotateBytes: 2 << 10})
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
	s, dir := summarizeTestStore(t, clock, StoreOptions{Months: 1})
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
	s, dir := summarizeTestStore(t, clock, StoreOptions{Months: 1})
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
	s, _ := summarizeTestStore(t, clock, StoreOptions{Months: 1})
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
	var bounded []SummaryDay
	if err := s.Summaries(2, func(sum SummaryDay) bool { bounded = append(bounded, sum); return true }); err != nil {
		t.Fatal(err)
	}
	if len(bounded) != 2 || bounded[0].Day != "2025-01-03" {
		t.Errorf("a bound of two gave %v", days(bounded))
	}
}

func days(sums []SummaryDay) []string {
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
	s, dir := summarizeTestStore(t, clock, StoreOptions{Months: 1})
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

// A summary line is bounded, and the bound is the one the documentation does
// its arithmetic with. The shape is the widest the format can emit: every
// outcome class and every removal reason present, a long repo id, and every
// counter run up as far as a real Mac could take it.
func TestASummaryLineIsTheSizeTheDocumentationSays(t *testing.T) {
	widest := Summary{
		At: 1767009600, Day: "2026-01-10", TZOffsetMin: -720,
		Model:              "mlx-community/DeepSeek-R1-Distill-Qwen-32B-8bit-mlx-experimental",
		Requests:           999999,
		PromptTokens:       999999999,
		CompletionTokens:   999999999,
		DurationMSTotal:    999999999,
		QueueWaitMSTotal:   999999999,
		LoadWaitMSTotal:    999999999,
		FirstTokenMSTotal:  999999999,
		FirstTokenRequests: 999999,
		Loads:              999999,
		FailedLoads:        999999,
		ByClass:            map[Class]int64{},
		Removals:           map[string]int64{},
	}
	for _, c := range OutcomeClasses() {
		widest.ByClass[c] = 999999
	}
	for _, r := range RemovalReasons() {
		widest.Removals[r] = 999999
	}
	b, err := json.Marshal(summaryLine{V: SchemaVersion, Kind: KindSummary, Summary: widest})
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > ApproxSummaryBytes {
		t.Errorf("the widest summary line is %d bytes, over the %d the documentation reasons from: %s", len(b), ApproxSummaryBytes, b)
	}
	if len(b) < ApproxSummaryBytes/2 {
		t.Errorf("the widest summary line is %d bytes against a bound of %d, which is loose enough to hide a field being added", len(b), ApproxSummaryBytes)
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
	s, dir := summarizeTestStore(t, clock, StoreOptions{Months: 1})
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

// A record file that goes from under the writer — deleted by hand, restored
// from a backup — must cost that file and nothing else. Before the fold, a
// removal that found nothing there was tolerated; the fold must tolerate it
// too, or every rotation from then on fails and the store stops recording for
// the life of the process.
func TestARecordFileThatVanishesDoesNotStopTheStoreRecording(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, dir := summarizeTestStore(t, clock, StoreOptions{MaxBytes: 8 << 10, RotateBytes: 1 << 10})
	on(t, s)
	for i := range 40 {
		if err := s.AppendRequest(request(day(2026, time.January, 10, 9).Add(time.Duration(i)*time.Second), "org/a")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	held := recordFiles(t, dir)
	if len(held) < 2 {
		t.Fatalf("the store holds %v, want more than one file so one can be taken away", held)
	}
	if err := os.Remove(filepath.Join(dir, held[0])); err != nil {
		t.Fatal(err)
	}

	// Everything that follows must still be recorded.
	for i := range 200 {
		if err := s.AppendRequest(request(day(2026, time.January, 11, 9).Add(time.Duration(i)*time.Second), "org/b")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	seen := 0
	if err := s.Latest(0, func(l Line) bool {
		if l.Kind == KindRequest && l.Request.Model == "org/b" {
			seen++
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Errorf("nothing recorded after a file was taken away; %d records were dropped", s.Status().Dropped)
	}
}

// A crash between making the summary's temporary file and renaming it into
// place leaves an orphan. Nothing else names it, so nothing else would ever
// remove it, and it would sit against a cap it was not counted toward.
func TestAHalfWrittenSummaryIsSweptAtTheNextStart(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, dir := summarizeTestStore(t, clock, StoreOptions{Months: 1})
	on(t, s)
	if err := s.AppendRequest(request(day(2026, time.January, 10, 9), "org/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, summaryTempPrefix+"deadbeef"+summaryTempSuffix)
	if err := os.WriteFile(orphan, make([]byte, 900<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(keep, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	on(t, s)
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("a half-written summary survived the restart, against a cap that does not count it: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("the sweep took a file that is not the store's: %v", err)
	}
}

// A fold that cannot be done must cost retention and nothing else. It reaches
// the write path through rotate, where an error is counted as a lost record
// and the open file is dropped — so one unreadable file would otherwise cost
// every record from then on, silently, for the life of the process.
func TestAFoldThatCannotBeDoneStopsRetentionAndNothingElse(t *testing.T) {
	const wrote = 300
	// What M1 is about: the write path loses nothing and recording carries on.
	// How much of the past survives is retention's business, and once the store
	// is over its cap with no way to summarize what it drops, the cap wins and
	// the loss is counted where it can be seen — which is the decision recorded
	// against adr-2609061610107154.
	live := func(t *testing.T, s *FileStore, newest int64) {
		t.Helper()
		if got := s.Status().Dropped; got != 0 {
			t.Errorf("%d records were lost from the write path because retention could not summarize what it wanted to drop", got)
		}
		seen, latest := 0, int64(0)
		if err := s.Latest(0, func(l Line) bool {
			if l.Kind == KindRequest {
				seen++
				if l.At > latest {
					latest = l.At
				}
			}
			return true
		}); err != nil {
			t.Fatal(err)
		}
		if seen == 0 {
			t.Fatal("the store recorded nothing at all")
		}
		if latest != newest {
			t.Errorf("the newest record held is from %d, want the last one written at %d: recording stopped", latest, newest)
		}
		st := s.Status()
		if !st.RetentionWedged && st.Unsummarized == 0 {
			t.Error("the store says nothing about a retention it could not do: neither stuck nor a loss counted")
		}
	}

	t.Run("a link planted under the summary", func(t *testing.T) {
		clock := &testClock{}
		clock.set(day(2026, time.January, 10, 9))
		s, dir := summarizeTestStore(t, clock, StoreOptions{MaxBytes: 8 << 10, RotateBytes: 1 << 10})
		on(t, s)
		target := filepath.Join(t.TempDir(), "elsewhere.jsonl")
		if err := os.WriteFile(target, []byte("mine\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, summaryFileName)); err != nil {
			t.Fatal(err)
		}
		for i := range wrote {
			if err := s.AppendRequest(request(day(2026, time.January, 10, 9).Add(time.Duration(i)*time.Second), "org/a")); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Flush(); err != nil {
			t.Fatal(err)
		}
		live(t, s, day(2026, time.January, 10, 9).Add(time.Duration(wrote-1)*time.Second).Unix())
		if !s.Status().RetentionWedged {
			t.Error("the store does not say retention is stuck, though the summary's name is a link it will never write through")
		}
		raw, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != "mine\n" {
			t.Errorf("the file the link pointed at was written through: %q", raw)
		}
	})

	t.Run("a record file that cannot be read", func(t *testing.T) {
		clock := &testClock{}
		clock.set(day(2026, time.January, 10, 9))
		s, dir := summarizeTestStore(t, clock, StoreOptions{MaxBytes: 8 << 10, RotateBytes: 1 << 10})
		on(t, s)
		for i := range wrote {
			if i == 30 {
				if err := s.Flush(); err != nil {
					t.Fatal(err)
				}
				held := recordFiles(t, dir)
				if len(held) == 0 {
					t.Fatal("nothing was written yet, so there is no file to make unreadable")
				}
				shut := filepath.Join(dir, held[0])
				if err := os.Chmod(shut, 0o000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(shut, 0o600) })
			}
			if err := s.AppendRequest(request(day(2026, time.January, 10, 9).Add(time.Duration(i)*time.Second), "org/a")); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Flush(); err != nil {
			t.Fatal(err)
		}
		live(t, s, day(2026, time.January, 10, 9).Add(time.Duration(wrote-1)*time.Second).Unix())
	})
}

// The fold rewrites the whole summary file, so a line it cannot read is a line
// it would delete. The format's own promise is the opposite: a reader skips
// what it does not understand. A newer Gropius's line must survive an older
// one's fold.
func TestAFoldCarriesALineThisBuildCannotReadThrough(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, dir := summarizeTestStore(t, clock, StoreOptions{Months: 1})
	on(t, s)
	if err := s.AppendRequest(request(day(2025, time.January, 10, 9), "org/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	newer := fmt.Sprintf(`{"v":%d,"kind":%q,"at":1735516800,"day":"2025-01-01","model":"org/z","requests":7,"something_new":true}`,
		SchemaVersion+1, KindSummary)
	torn := `{"v":1,"kind":"summary","day":"2024-12-`
	planted := fmt.Sprintf("{\"v\":%d,\"kind\":%q,\"folded\":[]}\n%s\n%s\n", SchemaVersion, KindSummaryIndex, newer, torn)
	if err := os.WriteFile(filepath.Join(dir, summaryFileName), []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}

	on(t, s)
	s.SetRetention(2, 1<<20)
	s.SetRetention(1, 1<<20)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, summaryFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), newer) {
		t.Errorf("the fold deleted a line a newer Gropius wrote:\n%s", raw)
	}
	if !strings.Contains(string(raw), torn) {
		t.Errorf("the fold deleted a line it could not parse:\n%s", raw)
	}
	if _, n := find(summaries(t, s), "2025-01-10", "org/a"); n != 1 {
		t.Errorf("the fold did not do its own work while carrying the others through:\n%s", raw)
	}
}

// A file that was counted and could not then be removed must never be appended
// to. The next fold would count what was appended a second time, and the
// records it holds are already in the summary.
func TestAFoldedFileThatCouldNotBeRemovedIsNeverAppendedTo(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	stick := true
	s, dir := summarizeTestStore(t, clock, StoreOptions{Months: 1, afterSummary: func() error {
		if stick {
			return errStoppedForTest
		}
		return nil
	}})
	on(t, s)
	for range 3 {
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
	held := recordFiles(t, dir)
	if len(held) != 1 {
		t.Fatalf("the store holds %v, want the one file the fold counted and could not remove", held)
	}
	folded := filepath.Join(dir, held[0])
	before, err := os.Stat(folded)
	if err != nil {
		t.Fatal(err)
	}

	// Everything recorded from here must go somewhere else.
	if err := s.AppendRequest(request(day(2026, time.January, 10, 9), "org/b")); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(folded)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Errorf("a file already counted in the summary grew from %d to %d bytes; the next fold would count that twice",
			before.Size(), after.Size())
	}
	if got := len(recordFiles(t, dir)); got != 2 {
		t.Errorf("the store holds %v, want a fresh file beside the one it could not remove", recordFiles(t, dir))
	}
	if got, _ := find(summaries(t, s), "2025-01-10", "org/a"); got.Requests != 3 {
		t.Errorf("the summary counts %d requests, want the 3 it folded once", got.Requests)
	}
}

// The summary is bounded by its share of the cap, but a summary that dropped
// its way to empty would take room while saying nothing.
func TestTheLastDayOfTheSummaryIsKeptEvenOverItsShare(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, _ := summarizeTestStore(t, clock, StoreOptions{Months: 1, MaxBytes: 2 << 10, RotateBytes: 512})
	on(t, s)
	for range 3 {
		if err := s.AppendRequest(request(day(2025, time.January, 10, 9), "org/a")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	s.SetRetention(2, 2<<10)
	s.SetRetention(1, 2<<10)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	sums := summaries(t, s)
	if len(sums) != 1 {
		t.Fatalf("the summary holds %d lines against a share of %d bytes, want the one day it counted: %v",
			len(sums), SummaryShareOfCap(2<<10), days(sums))
	}
}

// A day the store still holds records from is only partly summarized, and a
// view has to be able to tell the two apart: "these are the totals for a month
// whose detail is gone" is a different sentence from "these are the totals for
// the part of today that has been dropped".
func TestASummaryDaySaysWhetherAnyOfItsDetailIsHeld(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 11, 9))
	s, dir := summarizeTestStore(t, clock, StoreOptions{})
	on(t, s)
	// One file spanning two days, then a second file holding only the later
	// day, so a drop of the first leaves the later day partly held. The clock
	// moves between them because a file is started by the day it is opened on,
	// not by the day its records fall on.
	for _, at := range []time.Time{day(2026, time.January, 10, 9), day(2026, time.January, 11, 9)} {
		if err := s.AppendRequest(request(at, "org/a")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	clock.set(day(2026, time.January, 12, 9))
	on(t, s)
	for range 3 {
		if err := s.AppendRequest(request(day(2026, time.January, 11, 12), "org/a")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	held := recordFiles(t, dir)
	if len(held) != 2 {
		t.Fatalf("the store holds %v, want the two files this test wrote", held)
	}
	second, err := os.Stat(filepath.Join(dir, held[1]))
	if err != nil {
		t.Fatal(err)
	}
	// A cap with room for the second file and the next one, but not for both.
	s.SetRetention(1200, second.Size()+(64<<10))
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	sums := summaries(t, s)
	gone, n := find(sums, "2026-01-10", "org/a")
	if n != 1 {
		t.Fatalf("the summary holds %d lines for the day that is wholly gone: %v", n, days(sums))
	}
	if gone.DetailHeld {
		t.Error("a day with no records left is reported as one whose detail is still held")
	}
	partly, n := find(sums, "2026-01-11", "org/a")
	if n != 1 {
		t.Fatalf("the summary holds %d lines for the day that is partly gone: %v", n, days(sums))
	}
	if !partly.DetailHeld {
		t.Error("a day the store still holds records from is reported as one whose detail is gone")
	}
}

// promptly runs what a wedged store must never make a caller wait for, and
// fails rather than hanging the suite. A named pipe left under a name the
// store reads parks whoever opens it forever, and the store's lifecycle lock
// is held on the way in — so this is the difference between a store that
// refuses and an app whose settings, figures and shutdown are all stuck while
// it goes on answering requests.
func promptly(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return within five seconds: a file planted in the store directory has parked it", what)
	}
}

// A pipe, a device or a directory under a name the store reads must be refused
// on the handle, not opened and waited on.
func TestSomethingThatIsNotAFileUnderTheSummaryNameIsRefusedNotWaitedOn(t *testing.T) {
	t.Run("planted before recording starts", func(t *testing.T) {
		clock := &testClock{}
		clock.set(day(2026, time.January, 10, 9))
		s, dir := summarizeTestStore(t, clock, StoreOptions{Months: 1})
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(filepath.Join(dir, summaryFileName), 0o600); err != nil {
			t.Skipf("this filesystem will not take a named pipe: %v", err)
		}
		promptly(t, "switching recording on", func() { s.SetEnabled(true) })
		if !s.Enabled() {
			t.Fatal("the store refused to record because of a file that is not one of its own")
		}
		promptly(t, "recording a request", func() {
			if err := s.AppendRequest(request(day(2025, time.January, 10, 9), "org/a")); err != nil {
				t.Error(err)
			}
		})
		promptly(t, "reading the summaries", func() {
			// An error is the right answer; hanging is not.
			_ = s.Summaries(0, func(SummaryDay) bool { return true })
		})
		promptly(t, "flushing", func() {
			if err := s.Flush(); err != nil {
				t.Error(err)
			}
		})
		promptly(t, "closing", func() {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
		fi, err := os.Lstat(filepath.Join(dir, summaryFileName))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&os.ModeNamedPipe == 0 {
			t.Error("the store wrote over what was planted rather than refusing it")
		}
	})

	t.Run("planted while recording", func(t *testing.T) {
		clock := &testClock{}
		clock.set(day(2026, time.January, 10, 9))
		s, dir := summarizeTestStore(t, clock, StoreOptions{MaxBytes: 8 << 10, RotateBytes: 1 << 10})
		on(t, s)
		if err := s.AppendRequest(request(day(2026, time.January, 10, 9), "org/a")); err != nil {
			t.Fatal(err)
		}
		if err := s.Flush(); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(filepath.Join(dir, summaryFileName), 0o600); err != nil {
			t.Skipf("this filesystem will not take a named pipe: %v", err)
		}
		promptly(t, "recording through a rotation", func() {
			for i := range 200 {
				if err := s.AppendRequest(request(day(2026, time.January, 10, 9).Add(time.Duration(i)*time.Second), "org/a")); err != nil {
					t.Error(err)
					return
				}
			}
		})
		promptly(t, "flushing", func() {
			if err := s.Flush(); err != nil {
				t.Error(err)
			}
		})
		promptly(t, "reading the summaries", func() {
			_ = s.Summaries(0, func(SummaryDay) bool { return true })
		})
		promptly(t, "switching recording off", func() {
			if err := s.SetEnabled(false); err != nil {
				t.Error(err)
			}
		})
		if !s.Status().RetentionWedged {
			t.Error("the store does not say retention is stuck, though it cannot write its summary")
		}
	})
}

// The size limit is the hard bound and it still wins. Keeping records that
// cannot be summarized is the right first answer; growing without end because
// of it is not, and a full disk is exactly the case that makes the fold fail.
func TestTheSizeLimitStillWinsWhenTheSummaryCannotBeWritten(t *testing.T) {
	const cap = 8 << 10
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, dir := summarizeTestStore(t, clock, StoreOptions{MaxBytes: cap, RotateBytes: 1 << 10})
	on(t, s)
	if err := s.AppendRequest(request(day(2026, time.January, 10, 9), "org/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	// A summary that cannot be read is a summary that cannot be written: the
	// fold reads it before it folds into it. This is what a full disk does to
	// the same path.
	blocked := filepath.Join(dir, summaryFileName)
	if err := os.WriteFile(blocked, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blocked, 0o600) })

	for i := range 400 {
		if err := s.AppendRequest(request(day(2026, time.January, 10, 9).Add(time.Duration(i)*time.Second), "org/a")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}

	var total int64
	for _, name := range names(t, dir) {
		fi, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		total += fi.Size()
	}
	if total > cap+(1<<10) {
		t.Errorf("the store holds %d bytes against a %d-byte cap it cannot summarize its way down to", total, cap)
	}
	st := s.Status()
	if !st.RetentionWedged {
		t.Error("the store dropped records without a summary and does not say retention is stuck")
	}
	if st.Unsummarized == 0 {
		t.Error("records were dropped without a summary and the figure nobody can see is zero")
	}
	if st.Dropped != 0 {
		t.Errorf("%d records were lost from the write path; only retention should be losing anything here", st.Dropped)
	}
}

// Lines this build cannot read are carried through, but they are inside the
// bound like everything else: a blob of them would otherwise be a permanent
// tax on the records the operator asked to keep.
func TestCarriedLinesAreInsideTheSummarysShareOfTheCap(t *testing.T) {
	const cap = 64 << 10
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	s, dir := summarizeTestStore(t, clock, StoreOptions{Months: 1, MaxBytes: cap, RotateBytes: 2 << 10})
	on(t, s)
	if err := s.AppendRequest(request(day(2025, time.January, 10, 9), "org/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var planted strings.Builder
	fmt.Fprintf(&planted, "{\"v\":%d,\"kind\":%q,\"folded\":[]}\n", SchemaVersion, KindSummaryIndex)
	for i := range 400 {
		fmt.Fprintf(&planted, `{"v":99,"kind":"summary","day":"2019-01-01","model":"org/from-the-future","n":%d,"pad":"%s"}`+"\n",
			i, strings.Repeat("x", 200))
	}
	if err := os.WriteFile(filepath.Join(dir, summaryFileName), []byte(planted.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(filepath.Join(dir, summaryFileName))
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() <= SummaryShareOfCap(cap) {
		t.Fatalf("the planted summary is %d bytes, inside the %d-byte share, so this proves nothing",
			before.Size(), SummaryShareOfCap(cap))
	}

	on(t, s)
	s.SetRetention(2, cap)
	s.SetRetention(1, cap)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filepath.Join(dir, summaryFileName))
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() > SummaryShareOfCap(cap) {
		t.Errorf("the summary holds %d bytes, over its %d-byte share, because the lines it carries through are exempt from it",
			after.Size(), SummaryShareOfCap(cap))
	}
	// What it folded itself survives, and some of what it carried does too.
	if _, n := find(summaries(t, s), "2025-01-10", "org/a"); n != 1 {
		t.Error("the fold dropped its own day rather than the lines it cannot read")
	}
	raw, err := os.ReadFile(filepath.Join(dir, summaryFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "org/from-the-future") {
		t.Error("every carried line was dropped; the bound should take the oldest, not all of them")
	}
}

// A pass that changed nothing must not rewrite the file. Every rewrite is an
// fsync, and a fold that is only retrying a removal changes nothing at all.
func TestAPassThatChangedNothingDoesNotRewriteTheSummary(t *testing.T) {
	clock := &testClock{}
	clock.set(day(2026, time.January, 10, 9))
	stick := true
	s, dir := summarizeTestStore(t, clock, StoreOptions{Months: 1, afterSummary: func() error {
		if stick {
			return errStoppedForTest
		}
		return nil
	}})
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
	summary := filepath.Join(dir, summaryFileName)
	// A line this build cannot read, which is the case the condition got
	// wrong: it is already in the file exactly as it would be written back.
	f, err := os.OpenFile(summary, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"v":99,"kind":"summary","day":"2019-01-01","model":"org/from-the-future"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	first, err := os.Stat(summary)
	if err != nil {
		t.Fatal(err)
	}
	// A whole second, so a rewrite cannot land inside the same timestamp.
	clock.set(day(2026, time.January, 10, 10))
	os.Chtimes(summary, first.ModTime().Add(-time.Hour), first.ModTime().Add(-time.Hour))
	was, err := os.Stat(summary)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		s.SetRetention(2, 1<<20)
		s.SetRetention(1, 1<<20)
		if err := s.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	now, err := os.Stat(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !now.ModTime().Equal(was.ModTime()) {
		t.Errorf("the summary was rewritten by a pass that folded nothing: %s became %s", was.ModTime(), now.ModTime())
	}
}
