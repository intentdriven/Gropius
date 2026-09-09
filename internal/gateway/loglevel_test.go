package gateway

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/applog"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/mlxtest"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// levelBuffer collects the gateway's log while the handler goroutine writes it.
type levelBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *levelBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *levelBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// launchFailureLogAt drives a model server that will not start through the
// gateway, logging at the given level, and returns what reached the log.
func launchFailureLogAt(t *testing.T, level slog.Level) string {
	t.Helper()
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	var logged levelBuffer
	lv := new(slog.LevelVar)
	lv.Set(level)

	models := &stubModels{models: []registry.Model{{RepoID: "org/m", State: registry.StateReady}}}
	leaky := fmt.Errorf("python runtime is not installed (/Users/carol/Library/Application Support/Gropius/venv/bin/python): file does not exist")
	pool := &stubPool{srv: fake, acquireErr: fmt.Errorf("start model server for org/m: %w", &runtime.LaunchError{Err: leaky})}
	g := New(Options{
		Config: config.Default(), Pool: pool, Models: models,
		Log: slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: lv})),
	})
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()

	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":    "org/m",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	return logged.String()
}

// A model server that would not start is an event that mattered, so the sparse
// level says so and names the model. What it does not say is what the child
// process said: that error carries absolute paths out of the serving account's
// own home directory, and it is a figure — the operator asks for it by
// switching to detailed.
func TestASparseLaunchFailureNamesTheModelAndNotTheError(t *testing.T) {
	got := launchFailureLogAt(t, slog.LevelInfo)

	if !strings.Contains(got, "model launch failed") || !strings.Contains(got, "model=org/m") {
		t.Errorf("the sparse log does not report the failed launch:\n%s", got)
	}
	if strings.Contains(got, "/Users/") || strings.Contains(got, "carol") {
		t.Errorf("the sparse log carries the wrapped launch error:\n%s", got)
	}
}

// Detailed carries it, on the same event, without taking the sparse line away.
func TestADetailedLaunchFailureCarriesTheWrappedError(t *testing.T) {
	got := launchFailureLogAt(t, slog.LevelDebug)

	if !strings.Contains(got, "model launch failed") {
		t.Errorf("the detailed log dropped the sparse line:\n%s", got)
	}
	if !strings.Contains(got, "python runtime is not installed") {
		t.Errorf("the detailed log does not carry the error behind the failed launch:\n%s", got)
	}
}

