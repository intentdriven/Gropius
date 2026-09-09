package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/mlxtest"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// refusingGateway is an install whose pool refuses every acquisition with err,
// so a test can read the refusal two clients are served for the same error.
func refusingGateway(t *testing.T, key string, err error) http.Handler {
	t.Helper()
	h, _ := refusingGatewayLogging(t, key, err, slog.LevelInfo)
	return h
}

// refusingGatewayLogging is the same install with its log in hand, for the
// tests that read what the operator is told.
//
// The level is the caller's, because what the operator is told is now two
// things and not one: which model and which refusal at the sparse level, and
// the pool's own message — which names this Mac's memory budget — at the
// detailed one. A test that did not choose would be asserting against
// whichever level happened to be the default.
func refusingGatewayLogging(t *testing.T, key string, err error, level slog.Level) (http.Handler, *bytes.Buffer) {
	t.Helper()
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	t.Cleanup(fake.Close)

	cfg := config.Default()
	cfg.APIKey = key
	models := &stubModels{models: []registry.Model{
		{RepoID: "org/warm", State: registry.StateReady, Path: "/models/org/warm"},
		{RepoID: "org/other", State: registry.StateReady, Path: "/models/org/other"},
	}}
	var logged bytes.Buffer
	return New(Options{
		Config: cfg,
		Pool:   &stubPool{srv: fake, acquireErr: err},
		Models: models,
		Log:    slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: level})),
	}).Handler(), &logged
}

// completionForAs is completionAs for a named model.
func completionForAs(t *testing.T, h http.Handler, model, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+model+`","messages":[{"role":"user","content":"hi"}]}`))
	req.RemoteAddr = remoteAddr
	req.Host = "127.0.0.1:11535"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// completionAs sends one completion request as a client at remoteAddr would,
