package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/applog"
	"github.com/intentdriven/Gropius/internal/config"
)

// The log file is the whole point of this feature and it is also a privacy
// surface: a Finder-launched app now writes to disk what it used to throw
// away, so what it writes has to be exactly what it would have written to a
// terminal, and no more.
//
// This drives a refused request through the real logging wrapper and the real
// file, at the detailed level — the level that says the most — and reads the
// bytes back. Nothing the client sent, nothing the operator configured, and
// nothing about who the client was may be in them.
func TestTheLogFileAfterARefusalNamesNoClientNoPromptAndNoSecret(t *testing.T) {
	dir := t.TempDir()
	l, err := applog.Open(applog.Options{Dir: dir, Stderr: io.Discard, Level: slog.LevelDebug})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	const (
		apiKey  = "sk-test-2f9c4a1e7b3d"
		hfToken = "hf_testtokendonotlog"
		prompt  = "the quick brown fox jumps over the lazy dog"
	)
	req := apiRequest(http.MethodPost, "/v1/chat/completions", "192.0.2.50:54321")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	// A handler that refuses the way the gateway refuses: it writes a status
	// and a body, and it says on the server's own log which model and which
	// refusal — the sparse line — with the figures behind it at detailed.
	withLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.Logger.Info("refused a request, and told the client only that it could not be served",
			"model", "org/repo", "class", "overloaded")
		l.Logger.Debug("refused a request, and told the client only that it could not be served",
			"model", "org/repo", "in_flight", 12, "limit", "24.0 GB")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"cannot serve this model right now"}}`))
	}), l.Logger).ServeHTTP(httptest.NewRecorder(), req)

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, applog.DefaultName))
	if err != nil {
		t.Fatalf("the log file was not written: %v", err)
	}
	got := string(raw)

	// It has to have said something, or the checks below pass on an empty file.
	for _, want := range []string{"refused a request", "model=org/repo", "status=503", "in_flight=12"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the log file is missing %q, so this test proves nothing:\n%s", want, got)
		}
	}
	if leaks := clientAddressLeaks(t, got, req.RemoteAddr); len(leaks) > 0 {
		t.Errorf("the log file names the client %v:\n%s", leaks, got)
	}
	for name, secret := range map[string]string{
		"the API key":              apiKey,
		"the HuggingFace token":    hfToken,
		"a prompt":                 prompt,
		"the Authorization header": "Bearer ",
	} {
		if strings.Contains(got, secret) {
			t.Errorf("the log file carries %s:\n%s", name, got)
		}
	}
}

// The log's own default level is the setting's own default, so a build that
// changed one and not the other would ship a server that logs more than its
// settings say it does.
func TestTheLogStartsAtTheLevelTheSettingsName(t *testing.T) {
	dir := t.TempDir()
	l, err := applog.Open(applog.Options{
		Dir:    dir,
		Stderr: io.Discard,
		Level:  config.Default().SlogLevel(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	if got := l.Level.Level(); got != slog.LevelInfo {
		t.Errorf("a fresh install's log starts at %v, want %v", got, slog.LevelInfo)
	}
}

// The log lives beside the model servers' own logs, in the directory that
// belongs to this account rather than to the installation — never in the
// shared root, where every other account on the Mac could read it.
func TestTheLogLivesInTheAccountsOwnLogDirectory(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	if paths.Logs == "" {
		t.Fatal("Paths.Logs is empty")
	}
	if got := filepath.Dir(paths.Logs); got != paths.Account {
		t.Errorf("the log directory is under %q, want the account directory %q", got, paths.Account)
	}
}
