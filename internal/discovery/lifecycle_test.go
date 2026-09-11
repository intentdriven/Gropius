package discovery

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brutella/dnssd"
)

// fakeAnnouncer stands in for the real dnssd registration so the advertisement
// lifecycle can be driven in-process, with no multicast socket and no waiting
// on the production refresh interval.
//
// Every step appends to an ordered event log, which is what the tests assert
// on: the ORDER of register/respond/exit is the whole point of iss-12.
type fakeAnnouncer struct {
	mu       sync.Mutex
	events   []string
	texts    []map[string]string // TXT record of each successful registration
	live     int                 // registrations currently inside Respond
	failAt   map[int]bool        // 0-based registration attempts that fail
	attempts int
	blockAt  int           // 0-based attempt that waits on gate (-1: none)
	gate     chan struct{} // released by the test

	serveFailAt map[int]bool // 0-based Respond attempts that fail immediately
	serves      int

	// serveBlockAt is the 0-based Respond attempt that announces itself on
	// serveEntered and then waits on serveGate (-1: none). Holding a responder
	// at the door is how a test turns "what state was the lifecycle in between
	// these two steps" into an assertion it can take at leisure, instead of a
	// count read at whatever instant the step happened to complete.
	serveBlockAt int
	serveEntered chan struct{} // closed when that attempt is entered
	serveGate    chan struct{} // released by the test

	// outcomes counts the Respond attempts that have recorded what became of
	// them. serves counts the ones that have started, so the two being equal
	// says no responder is mid-flight — the one thing a test cannot read off
	// the event log, and the thing the refresh loop's own evidence turns on.
	outcomes int
}

func newFakeAnnouncer() *fakeAnnouncer {
	return &fakeAnnouncer{
		failAt:       map[int]bool{},
		serveFailAt:  map[int]bool{},
		blockAt:      -1,
		gate:         make(chan struct{}),
		serveBlockAt: -1,
		serveEntered: make(chan struct{}),
		serveGate:    make(chan struct{}),
	}
}

func (f *fakeAnnouncer) record(ev string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *fakeAnnouncer) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

// settle marks the current Respond attempt as having reached its outcome.
func (f *fakeAnnouncer) settle() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes++
}

// settled reports whether every Respond attempt so far has reached its outcome.
func (f *fakeAnnouncer) settled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.serves == f.outcomes
}

func (f *fakeAnnouncer) liveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live
}

func (f *fakeAnnouncer) registeredTexts() []map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]string(nil), f.texts...)
}

func (f *fakeAnnouncer) Register(cfg dnssd.Config) (registration, error) {
	f.mu.Lock()
	n := f.attempts
	f.attempts++
	fail := f.failAt[n]
	block := n == f.blockAt
	f.mu.Unlock()

	if block {
		f.record("register-entered")
		<-f.gate
	}
	if fail {
		f.record("register-failed")
		return nil, errors.New("mDNS registration refused")
	}

	f.mu.Lock()
	id := len(f.texts)
	f.texts = append(f.texts, cfg.Text)
	f.mu.Unlock()
	f.record("register:" + cfg.Text["auth"])
	return &fakeRegistration{f: f, id: id}, nil
}

// fakeRegistration is one registration. id identifies it, so a test can tell a
// registration that was served twice from two registrations served once — the
// difference between reusing a socket pair and opening another.
type fakeRegistration struct {
	f  *fakeAnnouncer
	id int
}

func (r *fakeRegistration) Respond(ctx context.Context) error {
	r.f.mu.Lock()
	n := r.f.serves
	r.f.serves++
	fail := r.f.serveFailAt[n]
	block := n == r.f.serveBlockAt
	r.f.mu.Unlock()

	// Held before anything is recorded, so a test that waits on serveEntered
	// knows every earlier step is in the log and no part of this one is.
	if block {
		close(r.f.serveEntered)
		<-r.f.serveGate
	}

	// dnssd probes and announces inside Respond, not in Add, so a registration
	// that was accepted can still fail the moment it is served.
	if fail {
		r.f.record("serve-failed#" + strconv.Itoa(r.id))
		r.f.settle()
		return errors.New("mDNS probe failed")
	}

	r.f.mu.Lock()
	r.f.live++
	r.f.mu.Unlock()
	r.f.record("respond#" + strconv.Itoa(r.id))
	r.f.settle()

	<-ctx.Done()

	r.f.mu.Lock()
	r.f.live--
	r.f.mu.Unlock()
	r.f.record("exit#" + strconv.Itoa(r.id))
	return nil
}

