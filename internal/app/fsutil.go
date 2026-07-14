package app

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// dirSize sums the size of every regular file under dir.
func dirSize(dir string) int64 {
	var total int64
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// validateModelDir does a cheap sanity check that a downloaded directory is a
// plausible MLX model, so a byte-complete-but-junk download is not advertised as
// ready.
//
// This is deliberately NOT a full load: actually loading every model on the GPU
// at download time would be slow and memory-hungry. It parses config.json and
// confirms weights are present — enough to catch a truncated JSON, an HTML error
// page saved as config.json, or a repo with no safetensors. The authoritative
// check that a model *runs* is the pool's readiness probe on first use, which
// issues a real completion.
func validateModelDir(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return fmt.Errorf("config.json is missing or unreadable: %w", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("config.json is not valid JSON: %w", err)
	}
	// mlx-lm keys off model_type (and, for some, architectures). Its absence
	// means this is not a model config we can serve.
	if cfg["model_type"] == nil && cfg["architectures"] == nil {
		return fmt.Errorf("config.json has neither model_type nor architectures — not a loadable model")
	}

	weights, _ := filepath.Glob(filepath.Join(dir, "*.safetensors"))
	if len(weights) == 0 {
		return fmt.Errorf("no .safetensors weights present")
	}
	return nil
}
