package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Progress is a snapshot of a repo download, emitted as it advances.
type Progress struct {
	RepoID string
	// File is the file that most recently made progress.
	File string
	// Completed/Total are byte counts across the whole repo.
	Completed int64
	Total     int64
	// FilesDone/FilesTotal count whole files.
	FilesDone  int
	FilesTotal int
	// BytesPerSec is a smoothed transfer rate for the whole download.
	BytesPerSec int64
}

// Percent returns download completion in [0,100].
func (p Progress) Percent() float64 {
	if p.Total <= 0 {
		return 0
	}
	return float64(p.Completed) / float64(p.Total) * 100
}

// ETA estimates the remaining time; zero when it cannot be determined.
func (p Progress) ETA() time.Duration {
	if p.BytesPerSec <= 0 || p.Completed >= p.Total {
		return 0
	}
	return time.Duration(float64(p.Total-p.Completed)/float64(p.BytesPerSec)) * time.Second
}

// DownloadRequest describes a repo download.
type DownloadRequest struct {
	RepoID   string
	Revision string // defaults to "main"
	// Dest is the directory the files land in. It is created if absent.
	Dest string
	// Concurrency is how many files transfer at once. Defaults to 4.
	Concurrency int
	// OnProgress, if set, is called as bytes arrive. Calls are serialised, so
	// the callback need not be safe for concurrent use, but it should return
	// promptly: it runs on the goroutine that is moving bytes.
	OnProgress func(Progress)
}

// partSuffix marks an in-flight file. A download is only renamed onto its final
// name once complete and size-checked, so an interrupted run never leaves a
// truncated file that later looks valid.
const partSuffix = ".gropius-part"

// Download fetches every needed file in a repo into req.Dest, resuming any
// partial transfers from a previous run.
//
// It is safe to call again after a failure: complete files are skipped and
// partial ones resume with a Range request.
func (c *Client) Download(ctx context.Context, req DownloadRequest) error {
	if req.RepoID == "" {
		return errors.New("download: RepoID is required")
	}
	if req.Dest == "" {
		return errors.New("download: Dest is required")
	}
	if req.Concurrency <= 0 {
		req.Concurrency = 4
	}

	all, err := c.Files(ctx, req.RepoID, req.Revision)
	if err != nil {
		return err
	}
	files := WantedFiles(all)
	if len(files) == 0 {
		return fmt.Errorf("%s has no downloadable files", req.RepoID)
	}
	if !HasWeights(files) {
		return fmt.Errorf("%s contains no .safetensors weights — it is not an MLX-loadable model", req.RepoID)
	}

	if err := os.MkdirAll(req.Dest, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", req.Dest, err)
	}

	total := TotalSize(files)
	tracker := &progressTracker{
		repoID:     req.RepoID,
		total:      total,
		filesTotal: len(files),
		onProgress: req.OnProgress,
		started:    time.Now(),
	}

	// Count bytes already on disk from a previous run so progress starts where
	// it left off rather than at zero.
	for _, f := range files {
		if n := existingBytes(filepath.Join(req.Dest, f.Path)); n > 0 {
			tracker.addCompleted(n)
		}
	}
	// The transfer rate must measure THIS session's bytes, not the ones already
	// on disk. Without a baseline, resuming a nearly-complete download reports an
	// absurd rate (e.g. 27 GB "transferred" in the first second) and a near-zero ETA.
	tracker.baseline = tracker.completed.Load()
	tracker.emit("")

	sem := make(chan struct{}, req.Concurrency)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once

	// A failure in one file should stop the others rather than let the rest of
	// a multi-gigabyte download grind on pointlessly.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, f := range files {
		wg.Add(1)
		go func(f File) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			if err := c.downloadFile(ctx, req, f, tracker); err != nil {
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}
			tracker.fileDone(f.Path)
		}(f)
	}
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	// A cancelled download is INCOMPLETE, not successful. Returning nil here would
	// let the caller mark a half-downloaded model as ready. Surface the
	// cancellation (and any other context error) as the error it is.
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// safeJoin joins a repo-relative file path onto dest, refusing any result that
// escapes dest.
//
// File paths come from the HuggingFace API, i.e. from a third party. A repo
// whose tree lists "weights/../../../../.zshrc" would otherwise let a download
// clobber files anywhere the user can write. filepath.Join cleans ".." *after*
// joining, so the check must compare the cleaned absolute result against dest —
// inspecting the raw path for ".." is not enough (it misses percent-encoding and
// is easy to get subtly wrong).
func safeJoin(dest, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("refusing empty file path")
	}
	// An absolute path in the tree listing has no legitimate use and, joined,
	// would be silently re-rooted under dest — reject it outright so the intent
	// is unambiguous.
	if filepath.IsAbs(filepath.FromSlash(name)) {
		return "", fmt.Errorf("refusing file %q: absolute paths are not allowed", name)
	}
	cleanDest := filepath.Clean(dest)
	joined := filepath.Join(cleanDest, filepath.FromSlash(name))
	// joined is already Clean per filepath.Join. It is contained iff it equals
	// dest or sits under dest + separator.
	if joined != cleanDest && !strings.HasPrefix(joined, cleanDest+string(os.PathSeparator)) {
		return "", fmt.Errorf("refusing file %q: it escapes the model directory", name)
	}
	return joined, nil
}

// existingBytes returns the size of a completed file, or of a partial one.
func existingBytes(dest string) int64 {
	if fi, err := os.Stat(dest); err == nil {
		return fi.Size()
	}
	if fi, err := os.Stat(dest + partSuffix); err == nil {
		return fi.Size()
	}
	return 0
}