// hints holds the live auth/model state the advertiser reads. The refresh loop
// reads it from its own goroutine, so a test that changes it must do so under a
// lock — otherwise the test itself is the data race.
type hints struct {
	mu     sync.Mutex
	auth   bool
	models int
}

func (h *hints) setAuth(v bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.auth = v
}

func (h *hints) bump(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.auth = !h.auth
	h.models += n
}

// logRecorder captures what the advertiser says. An outage that persists fails
// identically every tick, so how OFTEN it is reported is a property worth
// asserting, not only what is reported.
type logRecorder struct {
	mu   sync.Mutex
	said []string // "LEVEL message"

	// before, if set, runs in the logging goroutine before the line is
	// recorded. The refresh loop logs from its own goroutine, so this is the
	// one seam a test has INSIDE that loop — and the only one that falls
	// between the moment a responder is started and the moment the loop looks
	// at it again, which is exactly where the recovery report's evidence is
	// decided.
	before func(msg string)
}

func (l *logRecorder) Enabled(context.Context, slog.Level) bool { return true }

// hold installs fn, which runs on the logging goroutine before each line.
func (l *logRecorder) hold(fn func(msg string)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.before = fn
}

func (l *logRecorder) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	before := l.before
	l.mu.Unlock()
	if before != nil {
		before(r.Message)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.said = append(l.said, r.Level.String()+" "+r.Message)
	return nil
}

func (l *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logRecorder) WithGroup(string) slog.Handler      { return l }

func (l *logRecorder) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.said...)
}

// saidAbout counts the recorded lines containing substr.
func (l *logRecorder) saidAbout(substr string) int {
	n := 0
	for _, line := range l.lines() {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

// testAdvertiser builds an Advertiser wired to the fake, ticking fast enough
// that a test never waits on the production interval.
func testAdvertiser(f *fakeAnnouncer, h *hints) (*Advertiser, *logRecorder) {
	rec := &logRecorder{}
	return &Advertiser{
		Port: 11434,
		AuthRequired: func() bool {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.auth
		},
		Models: func() int {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.models
		},
		Log:      slog.New(rec),
		announce: f,
		interval: time.Millisecond,
	}, rec
}

// waitFor polls until cond holds over the fake's event log, or fails the test.
func waitFor(t *testing.T, f *fakeAnnouncer, what string, cond func([]string) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond(f.log()) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; events were %v", what, f.log())
}

// waitUntil polls cond until it holds, or fails the test.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func count(events []string, prefix string) int {
	n := 0
	for _, e := range events {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			n++
		}
	}
	return n
}

// A change to the advertised TXT record must be published by withdrawing the
// whole advertisement and registering it afresh — never by mutating the service
// a running responder is reading.
//
// dnssd's ServiceHandle.UpdateText writes Service.Text with no locking, while
// the responder goroutine reads that same field under a mutex private to the
// library. The two are genuinely unsynchronized and nothing in this package can
// close the gap from the outside, so the fix is to stop doing it: cancel the
// responder, WAIT for it to exit, then register again.
//
// The order is the assertion. A second registration that begins before the
// first responder has exited would mean two responders briefly serving the same
// name, which is the overlap this test exists to refuse.
func TestATextChangeReRegistersInsteadOfMutatingTheLiveService(t *testing.T) {
	f := newFakeAnnouncer()
	var h hints
	a, rec := testAdvertiser(f, &h)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	waitFor(t, f, "the first advertisement to be responding", func(ev []string) bool {
		return len(ev) >= 2 && ev[0] == "register:none" && ev[1] == "respond#0"
	})

	// Alice sets an API key in the control panel: the advertised auth hint
	// changes from none to bearer.
	h.setAuth(true)

	waitFor(t, f, "the advertisement to be re-registered", func(ev []string) bool {
		return count(ev, "register:") == 2
	})

	ev := f.log()
	if len(ev) < 4 {
		t.Fatalf("events = %v, want at least register, respond, exit, register", ev)
	}
	want := []string{"register:none", "respond#0", "exit#0", "register:bearer"}
	for i, w := range want {
		if ev[i] != w {
			t.Fatalf("events = %v, want them to start %v — a TXT change must stop the "+
				"running responder and wait for it to exit before registering again", ev, want)
		}
	}

	waitFor(t, f, "the replacement advertisement to be responding", func(ev []string) bool {
		return count(ev, "respond#") == 2
	})

	texts := f.registeredTexts()
	if len(texts) != 2 || texts[1]["auth"] != "bearer" {
		t.Fatalf("registered TXT records = %v, want the second to carry auth=bearer", texts)
	}
	if got := rec.saidAbout("updated network advertisement"); got != 1 {
		t.Errorf("logged the update %d times, want once: %v", got, rec.lines())
	}
}

