package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serveThroughLogging runs one request through withLogging and returns whatever
// the wrapper wrote to the log.
func serveThroughLogging(t *testing.T, req *http.Request, next http.Handler) string {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	withLogging(next, log).ServeHTTP(httptest.NewRecorder(), req)
	return buf.String()
}

// The operational log is a privacy surface: it must describe the call without
// naming the caller. Method, path, status and duration are what a failure is
// diagnosed from; the client's network address is not.
func TestRequestLogOmitsTheClientAddress(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.50:54321"

	line := serveThroughLogging(t, req, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	for _, leak := range []string{"192.168.1.50", "54321"} {
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
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "10.0.0.7:5555"

	line := serveThroughLogging(t, req, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{}"))
	}))

	if !strings.Contains(line, "status=200") {
		t.Errorf("request log did not report the implicit 200:\n%s", line)
	}
	if strings.Contains(line, "10.0.0.7") {
		t.Errorf("request log leaks the client address:\n%s", line)
	}
}

// Streaming completions flush through an http.ResponseController, which finds
// the flusher by unwrapping. A status-recording wrapper that does not unwrap
// would silently turn streaming into one buffered blob at the end.
func TestLoggedResponseWriterStaysFlushable(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.50:54321"

	var flushErr error
	flushed := false
	serveThroughLogging(t, req, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: hello\n"))
		flushErr = http.NewResponseController(w).Flush()
		flushed = true
	}))

	if !flushed {
		t.Fatal("handler did not run")
	}
	if flushErr != nil {
		t.Errorf("flush through the logging wrapper failed: %v", flushErr)
	}
}
