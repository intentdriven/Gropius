package app

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/registry"
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

// Symlinked weights are likewise never something the downloader wrote: they
// serve bytes from outside the model directory while dirSize charges the
// memory budget only the link's own size. validateModelDir must refuse them,
// matching registry.Rescan's treatment of the same layout.
func TestValidateModelDirRejectsSymlinkedWeights(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"model_type":"test"}`), 0o644)
	outside := filepath.Join(t.TempDir(), "real.safetensors")
	if err := os.WriteFile(outside, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "model.safetensors")); err != nil {
		t.Fatal(err)
	}

	if err := validateModelDir(dir); err == nil {
		t.Error("weights that are a symlink must be rejected, not followed")
	}
}

// validateModelDir must apply the same shard-completeness rule as the
// registry's rescan: a repo whose model.safetensors.index.json names a shard
// the tree did not contain downloads "successfully" (the hub fetches the tree
// listing, never the index) and would otherwise be advertised as ready, then
// fail on every load.
func TestValidateModelDirRejectsMissingIndexedShard(t *testing.T) {
	mk := func(files map[string]string) string {
		dir := t.TempDir()
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	index := `{"weight_map":{"a":"model-00001-of-00002.safetensors","b":"model-00002-of-00002.safetensors"}}`
	incomplete := mk(map[string]string{
		"config.json":                      `{"model_type":"qwen3"}`,
		"model-00001-of-00002.safetensors": "w",
		"model.safetensors.index.json":     index,
	})
	if err := validateModelDir(incomplete); err == nil {
		t.Error("a model missing an index-named shard was validated as ready")
	}
	complete := mk(map[string]string{
		"config.json":                      `{"model_type":"qwen3"}`,
		"model-00001-of-00002.safetensors": "w",
		"model-00002-of-00002.safetensors": "w",
		"model.safetensors.index.json":     index,
	})
	if err := validateModelDir(complete); err != nil {
		t.Errorf("a shard-complete model was rejected: %v", err)
	}
}

// Download validation and the rescan must agree about what a model's
// config.json has to be. They used to hold one copy of that rule each — the
// same criteria, a separate size cap, and nothing keeping the two in step —
// so a directory could pass validation on download and then be refused by
// every rescan, or the reverse. This pins the two verdicts together over the
// shapes that distinguish them.
func TestDownloadValidationAndRescanAgreeOnTheModelConfig(t *testing.T) {
	cases := []struct {
		name   string
		config string
		want   bool // both paths accept it
	}{
		{"model_type", `{"model_type":"qwen3"}`, true},
		{"architectures only", `{"architectures":["Qwen3ForCausalLM"]}`, true},
		{"neither key", `{"hello":"world"}`, false},
		{"an HTML error page", `<!DOCTYPE html><html>404</html>`, false},
		{"truncated JSON", `{"model_type":`, false},
		{"a JSON array", `[{"model_type":"qwen3"}]`, false},
		{"null", `null`, false},
		{"empty", ``, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "models", "org", "m")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(c.config), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), []byte("weights"), 0o644); err != nil {
				t.Fatal(err)
			}

			validated := validateModelDir(dir) == nil
			if validated != c.want {
				t.Errorf("validateModelDir accepted = %v, want %v", validated, c.want)
			}

			// The rescan's verdict on the same directory: adoption.
			reg, err := registry.Open(filepath.Join(root, "registry.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := reg.Rescan(filepath.Join(root, "models")); err != nil {
				t.Fatal(err)
			}
			_, err = reg.Get("org/m")
			adopted := err == nil
			if adopted != validated {
				t.Errorf("the rescan adopted = %v but download validation accepted = %v — the two rules have drifted apart", adopted, validated)
			}
		})
	}
}
