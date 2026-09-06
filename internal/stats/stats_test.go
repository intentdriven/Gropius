package stats

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

// clock is a stopped clock the tests move by hand, so a rollup window and a
// record's timestamp are asserted on a figure rather than on wall time.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// refusingStore fails the test if it is written to at all. Nothing in this
// intent has a store; the seam exists for itd-2609061521102742, and a recorder
// that quietly wrote through it would be writing files this unit promised not
// to.
type refusingStore struct{ t *testing.T }

func (s refusingStore) AppendRequest(Record) error {
	s.t.Error("the recorder wrote a request to the store while statistics were off")
	return nil
}

func (s refusingStore) AppendEvent(Event) error {
	s.t.Error("the recorder wrote an event to the store while statistics were off")
	return nil
}

func newTestRecorder(t *testing.T) (*Recorder, *clock) {
	t.Helper()
	c := &clock{now: time.Date(2026, 9, 6, 12, 0, 30, 0, time.UTC)}
	return New(Options{Now: c.Now}), c
}

func okRecord(model string) Record {
	return Record{
		Model:            model,
		Class:            ClassOK,
		Streamed:         true,
		PromptTokens:     11,
		CompletionTokens: 7,
		FirstTokenMS:     20,
		DurationMS:       120,
	}
}

// While the switch is off the recorder is inert: it keeps no record, counts
// nothing, shows nothing and writes nothing through the store seam. That is
// what makes "off is identical to today" a property of the code rather than a
// promise in the documentation.
func TestOffRecordsNothing(t *testing.T) {
	c := &clock{now: time.Date(2026, 9, 6, 12, 0, 30, 0, time.UTC)}
	r := New(Options{Now: c.Now, Store: refusingStore{t}})

	if r.Enabled() {
		t.Error("a new recorder is recording; the switch is off until it is turned on")
	}
	r.Add(okRecord("org/a"))
	r.LoadStarted("org/a")
	r.LoadFinished("org/a", time.Second, nil)
	r.Removed("org/a", ReasonEvicted)

	if got := r.Summary(); len(got) != 0 {
		t.Errorf("Summary() = %v while off, want nothing", got)
	}
	view := r.View()
	if view.Enabled || len(view.Models) != 0 || len(view.Requests) != 0 || len(view.Rollups) != 0 {
		t.Errorf("View() = %+v while off, want an empty view", view)
	}
}

// The panel's figures: one row per request in the order they were served, and
// per-model counters that add up.
func TestOnKeepsRequestsAndCountsThemPerModel(t *testing.T) {
	r, c := newTestRecorder(t)
	r.SetEnabled(true)

	r.Add(okRecord("org/a"))
	c.advance(time.Second)
	r.Add(okRecord("org/a"))
	c.advance(time.Second)
	r.Add(Record{Model: "org/b", Class: ClassBusy, FirstTokenMS: NoFirstToken})

	view := r.View()
	if !view.Enabled {
		t.Error("the view does not report itself as recording")
	}
	if len(view.Requests) != 3 {
		t.Fatalf("the view holds %d requests, want 3", len(view.Requests))
	}
	if view.Requests[0].At != c.Now().Add(-2*time.Second).Unix() {
		t.Errorf("the first request is stamped %d, want the second it was recorded at", view.Requests[0].At)
	}
	if view.Requests[2].Model != "org/b" {
		t.Errorf("the last request is %q, want the most recently recorded", view.Requests[2].Model)
	}

	byModel := map[string]ModelCounters{}
	for _, m := range r.Summary() {
		byModel[m.Model] = m
	}
	a := byModel["org/a"]
	if a.Requests != 2 || a.PromptTokens != 22 || a.CompletionTokens != 14 {
		t.Errorf("org/a counted %+v, want 2 requests, 22 prompt and 14 completion tokens", a)
	}
	if a.ByClass[ClassOK] != 2 {
		t.Errorf("org/a counted %d ok requests, want 2", a.ByClass[ClassOK])
	}
	b := byModel["org/b"]
	if b.Requests != 1 || b.ByClass[ClassBusy] != 1 || b.PromptTokens != 0 {
		t.Errorf("org/b counted %+v, want one busy request and no tokens", b)
	}
	if b.LastFirstTokenMS != NoFirstToken {
		t.Errorf("org/b's last time to first token is %d, want none", b.LastFirstTokenMS)
	}
}

