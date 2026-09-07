package stats

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestStore returns a store whose directory does not exist yet, so a test
// that never turns it on can assert that nothing was created.
func newTestStore(t *testing.T, opts StoreOptions) (*FileStore, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "stats")
	if opts.Months == 0 {
		// A century, so that a test which is not about the horizon can stamp
		// its records with whatever number reads clearly and still find them
		// afterwards. A test about the horizon sets its own figure.
		opts.Months = 1200
	}
	if opts.MaxBytes == 0 {
		opts.MaxBytes = 1 << 20
	}
	if opts.RotateBytes == 0 {
		opts.RotateBytes = 64 << 10
	}
	s := NewStore(dir, opts)
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func on(t *testing.T, s *FileStore) {
	t.Helper()
	if err := s.SetEnabled(true); err != nil {
		t.Fatalf("the store refused to open: %v", err)
	}
}

// read returns every record in the store, newest first.
func read(t *testing.T, s *FileStore, limit int) []Line {
	t.Helper()
	var out []Line
	if err := s.Latest(limit, func(l Line) bool { out = append(out, l); return true }); err != nil {
		t.Fatalf("reading the store: %v", err)
	}
	return out
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

// The point of the store: what was recorded is still there after the process
// that recorded it is gone, and every line says which schema it was written
// under so a later Gropius can read it.
func TestTheRecordsAreStillThereAfterARestart(t *testing.T) {
	s, dir := newTestStore(t, StoreOptions{})
	on(t, s)
	for i := range 10 {
		if err := s.AppendRequest(Record{
			Model: "org/a", At: int64(1788696030 + i), Class: ClassOK, Streamed: true,
			PromptTokens: 10 + i, CompletionTokens: 20 + i, FirstTokenMS: 200,
			DurationMS: 4200, QueueWaitMS: 40, LoadWaitMS: 1200,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AppendEvent(Event{At: 1788696040, Model: "org/a", Kind: EventLoad, DurationMS: 900}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendSettings(Settings{At: 1788696000, BudgetBytes: 42 << 30, DecodeConcurrency: 4, Months: 6, MaxBytes: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// A second process, opening the same directory.
	again := NewStore(dir, StoreOptions{Months: 1200, MaxBytes: 1 << 20, RotateBytes: 64 << 10})
	defer again.Close()
	on(t, again)

	lines := read(t, again, 0)
	var requests, loads, settings int
	for _, l := range lines {
		if l.V != SchemaVersion {
			t.Errorf("a %s line carries schema version %d, want %d", l.Kind, l.V, SchemaVersion)
		}
		switch l.Kind {
		case KindRequest:
			requests++
			if l.Request.Model != "org/a" || l.Request.Class != ClassOK || l.Request.DurationMS != 4200 {
				t.Errorf("a request came back as %+v", l.Request)
			}
		case KindLoad:
			loads++
			if l.Event.DurationMS != 900 || l.Event.Model != "org/a" {
				t.Errorf("a load came back as %+v", l.Event)
			}
		case KindSettings:
			settings++
			if l.Settings.BudgetBytes != 42<<30 || l.Settings.DecodeConcurrency != 4 {
				t.Errorf("the settings record came back as %+v", l.Settings)
			}
		default:
			t.Errorf("the store holds a %q line, which no record kind writes", l.Kind)
		}
	}
	if requests != 10 || loads != 1 || settings != 1 {
		t.Errorf("read back %d requests, %d loads and %d settings records, want 10, 1 and 1", requests, loads, settings)
	}

	// Newest first, and a bound is a bound.
	if got := read(t, again, 3); len(got) != 3 {
		t.Fatalf("a bound of 3 returned %d records", len(got))
	} else if got[0].Kind != KindSettings {
		t.Errorf("the first record back is a %s; the newest record was the settings one", got[0].Kind)
	}
}

// Every line is a JSON object a person can read with any tool they like, which
// is the whole reason the format was chosen over a database.
func TestEveryLineIsOneJSONObject(t *testing.T) {
	s, dir := newTestStore(t, StoreOptions{})
	on(t, s)
	if err := s.AppendRequest(Record{Model: "org/a", At: 1788696030, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	files, err := s.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("the store holds %d files, want 1", len(files))
	}
	raw, err := os.ReadFile(filepath.Join(dir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Error("the file does not end in a newline, so an appended line would join the last one")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSuffix(string(raw), "\n")), &obj); err != nil {
		t.Fatalf("the line is not a JSON object: %v", err)
	}
	for _, key := range []string{"v", "kind", "model", "at", "class"} {
		if _, ok := obj[key]; !ok {
			t.Errorf("the line carries no %q", key)
		}
	}
}

// Off is off: no directory, no file, and nothing changed in one that already
// exists. This is the switch adr-2609061503319212 rests on, applied to disk.
func TestNothingIsWrittenWhileTheSwitchIsOff(t *testing.T) {
	s, dir := newTestStore(t, StoreOptions{})
	if err := s.AppendRequest(Record{Model: "org/a", At: 1, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendSettings(Settings{At: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the store directory exists with the switch off (%v)", err)
	}

	// Now on, then off again: what is on disk must not move.
	on(t, s)
	for i := range 5 {
		if err := s.AppendRequest(Record{Model: "org/a", At: int64(i), Class: ClassOK}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	before := snapshotDir(t, dir)
	if len(before) == 0 {
		t.Fatal("nothing was written while the switch was on")
	}
	for i := range 5 {
		if err := s.AppendRequest(Record{Model: "org/b", At: int64(100 + i), Class: ClassOK}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if after := snapshotDir(t, dir); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Errorf("the store changed with the switch off:\n before %v\n after  %v", before, after)
	}
}

// snapshotDir describes every file in dir by name, size and modification time.
func snapshotDir(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, name := range names(t, dir) {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s %d %s", name, fi.Size(), fi.ModTime()))
	}
	return out
}

// The size cap is the hard bound: it holds however many records arrive, and
// what goes is always the oldest file, so the store loses its past rather than
// its present.
func TestTheStoreStaysUnderItsSizeCap(t *testing.T) {
	const cap = 8 << 10
	s, dir := newTestStore(t, StoreOptions{MaxBytes: cap, RotateBytes: 1 << 10})
	on(t, s)
	for i := range 300 {
		if err := s.AppendRequest(Record{
			Model: "org/a", At: int64(1788696030 + i), Class: ClassOK,
			PromptTokens: i, CompletionTokens: i, DurationMS: int64(i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}

	var total int64
	for _, name := range names(t, dir) {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		total += fi.Size()
	}
	if total > cap {
		t.Errorf("the store holds %d bytes, over its %d-byte cap", total, cap)
	}
	if total == 0 {
		t.Fatal("the store holds nothing at all")
	}

	// What survived is the recent past, and the reported oldest date is the
	// oldest record actually still held rather than one that was dropped.
	lines := read(t, s, 0)
	if len(lines) == 0 {
		t.Fatal("no record survived")
	}
	oldest := lines[len(lines)-1]
	if got := s.Status().Oldest; got != oldest.At {
		t.Errorf("the store reports its oldest record as %d; the oldest one it holds is %d", got, oldest.At)
	}
	if oldest.At == 1788696030 {
		t.Error("nothing was pruned, so the cap was never reached and this test proves nothing")
	}
}

// The months figure prunes within the cap: a file whose newest record has
// fallen off the horizon goes even while there is room for it.
func TestFilesOlderThanTheHorizonArePruned(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	s, dir := newTestStore(t, StoreOptions{
		Months: 2, MaxBytes: 1 << 20, RotateBytes: 512,
		Now: func() time.Time { return now },
	})
	on(t, s)

	// A day's worth of records from eight months ago, then enough of today's
	// to rotate past them.
	old := now.AddDate(0, -8, 0).Unix()
	for i := range 40 {
		if err := s.AppendRequest(Record{Model: "org/a", At: old + int64(i), Class: ClassOK}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 40 {
		if err := s.AppendRequest(Record{Model: "org/a", At: now.Unix() + int64(i), Class: ClassOK}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	kept := names(t, dir)
	if len(kept) == 0 {
		t.Fatal("the store holds no files")
	}
	if slices.Contains(kept, "stats-20260907-001.jsonl") {
		t.Errorf("the first file is still there, so nothing was pruned: %v", kept)
	}
	// Retention removes whole files, so what must be gone is every file whose
	// newest record has fallen off the horizon. A file written across the
	// horizon is kept until its own newest record falls beyond it.
	cutoff := now.AddDate(0, -2, 0).Unix()
	for _, name := range kept {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		var newest Line
		for _, line := range lines {
			var l Line
			if err := json.Unmarshal([]byte(line), &struct {
				*Line
				At *int64 `json:"at"`
			}{Line: &l, At: &l.At}); err == nil {
				newest = l
			}
		}
		if newest.At < cutoff {
			t.Errorf("%s holds nothing newer than %s and is still there",
				name, time.Unix(newest.At, 0).UTC().Format(time.DateOnly))
		}
	}
}

// Clear removes what the store wrote and nothing else. Removing whatever a
// directory listing happened to return is the class of bug the registry was
// hardened against in the 2026-09-06 triage.
func TestClearRemovesOnlyTheStoresOwnFiles(t *testing.T) {
	s, dir := newTestStore(t, StoreOptions{})
	on(t, s)
	for i := range 5 {
		if err := s.AppendRequest(Record{Model: "org/a", At: int64(i), Class: ClassOK}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	bystander := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(bystander, []byte("someone else's file"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if files, err := s.Files(); err != nil {
		t.Fatal(err)
	} else if len(files) != 0 {
		t.Errorf("the store still holds %v after Clear", files)
	}
	if _, err := os.Stat(bystander); err != nil {
		t.Errorf("Clear removed a file that is not the store's: %v", err)
	}
	if st := s.Status(); st.Bytes != 0 || st.Oldest != 0 {
		t.Errorf("after Clear the store reports %+v, want nothing held", st)
	}

	// And it keeps recording: Clear empties the store, it does not switch it
	// off.
	if err := s.AppendRequest(Record{Model: "org/a", At: 99, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, s, 0); len(got) != 1 {
		t.Errorf("after Clear the store holds %d records, want the one written since", len(got))
	}
}

// A crash costs the line it was in the middle of writing and nothing else. The
// reader must skip it rather than refuse the file, or one power cut would take
// months of records with it.
func TestATornLastLineCostsOnlyThatLine(t *testing.T) {
	s, dir := newTestStore(t, StoreOptions{})
	on(t, s)
	for i := range 3 {
		if err := s.AppendRequest(Record{Model: "org/a", At: int64(10 + i), Class: ClassOK}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// The file as a crash mid-write leaves it: a half-written object, with no
	// newline after it.
	files, err := s.Files()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, files[0])
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"v":1,"kind":"request","model":"org/a","at":13,"cl`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	again := NewStore(dir, StoreOptions{Months: 1200})
	defer again.Close()
	on(t, again)
	got := read(t, again, 0)
	if len(got) != 3 {
		t.Fatalf("read %d records back out of a torn file, want the 3 whole ones", len(got))
	}
	// And the store carries on from there: the torn line must not make every
	// later record unreadable.
	if err := again.AppendRequest(Record{Model: "org/a", At: 20, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	if err := again.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, again, 0); len(got) != 4 {
		t.Errorf("after a torn line the store reads %d records, want 4", len(got))
	}
}

// A line from a build that knew more fields than this one still reads; a line
// from a build that changed the shape does not, because that is the whole of
// what the version is for; and a line that is not JSON at all costs itself
// alone. Each one that cannot be used is counted, so an aggregate drawn over
// half a file is not drawn silently.
func TestUnknownFieldsAreIgnoredAndUnreadableLinesAreCounted(t *testing.T) {
	s, dir := newTestStore(t, StoreOptions{})
	on(t, s)
	if err := s.AppendRequest(Record{Model: "org/a", At: 1788696030, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	files, _ := s.Files()
	f, err := os.OpenFile(filepath.Join(dir, files[0]), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// A field this build does not know, under a version it does: read, with
	// the field ignored.
	f.WriteString(`{"v":1,"kind":"request","model":"org/b","at":1788696031,"class":"ok","session":"whatever"}` + "\n")
	// A version this build does not know: skipped, whatever it says.
	f.WriteString(`{"v":2,"kind":"request","model":"org/c","at":1788696032,"class":"ok"}` + "\n")
	f.WriteString("not json at all\n")
	f.WriteString("\n")
	f.Close()

	again := NewStore(dir, StoreOptions{Months: 1200})
	defer again.Close()
	on(t, again)
	got := read(t, again, 0)
	if len(got) != 2 {
		t.Fatalf("read %d records, want the 2 this build can use: %+v", len(got), got)
	}
	for _, l := range got {
		if l.Request.Model == "org/c" {
			t.Error("a line written under a later version of the format was read with this version's field names")
		}
	}
	if got[0].Request.Model != "org/b" {
		t.Errorf("the newest usable line came back as %+v", got[0])
	}
	if n := again.Status().Skipped; n != 2 {
		t.Errorf("the read reported %d unreadable lines, want the later version and the rubbish", n)
	}
}

// A bound is a bound on the work as well as on the answer: asking for the last
// record must not decode every record in the newest file.
func TestABoundStopsTheReadRatherThanTheAnswer(t *testing.T) {
	s, dir := newTestStore(t, StoreOptions{})
	on(t, s)
	if err := s.AppendRequest(Record{Model: "org/a", At: 1788696030, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Rubbish at the front of the file, and a good record after it. A read
	// bounded at one record must never reach the rubbish, and so must not
	// count it.
	files, _ := s.Files()
	path := filepath.Join(dir, files[0])
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte("not json at all\n"), raw...), 0o600); err != nil {
		t.Fatal(err)
	}

	again := NewStore(dir, StoreOptions{Months: 1200})
	defer again.Close()
	on(t, again)
	if got := read(t, again, 1); len(got) != 1 {
		t.Fatalf("a bound of 1 returned %d records", len(got))
	}
	if n := again.Status().Skipped; n != 0 {
		t.Errorf("a read bounded at one record still worked through %d unreadable lines before it", n)
	}
	if got := read(t, again, 0); len(got) != 1 {
		t.Fatalf("an unbounded read returned %d records", len(got))
	}
	if n := again.Status().Skipped; n != 1 {
		t.Errorf("an unbounded read reported %d unreadable lines, want the one at the front", n)
	}
}

// The store is one account's own record, so it refuses to write itself
// anywhere another account could read or replace it.
func TestTheStoreRefusesADirectoryOthersCanWrite(t *testing.T) {
	t.Run("the store's own directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "stats")
		if err := os.Mkdir(dir, 0o770); err != nil {
			t.Fatal(err)
		}
		// Explicitly, because the process umask would otherwise have taken the
		// group bit straight back off and left the test asserting nothing.
		if err := os.Chmod(dir, 0o770); err != nil {
			t.Fatal(err)
		}
		s := NewStore(dir, StoreOptions{})
		defer s.Close()
		if err := s.SetEnabled(true); err == nil {
			t.Fatal("the store opened inside a group-writable directory")
		}
		if s.Enabled() {
			t.Error("the store reports itself as recording after refusing to open")
		}
		if len(names(t, dir)) != 0 {
			t.Error("the store wrote a file into a directory it refused")
		}
	})

	t.Run("a directory above it", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "root")
		if err := os.Mkdir(root, 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(root, 0o777); err != nil {
			t.Fatal(err)
		}
		s := NewStore(filepath.Join(root, "stats"), StoreOptions{})
		defer s.Close()
		if err := s.SetEnabled(true); err == nil {
			t.Fatal("the store opened under a world-writable directory")
		}
		if _, err := os.Stat(filepath.Join(root, "stats")); !os.IsNotExist(err) {
			t.Error("the store created its directory under a world-writable one")
		}
	})

	t.Run("something planted under the store's own name", func(t *testing.T) {
		base := t.TempDir()
		elsewhere := filepath.Join(base, "elsewhere")
		if err := os.Mkdir(elsewhere, 0o700); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(base, "stats")
		if err := os.Symlink(elsewhere, dir); err != nil {
			t.Fatal(err)
		}
		s := NewStore(dir, StoreOptions{})
		defer s.Close()
		if err := s.SetEnabled(true); err == nil {
			t.Fatal("the store followed a symlink planted under its own name")
		}
	})
}

// The directory and the files are the account's own, at the modes the
// launcher already uses for a model server's log.
func TestTheStoreIsOwnerOnly(t *testing.T) {
	s, dir := newTestStore(t, StoreOptions{})
	on(t, s)
	if err := s.AppendRequest(Record{Model: "org/a", At: 1, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o700 {
		t.Errorf("the store directory is %04o, want 0700", got)
	}
	for _, name := range names(t, dir) {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Errorf("%s is %04o, want 0600", name, got)
		}
	}
}

// Rotation keeps any one file small enough to read whole, and names each file
// after the day it was started so a person can find the week they are after.
func TestFilesRotateAtTheirLimit(t *testing.T) {
	s, dir := newTestStore(t, StoreOptions{RotateBytes: 512, MaxBytes: 1 << 20})
	on(t, s)
	for i := range 100 {
		if err := s.AppendRequest(Record{Model: "org/a", At: int64(1788696030 + i), Class: ClassOK}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	files := names(t, dir)
	if len(files) < 3 {
		t.Fatalf("100 records made %d files at a 512-byte limit: %v", len(files), files)
	}
	for _, name := range files {
		if !storeFilePattern.MatchString(name) {
			t.Errorf("%q is not a store file name", name)
		}
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Size() > 512+256 {
			t.Errorf("%s is %d bytes, well past the 512-byte rotation limit", name, fi.Size())
		}
	}
}

// A request is never made to wait for a disk. If the writer falls behind, the
// records that cannot be queued are counted and dropped, and the count is
// shown rather than hidden.
func TestAFullQueueDropsRecordsRatherThanHoldingUpARequest(t *testing.T) {
	held := make(chan struct{})
	s, _ := newTestStore(t, StoreOptions{QueueSize: 1, beforeWrite: func() { <-held }})
	on(t, s)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 200 {
			s.AppendRequest(Record{Model: "org/a", At: int64(i), Class: ClassOK})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("appending blocked on a writer that is not writing")
	}
	if got := s.Status().Dropped; got == 0 {
		t.Error("nothing was reported as dropped, though the writer never wrote")
	}
	close(held)
}

// The settings a person actually served under are recorded, once, so a later
// reader can see what changed and when. Writing the same values again on every
// restart-free save would be a record of nothing.
func TestTheSettingsInForceAreRecordedOnceUntilTheyChange(t *testing.T) {
	s, _ := newTestStore(t, StoreOptions{})
	on(t, s)
	set := Settings{At: 100, BudgetBytes: 1 << 30, DecodeConcurrency: 4, IdleTimeoutSec: 0, Months: 6, MaxBytes: 1 << 20}
	for range 3 {
		if err := s.AppendSettings(set); err != nil {
			t.Fatal(err)
		}
	}
	changed := set
	changed.At = 200
	changed.Months = 3
	if err := s.AppendSettings(changed); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	var got []Settings
	for _, l := range read(t, s, 0) {
		if l.Kind == KindSettings {
			got = append(got, l.Settings)
		}
	}
	if len(got) != 2 {
		t.Fatalf("the store holds %d settings records, want the two distinct ones", len(got))
	}
	if got[0].Months != 3 || got[1].Months != 6 {
		t.Errorf("the settings records came back as %+v", got)
	}
}

// Every field the store writes is named by StoreFields, which is what the
// documentation is held to.
func TestStoreFieldsNamesEveryFieldWritten(t *testing.T) {
	s, dir := newTestStore(t, StoreOptions{})
	on(t, s)
	if err := s.AppendRequest(Record{Model: "org/a", At: 1, Class: ClassOK, FirstTokenMS: 1, QueueWaitMS: 1, LoadWaitMS: 1, DurationMS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(Event{At: 2, Model: "org/a", Kind: EventRemoved, Reason: ReasonEvicted}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(Event{At: 3, Model: "org/a", Kind: EventLoad, DurationMS: 5, Failed: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendSettings(Settings{At: 4, BudgetBytes: 1, DecodeConcurrency: 1, IdleTimeoutSec: 1, Months: 1, MaxBytes: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}

	known := map[string]bool{}
	for _, f := range StoreFields() {
		known[f] = true
	}
	files, _ := s.Files()
	for _, name := range files {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line == "" {
				continue
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				t.Fatal(err)
			}
			for k := range obj {
				if !known[k] {
					t.Errorf("the store writes %q, which StoreFields does not name", k)
				}
			}
		}
	}
}

// An emptied store states the settings in force again before the first record
// that follows it — but not before, because Clear leaves the directory empty
// and a file that appeared as the button was pressed would be a file the
// operator did not ask for.
func TestAnEmptiedStoreStatesTheSettingsAgainBeforeTheNextRecord(t *testing.T) {
	s, dir := newTestStore(t, StoreOptions{})
	on(t, s)
	if err := s.AppendSettings(Settings{At: 10, DecodeConcurrency: 4, Months: 6}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendRequest(Record{Model: "org/a", At: 11, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); len(got) != 0 {
		t.Fatalf("Clear left %v behind", got)
	}

	if err := s.AppendRequest(Record{Model: "org/a", At: 12, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	got := read(t, s, 0)
	if len(got) != 2 {
		t.Fatalf("the store holds %d records, want the request and the settings before it", len(got))
	}
	if got[0].Kind != KindRequest || got[1].Kind != KindSettings {
		t.Fatalf("the store holds %s then %s, want the settings first", got[1].Kind, got[0].Kind)
	}
	if got[1].Settings.DecodeConcurrency != 4 || got[1].Settings.Months != 6 {
		t.Errorf("the restated settings are %+v, not the ones in force", got[1].Settings)
	}
	if got[1].Settings.At == 10 {
		t.Error("the restated settings carry the old record's time, not the time they were written again")
	}
}

// Reading and clearing hold the same rule about where the store may be as
// writing does. os.OpenRoot does not refuse a symbolic link at its own final
// component, so a store directory that is a link into somebody else's
// directory would otherwise have every matching file in that directory
// deleted by Clear.
func TestReadingAndClearingRefuseADirectoryTheWriterWouldRefuse(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(base, "victim")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(victim, "stats-20260907-001.jsonl")
	if err := os.WriteFile(planted, []byte(`{"v":1,"kind":"request","model":"org/a","at":1,"class":"ok"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "stats")
	if err := os.Symlink(victim, dir); err != nil {
		t.Fatal(err)
	}

	s := NewStore(dir, StoreOptions{})
	defer s.Close()
	if err := s.Clear(); err == nil {
		t.Error("Clear followed a symbolic link standing where the store's own directory should be")
	}
	if _, err := os.Stat(planted); err != nil {
		t.Errorf("Clear deleted a file in the directory the link pointed at: %v", err)
	}
	if _, err := s.Files(); err == nil {
		t.Error("Files listed a directory the writer would refuse")
	}
	if err := s.Latest(0, func(Line) bool { return true }); err == nil {
		t.Error("Latest read a directory the writer would refuse")
	}
}

// A request is never made to wait for a flush, either. Flush waits for the
// writer, so a flush that held the lock every record takes would turn one slow
// disk into a stall of every request goroutine.
func TestAFlushDoesNotHoldUpARecord(t *testing.T) {
	held := make(chan struct{})
	s, _ := newTestStore(t, StoreOptions{beforeWrite: func() { <-held }})
	on(t, s)
	if err := s.AppendRequest(Record{Model: "org/a", At: 1, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}

	flushing := make(chan struct{})
	go func() {
		close(flushing)
		s.Flush()
	}()
	<-flushing

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.AppendRequest(Record{Model: "org/a", At: 2, Class: ClassOK})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(held)
		t.Fatal("recording a request waited for a flush that was waiting for the disk")
	}
	close(held)
}

// Rotation starts a new file at the limit rather than one record past it, so
// "one file at a time, rotated by size" is what the files on disk actually
// show.
func TestNoFileGrowsPastTheRotationLimit(t *testing.T) {
	const limit = 512
	s, dir := newTestStore(t, StoreOptions{RotateBytes: limit, MaxBytes: 1 << 20})
	on(t, s)
	for i := range 100 {
		if err := s.AppendRequest(Record{Model: "org/a", At: int64(1788696030 + i), Class: ClassOK}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	for _, name := range names(t, dir) {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Size() > limit {
			t.Errorf("%s is %d bytes, past the %d-byte rotation limit", name, fi.Size(), limit)
		}
	}
}

// Switching off and clearing cannot interleave. A Clear that began while
// recording was on must not finish by reopening a store the operator switched
// off while it ran — which would create the directory, and leave it recording,
// with the switch off.
func TestASwitchOffCannotSlipInsideAClear(t *testing.T) {
	var s *FileStore
	inside := make(chan struct{}, 1)
	switched := make(chan struct{})
	opts := StoreOptions{}
	opts.duringClear = func() {
		select {
		case inside <- struct{}{}:
		default:
			return // only the first Clear
		}
		go func() {
			s.SetEnabled(false)
			close(switched)
		}()
		select {
		case <-switched:
			t.Error("recording was switched off in the middle of a Clear, which would leave the Clear to switch it back on")
		case <-time.After(50 * time.Millisecond):
		}
	}
	s, _ = newTestStore(t, opts)
	on(t, s)
	if err := s.AppendRequest(Record{Model: "org/a", At: 1, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	<-switched
	if s.Enabled() {
		t.Error("the store is recording after the switch went off")
	}
}

// And the same property under a hammer, as the race detector sees it.
func TestClearingAndSwitchingOffDoNotInterleave(t *testing.T) {
	s, dir := newTestStore(t, StoreOptions{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				s.SetEnabled(true)
				s.AppendRequest(Record{Model: "org/a", At: 1, Class: ClassOK})
				s.Clear()
				s.SetEnabled(false)
			}
		}()
	}
	wg.Wait()

	if s.Enabled() {
		t.Error("the store is recording after both goroutines switched it off")
	}
	// And with the switch off nothing may be added to what is on disk.
	before := snapshotDir(t, dir)
	s.AppendRequest(Record{Model: "org/a", At: 2, Class: ClassOK})
	if after := snapshotDir(t, dir); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Errorf("the store changed with the switch off:\n before %v\n after  %v", before, after)
	}
}

// Clear removes the records whether or not recording is on: a person who
// switches recording off and then decides the history should go too must not
// have to switch it back on to remove it.
func TestClearWorksWithTheSwitchOff(t *testing.T) {
	s, dir := newTestStore(t, StoreOptions{})
	on(t, s)
	if err := s.AppendRequest(Record{Model: "org/a", At: 1, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if len(names(t, dir)) == 0 {
		t.Fatal("nothing was recorded, so this test proves nothing")
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); len(got) != 0 {
		t.Errorf("the store still holds %v after Clear", got)
	}
	if s.Enabled() {
		t.Error("Clear switched recording back on")
	}
}

// The horizon is a promise about how far back the records reach, so it holds
// on a Mac that never writes enough to rotate a file: at a few dozen requests
// a day the first file takes years to fill, and "keep records for one month"
// would otherwise mean "keep them until something else happens".
func TestTheHorizonIsAppliedWithoutAnyRotation(t *testing.T) {
	now := time.Date(2028, 3, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	t.Run("when recording starts", func(t *testing.T) {
		s, dir := newTestStore(t, StoreOptions{Months: 1, Now: clock})
		on(t, s)
		if err := s.AppendRequest(Record{Model: "org/a", At: now.AddDate(-2, 0, 0).Unix(), Class: ClassOK}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetEnabled(false); err != nil {
			t.Fatal(err)
		}
		if len(names(t, dir)) == 0 {
			t.Fatal("nothing was written, so this test proves nothing")
		}
		on(t, s)
		if got := names(t, dir); len(got) != 0 {
			t.Errorf("a record two years past a one-month horizon is still held: %v", got)
		}
		if got := s.Status().Oldest; got != 0 {
			t.Errorf("the store still reports records from %s", time.Unix(got, 0).UTC().Format(time.DateOnly))
		}
	})

	t.Run("while it is running", func(t *testing.T) {
		s, dir := newTestStore(t, StoreOptions{Months: 1, Now: clock, PruneEvery: 5 * time.Millisecond})
		on(t, s)
		if err := s.AppendRequest(Record{Model: "org/a", At: now.AddDate(-2, 0, 0).Unix(), Class: ClassOK}); err != nil {
			t.Fatal(err)
		}
		if err := s.Flush(); err != nil {
			t.Fatal(err)
		}
		if len(names(t, dir)) == 0 {
			t.Fatal("nothing was written, so this test proves nothing")
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if len(names(t, dir)) == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("a record two years past a one-month horizon is still held: %v", names(t, dir))
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
}

// Lowering either limit takes effect when it is saved, not at some later
// moment the operator cannot see: the panel says the size limit always wins,
// and a store that stays 195 MB over it for weeks says otherwise.
func TestLoweringTheSizeCapPrunesAtOnce(t *testing.T) {
	s, dir := newTestStore(t, StoreOptions{MaxBytes: 1 << 20, RotateBytes: 512})
	on(t, s)
	for i := range 200 {
		if err := s.AppendRequest(Record{Model: "org/a", At: int64(1788696030 + i), Class: ClassOK}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(names(t, dir)) < 4 {
		t.Fatalf("the store holds %v, too few files for this test", names(t, dir))
	}

	const lowered = 2 << 10
	s.SetRetention(6, lowered)
	// A flush rides the same queue, so it answers only once the pruning the
	// setter asked for has been done.
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, name := range names(t, dir) {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		total += fi.Size()
	}
	if total > lowered {
		t.Errorf("the store holds %d bytes, over the %d-byte limit it was just given", total, lowered)
	}
	if total == 0 {
		t.Error("the whole store went; lowering the limit must keep what fits under it")
	}
}

// A link planted under a record file's own name is refused, not followed. The
// name is predictable, so this is the half of the file discipline O_NOFOLLOW
// exists for — and it must hold whatever the directory handle happens to do.
func TestALinkPlantedUnderARecordFileNameIsRefused(t *testing.T) {
	s, dir := newTestStore(t, StoreOptions{Now: func() time.Time {
		return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	}})
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("someone else's file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "stats-20260907-001.jsonl")); err != nil {
		t.Fatal(err)
	}

	on(t, s)
	if err := s.AppendRequest(Record{Model: "org/a", At: 1788696030, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "someone else's file" {
		t.Errorf("the record was written through the link: the file now reads %q", got)
	}
	fi, err := os.Lstat(filepath.Join(dir, "stats-20260907-001.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced by a real file, so something wrote through the name")
	}
	if files, err := s.Files(); err != nil {
		t.Fatal(err)
	} else if slices.Contains(files, "stats-20260907-001.jsonl") {
		t.Error("the store lists a link as one of its own files")
	}
	if n := s.Status().Dropped; n == 0 {
		t.Error("the record that could not be written was not counted as lost")
	}
}

// A disk that was full for a moment must not stop the store for good. A
// bufio.Writer keeps its first error forever, so without dropping the buffer
// one failed write would mean nothing is ever written again — however much
// room is freed afterwards — and the panel would go on saying nothing is
// wrong.
func TestAWriteThatFailedOnceRecoversWhenItCan(t *testing.T) {
	var logged bytes.Buffer
	s, dir := newTestStore(t, StoreOptions{Log: slog.New(slog.NewTextHandler(&logged, nil))})
	on(t, s)

	// A directory nothing can be created in, which is how a full disk looks
	// from here: every attempt to write a record fails.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if err := s.AppendRequest(Record{Model: "org/a", At: 1788696030, Class: ClassOK}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if n := s.Status().Dropped; n != 5 {
		t.Errorf("%d records were counted as lost, want the 5 that could not be written", n)
	}
	if n := strings.Count(logged.String(), "could not be written"); n != 1 {
		t.Errorf("the failure was logged %d times; a spell of failure is worth one line, not one per request", n)
	}

	// The room comes back, as it does when a disk is emptied.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendRequest(Record{Model: "org/b", At: 1788696040, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	got := read(t, s, 0)
	if len(got) != 1 || got[0].Request.Model != "org/b" {
		t.Errorf("the store holds %d records, want the one written after the disk came back", len(got))
	}
}

// In shared-cache mode the layout is created under the shared root, and this
// store is the one thing that stays under the account's own — which for an
// account that has never run Gropius per-user does not exist yet. It is
// created, owner-only, rather than the store refusing to open.
func TestTheAccountsOwnFolderIsCreatedWhenItIsNotThereYet(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "Library", "Application Support", "Gropius", "stats")
	s := NewStore(dir, StoreOptions{Months: 1200})
	t.Cleanup(func() { s.Close() })

	if err := s.SetEnabled(true); err != nil {
		t.Fatalf("the store refused to open under a folder that does not exist yet: %v", err)
	}
	if err := s.AppendRequest(Record{Model: "org/a", At: 1788696030, Class: ClassOK}); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, s, 0); len(got) != 1 {
		t.Errorf("the store holds %d records, want the one written", len(got))
	}
	for p := dir; p != base; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatalf("%s was not created: %v", p, err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is %04o, want it owner-only", p, fi.Mode().Perm())
		}
	}
}
