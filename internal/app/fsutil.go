package app

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// readCapped reads at most max bytes from the file at path. It returns an error
// if the file is larger than max, so a hostile oversized file is refused rather
// than silently truncated into a parse.
//
// In shared-cache mode a model directory can be adopted as ready from an
// account other than the one now running the server (registry.Rescan), and
// that owning account keeps the ability to replace its own config.json with a
// FIFO or a symlink at any time — the shared root's sticky bit only blocks a
// non-owner from doing so, not the owner. A later retry of that repo reaches
// this file via validateModelDir, and a plain Open would either block forever
// on the FIFO (wedging the download goroutine, and with it App.Close's
// dlWG.Wait) or follow the symlink and read an arbitrary file with this
// account's privileges. O_NONBLOCK makes the open itself unblockable,
// O_NOFOLLOW refuses symlinks outright, and the fstat on the opened handle
// (not the path, so a swap between check and open cannot be raced in) refuses
// anything but a regular file before any read — mirroring registry.go's
// readManifest, which guards the same hazard for registry.json/config.json.
func readCapped(path string, max int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("file %s exceeds %d bytes", filepath.Base(path), max)
	}
	return b, nil
}

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
// maxConfigJSON caps how much of a model's config.json we read. A real config
// is a few KB; anything approaching this is either broken or a hostile file
// planted to make validation balloon memory. The read is bounded rather than
// slurped whole with os.ReadFile.
const maxConfigJSON = 8 << 20

func validateModelDir(dir string) error {
	b, err := readCapped(filepath.Join(dir, "config.json"), maxConfigJSON)
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
	return nil
}
