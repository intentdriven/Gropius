package main

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spyWriter is the writer underneath the logging wrapper. It records exactly
// what reaches it, so a test can tell what the client would have seen — a
// wrapper that swallowed a WriteHeader or faked a Write result looks identical
// from the log's side but not from here.
type spyWriter struct {
	header  http.Header
	codes   []int
	body    bytes.Buffer
	flushes int

	// A client that goes away mid-stream shows up as a short write and an
	// error; both have to reach the handler, or the streaming relay never
	// notices and keeps pumping the upstream body into a dead socket.
	shortBy  int
	writeErr error
}

func (s *spyWriter) Header() http.Header {
	if s.header == nil {
		s.header = http.Header{}
	}
	return s.header
}

func (s *spyWriter) Write(b []byte) (int, error) {
	n := len(b) - s.shortBy
	if n < 0 {
		n = 0
	}
	s.body.Write(b[:n])
	return n, s.writeErr
}

func (s *spyWriter) WriteHeader(code int) { s.codes = append(s.codes, code) }

// Flush is deliberately NOT promoted through the wrapper: statusRecorder
// embeds the http.ResponseWriter interface, so a handler reaches this only via
// http.NewResponseController, which unwraps.
func (s *spyWriter) Flush() { s.flushes++ }

// errClientGone stands in for the error a writer returns once the peer has
// gone away.
var errClientGone = errors.New("client went away")

// serveThroughLogging runs one request through withLogging against w and
// returns whatever the wrapper wrote to the log.
func serveThroughLogging(t *testing.T, w http.ResponseWriter, req *http.Request, next http.Handler) string {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	withLogging(next, log).ServeHTTP(w, req)
	return buf.String()
}

func apiRequest(method, path, addr string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = addr
	return req
}

// The operational log is a privacy surface: it must describe the call without
// naming the caller. Method, path, status and duration are what a failure is
// diagnosed from; the client's network address is not.
func TestRequestLogOmitsTheClientAddress(t *testing.T) {
	req := apiRequest(http.MethodPost, "/v1/chat/completions", "192.0.2.50:54321")

	line := serveThroughLogging(t, httptest.NewRecorder(), req, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	for _, leak := range []string{"192.0.2.50", "54321"} {
		if strings.Contains(line, leak) {
			t.Errorf("request log leaks the client address %q:\n%s", leak, line)
		}
	}
	for _, want := range []string{"method=POST", "path=/v1/chat/completions", "status=201", "took="} {
		if !strings.Contains(line, want) {
			t.Errorf("request log is missing %q:\n%s", want, line)
		}
	}
}

// A handler that never calls WriteHeader still returns 200, and the log has to
// say so rather than reporting a zero status.
func TestRequestLogReportsTheImplicitStatus(t *testing.T) {
	req := apiRequest(http.MethodGet, "/v1/models", "198.51.100.7:5555")

	line := serveThroughLogging(t, httptest.NewRecorder(), req, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{}"))
	}))

	if !strings.Contains(line, "status=200") {
		t.Errorf("request log did not report the implicit 200:\n%s", line)
	}
	if strings.Contains(line, "198.51.100.7") {
		t.Errorf("request log leaks the client address:\n%s", line)
	}
}

