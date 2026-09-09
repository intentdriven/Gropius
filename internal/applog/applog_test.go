package applog_test

import (
	"bytes"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/intentdriven/Gropius/internal/applog"
)

// openIn builds a log in dir with the defaults a test wants: a small file, a
// few of them, and a stderr the test can read.
func openIn(t *testing.T, dir string, opts applog.Options) (*applog.Log, *bytes.Buffer) {
	t.Helper()
	var stderr bytes.Buffer
	opts.Dir = dir
	opts.Stderr = &stderr
	l, err := applog.Open(opts)
	if err != nil {
		t.Fatalf("Open(%s): %v", dir, err)
	}
	t.Cleanup(func() { l.Close() })
	return l, &stderr
}

// The file holds one account's record of what its own server did, so it is
// owner-only — the same 0600 the model servers' logs are opened with — and it
// is a regular file rather than whatever was standing under the name.
func TestTheLogFileIsOwnerOnlyAndRegular(t *testing.T) {
	dir := t.TempDir()
	l, _ := openIn(t, dir, applog.Options{})

	l.Logger.Info("serving")

	if want := filepath.Join(dir, "gropius.log"); l.Path != want {
		t.Errorf("Path = %q, want %q", l.Path, want)
	}
	fi, err := os.Lstat(l.Path)
	if err != nil {
		t.Fatalf("the log file was not created: %v", err)
	}
	if !fi.Mode().IsRegular() {
		t.Errorf("log file mode = %v, want a regular file", fi.Mode())
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("log file permissions = %#o, want 0600", perm)
	}
}

// The name is predictable, so a link left under it would let the open stream
// this account's log into any file this account can write. O_NOFOLLOW refuses
// it, and the target must be untouched.
func TestOpenRefusesALinkLeftUnderTheLogsName(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(target, []byte("not the log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "gropius.log")); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	l, err := applog.Open(applog.Options{Dir: dir, Stderr: &stderr})
	if err == nil {
		l.Close()
		t.Fatal("Open followed a symbolic link left under the log's name")
	}
	t.Cleanup(func() { l.Close() })
	l.Logger.Info("serving")
	if got, _ := os.ReadFile(target); string(got) != "not the log\n" {
		t.Errorf("the link's target was written through: %q", got)
	}
}

// A FIFO planted under the name would block the open forever without
// O_NONBLOCK, which would hang the whole start-up.
func TestOpenRefusesAFifoLeftUnderTheLogsName(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "gropius.log"), 0o600); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}

	var stderr bytes.Buffer
	l, err := applog.Open(applog.Options{Dir: dir, Stderr: &stderr})
	if l != nil {
		t.Cleanup(func() { l.Close() })
	}
	if err == nil {
		t.Fatal("Open accepted a FIFO left under the log's name")
	}
}

// A log directory that cannot be used costs the file and nothing else: the
// process still starts, and `make run` still prints its lines.
func TestAnUnusableDirectoryStillYieldsAWorkingLogger(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "logs")
	if err := os.WriteFile(notADir, []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	l, err := applog.Open(applog.Options{Dir: notADir, Stderr: &stderr})
	if err == nil {
		t.Fatal("Open reported no problem with a log directory that is a file")
	}
	if l == nil || l.Logger == nil {
		t.Fatal("Open returned no logger; a failed log file must not cost the process its log")
	}
	t.Cleanup(func() { l.Close() })
	if l.Path != "" {
		t.Errorf("Path = %q, want empty when there is no file", l.Path)
	}
	l.Logger.Info("serving")
	if !strings.Contains(stderr.String(), "serving") {
		t.Errorf("the stderr line was lost with the file:\n%s", stderr.String())
	}
}

