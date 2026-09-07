package stats

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"
)

// The summary written before deletion (itd-2609061602043757).
//
// The store is bounded on purpose, so its oldest records are dropped exactly
// as they become the most interesting thing in it: how use has changed. What
// this file adds is one line per model per day, folded out of the records that
// are about to go and kept long after them. It is coarse by construction —
// counts and totals, never a per-request figure — and it can hold nothing the
// detail did not, which is what keeps it under the same promise as the rest of
// the store.
//
// It is written from the records being dropped, never from the live view, and
// it is on disk before the detail is removed. A crash in the middle therefore
// leaves a summary that counts a file still present, which the index at the
// head of the file resolves: the next start removes the file the index names
// and nothing is counted twice.

const (
	// KindSummary is one model's day, folded out of records that have been
	// dropped.
	KindSummary = "summary"
	// KindSummaryIndex is the first line of the summary file: the detail files
	// already folded into it and not yet removed.
	KindSummaryIndex = "summary_index"
)

// summaryFileName is the store's summary. It is deliberately not a name
// storeFilePattern matches, so retention, rotation and the reader walk past it:
// it is not a file of records and is never the file chosen to be dropped.
const summaryFileName = "summary.jsonl"

// summaryTempPattern is what a half-written summary is called. The name is
// random and created exclusively, so a name planted in the directory cannot be
// written through.
const summaryTempPrefix = "summary-"
const summaryTempSuffix = ".tmp"

// summaryShare is the fraction of the store's size cap the summary may use: a
// twentieth, which at the 200 MB default is years of daily lines — three of
// them for ten models — without the summary crowding out the detail it exists
// to outlive. Over that, its oldest days go first.
const summaryShare = 20

// ApproxSummaryBytes is what one summary line measures at its widest, which is
// what the documentation's arithmetic about how many days a summary holds
// rests on. It is measured rather than guessed: the widest line this format can
// emit — all ten outcome classes and all seven removal reasons present, a
// 64-character repo id, and every counter run up into the millions and
// billions — marshals to 767 bytes, which
// TestASummaryLineIsTheSizeTheDocumentationSays builds and holds to this
// figure. It is a figure to reason from rather than a limit the code enforces:
// a repo id longer than 64 characters makes a longer line, and the arithmetic
// it feeds is about how many days a summary holds, not about what is allowed.
const ApproxSummaryBytes = 800

// SummaryShareOfCap is how much of the store's size limit the summary may use:
// a twentieth. It is exported so the documentation's arithmetic about how many
// days that holds can be held to this one figure rather than to someone's
// memory.
func SummaryShareOfCap(maxBytes int64) int64 { return maxBytes / summaryShare }

// Summary is one model's day, as counts and totals.
//
// Every field is a number or the repo id of a model this Mac holds, and every
// number is a count or a sum over a whole day. There is nothing here that
// describes one request, which is what makes a summary safe to keep for years
// after the records it was folded from are gone.
type Summary struct {
	// At is the start of the local day this line covers, in whole UTC seconds,
	// so a reader can order summaries without parsing Day.
	At int64 `json:"at"`
	// Day is the local day, as YYYY-MM-DD. Local rather than UTC because the
	// views bucket by the day the person using this Mac had, and a summary that
	// disagreed with the detail either side of midnight would be worse than
	// none.
	Day string `json:"day"`
	// TZOffsetMin is the offset from UTC in force on that day, in minutes, so a
	// day recorded in one zone can still be placed by a reader in another. A
	// day is keyed by its date alone, so a Mac that changed zone between two
	// drops for the same day keeps the offset the first of them recorded.
	TZOffsetMin int `json:"tz_offset_min"`
	// Model is the repo id that served the requests, empty for requests refused
	// before they resolved to one.
	Model string `json:"model"`
	// Requests is how many request records were folded into this line.
	Requests int64 `json:"requests"`
	// ByClass counts how those requests ended, by the same class names a record
	// carries. It sums to Requests.
	ByClass map[Class]int64 `json:"by_class,omitempty"`
	// PromptTokens and CompletionTokens are the day's totals.
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	// The latency totals, each the sum over the day of the record field of the
	// same name. A total and a count is what a mean is made of; a percentile
	// would need the records themselves, which is precisely what is gone.
	DurationMSTotal   int64 `json:"duration_ms_total"`
	QueueWaitMSTotal  int64 `json:"queue_wait_ms_total"`
	LoadWaitMSTotal   int64 `json:"load_wait_ms_total"`
	FirstTokenMSTotal int64 `json:"first_token_ms_total"`
	// FirstTokenRequests is how many of the requests produced a first token at
	// all, which is the divisor FirstTokenMSTotal is a mean over.
	FirstTokenRequests int64 `json:"first_token_requests"`
	// Loads and FailedLoads count the model server's comings; Removals counts
	// its goings, by the same reasons a removal line carries.
	Loads       int64            `json:"loads"`
	FailedLoads int64            `json:"failed_loads"`
	Removals    map[string]int64 `json:"removals,omitempty"`
}

