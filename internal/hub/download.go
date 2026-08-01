package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	// OnProgress, if set, is called as bytes arrive. Calls are serialized, so
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

	if err := mkdirAllInherit(req.Dest); err != nil {
		return fmt.Errorf("create %s: %w", req.Dest, err)
	}
	// Every filesystem operation below happens inside this root. os.Root
	// resolves each path component without ever following a symlink out of the
	// tree, so a symlinked parent directory planted in a shared, group-writable
	// cache cannot redirect writes elsewhere — a guarantee that O_NOFOLLOW on
	// the final component alone cannot give.
	root, err := os.OpenRoot(req.Dest)
	if err != nil {
		return fmt.Errorf("open %s: %w", req.Dest, err)
	}
	defer root.Close()

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
		rel, err := relPath(f.Path)
		if err != nil {
			continue // downloadFile will refuse it with a proper error
		}
		if n := existingBytes(root, rel); n > 0 {
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

			if err := c.downloadFile(ctx, req, root, f, tracker); err != nil {
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

// mkdirAllInherit creates dest and any missing parents, then — when the
// nearest pre-existing ancestor is a setgid directory — widens the directories
// it created to setgid group-writable, preserving the ancestor's sticky bit.
// The shared cache depends on this: the installer marks the shared models root
// setgid group-writable and sticky (3775) so a model one account downloads is
// writable by the next, but only its owner can delete or rename it, but
// MkdirAll can never produce a group-writable or sticky directory (0o755
// carries neither bit, and umask would strip one anyway), which would leave
// the org/name directories the first account creates closed to every other
// account. Dropping the sticky bit here would let any account in the group
// delete or replace another account's model directory — the fix must not
// widen access at the cost of that protection. A per-user root has no setgid
// bit and keeps plain 0755. Chmod failures are ignored: only directories this
// call created are touched, and a download into a tree we can write must not
// fail over modes we cannot change.
func mkdirAllInherit(dest string) error {
	anc := filepath.Clean(dest)
	var created []string
	for {
		if _, err := os.Stat(anc); err == nil {
			break
		}
		created = append(created, anc)
		parent := filepath.Dir(anc)
		if parent == anc {
			break
		}
		anc = parent
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if fi, err := os.Stat(anc); err != nil || fi.Mode()&os.ModeSetgid == 0 {
		return nil
	}
	for i := len(created) - 1; i >= 0; i-- {
		_ = os.Chmod(created[i], 0o775|os.ModeSetgid|os.ModeSticky)
	}
	return nil
}

// relPath validates a repo-relative file path and returns it cleaned, for use
// inside an os.Root opened at the model directory.
//
// File paths come from the HuggingFace API, i.e. from a third party. A repo
// whose tree lists "weights/../../../../.zshrc" would otherwise let a download
// clobber files anywhere the user can write. os.Root confines the actual I/O
// regardless, but validating up front turns an attack into a clear refusal
// instead of a confusing I/O error — and keeps the check testable on its own.
func relPath(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("refusing empty file path")
	}
	// An absolute path in the tree listing has no legitimate use — reject it
	// outright so the intent is unambiguous.
	if filepath.IsAbs(filepath.FromSlash(name)) {
		return "", fmt.Errorf("refusing file %q: absolute paths are not allowed", name)
	}
	rel := filepath.Clean(filepath.FromSlash(name))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("refusing file %q: it escapes the model directory", name)
	}
	return rel, nil
}

// safeJoin joins a repo-relative file path onto dest, refusing any result that
// escapes dest.
func safeJoin(dest, name string) (string, error) {
	rel, err := relPath(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Clean(dest), rel), nil
}

// existingBytes returns how many bytes are already on disk for a target: the sum
// of any completed file and any leftover partial. Both are counted because the
// two can coexist — a wrong-sized final file beside a resumable .part — and
// downloadFile's per-file accounting expects the pre-download tally to have
// credited each of them, uncounting whichever it later discards.
func existingBytes(root *os.Root, rel string) int64 {
	var n int64
	if fi, err := root.Stat(rel); err == nil {
		n += fi.Size()
	}
	if fi, err := root.Stat(rel + partSuffix); err == nil {
		n += fi.Size()
	}
	return n
}

// downloadFile fetches one file, resuming if a partial exists. All filesystem
// access goes through root, which confines it to the model directory.
func (c *Client) downloadFile(ctx context.Context, req DownloadRequest, root *os.Root, f File, tr *progressTracker) error {
	final, err := relPath(f.Path)
	if err != nil {
		// A repo whose file tree contains "../" escapes is either malicious or
		// broken; either way we refuse rather than write outside the model dir.
		return err
	}
	part := final + partSuffix

	// Already complete from a previous run? When the manifest gives a size,
	// require an exact match; when it does not (size 0 = unknown), accept only a
	// non-empty file — a zero-byte "complete" file is never a real weight/config.
	if fi, err := root.Stat(final); err == nil {
		complete := (f.Size > 0 && fi.Size() == f.Size) || (f.Size == 0 && fi.Size() > 0)
		if complete {
			// Drop any leftover .part orphaned beside a completed file, so it does
			// not linger across every future run. Its bytes were added to the
			// pre-download tally (existingBytes sums final + .part), so uncount them
			// here or the total would overshoot.
			if pfi, perr := root.Stat(part); perr == nil {
				tr.addCompleted(-pfi.Size())
			}
			_ = root.Remove(part)
			return nil
		}
		// Wrong size or empty: refetch.
		if err := root.Remove(final); err != nil {
			return fmt.Errorf("remove corrupt %s: %w", f.Path, err)
		}
		tr.addCompleted(-fi.Size())
	}

	if dir := filepath.Dir(final); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		// Carry the shared cache's group-writability and sticky bit into
		// nested directories too (see mkdirAllInherit); os.Root confines the
		// chmod to the model directory. Re-chmodding a directory another
		// goroutine created is idempotent, and failures on another account's
		// directories are ignored for the same reason as above.
		if fi, err := root.Stat("."); err == nil && fi.Mode()&os.ModeSetgid != 0 {
			for p := dir; p != "."; p = filepath.Dir(p) {
				_ = root.Chmod(p, 0o775|os.ModeSetgid|os.ModeSticky)
			}
		}
	}

	var resumeAt int64
	if fi, err := root.Stat(part); err == nil {
		resumeAt = fi.Size()
		// A .part at or beyond the expected size is not trustworthy; start over.
		if f.Size > 0 && resumeAt >= f.Size {
			if err := root.Remove(part); err != nil {
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
		// Resuming as requested — but only if the server actually resumed where we
		// asked. A 206 whose Content-Range starts at a different offset would,
		// appended onto our .part, produce a wrong-length-but-plausible or a
		// silently-corrupt file. If the range is wrong or unparseable, discard the
		// partial and restart from scratch.
		if resumeAt > 0 && !validContentRange(resp.Header.Get("Content-Range"), resumeAt, f.Size) {
			if err := root.Remove(part); err != nil && !os.IsNotExist(err) {
				return err
			}
			tr.addCompleted(-resumeAt)
			return c.downloadFile(ctx, req, root, f, tr)
		}
	case http.StatusRequestedRangeNotSatisfiable:
		// The .part is already at (or beyond) the server's full length, so its
		// contents cannot be trusted; discard it and refetch from scratch, so
		// the fresh copy goes through the verification below. Only a request
		// that sent a Range can mean that: a 416 to a plain GET is a server
		// error, and retrying it would recurse forever.
		if resumeAt == 0 {
			return apiError(resp, u)
		}
		if err := root.Remove(part); err != nil && !os.IsNotExist(err) {
			return err
		}
		tr.addCompleted(-resumeAt)
		return c.downloadFile(ctx, req, root, f, tr)
	default:
		return apiError(resp, u)
	}

	// O_NOFOLLOW: refuse to write through a symlink planted at the .part path. In
	// a shared, group-writable model cache another local account could point that
	// predictable name at a file the downloading user can write, turning a model
	// fetch into a write-what-where. root already refuses links that leave the
	// model dir; O_NOFOLLOW additionally refuses in-tree links on the final
	// component, so such an open fails (ELOOP) instead of following.
	flags := os.O_CREATE | os.O_WRONLY | syscall.O_NOFOLLOW
	if resumeAt > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	out, err := root.OpenFile(part, flags, 0o644)
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

	fi, err := root.Stat(part)
	if err != nil {
		return err
	}
	switch {
	case f.Size > 0 && fi.Size() != f.Size:
		root.Remove(part)
		return fmt.Errorf("download %s: got %d bytes, expected %d", f.Path, fi.Size(), f.Size)
	case f.Size == 0 && fi.Size() == 0:
		// Size was unknown and the server returned nothing: a truncated/empty file
		// is never a valid download, and with no size to check it would otherwise
		// be renamed into place and pass as complete.
		root.Remove(part)
		return fmt.Errorf("download %s: server returned an empty file", f.Path)
	}

	// Content-integrity check for LFS files (the weights): LFS.OID is the sha256
	// of the file's content, so verifying it turns "right size" into "exact
	// bytes". Size alone cannot catch a corrupt-but-right-length body, nor
	// corruption that predates a resume (the appended tail is size-checked, the
	// resumed prefix is not). Non-LFS files carry a git-blob sha1, not a content
	// hash, so they are size-checked only.
	if f.LFS != nil && f.LFS.OID != "" {
		if err := verifySHA256(root, part, f.LFS.OID); err != nil {
			root.Remove(part)
			return fmt.Errorf("download %s: %w", f.Path, err)
		}
	}

	if err := root.Rename(part, final); err != nil {
		return fmt.Errorf("finalize %s: %w", f.Path, err)
	}
	return nil
}

// validContentRange reports whether a 206 response's Content-Range header
// ("bytes <start>-<end>/<total>") resumes exactly where we asked: start must
// equal resumeAt, and — when the expected size is known — total must equal it.
func validContentRange(h string, resumeAt, size int64) bool {
	h = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(h), "bytes"))
	dash := strings.IndexByte(h, '-')
	slash := strings.IndexByte(h, '/')
	if dash <= 0 || slash <= dash {
		return false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(h[:dash]), 10, 64)
	if err != nil || start != resumeAt {
		return false
	}
	if size > 0 {
		total, err := strconv.ParseInt(strings.TrimSpace(h[slash+1:]), 10, 64)
		if err != nil || total != size {
			return false
		}
	}
	return true
}

// verifySHA256 streams the file at name through SHA-256 and compares it to the
// expected hex digest.
func verifySHA256(root *os.Root, name, wantHex string) error {
	f, err := root.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, wantHex) {
		return fmt.Errorf("content hash mismatch: got sha256:%s, expected sha256:%s", got, wantHex)
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

	// emitMu serializes calls into onProgress. Files download concurrently, so
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
	// Clamp the reported total to [0, total]. The byte accounting can briefly go
	// out of range in a pathological pre-existing-file state (a corrupt final file
	// plus an oversized leftover .part, each adjusted independently); that must
	// never surface as a negative or >100% progress bar.
	done := t.completed.Load()
	if done < 0 {
		done = 0
	}
	if t.total > 0 && done > t.total {
		done = t.total
	}
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
