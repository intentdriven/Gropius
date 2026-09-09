package discovery

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
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
}

func newFakeAnnouncer() *fakeAnnouncer {
	return &fakeAnnouncer{failAt: map[int]bool{}, blockAt: -1, gate: make(chan struct{})}
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
	f.texts = append(f.texts, cfg.Text)
	f.mu.Unlock()
	f.record("register:" + cfg.Text["auth"])
	return &fakeRegistration{f: f}, nil
}

type fakeRegistration struct{ f *fakeAnnouncer }

func (r *fakeRegistration) Respond(ctx context.Context) error {
	r.f.mu.Lock()
	r.f.live++
	r.f.mu.Unlock()
	r.f.record("respond")

	<-ctx.Done()

	r.f.mu.Lock()
	r.f.live--
	r.f.mu.Unlock()
	r.f.record("exit")
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

// testAdvertiser builds an Advertiser wired to the fake, ticking fast enough
// that a test never waits on the production interval.
func testAdvertiser(f *fakeAnnouncer, h *hints) *Advertiser {
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
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		announce: f,
		interval: time.Millisecond,
	}
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
	a := testAdvertiser(f, &h)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	waitFor(t, f, "the first advertisement to be responding", func(ev []string) bool {
		return len(ev) >= 2 && ev[0] == "register:none" && ev[1] == "respond"
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
	want := []string{"register:none", "respond", "exit", "register:bearer"}
	for i, w := range want {
		if ev[i] != w {
			t.Fatalf("events = %v, want them to start %v — a TXT change must stop the "+
				"running responder and wait for it to exit before registering again", ev, want)
		}
	}

	waitFor(t, f, "the replacement advertisement to be responding", func(ev []string) bool {
		return count(ev, "respond") == 2
	})

	texts := f.registeredTexts()
	if len(texts) != 2 || texts[1]["auth"] != "bearer" {
		t.Fatalf("registered TXT records = %v, want the second to carry auth=bearer", texts)
	}
}

// Withdrawing and re-probing costs peers a cache flush, so it happens only on
// an actual change. Ticks that find the same hints must leave the running
// advertisement alone.
func TestUnchangedHintsDoNotReRegister(t *testing.T) {
	f := newFakeAnnouncer()
	var h hints
	a := testAdvertiser(f, &h)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, f, "the advertisement to be responding", func(ev []string) bool {
		return count(ev, "respond") == 1
	})

	// Many ticks at a millisecond apiece, with nothing changing.
	time.Sleep(60 * time.Millisecond)
	a.Stop()

	if got := count(f.log(), "register:"); got != 1 {
		t.Errorf("%d registrations over ~60 ticks with unchanged hints, want 1: %v", got, f.log())
	}
}

// A re-registration that fails leaves the service off the network entirely, so
// the next tick must try again rather than treating the change as published.
func TestAFailedReRegistrationIsRetriedOnTheNextTick(t *testing.T) {
	f := newFakeAnnouncer()
	f.failAt[1] = true // the re-registration, not the initial one
	var h hints
	a := testAdvertiser(f, &h)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	waitFor(t, f, "the advertisement to be responding", func(ev []string) bool {
		return count(ev, "respond") == 1
	})

	h.setAuth(true)

	waitFor(t, f, "the failed re-registration to be retried", func(ev []string) bool {
		return count(ev, "register-failed") == 1 && count(ev, "register:") == 2
	})
	waitFor(t, f, "the retried advertisement to be responding", func(ev []string) bool {
		return count(ev, "respond") == 2
	})

	if got := f.liveCount(); got != 1 {
		t.Errorf("live responders = %d after the retry, want 1", got)
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
	a := testAdvertiser(f, &h)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, f, "the advertisement to be responding", func(ev []string) bool {
		return count(ev, "respond") == 1
	})

	h.setAuth(true)
	waitFor(t, f, "the re-registration to be in flight", func(ev []string) bool {
		return count(ev, "register-entered") == 1
	})

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		a.Stop()
	}()

	// Let the in-flight registration complete, after Stop has been asked for.
	close(f.gate)

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatalf("Stop did not return; events were %v", f.log())
	}

	if got := f.liveCount(); got != 0 {
		t.Errorf("%d responders still live after Stop returned, want 0: %v", got, f.log())
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
	a := testAdvertiser(f, &h)

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