// Withdrawing and re-probing costs peers a cache flush, so it happens only on
// an actual change. Ticks that find the same hints must leave the running
// advertisement alone.
func TestUnchangedHintsDoNotReRegister(t *testing.T) {
	f := newFakeAnnouncer()
	var h hints
	a, rec := testAdvertiser(f, &h)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, f, "the advertisement to be responding", func(ev []string) bool {
		return count(ev, "respond#") == 1
	})

	// Many ticks at a millisecond apiece, with nothing changing.
	time.Sleep(60 * time.Millisecond)
	a.Stop()

	if got := count(f.log(), "register:"); got != 1 {
		t.Errorf("%d registrations over ~60 ticks with unchanged hints, want 1: %v", got, f.log())
	}
	if got := rec.saidAbout("updated network advertisement"); got != 0 {
		t.Errorf("logged an update %d times with nothing changed: %v", got, rec.lines())
	}
}

// A re-registration that fails leaves the service off the network entirely, so
// the next tick must try again rather than treating the change as published.
func TestAFailedReRegistrationIsRetriedOnTheNextTick(t *testing.T) {
	f := newFakeAnnouncer()
	f.failAt[1] = true // the re-registration, not the initial one
	var h hints
	a, rec := testAdvertiser(f, &h)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	waitFor(t, f, "the advertisement to be responding", func(ev []string) bool {
		return count(ev, "respond#") == 1
	})

	h.setAuth(true)

	waitFor(t, f, "the failed re-registration to be retried", func(ev []string) bool {
		return count(ev, "register-failed") == 1 && count(ev, "register:") == 2
	})
	waitFor(t, f, "the retried advertisement to be responding", func(ev []string) bool {
		return count(ev, "respond#") == 2
	})

	if got := f.liveCount(); got != 1 {
		t.Errorf("live responders = %d after the retry, want 1", got)
	}
	if got := rec.saidAbout("could not publish"); got != 1 {
		t.Errorf("reported the failure %d times, want once: %v", got, rec.lines())
	}
	texts := f.registeredTexts()
	if texts[len(texts)-1]["auth"] != "bearer" {
		t.Errorf("last registered TXT = %v, want auth=bearer", texts[len(texts)-1])
	}
}

// Stop must win a race with a refresh that is already in flight. A registration
// that completes after Stop was called must not leave a responder alive: once
// Stop returns, the service is off the network and stays off.
func TestARefreshInFlightDoesNotSurviveStop(t *testing.T) {
	f := newFakeAnnouncer()
	f.blockAt = 1 // hold the re-registration open
	var h hints
	a, rec := testAdvertiser(f, &h)

	// Cancelling the context Start was given is what Stop does internally, and
	// doing it from here makes the ordering exact: the cancellation is complete
	// before the held registration is released, with no sleep to tune.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, f, "the advertisement to be responding", func(ev []string) bool {
		return count(ev, "respond#") == 1
	})

	h.setAuth(true)
	waitFor(t, f, "the re-registration to be in flight", func(ev []string) bool {
		return count(ev, "register-entered") == 1
	})

	cancel()
	// Let the in-flight registration complete, after the shutdown was asked for.
	close(f.gate)

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		a.Stop()
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatalf("Stop did not return; events were %v", f.log())
	}

	if got := f.liveCount(); got != 0 {
		t.Errorf("%d responders still live after Stop returned, want 0: %v", got, f.log())
	}

	// Nothing is claimed to have been published for a service Stop was about to
	// tear down.
	if rec.saidAbout("advertisement") != 0 {
		t.Errorf("the advertiser reported publishing work during Stop: %v", rec.lines())
	}

	// And nothing starts up afterwards.
	before := len(f.log())
	time.Sleep(50 * time.Millisecond)
	if after := f.log(); len(after) != before {
		t.Errorf("the advertiser was still working after Stop returned: %v", after[before:])
	}
}