// The wrapper sits on the response path of every non-noisy request, so what
// the client receives must be exactly what the handler sent: an unauthenticated
// caller's 401 has to arrive as a 401, with its headers, its body, and a Write
// result that reports what the writer underneath actually did — the streaming
// relay bails out on a short write or an error to notice a client that left.
func TestLoggingWrapperForwardsTheResponseVerbatim(t *testing.T) {
	body := []byte(`{"error":"unauthorized"}`)

	t.Run("explicit status", func(t *testing.T) {
		spy := &spyWriter{}
		var gotN int
		var gotErr error
		serveThroughLogging(t, spy, apiRequest(http.MethodPost, "/v1/chat/completions", "192.0.2.50:54321"),
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				gotN, gotErr = w.Write(body)
			}))

		if len(spy.codes) != 1 || spy.codes[0] != http.StatusUnauthorized {
			t.Errorf("client-visible status codes = %v, want [401]", spy.codes)
		}
		if got := spy.body.String(); got != string(body) {
			t.Errorf("client-visible body = %q, want %q", got, body)
		}
		if got := spy.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("client-visible Content-Type = %q, want application/json", got)
		}
		if gotN != len(body) || gotErr != nil {
			t.Errorf("Write returned (%d, %v), want (%d, nil)", gotN, gotErr, len(body))
		}
	})

	t.Run("short write and error", func(t *testing.T) {
		spy := &spyWriter{shortBy: 2, writeErr: errClientGone}
		var gotN int
		var gotErr error
		serveThroughLogging(t, spy, apiRequest(http.MethodPost, "/v1/chat/completions", "192.0.2.50:54321"),
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotN, gotErr = w.Write([]byte("data: hello\n"))
			}))

		if want := len("data: hello\n") - 2; gotN != want {
			t.Errorf("Write reported %d bytes, want the delegate's %d", gotN, want)
		}
		if !errors.Is(gotErr, errClientGone) {
			t.Errorf("Write returned err %v, want the delegate's %v", gotErr, errClientGone)
		}
	})

	t.Run("implicit status", func(t *testing.T) {
		spy := &spyWriter{}
		serveThroughLogging(t, spy, apiRequest(http.MethodGet, "/v1/models", "198.51.100.7:5555"),
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("{}"))
			}))

		// The handler wrote no header, so neither may the wrapper: net/http
		// itself supplies the 200 when the body starts.
		if len(spy.codes) != 0 {
			t.Errorf("wrapper invented WriteHeader calls %v on an implicit 200", spy.codes)
		}
		if got := spy.body.String(); got != "{}" {
			t.Errorf("client-visible body = %q, want %q", got, "{}")
		}
	})
}

// The status latch: the first final code wins, a later Write must not reset it
// to 200, and every WriteHeader is still forwarded so net/http keeps emitting
// its "superfluous WriteHeader" warning.
func TestRequestLogLatchesTheFirstFinalStatus(t *testing.T) {
	spy := &spyWriter{}
	line := serveThroughLogging(t, spy, apiRequest(http.MethodPost, "/v1/chat/completions", "192.0.2.50:54321"),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.WriteHeader(http.StatusOK) // a buggy handler; net/http ignores it
			_, _ = w.Write([]byte("busy"))
		}))

	if !strings.Contains(line, "status=503") {
		t.Errorf("log did not latch the first final status:\n%s", line)
	}
	if len(spy.codes) != 2 {
		t.Errorf("wrapper swallowed a WriteHeader: forwarded %v, want both", spy.codes)
	}
}

// An interim 1xx is not the response's status: net/http permits WriteHeader to
// be called again afterwards, and the log must report the terminal code.
func TestRequestLogIgnoresInterimStatus(t *testing.T) {
	spy := &spyWriter{}
	line := serveThroughLogging(t, spy, apiRequest(http.MethodGet, "/v1/models", "198.51.100.7:5555"),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusEarlyHints) // 103
			w.WriteHeader(http.StatusInternalServerError)
		}))

	if strings.Contains(line, "status=103") {
		t.Errorf("log reported an interim status as the final one:\n%s", line)
	}
	if !strings.Contains(line, "status=500") {
		t.Errorf("log did not report the terminal status:\n%s", line)
	}
	if len(spy.codes) != 2 || spy.codes[0] != http.StatusEarlyHints {
		t.Errorf("wrapper did not forward the interim response: %v", spy.codes)
	}
}

// Streaming completions flush through an http.ResponseController, which finds
// the flusher by unwrapping. This pins the constraint stated on statusRecorder:
// a flush must reach the writer underneath, and it can only get there via the
// controller — the wrapper does not satisfy http.Flusher itself, so a handler
// using a type assertion would silently skip the flush.
func TestLoggedResponseWriterStaysFlushable(t *testing.T) {
	spy := &spyWriter{}
	var flushErr error
	var assertable bool
	serveThroughLogging(t, spy, apiRequest(http.MethodPost, "/v1/chat/completions", "192.0.2.50:54321"),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("data: hello\n"))
			_, assertable = w.(http.Flusher)
			flushErr = http.NewResponseController(w).Flush()
		}))

	if flushErr != nil {
		t.Errorf("flush through the logging wrapper failed: %v", flushErr)
	}
	if spy.flushes != 1 {
		t.Errorf("flush did not reach the writer underneath: %d flushes, want 1", spy.flushes)
	}
	if assertable {
		t.Error("the wrapper now satisfies http.Flusher; the comment on statusRecorder says it does not")
	}
}
