package gateway

import (
	"bufio"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// postRaw sends one completion request body as it is written, so a test can
// say "stream" without building a map for it.
func postRaw(t *testing.T, srv *httptest.Server, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// A request that found its model warm and free says so, and reports no wait.
// Two values and one number: "waited" is exactly "the queue time is not zero",
// so a client never has to reconcile them.
func TestAWarmCompletionSaysItDidNotWait(t *testing.T) {
	srv, _, _ := newTestGateway(t, config.Default())

	resp := postRaw(t, srv, `{"model":"mlx-community/Qwen3-8B-4bit","messages":[{"role":"user","content":"hi"}]}`)
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Gropius-State"); got != "warm" {
		t.Errorf("X-Gropius-State = %q, want warm", got)
	}
	if got := resp.Header.Get("X-Gropius-Queue-Time"); got != "0" {
		t.Errorf("X-Gropius-Queue-Time = %q, want 0", got)
	}
}

// The streamed path specifically: the headers must be on the wire before the
// first event, which is the case the non-streamed path cannot prove.
func TestAStreamedCompletionReportsTheWaitItPaid(t *testing.T) {
	srv, pool, _ := newTestGateway(t, config.Default())
	pool.waits = runtime.AcquireStats{QueueWait: 1500 * time.Millisecond}

	resp := postRaw(t, srv,
		`{"model":"mlx-community/Qwen3-8B-4bit","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want a streamed answer", got)
	}
	if got := resp.Header.Get("X-Gropius-State"); got != "waited" {
		t.Errorf("X-Gropius-State = %q, want waited", got)
	}
	if got := resp.Header.Get("X-Gropius-Queue-Time"); got != "1500" {
		t.Errorf("X-Gropius-Queue-Time = %q, want 1500", got)
	}
	// And the headers arrived with the status line, before any event: reading
	// one proves the response was already committed.
	sc := bufio.NewScanner(resp.Body)
	if !sc.Scan() {
		t.Fatal("the streamed answer carried nothing")
	}
}

// A load wait is a wait too. A client that paid a cold start is told the same
// way as one that queued for room, because from where it sits they are the
// same fact: this took longer than a warm model would have.
func TestALoadWaitIsReportedAsAWait(t *testing.T) {
	srv, pool, _ := newTestGateway(t, config.Default())
	pool.waits = runtime.AcquireStats{LoadWait: 4 * time.Second}

	resp := postRaw(t, srv, `{"model":"mlx-community/Qwen3-8B-4bit","messages":[{"role":"user","content":"hi"}]}`)
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Gropius-State"); got != "waited" {
		t.Errorf("X-Gropius-State = %q, want waited", got)
	}
	if got := resp.Header.Get("X-Gropius-Queue-Time"); got != "4000" {
		t.Errorf("X-Gropius-Queue-Time = %q, want 4000", got)
	}
}

// The refusal path carries them too: a client that held a connection open for
// minutes and then got a 503 is the one that most needs to be told it waited.
func TestARefusalAfterAWaitCarriesTheWaitHeaders(t *testing.T) {
	srv, pool, _ := newTestGateway(t, config.Default())
	pool.acquireErr = &runtime.NoRoomError{
		Limit:  8 << 30,
		Waited: 300 * time.Second,
	}

	resp := postRaw(t, srv, `{"model":"mlx-community/Qwen3-8B-4bit","messages":[{"role":"user","content":"hi"}]}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Gropius-State"); got != "waited" {
		t.Errorf("X-Gropius-State = %q, want waited", got)
	}
	if got := resp.Header.Get("X-Gropius-Queue-Time"); got != "300000" {
		t.Errorf("X-Gropius-Queue-Time = %q, want 300000", got)
	}
	if !errors.Is(pool.acquireErr, runtime.ErrBusy) {
		t.Error("the no-room refusal no longer wraps ErrBusy")
	}
}

// Both headers are API surface a client will come to depend on, so both are
// described where a client looks them up.
func TestTheResponseHeaderReferenceDescribesEveryHeaderServed(t *testing.T) {
	page, err := os.ReadFile("../../docs/response-headers.md")
	if err != nil {
		t.Fatalf("the response-header reference is missing: %v", err)
	}
	for _, header := range []string{"X-Gropius-State", "X-Gropius-Queue-Time"} {
		if !strings.Contains(string(page), header) {
			t.Errorf("the gateway serves %s, which the reference page does not describe", header)
		}
	}
	for _, value := range []string{"`warm`", "`waited`"} {
		if !strings.Contains(string(page), value) {
			t.Errorf("the reference page does not give the value %s", value)
		}
	}
}