// SummaryDay is one summary line and what it means for the day it covers.
//
// The summary always describes records that no longer exist, so a caller adds
// it to whatever detail is still held rather than reconciling the two. What it
// cannot say on its own is whether there is any such detail left, which is the
// difference between "this day is partly summarized" and "the detail for this
// day is gone" — the sentence a view has to be able to write.
type SummaryDay struct {
	Summary
	// DetailHeld says the store still holds records from this day, so this line
	// covers only the part of it that has been dropped. False means every
	// record of that day is gone and this line is all there is. It is computed
	// at read time from what the store still holds, not written to disk: a day
	// only partly dropped becomes wholly dropped later, and a field on disk
	// would say the wrong thing from the moment it was written.
	DetailHeld bool `json:"detail_held"`
}

// SummaryFields lists a summary line's fields under the names they are written
// down as, so the documentation's table is held to the code.
func SummaryFields() []string { return jsonFields(Summary{}) }

// summaryIndexFields lists the index line's own fields, which a person reading
// the file with their own tools sees first and is owed an explanation of.
func summaryIndexFields() []string {
	return append([]string{"folded"}, jsonFields(foldedFile{})...)
}

// StoreKinds names every kind of line the store can write, so the
// documentation's list of them is held to this one.
func StoreKinds() []string {
	return []string{KindRequest, KindLoad, KindRemoved, KindSettings, KindSummary, KindSummaryIndex}
}

// foldedFile is one detail file already counted in the summary. The size and
// the newest record are its fingerprint: a file that a later run created under
// the same name is a different file, and must not be removed as though it had
// been folded.
type foldedFile struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	Newest int64  `json:"newest"`
}

type (
	summaryLine struct {
		V    int    `json:"v"`
		Kind string `json:"kind"`
		Summary
	}
	summaryIndexLine struct {
		V      int          `json:"v"`
		Kind   string       `json:"kind"`
		Folded []foldedFile `json:"folded"`
	}
)

// summaryKey is what a summary line is one of: a local day and a model.
type summaryKey struct {
	day   string
	model string
}

// summarySet is the summary file, read into memory.
type summarySet struct {
	index []foldedFile
	days  map[summaryKey]*Summary
	// kept are the lines this build could not read: one a newer Gropius wrote,
	// one a crash tore. The fold is a read-modify-rename, so a line dropped on
	// the way in is a line deleted — and the format's own promise is that a
	// reader skips what it does not understand rather than destroying it. They
	// are written back exactly as they were found.
	kept [][]byte
}

// readSummarySet reads the summary file. A file that is not there is an empty
// set; a line that cannot be read is skipped, as everywhere else in the store.
func readSummarySet(root *os.Root) (*summarySet, error) {
	set, _, err := readSummarySetSized(root)
	return set, err
}