// The refresh loop reads the auth and model callbacks while the rest of the
// program writes them; run under -race this pins that the whole cycle —
// including the withdraw and re-register — carries no data race of its own.
func TestConcurrentHintChangesAndStopAreRaceFree(t *testing.T) {
	f := newFakeAnnouncer()
	var h hints
	a, _ := testAdvertiser(f, &h)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				h.bump(n)
				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	time.Sleep(50 * time.Millisecond)
	a.Stop()
	close(done)
	wg.Wait()

	if got := f.liveCount(); got != 0 {
		t.Errorf("%d responders still live after Stop, want 0", got)
	}
	// Stop is idempotent.
	a.Stop()
}

// Registering a service and serving it are two steps in dnssd: Register only
// builds the service and hands it to a responder, while the probe and the first
// announcement happen inside Respond. So a registration can be accepted and its
// responder still fail immediately — the network went away between the two —
// and treating the registration's success as "published" leaves the Mac on no
// browser's list until something else changes.
//
// The responder that gave up must be served again, and — because dnssd closes
// its sockets only on the way out of a Respond that ran to cancellation — the
// retry must reuse the registration it already has rather than build another,
// which would strand a socket pair on every attempt.
func TestAResponderThatFailsIsServedAgainOnTheSameRegistration(t *testing.T) {
	f := newFakeAnnouncer()
	f.serveFailAt[0] = true // the network is down when the service is first served
	f.serveFailAt[1] = true // and still down on the first retry
	// The third serve is the one that works, and it is held at the door. Every
	// property this test is about is an ORDERING — what the lifecycle had done
	// by the time the service went back on the network, and what it had not —
	// so the assertions are taken against that boundary while the fake stands
	// still, never against a count read at the instant a step completed or
	// against a sleep, neither of which proves anything on a loaded runner.
	f.serveBlockAt = 2
	var h hints
	a, rec := testAdvertiser(f, &h)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	var freed sync.Once
	release := func() { freed.Do(func() { close(f.serveGate) }) }
	defer release()

	select {
	case <-f.serveEntered:
	case <-time.After(5 * time.Second):
		t.Fatalf("the registration was never served again after the outage; events were %v", f.log())
	}

	// Held between the second failure and the first successful announcement.
	if got := count(f.log(), "respond#"); got != 0 {
		t.Errorf("%d responders announced before the retry was released, want 0: %v", got, f.log())
	}
	if got := count(f.log(), "serve-failed#"); got != 2 {
		t.Errorf("%d failed serves recorded, want 2: %v", got, f.log())
	}
	if got := count(f.log(), "register:"); got != 1 {
		t.Errorf("%d registrations across two failed serves, want 1 — each extra one "+
			"opens a socket pair that dnssd will never close: %v", got, f.log())
	}
	// An outage that persists fails the same way every tick. It is reported on
	// the way in and on the way out, not once per tick in between — and the
	// way out has not happened yet, so one report is all there can be.
	if got := rec.saidAbout("has stopped"); got != 1 {
		t.Errorf("reported the outage %d times over two failed serves, want once: %v", got, rec.lines())
	}

	release()
	waitFor(t, f, "the advertisement to be serving after the outage", func(ev []string) bool {
		return count(ev, "respond#") == 1
	})
	if got := f.liveCount(); got != 1 {
		t.Errorf("live responders = %d after recovery, want 1", got)
	}

	// Recovery is announced only once the responder has survived an interval,
	// so wait for it rather than reading the log the moment serving starts.
	waitUntil(t, "the recovery to be reported", func() bool {
		return rec.saidAbout("republished") >= 1
	})

	// And it stays quiet once it is back. Stop waits for the refresh goroutine
	// to exit, so what the recorder holds afterwards is the whole of what this
	// lifecycle ever said — a boundary a sleep can only guess at.
	a.Stop()
	if got := rec.saidAbout("republished"); got != 1 {
		t.Errorf("reported the recovery %d times, want once: %v", got, rec.lines())
	}
	if got := rec.saidAbout("has stopped"); got != 1 {
		t.Errorf("reported the outage %d times in all, want once: %v", got, rec.lines())
	}
}