// naming loopback in its Host as a genuine local client does, and returns the
// whole answer so a test can compare status, headers and body.
func completionAs(t *testing.T, h http.Handler, remoteAddr, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"org/warm","messages":[{"role":"user","content":"hi"}]}`))
	req.RemoteAddr = remoteAddr
	req.Host = "127.0.0.1:11535"
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// poolRefusals are the three refusals that describe this Mac rather than the
// request, each in the exact shape the pool produces it.
func poolRefusals() []struct {
	name    string
	err     error
	private string // the fact the refusal discloses
} {
	return []struct {
		name    string
		err     error
		private string
	}{
		{
			name:    "the overload refusal names the in-flight count",
			err:     fmt.Errorf("org/warm is overloaded (%d requests already in flight): %w", 7, runtime.ErrBusy),
			private: "7 requests already in flight",
		},
		{
			name:    "the nothing-can-be-freed refusal names the memory budget",
			err:     &runtime.NoRoomError{Limit: 41 << 30, Waited: 300 * time.Second},
			private: runtime.HumanBytes(41 << 30),
		},
		{
			name: "the too-large-to-load refusal names the memory budget",
			err: fmt.Errorf("org/warm needs about %s of memory but the limit is %s — raise the memory budget or choose a smaller quantization",
				runtime.HumanBytes(60<<30), runtime.HumanBytes(41<<30)),
			private: runtime.HumanBytes(41 << 30),
		},
	}
}

// The three pool refusals describe the machine, not the request: how many
// requests a model is already handling, and the resident memory budget in
// bytes — which is a fraction of this Mac's physical RAM, and so says roughly
// how much memory it has. A client can induce all three itself. That is the
// same class of fact the models list withholds from an open server's network
// clients, so the refusal withholds it on the same rule.
func TestAPoolRefusalTellsAnUnentitledClientNothingAboutThisMachine(t *testing.T) {
	for _, c := range poolRefusals() {
		t.Run(c.name, func(t *testing.T) {
			h := refusingGateway(t, "", c.err)
			w := completionAs(t, h, "203.0.113.50:9999", "")

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", w.Code)
			}
			body := w.Body.String()
			if strings.Contains(body, c.private) {
				t.Errorf("a LAN client on an open server was told %q: %s", c.private, body)
			}
			if !strings.Contains(body, genericRefusal) {
				t.Errorf("body = %s, want the generic refusal %q", body, genericRefusal)
			}
		})
	}
}

// The same error, two clients, two bodies — and one status line and one set of
// headers, compared whole. A client backing off honestly reads the status and
// the headers, and neither may move: what changes is only what the body says
// about this Mac.
//
// Note what this cannot assert, and why it is not a gap here. The two wait
// headers are written only on an install with a key, and on such an install
// every client that reaches this handler is entitled — withAuth admits it
// either on the key or on the loopback exemption, and both are arms of
// entitled. So no unentitled client can ever see those headers, on any install,
// and there is no pair of clients for which they are populated on both sides.
// What is held here is the whole header set, which is what would catch an
// informative header added to the entitled path later.
// TestTheWaitHeadersFollowTheResidencyRule holds the keyless case itself.
func TestAPoolRefusalKeepsItsStatusAndHeadersForEveryClient(t *testing.T) {
	for _, c := range poolRefusals() {
		t.Run(c.name, func(t *testing.T) {
			h := refusingGateway(t, "", c.err)
			local := completionAs(t, h, "127.0.0.1:52001", "")
			lan := completionAs(t, h, "203.0.113.50:9999", "")

			if local.Code != lan.Code {
				t.Errorf("status = %d for a client on this machine and %d for a LAN client; "+
					"back-off reads the status", local.Code, lan.Code)
			}
			if got, want := lan.Header(), local.Header(); !reflect.DeepEqual(got, want) {
				t.Errorf("headers = %v for a LAN client and %v for a client on this machine; "+
					"only the body text is gated", got, want)
			}
			if local.Body.String() == lan.Body.String() {
				t.Errorf("both clients were served the same body: %s", local.Body.String())
			}
			if !strings.Contains(local.Body.String(), c.private) {
				t.Errorf("a client on this machine lost the informative refusal: %s", local.Body.String())
			}
		})
	}
}

// The claim the test above rests on, made a test of its own: on an install
// with a key there is no unentitled client to serve, because a client without
// the key never reaches the handler at all.
func TestAKeyedInstallHasNoUnentitledClientAtThisHandler(t *testing.T) {
	h := refusingGateway(t, "bh_secret", &runtime.NoRoomError{Limit: 41 << 30})

	w := completionAs(t, h, "203.0.113.50:9999", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("a LAN client with no key got %d, want 401 — if it can reach the "+
			"refusal, the header-parity reasoning above needs redoing", w.Code)
	}
}

// The two entitled classes are the models list's, not a third rule of this
// path's own: a client on this machine, and any client a keyed install
// admitted. Both are served the refusal that says what is actually wrong,
// because both are already served exactly these facts by the listing.
func TestAPoolRefusalStaysInformativeForAnEntitledClient(t *testing.T) {
	err := &runtime.NoRoomError{Limit: 41 << 30}
	budget := runtime.HumanBytes(41 << 30)

	t.Run("a client on this machine, on a keyless install", func(t *testing.T) {
		w := completionAs(t, refusingGateway(t, "", err), "127.0.0.1:52001", "")
		if !strings.Contains(w.Body.String(), budget) {
			t.Errorf("body = %s, want the budget figure %s", w.Body.String(), budget)
		}
	})

	t.Run("a LAN client presenting the key", func(t *testing.T) {
		w := completionAs(t, refusingGateway(t, "bh_secret", err), "203.0.113.50:9999", "bh_secret")
		if !strings.Contains(w.Body.String(), budget) {
			t.Errorf("body = %s, want the budget figure %s", w.Body.String(), budget)
		}
	})
}

// A blind cross-origin fetch is not a client on this machine, here either: a
// page the operator's browser visits connects from 127.0.0.1 and, on a no-cors
// subresource, carries no Origin at all. The refusal is gated on the one
// predicate the whole server uses for "came from this machine", so the page is
// served what the LAN is.
func TestAPoolRefusalTellsACrossSiteFetchNothing(t *testing.T) {
	h := refusingGateway(t, "", &runtime.NoRoomError{Limit: 41 << 30})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"org/warm","messages":[{"role":"user","content":"hi"}]}`))
	req.RemoteAddr = "127.0.0.1:52001"
	req.Host = "127.0.0.1:11535"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if got := w.Body.String(); strings.Contains(got, runtime.HumanBytes(41<<30)) {
		t.Errorf("a cross-site fetch was told the memory budget: %s", got)
	}
}

// A launch failure is redacted for everybody, as it always was: its text can
// carry absolute local paths from the serving account's home directory, which
// is not a residency fact and is nobody's business, entitled or not.
func TestALaunchFailureStaysGenericForEveryClient(t *testing.T) {
	err := fmt.Errorf("start model server: %w",
		&runtime.LaunchError{Err: errors.New("exec: no such file or directory")})
	h := refusingGateway(t, "", err)

	for _, addr := range []string{"127.0.0.1:52001", "203.0.113.50:9999"} {
		w := completionAs(t, h, addr, "")
		if got := w.Body.String(); !strings.Contains(got, "the model could not be started") {
			t.Errorf("%s was served %s, want the redacted launch refusal", addr, got)
		}
	}
}

