// Package applog builds the log Gropius keeps about itself.
//
// It exists because the app had nowhere to say why. Everything the server knew
// about a refusal, a failed launch or a model leaving memory went to standard
// error, and a Finder-launched .app has no standard error — it goes to the
// bit bucket the moment launchd hands the process over. So the one place an
// operator could read the reason behind a refusal their client saw was a
// terminal they were not using.
//
// What this package adds is a file beside the model servers' own logs, in the
// account's own directory, written at the same time as stderr and from the
// same handler, so `make run` is unchanged and a Finder launch is no longer
// mute. The level is a slog.LevelVar the operator moves from Settings, which
// is why it is exported rather than baked into the handler: the handler is
// built once and the level is read on every line.
//
// Nothing here decides what goes on a line. That is the caller's business, and
// the rule the whole feature rests on — no prompt, no answer, no key, no
// token, no client address, at any level — is held where the lines are written
// and by the scans in internal/archtest, not here.
package applog

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// DefaultName is the current log's file name. It is fixed rather than dated so
// that "the log" is one path a person can tail, which is what an operator
// chasing a refusal actually does; the rotated files carry the numbers.
const DefaultName = "gropius.log"

// DefaultRotateBytes is how large the current log grows before the next one is
// started, and DefaultKeep is how many files survive — the current one and its
// predecessors together. Five megabytes is what internal/stats already rotates
// a file at, so the two logs on this Mac grow in the same unit; five files
// bound the whole log under 25 MB, which is small enough to leave alone on any
// Mac that can run a model and long enough to hold a session at the detailed
// level.
//
// They are constants because docs/logging.md prints them and an archtest holds
// the page to these names.
const (
	DefaultRotateBytes = 5 << 20
	DefaultKeep        = 5
)

// Options build a Log.
type Options struct {
	// Dir is the directory the log file lives in: config.Paths.Logs, which
	// resolves through the account directory, so one account's log is never
	// written into the shared root.
	Dir string
	// Name is the current log's file name. Empty means DefaultName.
	Name string
	// RotateBytes and Keep bound the log. Zero or negative means the defaults.
	RotateBytes int64
	Keep        int
	// Stderr is the second destination, and the only one when the file cannot
	// be opened. Nil means os.Stderr; a caller that genuinely wants no console
	// output passes io.Discard.
	Stderr io.Writer
	// Level is the level the logger starts at. The zero value is slog.LevelInfo,
	// which is the sparse level — so a caller that forgets it gets the default
	// the setting's own default names.
	Level slog.Level
}

// Log is the process's logger, the level it reads, and where its file is.
type Log struct {
	// Logger is what everything else in the process is handed.
	Logger *slog.Logger
	// Level is the one variable the handler reads on every line. Setting it
	// takes effect on the next line, which is what lets Settings change the
	// level without a restart.
	Level *slog.LevelVar
	// Path is the current log file, or "" when there is no file — a directory
	// that could not be used leaves the logger writing to stderr alone.
	Path string

	file *rotator
}

// Open builds the log.
//
// It returns a usable *Log even when it returns an error, and the two are
// independent: the error describes the file, and the Log is at worst the
// stderr-only logger the process had before this package existed. Nothing in
// the start-up path may exit because a log file could not be created — a
// server that will not start because it cannot write about starting is a
// worse outcome than one that starts quietly.
func Open(opts Options) (*Log, error) {
	if opts.Name == "" {
		opts.Name = DefaultName
	}
	if opts.RotateBytes <= 0 {
		opts.RotateBytes = DefaultRotateBytes
	}
	if opts.Keep <= 0 {
		opts.Keep = DefaultKeep
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}

	level := new(slog.LevelVar)
	level.Set(opts.Level)

	l := &Log{Level: level}
	out := opts.Stderr
	file, err := openRotator(opts)
	if err == nil {
		// stderr first, deliberately. io.MultiWriter stops at the first error,
		// so a file that fails mid-run costs the file and not the console.
		out = io.MultiWriter(opts.Stderr, file)
		l.file = file
		l.Path = filepath.Join(opts.Dir, opts.Name)
	}
	l.Logger = slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: level}))
	return l, err
}

// Close flushes and closes the file. It is safe to call more than once, and a
// logger that has been closed goes on writing to stderr — the app closes this
// from a defer that a second path can reach.
func (l *Log) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