// readSummarySetSized is readSummarySet, also reporting how much file content
// it read, which is what a bounded reader has to account for.
func readSummarySetSized(root *os.Root) (*summarySet, int64, error) {
	set := &summarySet{days: map[summaryKey]*Summary{}}
	lines, err := readRawLines(root, summaryFileName)
	if err != nil {
		return nil, 0, err
	}
	read := int64(0)
	for _, b := range lines {
		read += int64(len(b)) + 1
	}
	for _, b := range lines {
		l, ok := parseLine(b)
		if !ok {
			if t := bytes.TrimSpace(b); len(t) > 0 {
				set.kept = append(set.kept, append([]byte(nil), t...))
			}
			continue
		}
		switch l.Kind {
		case KindSummary:
			s := l.Summary
			set.days[summaryKey{day: s.Day, model: s.Model}] = &s
		case KindSummaryIndex:
			var idx summaryIndexLine
			if err := json.Unmarshal(b, &idx); err == nil {
				set.index = idx.Folded
			}
		default:
			// A line this file has no use for but that reads as something: a
			// record kind that has no business here, or one a newer Gropius
			// puts here for a reason this build does not know. Kept, for the
			// same reason an unreadable line is.
			set.kept = append(set.kept, append([]byte(nil), bytes.TrimSpace(b)...))
		}
	}
	return set, read, nil
}

// dayOf returns the line for one model's local day, making it if it is not
// there yet.
func (set *summarySet) dayOf(loc *time.Location, at int64, model string) *Summary {
	t := time.Unix(at, 0).In(loc)
	y, m, d := t.Date()
	key := summaryKey{day: fmt.Sprintf("%04d-%02d-%02d", y, int(m), d), model: model}
	if s := set.days[key]; s != nil {
		return s
	}
	midnight := time.Date(y, m, d, 0, 0, 0, 0, loc)
	_, offset := midnight.Zone()
	s := &Summary{At: midnight.Unix(), Day: key.day, TZOffsetMin: offset / 60, Model: model}
	set.days[key] = s
	return s
}

// add folds one record into the day it belongs to.
func (set *summarySet) add(loc *time.Location, l Line) {
	switch l.Kind {
	case KindRequest:
		r := l.Request
		s := set.dayOf(loc, r.At, r.Model)
		s.Requests++
		if s.ByClass == nil {
			s.ByClass = map[Class]int64{}
		}
		s.ByClass[r.Class]++
		s.PromptTokens += int64(r.PromptTokens)
		s.CompletionTokens += int64(r.CompletionTokens)
		s.DurationMSTotal += r.DurationMS
		s.QueueWaitMSTotal += r.QueueWaitMS
		s.LoadWaitMSTotal += r.LoadWaitMS
		// A request that produced no streamed chunk carries NoFirstToken rather
		// than a duration, and adding that to a total would make the mean a
		// smaller number the slower the Mac got.
		if r.FirstTokenMS >= 0 {
			s.FirstTokenMSTotal += r.FirstTokenMS
			s.FirstTokenRequests++
		}
	case KindLoad, KindRemoved:
		e := l.Event
		s := set.dayOf(loc, e.At, e.Model)
		switch {
		case e.Kind == EventLoad && e.Failed:
			s.FailedLoads++
		case e.Kind == EventLoad:
			s.Loads++
		default:
			if s.Removals == nil {
				s.Removals = map[string]int64{}
			}
			s.Removals[e.Reason]++
		}
	}
	// A settings line is not folded: it carries no count, and the figures it
	// states are about a run rather than about a day.
}

// sorted returns the summary's lines, oldest day first and by model within a
// day, which is the order they are written down in.
func (set *summarySet) sorted() []*Summary {
	out := make([]*Summary, 0, len(set.days))
	for _, s := range set.days {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		return out[i].Model < out[j].Model
	})
	return out
}