// The rule is a reference-page promise, not only a behaviour: the page that
// tells a client what an open server withholds must say that the refusals
// follow the listing.
func TestTheModelsListReferenceDescribesTheRefusalRule(t *testing.T) {
	page, err := os.ReadFile("../../docs/models-list.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), genericRefusal) {
		t.Errorf("the reference does not give the generic refusal %q that an open "+
			"server serves its network clients", genericRefusal)
	}
}

// What the client is no longer told, the operator is. Withholding the reason
// from the network and recording it nowhere would leave an operator asked "why
// are my requests being refused" with a class in the statistics and nothing
// else — and the reason the message was informative in the first place is that
// somebody has to be able to read it.
func TestARedactedRefusalIsRecordedForTheOperator(t *testing.T) {
	// Which model, which refusal and which client class are the sparse line:
	// they are what an operator asked "why are my requests being refused"
	// answers from, and they are on by default.
	t.Run("sparse", func(t *testing.T) {
		h, logged := refusingGatewayLogging(t, "", &runtime.NoRoomError{Limit: 41 << 30}, slog.LevelInfo)

		if w := completionAs(t, h, "203.0.113.50:9999", ""); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}

		line := logged.String()
		for _, want := range []string{
			"org/warm",          // which model
			"nothing evictable", // which refusal
			"unentitled",        // which client class
		} {
			if !strings.Contains(line, want) {
				t.Errorf("the log does not carry %q: %s", want, line)
			}
		}
		// The pool's message names this Mac's memory budget in bytes, which is
		// the fact genericRefusal strips out of the answer. It is not written
		// by default for the same reason it is not sent: a client can cause
		// this refusal every time it asks (itd-2609091412177263).
		if got := runtime.HumanBytes(41 << 30); strings.Contains(line, got) {
			t.Errorf("the sparse log carries the memory budget %q: %s", got, line)
		}
	})

	// And the message itself, which is the thing the client no longer gets, is
	// one level away rather than nowhere.
	t.Run("detailed", func(t *testing.T) {
		h, logged := refusingGatewayLogging(t, "", &runtime.NoRoomError{Limit: 41 << 30}, slog.LevelDebug)

		if w := completionAs(t, h, "203.0.113.50:9999", ""); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}

		line := logged.String()
		for _, want := range []string{
			"org/warm",
			"nothing evictable",
			"unentitled",
			runtime.HumanBytes(41 << 30), // the message the client did not get
		} {
			if !strings.Contains(line, want) {
				t.Errorf("the detailed log does not carry %q: %s", want, line)
			}
		}
	})
}

// An entitled client was told the reason itself, so there is nothing here for
// the log to rescue — and a line per served refusal would be noise on the one
// install where every refusal is a local client's own.
func TestAnInformativeRefusalIsNotLoggedTwice(t *testing.T) {
	// At the detailed level, so that "not logged" means not logged at all
	// rather than logged below the level this test happened to read at.
	h, logged := refusingGatewayLogging(t, "", &runtime.NoRoomError{Limit: 41 << 30}, slog.LevelDebug)

	completionAs(t, h, "127.0.0.1:52001", "")

	if got := logged.String(); strings.Contains(got, "told the client only") {
		t.Errorf("a refusal the client could read was logged as redacted: %s", got)
	}
}

// The client sets the rate at which this line can be written, so the line is
// held to one per model per minute. Without that, a LAN client that retries in
// a loop writes to this Mac's log as fast as it can send requests.
func TestTheRedactedRefusalLogIsRateLimitedPerModel(t *testing.T) {
	// At the sparse level, where one allowed refusal is one line. The detailed
	// level writes a second line for the same event, which
	// TestOneRateLimitedRefusalWritesBothItsLines holds.
	h, logged := refusingGatewayLogging(t, "", &runtime.NoRoomError{Limit: 41 << 30}, slog.LevelInfo)

	for range 5 {
		completionForAs(t, h, "org/warm", "203.0.113.50:9999")
	}
	if got := strings.Count(logged.String(), "org/warm"); got != 1 {
		t.Errorf("five refusals of one model wrote %d lines, want 1", got)
	}

	// A second model is a second fact, and is not held back by the first.
	completionForAs(t, h, "org/other", "203.0.113.50:9999")
	if got := strings.Count(logged.String(), "org/other"); got != 1 {
		t.Errorf("a refusal of another model wrote %d lines, want 1", got)
	}
}