// downloadFile fetches one file, resuming if a partial exists.
func (c *Client) downloadFile(ctx context.Context, req DownloadRequest, f File, tr *progressTracker) error {
	final, err := safeJoin(req.Dest, f.Path)
	if err != nil {
		// A repo whose file tree contains "../" escapes is either malicious or
		// broken; either way we refuse rather than write outside the model dir.
		return err
	}
	part := final + partSuffix

	// Already complete from a previous run?
	if fi, err := os.Stat(final); err == nil {
		if f.Size == 0 || fi.Size() == f.Size {
			return nil
		}
		// Size mismatch: the file is corrupt or truncated. Refetch it.
		if err := os.Remove(final); err != nil {
			return fmt.Errorf("remove corrupt %s: %w", f.Path, err)
		}
		tr.addCompleted(-fi.Size())
	}

	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}

	var resumeAt int64
	if fi, err := os.Stat(part); err == nil {
		resumeAt = fi.Size()
		// A .part at or beyond the expected size is not trustworthy; start over.
		if f.Size > 0 && resumeAt >= f.Size {
			if err := os.Remove(part); err != nil {
				return err
			}
			tr.addCompleted(-resumeAt)
			resumeAt = 0
		}
	}

	u := c.ResolveURL(req.RepoID, req.Revision, f.Path)
	httpReq, err := c.newRequest(ctx, http.MethodGet, u)
	if err != nil {
		return err
	}
	if resumeAt > 0 {
		httpReq.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeAt))
	}

	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return fmt.Errorf("download %s: %w", f.Path, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Server ignored our Range (or we asked for the whole file): restart.
		if resumeAt > 0 {
			tr.addCompleted(-resumeAt)
			resumeAt = 0
		}
	case http.StatusPartialContent:
		// Resuming as requested.
	case http.StatusRequestedRangeNotSatisfiable:
		// The .part is already the full length; treat as complete and verify below.
		if err := os.Remove(part); err != nil && !os.IsNotExist(err) {
			return err
		}
		tr.addCompleted(-resumeAt)
		return c.downloadFile(ctx, req, f, tr)
	default:
		return apiError(resp, u)
	}

	// O_NOFOLLOW: refuse to write through a symlink planted at the .part path. In
	// a shared, group-writable model cache another local account could point that
	// predictable name at a file the downloading user can write, turning a model
	// fetch into a write-what-where. Opening the final component without following
	// links makes such an open fail (ELOOP) instead.
	flags := os.O_CREATE | os.O_WRONLY | syscall.O_NOFOLLOW
	if resumeAt > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	out, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", part, err)
	}

	_, copyErr := io.Copy(out, &progressReader{
		r:    resp.Body,
		path: f.Path,
		tr:   tr,
	})
	closeErr := out.Close()
	if copyErr != nil {
		// Leave the .part in place — the next run resumes from here.
		return fmt.Errorf("download %s: %w", f.Path, copyErr)
	}
	if closeErr != nil {
		return closeErr
	}

	if f.Size > 0 {
		fi, err := os.Stat(part)
		if err != nil {
			return err
		}
		if fi.Size() != f.Size {
			os.Remove(part)
			return fmt.Errorf("download %s: got %d bytes, expected %d", f.Path, fi.Size(), f.Size)
		}
	}

	if err := os.Rename(part, final); err != nil {
		return fmt.Errorf("finalise %s: %w", f.Path, err)
	}
	return nil
}

// progressTracker aggregates byte counts across concurrent file downloads.
type progressTracker struct {
	repoID     string
	total      int64
	filesTotal int
	onProgress func(Progress)
	started    time.Time
	// baseline is the completed-byte count at the start of this session (bytes
	// already on disk from a prior run). Transfer rate is measured from it.
	baseline int64

	completed atomic.Int64
	filesDone atomic.Int64

	// emitMu serialises calls into onProgress. Files download concurrently, so
	// without this the caller's callback would be entered from several
	// goroutines at once — a trap for the obvious implementations (updating a
	// progress bar, appending to a slice).
	emitMu   sync.Mutex
	lastEmit time.Time
}

func (t *progressTracker) addCompleted(n int64) { t.completed.Add(n) }

func (t *progressTracker) fileDone(path string) {
	t.filesDone.Add(1)
	t.emit(path)
}

// advance records bytes and emits at most ~10x/sec to keep the UI cheap.
func (t *progressTracker) advance(path string, n int64) {
	t.completed.Add(n)

	t.emitMu.Lock()
	throttled := time.Since(t.lastEmit) < 100*time.Millisecond
	if !throttled {
		t.lastEmit = time.Now()
	}
	t.emitMu.Unlock()

	if !throttled {
		t.emit(path)
	}
}

func (t *progressTracker) emit(path string) {
	if t.onProgress == nil {
		return
	}
	done := t.completed.Load()
	var rate int64
	if el := time.Since(t.started).Seconds(); el > 0.5 {
		// Only this session's bytes count toward the rate, not those resumed from disk.
		if session := done - t.baseline; session > 0 {
			rate = int64(float64(session) / el)
		}
	}
	p := Progress{
		RepoID:      t.repoID,
		File:        path,
		Completed:   done,
		Total:       t.total,
		FilesDone:   int(t.filesDone.Load()),
		FilesTotal:  t.filesTotal,
		BytesPerSec: rate,
	}

	t.emitMu.Lock()
	defer t.emitMu.Unlock()
	t.onProgress(p)
}

// progressReader reports bytes to the tracker as they stream past.
type progressReader struct {
	r    io.Reader
	path string
	tr   *progressTracker
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.tr.advance(pr.path, int64(n))
	}
	return n, err
}