// dropOldestDay removes every model's line for the oldest day held, and
// reports whether there was one to remove. It is how the summary stays within
// its share of the cap: it loses its past rather than its present, exactly as
// the detail does.
//
// The last day is never dropped. On a cap small enough that one day's lines and
// the index together exceed a twentieth of it, a summary that dropped its way
// to empty would be a file that says nothing while still taking room; the
// overshoot is one day's lines, which is smaller than a single record file.
func (set *summarySet) dropOldestDay() bool {
	oldest, newest := "", ""
	for k := range set.days {
		if oldest == "" || k.day < oldest {
			oldest = k.day
		}
		if k.day > newest {
			newest = k.day
		}
	}
	if oldest == "" || oldest == newest {
		return false
	}
	for k := range set.days {
		if k.day == oldest {
			delete(set.days, k)
		}
	}
	return true
}

// encode renders the whole summary file: the index first, then one line per
// model per day.
func (set *summarySet) encode() ([]byte, error) {
	var out []byte
	idx, err := json.Marshal(summaryIndexLine{V: SchemaVersion, Kind: KindSummaryIndex, Folded: set.index})
	if err != nil {
		return nil, err
	}
	out = append(append(out, idx...), '\n')
	for _, b := range set.kept {
		out = append(append(out, b...), '\n')
	}
	for _, s := range set.sorted() {
		b, err := json.Marshal(summaryLine{V: SchemaVersion, Kind: KindSummary, Summary: *s})
		if err != nil {
			return nil, err
		}
		out = append(append(out, b...), '\n')
	}
	return out, nil
}

// fold counts the records of the files about to be dropped, and returns only
// once the summary that counts them is on disk.
//
// The order is the whole of the crash safety: the summary is written, forced
// to the disk and renamed into place, and only then does the caller remove the
// detail. A crash before the rename leaves both untouched and the next pass
// folds the file again from scratch; a crash after it leaves a summary that
// counts a file still present, which the index it carries resolves at the next
// start.
func (w *storeWriter) fold(doomed []storeFile) error {
	set, err := readSummarySet(w.root)
	if err != nil {
		return err
	}
	// Forget the files the index names that are no longer on disk: they were
	// removed as intended, and the index is only ever the short list of folds
	// whose removal has not been seen through.
	folded := 0
	pending := make([]foldedFile, 0, len(set.index))
	for _, e := range set.index {
		if w.stillOnDisk(e) {
			pending = append(pending, e)
		}
	}
	for _, f := range doomed {
		fp, err := w.fingerprint(f.name)
		if errors.Is(err, fs.ErrNotExist) {
			// Gone from under the writer: deleted by hand, restored from a
			// backup, or taken by another process on the same directory. There
			// is nothing to count and nothing to lose, and the removal below
			// tolerates it too. Failing here would wedge retention, and a
			// rotation that cannot prune stops the store recording at all.
			continue
		}
		if err != nil {
			return err
		}
		if slicesContainsFolded(pending, fp) {
			// Already counted by a fold whose removal a crash interrupted.
			continue
		}
		lines, err := readRawLines(w.root, f.name)
		if err != nil {
			return err
		}
		for _, b := range lines {
			if l, ok := parseLine(b); ok {
				set.add(w.loc, l)
			}
		}
		folded++
		pending = append(pending, fp)
	}
	if len(pending) == len(set.index) && folded == 0 {
		// Nothing new counted and the index says the same thing: a rewrite
		// would only churn the file, and every rewrite is an fsync. This is the
		// ordinary case while a removal that failed is being retried. Lines
		// carried through are no reason to rewrite either — they are already in
		// the file exactly as they will be written back.
		return nil
	}
	set.index = pending
	return w.writeSummary(set)
}

// stillOnDisk reports whether the file an index entry names is the file that
// was folded, rather than a later one wearing its name.
func (w *storeWriter) stillOnDisk(e foldedFile) bool {
	fp, err := w.fingerprint(e.Name)
	return err == nil && fp == e
}