// The limiter itself, on a clock a test can move: the interval has to end, or
// a model refused once is never reported again for the life of the process.
func TestLogEveryReleasesAfterItsInterval(t *testing.T) {
	now := time.Unix(1757145600, 0)
	l := newLogEvery(time.Minute)
	l.now = func() time.Time { return now }

	if !l.allow("org/warm") {
		t.Fatal("the first line of all was held back")
	}
	now = now.Add(59 * time.Second)
	if l.allow("org/warm") {
		t.Error("a second line was written inside the interval")
	}
	if !l.allow("org/other") {
		t.Error("another key was held back by the first key's line")
	}
	now = now.Add(2 * time.Second)
	if !l.allow("org/warm") {
		t.Error("the interval never ended")
	}
}

// The keys are repo ids the registry resolved, so the map is bounded by what
// this Mac has downloaded — but a long-running server should not hold an entry
// for a model it refused something for once, hours ago.
func TestLogEverySweepsEntriesItNoLongerNeeds(t *testing.T) {
	now := time.Unix(1757145600, 0)
	l := newLogEvery(time.Minute)
	l.now = func() time.Time { return now }

	for i := range maxLogEveryKeys {
		l.allow(fmt.Sprintf("org/m%d", i))
	}
	now = now.Add(2 * time.Minute)
	l.allow("org/new")

	l.mu.Lock()
	held := len(l.last)
	l.mu.Unlock()
	if held != 1 {
		t.Errorf("the limiter holds %d entries after every one of them expired, want 1", held)
	}
}

// The listing an open server serves the network carries ready models only, so
// a 404 that says a model is "not ready (downloading)" tells a client
// something the listing withholds — and it can be asked one guessed name at a
// time, which makes the completions endpoint a way to enumerate what this Mac
// is fetching. An unentitled client gets one sentence for both 404s, so a
// model that is downloading cannot be told apart from one this Mac has never
// heard of.
func TestANotReadyModelIsNotDisclosedToAnUnentitledClient(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	t.Cleanup(fake.Close)
	models := &stubModels{models: []registry.Model{
		{RepoID: "org/coming", State: registry.StateDownloading, Path: "/models/org/coming"},
	}}
	h := New(Options{
		Config: config.Default(),
		Pool:   &stubPool{srv: fake},
		Models: models,
	}).Handler()

	downloading := completionForAs(t, h, "org/coming", "203.0.113.50:9999")
	unknown := completionForAs(t, h, "org/never-heard-of", "203.0.113.50:9999")

	if downloading.Code != http.StatusNotFound || unknown.Code != http.StatusNotFound {
		t.Fatalf("status = %d and %d, want 404 for both", downloading.Code, unknown.Code)
	}
	for _, leaked := range []string{"not ready", string(registry.StateDownloading)} {
		if strings.Contains(downloading.Body.String(), leaked) {
			t.Errorf("a LAN client on an open server was told %q: %s",
				leaked, downloading.Body.String())
		}
	}
	// Each message echoes the name that was asked for, so they are compared
	// with that name taken out: what must match is everything else.
	downloadingMsg := errorMessage(t, downloading.Body.String())
	unknownMsg := errorMessage(t, unknown.Body.String())
	if strings.Replace(downloadingMsg, "org/coming", "X", 1) !=
		strings.Replace(unknownMsg, "org/never-heard-of", "X", 1) {
		t.Errorf("the two 404s can be told apart: %q and %q", downloadingMsg, unknownMsg)
	}
}

// And a client on this machine still gets the answer that says what to do
// about it: the model is here, it is downloading, wait.
func TestANotReadyModelIsStillNamedForAnEntitledClient(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	t.Cleanup(fake.Close)
	models := &stubModels{models: []registry.Model{
		{RepoID: "org/coming", State: registry.StateDownloading, Path: "/models/org/coming"},
	}}
	h := New(Options{
		Config: config.Default(),
		Pool:   &stubPool{srv: fake},
		Models: models,
	}).Handler()

	w := completionForAs(t, h, "org/coming", "127.0.0.1:52001")
	if !strings.Contains(w.Body.String(), "not ready") {
		t.Errorf("a client on this machine lost the reason: %s", w.Body.String())
	}
}

// errorMessage pulls the message out of an OpenAI-shaped error body.
func errorMessage(t *testing.T, body string) string {
	t.Helper()
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out.Error.Message
}