// refusalLogFile drives an unentitled client's refusal through a gateway whose
// log is a real file, at the given level, and returns what the file holds.
//
// A file rather than a buffer, deliberately. The whole of this record is that
// the reason a client was refused now survives on disk where an operator can
// read it, and what survives on disk is what has to be checked: a buffer would
// prove the handler's arguments and not the artefact.
func refusalLogFile(t *testing.T, level slog.Level, err error) (string, *http.Request) {
	t.Helper()
	dir := t.TempDir()
	l, openErr := applog.Open(applog.Options{Dir: dir, Stderr: io.Discard, Level: level})
	if openErr != nil {
		t.Fatalf("applog.Open: %v", openErr)
	}

	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	t.Cleanup(fake.Close)

	// A keyless install, which is the only kind that has an unentitled client
	// at this handler at all — see TestAKeyedInstallHasNoUnentitledClientAtThisHandler.
	// The HuggingFace token is set anyway: it is a secret this process holds
	// while it serves, and nothing about a refusal may put it on disk.
	cfg := config.Default()
	cfg.HFToken = hfTokenMarker
	models := &stubModels{models: []registry.Model{
		{RepoID: "org/warm", State: registry.StateReady, Path: "/models/org/warm"},
	}}
	h := New(Options{
		Config: cfg,
		Pool:   &stubPool{srv: fake, acquireErr: err},
		Models: models,
		Log:    l.Logger,
	}).Handler()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"org/warm","messages":[{"role":"user","content":"`+promptMarker+`"}]}`))
	req.RemoteAddr = "203.0.113.50:9999"
	req.Host = "127.0.0.1:11535"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+keyMarker)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("closing the log: %v", err)
	}
	raw, readErr := os.ReadFile(filepath.Join(dir, applog.DefaultName))
	if readErr != nil {
		t.Fatalf("the log file was not written: %v", readErr)
	}
	return string(raw), req
}

// The markers are values a real request and a real install would carry, chosen
// so that finding one in the log is unambiguous.
const (
	promptMarker  = "the quick brown fox jumps over the lazy dog"
	keyMarker     = "sk-marker-2f9c4a1e7b3d"
	hfTokenMarker = "hf_markertokendonotlog"
)

// Criterion 1, on the artefact. An unentitled client is told only that its
// request could not be served, and the operator's own log file carries one
// sparse line naming the model and which refusal it was — the two things
// diagnosing "my clients are being refused" needs and the answer no longer
// carries.
//
// Criterion 3 rides on the same run: whatever else that line says, the file
// must hold no prompt, no key, no HuggingFace token and no client address.
func TestTheLogFileAfterAnUnentitledRefusalNamesTheModelAndWhichRefusal(t *testing.T) {
	got, req := refusalLogFile(t, slog.LevelInfo,
		&runtime.NoRoomError{Limit: 41 << 30, Waited: 300 * time.Second})

	for _, want := range []string{
		"refused a request",
		"model=org/warm",
		refusalClass(&runtime.NoRoomError{}), // slog quotes a value with a space, so match the name
		"class=",
		"client=unentitled",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the sparse log file is missing %q:\n%s", want, got)
		}
	}
	// The figure the client is deliberately not told is not written either, at
	// this level: the budget in bytes is roughly how much memory this Mac has,
	// and a client can cause this refusal every time it asks.
	if private := runtime.HumanBytes(41 << 30); strings.Contains(got, private) {
		t.Errorf("the sparse log file carries the memory budget %q:\n%s", private, got)
	}

	assertNothingPrivateInTheLog(t, got, req)
}

// Criterion 5. Detailed carries what sparse leaves out, on the same event, and
// it does not take the sparse line away.
func TestTheDetailedLogFileCarriesTheRefusalsFigures(t *testing.T) {
	got, req := refusalLogFile(t, slog.LevelDebug,
		&runtime.NoRoomError{Limit: 41 << 30, Waited: 300 * time.Second})

	if !strings.Contains(got, "model=org/warm") {
		t.Errorf("the detailed log file dropped the sparse line:\n%s", got)
	}
	if private := runtime.HumanBytes(41 << 30); !strings.Contains(got, private) {
		t.Errorf("the detailed log file does not carry the memory budget %q:\n%s", private, got)
	}

	// Turning the level up buys the operator figures about their own Mac. It
	// does not buy them anything about the client or its request: that promise
	// holds at every level, which is why it is checked at this one too.
	assertNothingPrivateInTheLog(t, got, req)
}

// The busy refusal is the other figure the intent names: how many requests a
// model is already handling.
func TestTheInFlightCountIsDetailedOnly(t *testing.T) {
	busy := fmt.Errorf("org/warm is overloaded (12 requests already in flight): %w", runtime.ErrBusy)

	sparse, _ := refusalLogFile(t, slog.LevelInfo, busy)
	if strings.Contains(sparse, "12 requests") {
		t.Errorf("the sparse log file says how many requests are in flight:\n%s", sparse)
	}
	if !strings.Contains(sparse, "class="+refusalClass(busy)) { //nolint:staticcheck // no space in this one
		t.Errorf("the sparse log file does not say which refusal it was:\n%s", sparse)
	}

	detailed, _ := refusalLogFile(t, slog.LevelDebug, busy)
	if !strings.Contains(detailed, "12 requests") {
		t.Errorf("the detailed log file does not say how many requests are in flight:\n%s", detailed)
	}
}

// assertNothingPrivateInTheLog is the promise the whole feature rests on, in
// one place so that every level's test makes it.
func assertNothingPrivateInTheLog(t *testing.T, got string, req *http.Request) {
	t.Helper()
	for name, secret := range map[string]string{
		"the prompt":                       promptMarker,
		"the bearer token it sent":         keyMarker,
		"this install's HuggingFace token": hfTokenMarker,
		"the Authorization header":         "Bearer ",
	} {
		if strings.Contains(got, secret) {
			t.Errorf("the log file carries %s:\n%s", name, got)
		}
	}
	host, port, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		t.Fatalf("test address %q is not host:port: %v", req.RemoteAddr, err)
	}
	for _, form := range []string{req.RemoteAddr, ":" + port, "=" + port, host} {
		if strings.Contains(got, form) {
			t.Errorf("the log file names the client (%q):\n%s", form, got)
		}
	}
}

// The rate limiter and the level split meet on one line of code, and the
// meeting has a wrong way round: logEvery.allow consumes the interval whether
// or not the caller writes anything, so asking it twice would let the sparse
// line through and hold the detailed one back — on exactly the refusal an
// operator had just turned detailed on to read. One allow, both lines.
func TestOneRateLimitedRefusalWritesBothItsLines(t *testing.T) {
	h, logged := refusingGatewayLogging(t, "", &runtime.NoRoomError{Limit: 41 << 30}, slog.LevelDebug)

	for range 5 {
		completionForAs(t, h, "org/warm", "203.0.113.50:9999")
	}

	got := logged.String()
	if n := strings.Count(got, "org/warm"); n != 2 {
		t.Errorf("five refusals of one model wrote %d lines, want the sparse one and the detailed one:\n%s", n, got)
	}
	if !strings.Contains(got, runtime.HumanBytes(41<<30)) {
		t.Errorf("the detailed line was held back by the sparse line's own allow:\n%s", got)
	}
}