// fingerprint is what identifies a detail file: its name, its size and its
// newest record. A store that has been emptied can hand a new file the name of
// one that is gone, and removing that file as though it had been folded would
// lose records nothing had counted.
func (w *storeWriter) fingerprint(name string) (foldedFile, error) {
	// The store's own name pattern, here as everywhere else it removes
	// something. The index is read off a file, and a name read off a file is
	// never this code's licence to delete whatever it happens to say.
	if !storeFilePattern.MatchString(name) {
		return foldedFile{}, fmt.Errorf("%q is not a name this store writes", name)
	}
	fi, err := w.root.Stat(name)
	if err != nil {
		return foldedFile{}, err
	}
	return foldedFile{Name: name, Bytes: fi.Size(), Newest: w.newestRecordIn(name)}, nil
}

func slicesContainsFolded(list []foldedFile, want foldedFile) bool {
	for _, e := range list {
		if e == want {
			return true
		}
	}
	return false
}

// writeSummary writes the whole summary file and renames it into place, having
// first held it to its share of the store's cap.
func (w *storeWriter) writeSummary(set *summarySet) error {
	_, maxBytes := w.limits()
	share := maxBytes / summaryShare
	b, err := set.encode()
	if err != nil {
		return err
	}
	// Lines carried through are inside the bound like everything else. Keeping
	// what this build cannot read is a courtesy to the reader that wrote it,
	// not a promise worth a permanent tax on the records the operator asked to
	// keep: up to a whole read's worth of them could otherwise sit in the
	// summary forever, counted against the cap, starving the detail. They go
	// first, oldest first, because a day this build folded itself is worth more
	// than a line it cannot even parse.
	carried := 0
	for int64(len(b)) > share && len(set.kept) > 0 {
		set.kept = set.kept[1:]
		carried++
		if b, err = set.encode(); err != nil {
			return err
		}
	}
	if carried > 0 {
		w.opts.Log.Warn("lines in the request statistics summary that this build cannot read were dropped to keep it within its share of the size limit",
			"lines", carried, "share_bytes", share)
	}
	dropped := 0
	for int64(len(b)) > share && set.dropOldestDay() {
		dropped++
		if b, err = set.encode(); err != nil {
			return err
		}
	}
	if dropped > 0 {
		// The summary losing a day is the last thing that happens to a record
		// of it, so it is said out loud rather than done quietly.
		w.opts.Log.Info("the oldest days of the request statistics summary were dropped to keep it within its share of the size limit",
			"days", dropped, "share_bytes", share)
	}
	name, f, err := w.createSummaryTemp()
	if err != nil {
		return err
	}
	err = func() error {
		if _, err := f.Write(b); err != nil {
			return err
		}
		// The one place in this store that asks the disk to be sure. Everywhere
		// else the records are worth less than the answer they describe and a
		// forced write would put the disk in the path of every request; here the
		// write happens once per drop and is what makes "summarize, then delete"
		// mean anything at all.
		return f.Sync()
	}()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = w.root.Remove(name)
		return err
	}
	if err := w.root.Rename(name, summaryFileName); err != nil {
		_ = w.root.Remove(name)
		return err
	}
	// The directory entry itself, so the rename is as durable as the bytes it
	// published. Without it a power cut can leave the summary written and the
	// name still pointing at what it replaced, which is the one ordering this
	// design rests on. A directory that will not sync is not a reason to keep
	// the detail: the summary is on the disk either way.
	if d, err := w.root.Open("."); err == nil {
		_ = d.Sync()
		d.Close()
	}
	w.summaryBytes = int64(len(b))
	return nil
}

// createSummaryTemp makes the file the summary is written into before it is
// renamed over the one in place: a random name, created exclusively, owner
// only, following no link.
func (w *storeWriter) createSummaryTemp() (string, *os.File, error) {
	var last error
	for range 10 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", nil, err
		}
		name := summaryTempPrefix + hex.EncodeToString(b[:]) + summaryTempSuffix
		f, err := w.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0o600)
		if err == nil {
			return name, f, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", nil, err
		}
		last = err
	}
	return "", nil, last
}

