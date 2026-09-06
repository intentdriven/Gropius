package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/intentdriven/Gropius/internal/registry"
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
	// The registry owns the rule about what a model's config.json has to be,
	// and the bounded, regular-file-only read behind it: in shared-cache mode
	// a model directory adopted from another account stays writable by that
	// account, which can replace config.json with a FIFO or a symlink at any
	// time, and a plain Open here would block the download goroutine forever
	// or follow the link. Calling the registry's check rather than keeping a
	// copy of it is what stops a directory passing validation on download and
	// then being refused by every rescan.
	if err := registry.CheckModelConfig(dir); err != nil {
		return err
	}

	weights, _ := filepath.Glob(filepath.Join(dir, "*.safetensors"))
	if len(weights) == 0 {
		return fmt.Errorf("no .safetensors weights present")
	}
	// Weights must be regular files: the downloader only ever writes regular
	// files, and a symlink would serve weights from outside the directory
	// while dirSize charges the memory budget the link's own size — mirroring
	// registry.go's inspectModelDir, which refuses the same layout on rescan.
	for _, w := range weights {
		info, err := os.Lstat(w)
		if err != nil {
			return fmt.Errorf("cannot stat %s: %w", filepath.Base(w), err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", filepath.Base(w))
		}
	}
	// The same shard-completeness rule the rescan applies: the hub fetches the
	// tree listing, never the index, so a repo whose index names a shard the
	// tree lacks downloads "successfully" and would otherwise be advertised as
	// ready only to fail on every load.
	if err := registry.CheckShards(dir); err != nil {
		return err
	}
	return nil
}