// rotator is the size-rotating file the log is written to.
//
// It is the smallest thing that rotates: append until the next line would take
// the file past its cap, then shift the names along and start again.
// internal/stats/store.go rotates by size too, and this is deliberately not
// that writer — see iss-2609091714393599 and the spec: the store's rotation is
// bound up with UTC-day file names, a summary fold and a two-bound retention,
// and pulling a shared primitive out of it is a larger change to this
// repository's durable data format than a log file justifies. The duplication
// is recorded rather than hidden.
//
// What it does share is the discipline, and that is not negotiable: the file
// name is predictable, so every open goes through an os.Root on the log
// directory with O_NOFOLLOW against a planted link, O_NONBLOCK against a
// planted FIFO, mode 0600, and an fstat on the opened handle that refuses
// anything which is not a regular file. It is the rule
// internal/runtime/launcher.go applies to a model server's log, with O_APPEND
// where that has O_TRUNC — this log outlives one model server's run, and
// rotation rather than truncation is what bounds it.
type rotator struct {
	root *os.Root
	name string
	base string
	ext  string
	max  int64
	keep int
	perm os.FileMode
	mu   sync.Mutex
	f    *os.File
	n    int64
	// dead marks a writer that has failed to rotate. See Write.
	dead bool
}

// fileFlags is how the log is opened. The same set internal/stats and
// internal/runtime use, with O_APPEND in place of O_TRUNC.
const fileFlags = os.O_CREATE | os.O_WRONLY | os.O_APPEND | syscall.O_NONBLOCK | syscall.O_NOFOLLOW

// logPerm is the mode the log and every rotated file carry: this account's own
// record of what its own server did, and nobody else's business.
const logPerm os.FileMode = 0o600

func openRotator(opts Options) (*rotator, error) {
	// Lstat rather than MkdirAll, exactly as the launcher does: a path-based
	// MkdirAll would follow a link left under the directory's name and put this
	// account's log inside a directory it did not choose. The directory is
	// created at start-up by config.Paths.EnsureDirs; this only checks it.
	fi, err := os.Lstat(opts.Dir)
	if err != nil {
		return nil, fmt.Errorf("log directory %s: %w", opts.Dir, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("log directory %s is not a directory", opts.Dir)
	}
	root, err := os.OpenRoot(opts.Dir)
	if err != nil {
		return nil, fmt.Errorf("open log directory %s: %w", opts.Dir, err)
	}
	ext := filepath.Ext(opts.Name)
	r := &rotator{
		root: root,
		name: opts.Name,
		base: strings.TrimSuffix(opts.Name, ext),
		ext:  ext,
		max:  opts.RotateBytes,
		keep: opts.Keep,
		perm: logPerm,
	}
	if err := r.open(); err != nil {
		root.Close()
		return nil, err
	}
	return r, nil
}

// open opens the current log for appending and records how large it already
// is, so a restart continues the file rather than emptying it.
func (r *rotator) open() error {
	f, err := r.root.OpenFile(r.name, fileFlags, r.perm)
	if err != nil {
		return fmt.Errorf("create log %s: %w", r.name, err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err == nil {
			err = errors.New("not a regular file")
		}
		return fmt.Errorf("create log %s: %w", r.name, err)
	}
	r.f, r.n = f, info.Size()
	return nil
}

// Write appends one log record, rotating first if it would not fit.
//
// A write that fails is reported to io.MultiWriter, which is why stderr is
// written first: the line the operator can still see is never lost to a full
// disk. A rotation that fails puts the writer to sleep rather than retrying
// per line — a directory that has stopped working will not start again inside
// this process, and a log that tries every line turns one broken directory
// into a syscall storm on the request path.
func (r *rotator) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dead || r.f == nil {
		return 0, os.ErrClosed
	}
	if r.n > 0 && r.n+int64(len(b)) > r.max {
		if err := r.rotate(); err != nil {
			r.dead = true
			return 0, err
		}
	}
	n, err := r.f.Write(b)
	r.n += int64(n)
	return n, err
}

// rotate closes the current file, shifts the numbered names along, removes
// what falls past keep, and opens a fresh current file.
//
// The shift runs from the oldest name down so that no rename overwrites a file
// that has not been moved yet, and every one of them goes through the os.Root:
// a link or a directory left under a rotated name cannot become the place the
// previous log ends up, because Rename inside a root replaces the name rather
// than following it.
func (r *rotator) rotate() error {
	if err := r.f.Close(); err != nil {
		return err
	}
	r.f, r.n = nil, 0
	// keep counts the current file as one of them, so the highest number that
	// survives is keep-1 and anything at or above it goes.
	for i := r.keep - 1; i >= 1; i-- {
		from := r.name
		if i > 1 {
			from = r.numbered(i - 1)
		}
		to := r.numbered(i)
		if i == r.keep-1 {
			// The file this rename is about to land on is the oldest kept one,
			// and it is what falls out of the window.
			if err := r.root.Remove(to); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if err := r.root.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// keep == 1 means no history at all: the current file is simply removed.
	if r.keep == 1 {
		if err := r.root.Remove(r.name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return r.open()
}

// numbered is the name of the nth previous log: gropius.1.log, gropius.2.log…
func (r *rotator) numbered(n int) string {
	return fmt.Sprintf("%s.%d%s", r.base, n, r.ext)
}

// Close closes the file and the directory handle. Calling it twice is not an
// error, and neither is writing afterwards: a closed writer reports
// os.ErrClosed to the multi-writer, which has already written the line to
// stderr.
func (r *rotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	r.root.Close()
	return err
}