// reconcileSummary is the startup pass that closes the window between the
// summary and the drop.
//
// Anything the index still names has been counted, so it goes; anything else
// wearing that name has not, so it stays. Either way the index is emptied,
// because after this pass there is no fold left half-done.
func (w *storeWriter) reconcileSummary() error {
	// A crash between making the temporary file and renaming it into place
	// leaves an orphan that nothing else names, so nothing else would ever
	// remove it and it would sit against a cap that does not count it. Nothing
	// can be writing one here: this runs under the lifecycle lock with no
	// writer goroutine.
	if err := removeSummaryTemps(w.root); err != nil {
		return err
	}
	set, err := readSummarySet(w.root)
	if err != nil {
		return err
	}
	if len(set.index) == 0 {
		return nil
	}
	for _, e := range set.index {
		if !w.stillOnDisk(e) {
			continue
		}
		if err := w.root.Remove(e.Name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	set.index = nil
	return w.writeSummary(set)
}

// summaryFileBytes is how much room the summary takes, which counts toward the
// store's cap: the cap is the room the records are allowed, and the summary is
// one of them.
func (w *storeWriter) summaryFileBytes() int64 {
	fi, err := w.root.Stat(summaryFileName)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// removeSummary takes the summary and any half-written one with it, which is
// what Clear means by every file the store wrote.
func removeSummary(root *os.Root) error {
	err := removeSummaryFiles(root, func(name string) bool { return name == summaryFileName })
	if e := removeSummaryTemps(root); err == nil {
		err = e
	}
	return err
}

// removeSummaryTemps removes what a crash between writing a summary and
// renaming it into place leaves behind.
func removeSummaryTemps(root *os.Root) error {
	return removeSummaryFiles(root, isSummaryTemp)
}

// isSummaryTemp reports whether a name is one createSummaryTemp makes.
func isSummaryTemp(name string) bool {
	return len(name) > len(summaryTempPrefix)+len(summaryTempSuffix) &&
		strings.HasPrefix(name, summaryTempPrefix) &&
		strings.HasSuffix(name, summaryTempSuffix)
}

// removeSummaryFiles removes the regular files in the store directory whose
// names the predicate accepts. Regular only, and by name only: the store
// removes what it wrote, never whatever a directory listing happens to return.
func removeSummaryFiles(root *os.Root, match func(string) bool) error {
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	ents, err := d.ReadDir(-1)
	d.Close()
	if err != nil {
		return err
	}
	var first error
	for _, e := range ents {
		if !e.Type().IsRegular() || !match(e.Name()) {
			continue
		}
		if err := root.Remove(e.Name()); err != nil && !errors.Is(err, fs.ErrNotExist) && first == nil {
			first = err
		}
	}
	return first
}

// SummaryOptions bounds one read of the summaries, the way ReadOptions bounds
// one read of the records.
//
// The bounds are on the reading and not only on what it yields, for the reason
// ReadOptions gives: a summary file this build cannot read a line of hands back
// nothing while costing every byte of itself, so a caller that bounded only the
// days it accepted would have bounded nothing at all.
type SummaryOptions struct {
	// Days is the most days handed to fn, newest first. A day is one line per
	// model that served on it, so this is days rather than lines: a bound on
	// lines would hand a Mac running ten models a tenth of the span it handed a
	// Mac running one. Zero or less means every day the other bounds allow.
	Days int
	// MaxBytes is the most file content read — the summary itself, and the
	// record files walked to find the oldest record still held. Zero or less
	// means the store's own ceiling.
	MaxBytes int64
}

// SummaryStats is what one read of the summaries cost and what it met, the way
// ReadStats is for a read of the records.
type SummaryStats struct {
	// Days and Lines are how many days and how many lines were handed to fn.
	Days  int
	Lines int
	// Skipped counts lines of the summary that could not be used: one a newer
	// Gropius wrote, one a crash tore. They are kept on disk rather than
	// dropped, so a reader meets them on every read.
	Skipped int64
	// Bytes is how much file content was read.
	Bytes int64
	// Bounded reports a read a bound stopped rather than one that reached the
	// end; BoundedBy names which — "days" or "bytes" — so a caller telling a
	// reader the list was cut short can name the figure that did it.
	Bounded   bool
	BoundedBy string
}

// Summaries reads the store's summaries, newest day first, calling fn with each
// until it returns false or a bound is reached.
//
// It is the summary side of Read, and it is bounded and cancellable for the
// same reasons: this runs on a control-plane request, and a caller who has gone
// away must be let go of rather than paid for.
//
// This is how a view shows a month whose detail is gone: every day that comes
// back from here is a day whose records were dropped, so a caller can render
// the totals and say that the detail for that day is no longer held. Where a
// day appears here and in Read both, the detail is what is still held and the
// summary is what is not — the fold never subtracts, so the two are added
// rather than reconciled.
func (s *FileStore) Summaries(ctx context.Context, opts SummaryOptions, fn func(SummaryDay) bool) (SummaryStats, error) {
	var got SummaryStats
	if s == nil {
		return got, nil
	}
	// The same flush a read of the records does, so a caller that reads both
	// sees one store rather than two moments of it.
	if err := s.Flush(); err != nil {
		return got, err
	}
	root, err := openStoreRoot(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return got, nil
		}
		return got, err
	}
	defer root.Close()
	budget := opts.MaxBytes
	if budget <= 0 {
		budget = maxLatestBytes
	}
	if err := ctxErr(ctx); err != nil {
		return got, err
	}
	set, size, err := readSummarySetSized(root)
	if err != nil {
		return got, err
	}
	got.Bytes, got.Skipped = size, int64(len(set.kept))
	// Reported the same way a read of the records reports it, so a great many
	// unreadable lines cannot pass for a quiet store whichever side is read.
	defer func() {
		s.statusMu.Lock()
		s.status.Skipped = got.Skipped
		s.statusMu.Unlock()
	}()

	// The oldest record still held, which is what tells a summarized day from
	// one that is only partly summarized. The walk is bounded like everything
	// else: a store of many empty files must not turn a bounded read into an
	// unbounded one, and a walk that runs out of budget simply cannot say
	// whether any detail is held.
	oldest := int64(0)
	if files, err := listStoreFiles(root); err == nil {
		for _, f := range files {
			if got.Bytes >= budget {
				got.Bounded, got.BoundedBy = true, "bytes"
				break
			}
			if err := ctxErr(ctx); err != nil {
				return got, err
			}
			got.Bytes += f.size
			if at := firstRecordIn(root, f.name); at != 0 {
				oldest = at
				break
			}
		}
	}

	all := set.sorted()
	last := ""
	for i := len(all) - 1; i >= 0; i-- {
		if err := ctxErr(ctx); err != nil {
			return got, err
		}
		if all[i].Day != last {
			if opts.Days > 0 && got.Days >= opts.Days {
				got.Bounded, got.BoundedBy = true, "days"
				return got, nil
			}
			last = all[i].Day
			got.Days++
		}
		if !fn(SummaryDay{Summary: *all[i], DetailHeld: detailHeld(oldest, all[i], s.opts.loc)}) {
			return got, nil
		}
		got.Lines++
	}
	return got, nil
}

// ctxErr is the context check the readers share: nothing to do when there is no
// context, which is what a test and a one-off pass.
func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// detailHeld reports whether the store still holds records from a summarized
// day, which is the difference between "this day is partly summarized" and
// "the detail for this day is gone".
//
// The day ends at the next local midnight, computed rather than assumed: on the
// day a zone falls back, a day is twenty-five hours long, and a fixed 86,400
// would put the last hour of it on the wrong side of the line.
func detailHeld(oldest int64, s *Summary, loc *time.Location) bool {
	if oldest == 0 {
		return false
	}
	if loc == nil {
		loc = time.Local
	}
	start := time.Unix(s.At, 0).In(loc)
	y, m, d := start.Date()
	end := time.Date(y, m, d+1, 0, 0, 0, 0, loc).Unix()
	return oldest < end
}
