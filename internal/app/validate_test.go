package app

import (
	"os"
	"path/filepath"
	"testing"
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