// One handler, two destinations: a line cannot appear in one and not the
// other, which is what makes the file a faithful record of what `make run`
// would have shown.
func TestEveryLineReachesStderrAndTheFile(t *testing.T) {
	dir := t.TempDir()
	l, stderr := openIn(t, dir, applog.Options{})

	l.Logger.Info("refused a request", "model", "org/repo")
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	onDisk, err := os.ReadFile(filepath.Join(dir, "gropius.log"))
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{"stderr": stderr.String(), "file": string(onDisk)} {
		if !strings.Contains(got, "refused a request") || !strings.Contains(got, "org/repo") {
			t.Errorf("%s did not carry the line:\n%s", name, got)
		}
	}
}

// The level is one variable shared by the handler, so raising it takes effect
// on the next line rather than at the next start.
func TestTheLevelChangesWithoutReopening(t *testing.T) {
	dir := t.TempDir()
	l, _ := openIn(t, dir, applog.Options{Level: slog.LevelInfo})

	l.Logger.Debug("a figure sparse omits", "budget", 123)
	l.Level.Set(slog.LevelDebug)
	l.Logger.Debug("a figure detailed carries", "budget", 456)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "gropius.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "sparse omits") {
		t.Errorf("a Debug line was written while the level was Info:\n%s", got)
	}
	if !strings.Contains(string(got), "detailed carries") {
		t.Errorf("raising the level did not take effect:\n%s", got)
	}
}

// logFiles lists the log and its rotated siblings, and what they total.
func logFiles(t *testing.T, dir string) (names []string, total int64) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "gropius") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, e.Name())
		total += info.Size()
	}
	return names, total
}

// The log is bounded: it rotates at its size, keeps a fixed number of files,
// and removes the oldest rather than growing.
func TestTheLogRotatesAtItsSizeAndPrunesOldFiles(t *testing.T) {
	dir := t.TempDir()
	const rotate, keep = 2 << 10, 3
	l, _ := openIn(t, dir, applog.Options{RotateBytes: rotate, Keep: keep})

	for i := range 400 {
		l.Logger.Info("a model server left the pool", "n", i, "model", "org/repo")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	names, total := logFiles(t, dir)
	if len(names) > keep {
		t.Errorf("kept %d files %v, want at most %d", len(names), names, keep)
	}
	if max := int64(rotate) * keep * 2; total > max {
		t.Errorf("the log totals %d bytes, want it bounded under %d", total, max)
	}
	current, err := os.ReadFile(filepath.Join(dir, "gropius.log"))
	if err != nil {
		t.Fatalf("the current log is gone after rotating: %v", err)
	}
	if int64(len(current)) > rotate {
		t.Errorf("the current log is %d bytes, past its %d-byte cap", len(current), rotate)
	}
	if !strings.Contains(string(current), "n=399") {
		t.Errorf("the last line written is not in the current log:\n%s", current)
	}
	// The line before the rotation is in the file the rotation made, not lost.
	previous, err := os.ReadFile(filepath.Join(dir, "gropius.1.log"))
	if err != nil {
		t.Fatalf("rotation did not leave the previous file: %v", err)
	}
	if len(previous) == 0 {
		t.Error("the rotated file is empty")
	}
	// Every kept file is owner-only: a rotated log holds exactly what the
	// current one held a moment ago.
	for _, name := range names {
		fi, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s permissions = %#o, want 0600", name, perm)
		}
	}
}

// The writer is one mutex, and every caller of the process's logger goes
// through it — including the ones that rotate. Run under -race.
func TestRotationIsSafeUnderConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	l, _ := openIn(t, dir, applog.Options{RotateBytes: 1 << 10, Keep: 3})

	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 100 {
				l.Logger.Info("serving", "worker", w, "n", i)
				l.Level.Set(slog.LevelDebug)
				l.Logger.Debug("figures", "budget", 1<<30)
				l.Level.Set(slog.LevelInfo)
			}
		}()
	}
	wg.Wait()
	if err := l.Close(); err != nil {
		t.Fatalf("Close after concurrent writes: %v", err)
	}
	if names, _ := logFiles(t, dir); len(names) > 3 {
		t.Errorf("kept %d files %v, want at most 3", len(names), names)
	}
}