// A responder that has already given up is not a recovery.
//
// The refresh loop watches the responder and the refresh tick in one select,
// and select picks at random between cases that are both ready. So a responder
// can be dead — its death sitting unread in ad.done — and the tick be chosen
// anyway. Reporting recovery on that tick announces the outage as over while
// the Mac is on no browser's list, and buys a second outage report on the pass
// that finally reads the death (iss-2609111013499513).
//
// Forcing that coincidence is what the hold is for. The loop's last act after
// starting a responder is to log, so holding it there until the responder it
// just started has finished failing leaves both select cases ready when it
// comes back. Each retry is then a coin toss, and twenty of them make the
// wrong call a near-certainty for as long as the loop is willing to make it.
func TestAResponderThatHasAlreadyGivenUpIsNotReportedAsRecovered(t *testing.T) {
	const retries = 20

	f := newFakeAnnouncer()
	for i := 0; i < retries; i++ {
		f.serveFailAt[i] = true // one long outage, and then it comes back
	}
	var h hints
	a, rec := testAdvertiser(f, &h)

	// Every tick must reach the log, and only a changed TXT record does, so
	// the model count moves under the loop until the outage is over.
	var churn atomic.Bool
	var models atomic.Int64
	churn.Store(true)
	a.Models = func() int {
		if churn.Load() {
			return int(models.Add(1))
		}
		return int(models.Load())
	}

	rec.hold(func(msg string) {
		if !strings.Contains(msg, "updated network advertisement") {
			return
		}
		// Wait for the responder just started to have finished failing. The
		// bound matters: the serve that finally succeeds never settles, since
		// it stays inside Respond until the advertisement is withdrawn.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && !f.settled() {
			time.Sleep(50 * time.Microsecond)
		}
		// Slack for the responder goroutine's own exit — it has recorded the
		// failure and has only to close the channel the loop is watching —
		// and for the tick to come round while the loop is still held here.
		time.Sleep(5 * time.Millisecond)
	})

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	// Wait for the outage itself to end — the serve that finally works — not
	// for the recovery to be reported. A loop that miscounts a dead responder
	// as a recovery says so early and often, and stopping at the first such
	// line would end the test before the outage it belongs to was over.
	waitFor(t, f, "the advertisement to come back after the outage", func(ev []string) bool {
		return count(ev, "respond#") >= 1
	})
	churn.Store(false)
	waitUntil(t, "the recovery to be reported", func() bool {
		return rec.saidAbout("republished") >= 1
	})

	// Stop waits for the refresh goroutine to exit, so what the recorder holds
	// afterwards is the whole of what this lifecycle ever said.
	a.Stop()
	if got := rec.saidAbout("has stopped"); got != 1 {
		t.Errorf("reported the outage %d times across %d failed serves, want once — "+
			"a responder already known to be dead was counted as a recovery: %v",
			got, retries, rec.lines())
	}
	if got := rec.saidAbout("republished"); got != 1 {
		t.Errorf("reported the recovery %d times, want once: %v", got, rec.lines())
	}
}

// The same failure on the re-registration path: the hints changed, the old
// advertisement was withdrawn, the new registration was accepted and its
// responder then failed. The service is off the network and must go back on.
func TestAReRegistrationWhoseResponderFailsIsRetried(t *testing.T) {
	f := newFakeAnnouncer()
	f.serveFailAt[1] = true // the responder for the re-registration
	var h hints
	a, rec := testAdvertiser(f, &h)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	waitFor(t, f, "the advertisement to be responding", func(ev []string) bool {
		return count(ev, "respond#") == 1
	})

	h.setAuth(true)

	waitFor(t, f, "the replacement responder to fail", func(ev []string) bool {
		return count(ev, "serve-failed#1") == 1
	})
	waitFor(t, f, "the replacement to be serving after the retry", func(ev []string) bool {
		return count(ev, "respond#1") == 1
	})

	if got := count(f.log(), "register:"); got != 2 {
		t.Errorf("%d registrations, want 2 — the retry must reuse the registration "+
			"whose responder failed, not build another: %v", got, f.log())
	}
	if got := f.liveCount(); got != 1 {
		t.Errorf("live responders = %d, want 1", got)
	}
	if got := rec.saidAbout("has stopped"); got != 1 {
		t.Errorf("reported the failure %d times, want once: %v", got, rec.lines())
	}
}
