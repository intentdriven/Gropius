package app

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestValidateModelDir(t *testing.T) {
	mk := func(files map[string]string) string {
		dir := t.TempDir()
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	// Valid: parseable config with model_type, plus weights.
	ok := mk(map[string]string{
		"config.json":       `{"model_type":"qwen3"}`,
		"model.safetensors": "weights",
	})
	if err := validateModelDir(ok); err != nil {
		t.Errorf("a valid model dir was rejected: %v", err)
	}

	// An HTML error page saved as config.json (a real failure mode).
	junk := mk(map[string]string{
		"config.json":       `<!DOCTYPE html><html>404</html>`,
		"model.safetensors": "weights",
	})
	if err := validateModelDir(junk); err == nil {
		t.Error("config.json that is not JSON should be rejected")
	}

	// Parseable JSON but not a model config.
	notModel := mk(map[string]string{
		"config.json":       `{"hello":"world"}`,
		"model.safetensors": "weights",
	})
	if err := validateModelDir(notModel); err == nil {
		t.Error("config with no model_type/architectures should be rejected")
	}

	// No weights.
	noWeights := mk(map[string]string{
		"config.json": `{"model_type":"qwen3"}`,
	})
	if err := validateModelDir(noWeights); err == nil {
		t.Error("a dir with no safetensors should be rejected")
	}
}

// In shared-cache mode, an adopted model's config.json is still owned by the
// account that downloaded it, which can replace it with a FIFO at any time —
// the shared root's sticky bit only blocks a non-owner from doing that.
// Opening it for a retry's re-validation would then block until a writer
// appears — never, for a hostile plant — wedging the download goroutine and
// App.Close's dlWG.Wait. validateModelDir must refuse it instead of blocking.
func TestValidateModelDirDoesNotBlockOnFIFOConfig(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "config.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "model.safetensors"), []byte("weights"), 0o644)

	done := make(chan error, 1)
	go func() { done <- validateModelDir(dir) }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a config.json that is not a regular file must be rejected")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("validateModelDir blocked on a FIFO planted as config.json")
	}
}

// A symlinked config.json is never something the downloader wrote; following
// it would probe files outside the model directory with this account's
// privileges. validateModelDir must refuse it, not follow it.
func TestValidateModelDirDoesNotFollowSymlinkedConfig(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-config.json")
	if err := os.WriteFile(outside, []byte(`{"model_type":"test"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "model.safetensors"), []byte("weights"), 0o644)

	if err := validateModelDir(dir); err == nil {
		t.Error("a config.json that is a symlink must be rejected, not followed")
	}
}