// The ring is the live view, not the archive: it holds the most recent
// RingSize requests and forgets the rest. Keeping them all is what the durable
// store (itd-2609061521102742) is for.
func TestTheRingKeepsTheMostRecentRequests(t *testing.T) {
	r, _ := newTestRecorder(t)
	r.SetEnabled(true)

	for i := range RingSize + 5 {
		rec := okRecord("org/a")
		rec.DurationMS = int64(i)
		r.Add(rec)
	}

	got := r.View().Requests
	if len(got) != RingSize {
		t.Fatalf("the ring holds %d requests, want %d", len(got), RingSize)
	}
	if got[0].DurationMS != 5 {
		t.Errorf("the oldest request held is #%d, want #5 — the five before it should have been dropped", got[0].DurationMS)
	}
	if got[len(got)-1].DurationMS != int64(RingSize+4) {
		t.Errorf("the newest request held is #%d, want #%d", got[len(got)-1].DurationMS, RingSize+4)
	}
	if r.Summary()[0].Requests != RingSize+5 {
		t.Errorf("the counter reads %d, want every request served — the ring forgets, the counter does not",
			r.Summary()[0].Requests)
	}
}

// Turning the switch off is not just "stop recording": what was recorded goes
// too, so the panel returns to showing nothing worked out from a request and
// off is the same state as a fresh start.
func TestSwitchingOffEmptiesTheView(t *testing.T) {
	r, _ := newTestRecorder(t)
	r.SetEnabled(true)
	r.Add(okRecord("org/a"))
	r.Removed("org/a", ReasonEvicted)

	r.SetEnabled(false)

	if view := r.View(); view.Enabled || len(view.Requests) != 0 || len(view.Models) != 0 || len(view.Rollups) != 0 {
		t.Errorf("View() = %+v after the switch went off, want an empty view", view)
	}
	if len(r.Summary()) != 0 {
		t.Errorf("Summary() = %v after the switch went off, want nothing", r.Summary())
	}

	r.SetEnabled(true)
	if len(r.View().Requests) != 0 {
		t.Error("switching the recorder back on brought the old requests back")
	}
}

// Rollups are minute buckets over a day: enough for the panel's shape of
// traffic, bounded whatever the traffic is.
func TestRollupsBucketByMinuteAndSpanADay(t *testing.T) {
	r, c := newTestRecorder(t)
	r.SetEnabled(true)

	r.Add(okRecord("org/a"))
	r.Add(okRecord("org/a"))
	c.advance(90 * time.Second)
	r.Add(okRecord("org/a"))

	rollups := r.View().Rollups
	if len(rollups) != 2 {
		t.Fatalf("two minutes of traffic produced %d rollups, want 2", len(rollups))
	}
	if rollups[0].Minute%60 != 0 {
		t.Errorf("a rollup is stamped %d, which is not the start of a minute", rollups[0].Minute)
	}
	if rollups[0].Requests != 2 || rollups[1].Requests != 1 {
		t.Errorf("rollups counted %d then %d requests, want 2 then 1", rollups[0].Requests, rollups[1].Requests)
	}
	if rollups[0].CompletionTokens != 14 {
		t.Errorf("the first minute holds %d completion tokens, want 14", rollups[0].CompletionTokens)
	}

	c.advance(RollupWindow)
	r.Add(okRecord("org/a"))
	if got := r.View().Rollups; len(got) != 1 {
		t.Errorf("a day later the view holds %d rollups, want only the current minute", len(got))
	}
}

// The pool removes an entry by seven paths and only one of them is an
// eviction. A crash, an idle reap or an operator's unload are counted apart,
// or the load and eviction figures would not reconcile.
func TestOnlyAnEvictionCountsAsOne(t *testing.T) {
	r, _ := newTestRecorder(t)
	r.SetEnabled(true)

	r.LoadStarted("org/a")
	r.LoadFinished("org/a", 3*time.Second, nil)
	r.Removed("org/a", ReasonEvicted)
	r.Removed("org/a", ReasonIdle)
	r.Removed("org/a", ReasonUnloaded)
	r.Removed("org/a", ReasonCrashed)

	m := r.Summary()[0]
	if m.Evictions != 1 {
		t.Errorf("org/a counted %d evictions, want 1 — only the eviction is one", m.Evictions)
	}
	if m.Loads != 1 {
		t.Errorf("org/a counted %d loads, want 1", m.Loads)
	}
	if m.LastLoadMS != 3000 {
		t.Errorf("org/a's last load took %d ms, want 3000", m.LastLoadMS)
	}

	r2, _ := newTestRecorder(t)
	r2.SetEnabled(true)
	r2.LoadStarted("org/b")
	r2.LoadFinished("org/b", time.Second, errors.New("did not become ready"))
	if got := r2.Summary()[0]; got.Loads != 0 || got.FailedLoads != 1 {
		t.Errorf("a load that failed counted %+v, want no load and one failed load", got)
	}
}