// Rotation renames within the log directory and nowhere else. A link left
// under a rotated name must not become the place the previous log is written.
func TestRotationDoesNotFollowALinkLeftUnderARotatedName(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(target, []byte("not the log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "gropius.1.log")); err != nil {
		t.Fatal(err)
	}

	l, _ := openIn(t, dir, applog.Options{RotateBytes: 512, Keep: 3})
	for i := range 100 {
		l.Logger.Info("serving", "n", i)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(target); string(got) != "not the log\n" {
		t.Errorf("rotation wrote through a link: target now %q", got)
	}
	fi, err := os.Lstat(filepath.Join(dir, "gropius.1.log"))
	if err != nil {
		t.Fatalf("gropius.1.log: %v", err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		t.Error("gropius.1.log is still the planted link; rotation renamed around it")
	}
}

// A log that is opened twice in the life of one account — a restart — appends
// rather than truncating: the reason a refusal happened before a restart is
// exactly what an operator goes looking for after one.
func TestReopeningAppendsRatherThanTruncating(t *testing.T) {
	dir := t.TempDir()

	first, _ := openIn(t, dir, applog.Options{})
	first.Logger.Info("the first run")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, _ := openIn(t, dir, applog.Options{})
	second.Logger.Info("the second run")
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "gropius.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"the first run", "the second run"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("the log is missing %q after a restart:\n%s", want, got)
		}
	}
}

// The defaults are what the reference page prints, so they are stated once and
// read from here.
func TestTheDefaultsAreTheOnesTheDocsPrint(t *testing.T) {
	if applog.DefaultRotateBytes != 5<<20 {
		t.Errorf("DefaultRotateBytes = %d, want %d", applog.DefaultRotateBytes, 5<<20)
	}
	if applog.DefaultKeep != 5 {
		t.Errorf("DefaultKeep = %d, want 5", applog.DefaultKeep)
	}
	if applog.DefaultName != "gropius.log" {
		t.Errorf("DefaultName = %q, want gropius.log", applog.DefaultName)
	}
}

// A Close that has already happened must not panic or double-close the file;
// the app closes the log from a defer that a second path can reach.
func TestCloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	l, _ := openIn(t, dir, applog.Options{})
	l.Logger.Info("serving")
	if err := l.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// Writing after Close is dropped, not a panic and not a crash.
	l.Logger.Info("after close")
	_ = fmt.Sprint(l.Path)
}

// The log directory belongs to this account and may not exist yet: on a first
// run, and on a shared-cache install where this account has never run Gropius
// per-user, nothing has created it. Opening the log creates it owner-only
// rather than refusing — the alternative is a first run with no log, which is
// the run whose log is most worth having.
func TestOpenCreatesTheLogDirectoryWhenItIsMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")

	l, _ := openIn(t, dir, applog.Options{})
	l.Logger.Info("serving")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("the log directory was not created: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Errorf("log directory permissions = %#o, want 0700", perm)
	}
	if _, err := os.Stat(filepath.Join(dir, "gropius.log")); err != nil {
		t.Errorf("the log file is not in the created directory: %v", err)
	}
}

// A link standing where the log directory should be is refused rather than
// followed: an install that moved its logs, or anything that can write the
// parent, could otherwise choose where this account's log is written.
func TestOpenRefusesALinkStandingInForTheLogDirectory(t *testing.T) {
	parent := t.TempDir()
	elsewhere := t.TempDir()
	dir := filepath.Join(parent, "logs")
	if err := os.Symlink(elsewhere, dir); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	l, err := applog.Open(applog.Options{Dir: dir, Stderr: &stderr})
	if l != nil {
		t.Cleanup(func() { l.Close() })
	}
	if err == nil {
		t.Fatal("Open followed a link standing where the log directory should be")
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "gropius.log")); err == nil {
		t.Error("the log was written through the link")
	}
}