// The seam the durable store plugs into: every record and every event the
// recorder keeps is offered to the store as it happens, so
// itd-2609061521102742 needs no second collection path.
func TestEveryRecordIsOfferedToTheStore(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []Record
		events   []Event
	)
	c := &clock{now: time.Date(2026, 9, 6, 12, 0, 30, 0, time.UTC)}
	r := New(Options{Now: c.Now, Store: funcStore{
		request: func(rec Record) error {
			mu.Lock()
			defer mu.Unlock()
			requests = append(requests, rec)
			return nil
		},
		event: func(ev Event) error {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, ev)
			return nil
		},
	}})
	r.SetEnabled(true)

	r.Add(okRecord("org/a"))
	r.LoadStarted("org/a")
	r.LoadFinished("org/a", time.Second, nil)
	r.Removed("org/a", ReasonEvicted)

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 || requests[0].Model != "org/a" || requests[0].At == 0 {
		t.Errorf("the store was offered %v, want the one stamped request", requests)
	}
	if len(events) != 2 {
		t.Fatalf("the store was offered %d events, want the load and the removal", len(events))
	}
	if events[0].Kind != EventLoad || events[0].DurationMS != 1000 {
		t.Errorf("the load event is %+v, want a load of 1000 ms", events[0])
	}
	if events[1].Kind != EventRemoved || events[1].Reason != ReasonEvicted {
		t.Errorf("the removal event is %+v, want a removal with reason %q", events[1], ReasonEvicted)
	}
}

type funcStore struct {
	request func(Record) error
	event   func(Event) error
}

func (s funcStore) AppendRequest(r Record) error { return s.request(r) }
func (s funcStore) AppendEvent(e Event) error    { return s.event(e) }

// A record is a fixed set of counted and timed fields. The panel, the
// documentation and the durable store all read this list, and the field table
// on the how-to page is held to it (internal/archtest), so a field added here
// without a word in the documentation fails the build rather than shipping
// undocumented.
func TestARecordHoldsOnlyCountsTimingsAndTheModel(t *testing.T) {
	want := []string{
		"model", "at", "class", "streamed",
		"prompt_tokens", "completion_tokens",
		"first_token_ms", "duration_ms", "queue_wait_ms", "load_wait_ms",
	}
	if got := RecordFields(); !reflect.DeepEqual(got, want) {
		t.Errorf("a request record holds %v, want %v", got, want)
	}
}

// The recorder is written to from every request goroutine and read from the
// panel's; -race is the assertion.
func TestConcurrentRecordingIsSafe(t *testing.T) {
	r, _ := newTestRecorder(t)
	r.SetEnabled(true)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				r.Add(okRecord("org/a"))
				r.Removed("org/a", ReasonEvicted)
				_ = r.View()
				_ = r.Summary()
			}
		}()
	}
	// The interesting race is the switch going off under a request: that
	// replaces the ring and both maps while the goroutines above are reading
	// and writing them.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 20 {
			r.SetEnabled(false)
			r.SetEnabled(true)
		}
	}()
	wg.Wait()
}

// A recorder that was never built is not a reason for the control panel to
// stop answering: the readers tolerate one.
func TestTheReadersTolerateNoRecorderAtAll(t *testing.T) {
	var r *Recorder
	if r.Enabled() || len(r.Summary()) != 0 || r.View().Enabled {
		t.Error("a nil recorder claims to be recording something")
	}
}

// Off holds nothing, down to the ring itself.
func TestOffHoldsNoRing(t *testing.T) {
	r, _ := newTestRecorder(t)
	if r.ring != nil {
		t.Error("a recorder that is off has already allocated its ring")
	}
	r.SetEnabled(true)
	if len(r.ring) != RingSize {
		t.Errorf("a recording recorder has a ring of %d, want %d", len(r.ring), RingSize)
	}
	r.SetEnabled(false)
	if r.ring != nil {
		t.Error("switching off left the ring allocated")
	}
}
