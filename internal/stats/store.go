package stats

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The durable store: the records the recorder makes, kept on this Mac so that
// months of local model use can be looked at later (itd-2609061521102742).
//
// The format is fixed by adr-2609061610107154 and is deliberately the dullest
// one there is: append-only JSON Lines, one object per line, one file at a
// time, rotated by size, every line carrying the schema version it was written
// under. No database, no dependency, and any tool a person already has can
// read it.
//
// Everything the switch promises holds here too. Nothing is written while
// recording is off — not a file, not a directory. Nothing written is a prompt,
// an answer, a key or a client's address: the store is handed the recorder's
// own records, which cannot carry any of those.

// ApproxRecordBytes is what one line of the store measures, near enough for
// arithmetic about how long a size cap lasts: BenchmarkLatestAtTheCap fills
// the default cap and reports 206,342,511 bytes for 901,059 records, which is
// 229. It is a measured figure rather than a guess because the settings a
// person chooses rest on it — and the guess it replaces, 150 bytes, is not
// reachable: the field names are on every line, so the smallest request line
// this format can emit is already over 190 bytes.
const ApproxRecordBytes = 230

// SchemaVersion is stamped on every line as "v". It is bumped only for a
// change a reader of an older file could not survive; a new field is not one,
// because readers ignore what they do not know.
const SchemaVersion = 1

// The kinds of record the store holds, which are the three
// adr-2609061610107154 names — a request, a model server's coming and going,
// and the settings in force.
const (
	KindRequest  = "request"
	KindLoad     = "load"
	KindRemoved  = "removed"
	KindSettings = "settings"
)

// Settings is what Gropius was actually serving under when it was written: the
// effective values, not the saved ones. Decode concurrency and the idle
// timeout take a restart, so a value saved and not yet in force must never be
// recorded as though it were — a reader comparing "before and after I raised
// the budget" would otherwise draw the line in the wrong place.
type Settings struct {
	At int64 `json:"at"`
	// BudgetBytes is the memory budget model servers are held to.
	BudgetBytes       int64 `json:"budget_bytes"`
	DecodeConcurrency int   `json:"decode_concurrency"`
	IdleTimeoutSec    int   `json:"idle_timeout_sec"`
	// Months and MaxBytes are the store's own retention figures, so a reader
	// can tell a gap in the records from a gap in the traffic.
	Months   int   `json:"stats_months"`
	MaxBytes int64 `json:"stats_max_bytes"`
}

// sameAs reports whether two settings records say the same thing, ignoring
// when they were written.
func (s Settings) sameAs(o Settings) bool {
	s.At, o.At = 0, 0
	return s == o
}

// Line is one record read back out of the store. Exactly one of Request,
// Event and Settings is filled, according to Kind; the others are zero.
//
// V is the version the line was written under, which may be newer than this
// build's: a field this build does not know is ignored rather than refused,
// which is what makes an older Gropius able to read a newer file at all.
type Line struct {
	V    int
	Kind string
	// At is the record's own UTC timestamp, in whole seconds, whichever kind
	// it is — so a reader can order a file without switching on the kind.
	At       int64
	Request  Record
	Event    Event
	Settings Settings
	Summary  Summary
}

// The wire shapes. Each embeds the record it carries, so the fields on disk
// are the recorder's own fields under the recorder's own names: one vocabulary
// across the panel, the API and the store, and no translation table to keep
// correct. "v" and "kind" sit at the front as the envelope.
type (
	requestLine struct {
		V    int    `json:"v"`
		Kind string `json:"kind"`
		Record
	}
	eventLine struct {
		V    int    `json:"v"`
		Kind string `json:"kind"`
		Event
	}
	settingsLine struct {
		V    int    `json:"v"`
		Kind string `json:"kind"`
		Settings
	}
)

// StoreFields names every field the store can write, so the documentation's
// list of them can be held to the code rather than to someone's memory.
func StoreFields() []string {
	out := []string{"v", "kind"}
	seen := map[string]bool{"v": true, "kind": true}
	for _, fields := range [][]string{jsonFields(Record{}), jsonFields(Event{}), jsonFields(Settings{}),
		SummaryFields(), summaryIndexFields()} {
		for _, f := range fields {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// StoreOptions configures a FileStore. The zero value is usable: the defaults
// below are the ADR's starting figures.
type StoreOptions struct {
	// Months is the horizon: a file whose newest record is older than this is
	// removed even when there is room for it.
	Months int
	// MaxBytes is the hard bound. While the store is over it, its oldest file
	// goes, whatever the horizon says.
	MaxBytes int64
	// RotateBytes is how large one file grows before the next is started.
	RotateBytes int64
	// FlushEvery is how often the writer flushes what it has buffered, which
	// is therefore how much a crash can cost.
	FlushEvery time.Duration
	// PruneEvery is how often retention is applied to a store that is simply
	// sitting there. Both bounds are also applied when a file rotates, when
	// recording starts and when either figure is changed; this is what makes
	// the horizon hold on a Mac quiet enough that none of those happen.
	PruneEvery time.Duration
	// QueueSize is how many records may be waiting to be written before
	// further ones are dropped rather than made to wait.
	QueueSize int
	Now       func() time.Time
	Log       *slog.Logger

	// beforeWrite is a test seam: it runs on the writer's own goroutine before
	// each write, which is the only way to hold the writer still and see what
	// a request does when the queue fills.
	beforeWrite func()
	// duringClear is a test seam too: it runs inside Clear, after the writer
	// has stopped and before it is started again, which is the window in which
	// a switch-off must not be able to slip past.
	duringClear func()
	// afterSummary is a test seam: it runs after the summary of the files
	// about to be dropped is on disk and before they are removed, which is the
	// one window a crash can leave a fold half-done in. An error from it stands
	// for the crash.
	afterSummary func() error

	// loc is the zone whose days the summary buckets by. It is this Mac's own
	// zone everywhere but in a test, which needs to say which day a record
	// falls on without asking where this Mac is.
	loc *time.Location
}

// Defaults for a store built without them.
const (
	defaultRotateBytes = 5 << 20
	defaultFlushEvery  = 2 * time.Second
	defaultPruneEvery  = time.Hour
	defaultQueueSize   = 1024
	defaultMonths      = 6
	defaultMaxBytes    = 200 << 20
)

// storeFilePattern is the only name the store will ever create, read or
// remove: "stats-YYYYMMDD-NNN.jsonl", the date in UTC and a counter within the
// day. Rotation and Clear match against it rather than acting on whatever a
// directory listing returns, so a file that is not the store's own is never
// this code's to delete.
var storeFilePattern = regexp.MustCompile(`^stats-(\d{8})-(\d{3,})\.jsonl$`)

// StoreStatus is what the panel says about the store beside the two retention
// figures: how far back it reaches, how much room it is using, and whether
// anything was lost.
type StoreStatus struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
	// Oldest is the oldest record still held, in whole UTC seconds, and zero
	// when the store holds nothing.
	Oldest int64 `json:"oldest"`
	// Dropped counts records the writer could not keep up with. It is shown
	// rather than logged: a figure that is quietly missing is worse than one
	// that says it is missing.
	Dropped int64 `json:"dropped"`
	// Skipped counts lines the last read through the store could not use: a
	// line torn by a crash, one a newer Gropius wrote, one a full disk cut in
	// half. One is the ordinary cost of a crash; a great many mean an
	// aggregate drawn over a fraction of the records, and the same reasoning
	// applies as to Dropped — a figure that is quietly missing is worse than
	// one that says it is missing.
	Skipped int64 `json:"skipped"`
	// RetentionWedged reports that the store cannot summarize what it is about
	// to drop. The reason is in the log. It is the honest thing to show: the
	// alternative to saying so is a store quietly over the size the operator
	// set.
	RetentionWedged bool `json:"retention_wedged"`
	// Unsummarized counts records removed without being counted into the
	// summary first. It is what a wedged fold eventually costs: the size limit
	// is the hard bound (adr-2609061610107154), so a store that cannot
	// summarize what it drops still drops it rather than growing without end.
	// A file too damaged to read contributes only the records that could still
	// be read out of it; the log names the file and its size.
	Unsummarized int64 `json:"unsummarized"`
	// Refused reports a store that could not be opened where it must live, so
	// the figures are being kept in memory and nothing is on disk. The reason
	// is in the log; it names the directory, which is not the panel's to
	// publish.
	Refused bool `json:"refused"`
}

// FileStore writes the recorder's records to disk and reads them back.
//
// The write path is one goroutine that owns the open file, fed by a buffered
// channel. A request goroutine therefore never waits on a disk: it marshals
// its record, offers it to the channel, and moves on. A channel that is full
// means the writer has fallen behind, and the record is counted and dropped
// rather than allowed to hold up an answer.
type FileStore struct {
	dir  string
	opts StoreOptions
	now  func() time.Time
	log  *slog.Logger

	// lifeMu serializes the whole of switching on, switching off and clearing
	// against each other. Without it a Clear that began while recording was on
	// could finish by reopening a store the operator had switched off in the
	// meantime — and creating its directory in the act.
	lifeMu sync.Mutex

	// mu guards the lifecycle's own state: whether the store is on, and the
	// channels the writer is reading. Append holds it only for a non-blocking
	// send, and no path holds it while waiting for the writer.
	mu      sync.Mutex
	enabled bool
	ch      chan storeEntry
	quit    chan struct{}
	done    chan struct{}
	// lastSettings is the settings record last written, so that restarts and
	// saves that change nothing do not fill the store with a record of
	// nothing. pending says it has to be written again before the next record,
	// which is how an emptied store gets its baseline back without a file
	// appearing in a directory Clear has just emptied.
	lastSettings    Settings
	haveSettings    bool
	pendingSettings bool

	// statusMu guards the figures the panel reads and the two retention bounds
	// the writer applies. It is its own lock because the writer touches both
	// while a Flush may be holding mu and waiting for that same writer, and a
	// writer that had to take mu would deadlock against it.
	statusMu sync.Mutex
	status   StoreStatus
	months   int
	maxBytes int64
}

// NewStore returns a store for dir. It touches no filesystem: a store that is
// never switched on never creates its directory, which is what makes "nothing
// is written while recording is off" a property of the code rather than a
// promise.
func NewStore(dir string, opts StoreOptions) *FileStore {
	if opts.Months <= 0 {
		opts.Months = defaultMonths
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultMaxBytes
	}
	if opts.RotateBytes <= 0 {
		opts.RotateBytes = defaultRotateBytes
	}
	if opts.FlushEvery <= 0 {
		opts.FlushEvery = defaultFlushEvery
	}
	if opts.PruneEvery <= 0 {
		opts.PruneEvery = defaultPruneEvery
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = defaultQueueSize
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.loc == nil {
		opts.loc = time.Local
	}
	return &FileStore{dir: dir, opts: opts, now: opts.Now, log: opts.Log,
		months: opts.Months, maxBytes: opts.MaxBytes}
}

// SetRetention changes the two bounds and applies them at once.
//
// At once, because a figure the operator has just lowered is a figure they
// expect to see honoured: a store that stayed months over a limit until
// something else happened to it would make the panel's own words false. The
// pruning itself runs on the writer's goroutine, off the request path.
func (s *FileStore) SetRetention(months int, maxBytes int64) {
	if s == nil {
		return
	}
	if months <= 0 {
		months = defaultMonths
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	s.statusMu.Lock()
	changed := months != s.months || maxBytes != s.maxBytes
	s.months, s.maxBytes = months, maxBytes
	s.statusMu.Unlock()
	if !changed {
		return
	}
	// Applied now, on the writer's own goroutine. A queue too full to carry
	// the request is a busy store, which is a store about to rotate and prune
	// anyway, and the periodic pass would catch it in any case.
	s.mu.Lock()
	if s.enabled {
		select {
		case s.ch <- storeEntry{prune: true}:
		default:
		}
	}
	s.mu.Unlock()
}

// limits is what the writer prunes against.
func (s *FileStore) limits() (int, int64) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	return s.months, s.maxBytes
}

// Enabled reports whether the store is writing.
func (s *FileStore) Enabled() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

// SetEnabled turns the store on or off.
//
// Turning it on is the only moment a directory is created or a file opened,
// and it is where the refusals live: a directory that this account does not
// have to itself is not a place for a record of what it served, so the store
// stays in memory and says so once. Turning it off flushes what is buffered
// and closes the file; it never removes anything, because a person switching
// recording off is asking for it to stop, not for their history to be
// destroyed. Clear is what destroys it.
func (s *FileStore) SetEnabled(on bool) error {
	if s == nil {
		return nil
	}
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	return s.setEnabled(on)
}

// setEnabled is SetEnabled without the lifecycle lock, for the paths that
// already hold it. Callers must hold s.lifeMu.
func (s *FileStore) setEnabled(on bool) error {
	if !on {
		s.stop()
		return nil
	}
	s.mu.Lock()
	already := s.enabled
	s.mu.Unlock()
	if already {
		return nil
	}
	// Outside s.mu: opening reads the directory and can prune, which scans
	// files, and every request goroutine takes s.mu to offer a record.
	// s.lifeMu is what makes that safe — nothing else can be switching the
	// store on or off while this runs.
	w, err := s.openWriter()
	if err != nil {
		s.log.Warn("request statistics stay in memory: the store cannot be opened", "dir", s.dir, "err", err)
		// Said on the panel as well as in the log. A refused store that
		// reported itself as an empty one would have the operator believing
		// records were accumulating for as long as it took them to look for
		// them.
		s.statusMu.Lock()
		s.status = StoreStatus{Refused: true}
		s.statusMu.Unlock()
		return err
	}
	s.mu.Lock()
	s.enabled = true
	s.ch = make(chan storeEntry, s.opts.QueueSize)
	s.quit = make(chan struct{})
	s.done = make(chan struct{})
	go s.run(s.ch, s.quit, s.done, w)
	s.mu.Unlock()
	s.statusMu.Lock()
	s.status.Refused = false
	s.statusMu.Unlock()
	// What opening found, including whatever retention removed on the way in:
	// the panel must not go on showing figures from before the store was
	// pruned.
	s.publish(w)
	return nil
}

// stop shuts the writer down and waits for it to finish, so that a caller that
// switches recording off and then looks at the directory sees a finished file.
func (s *FileStore) stop() {
	s.mu.Lock()
	if !s.enabled {
		s.mu.Unlock()
		return
	}
	s.enabled = false
	quit, done := s.quit, s.done
	s.ch, s.quit, s.done = nil, nil, nil
	s.haveSettings, s.pendingSettings = false, false
	// The channel itself is never closed. A Flush waits for the writer with
	// s.mu released, so a close here would be a send on a closed channel;
	// closing quit tells the writer to drain what is queued and stop.
	close(quit)
	s.mu.Unlock()
	<-done
}

// Close stops the store. It is safe on a store that was never on.
func (s *FileStore) Close() error {
	if s == nil {
		return nil
	}
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	s.stop()
	return nil
}

// AppendRequest, AppendEvent and AppendSettings are the three kinds of record
// adr-2609061610107154 names. Each returns without touching a disk when the
// store is off.
func (s *FileStore) AppendRequest(r Record) error {
	if s == nil {
		return nil
	}
	b, err := json.Marshal(requestLine{V: SchemaVersion, Kind: KindRequest, Record: r})
	if err != nil {
		return err
	}
	s.offer(b)
	return nil
}

func (s *FileStore) AppendEvent(e Event) error {
	if s == nil {
		return nil
	}
	kind := KindRemoved
	if e.Kind == EventLoad {
		kind = KindLoad
	}
	b, err := json.Marshal(eventLine{V: SchemaVersion, Kind: kind, Event: e})
	if err != nil {
		return err
	}
	s.offer(b)
	return nil
}

// AppendSettings records the settings in force. It writes nothing when they
// say the same as the last record written, so a save that changed something
// else, or a restart that changed nothing, does not fill the store with a
// record of nothing.
func (s *FileStore) AppendSettings(set Settings) error {
	if s == nil {
		return nil
	}
	b, err := json.Marshal(settingsLine{V: SchemaVersion, Kind: KindSettings, Settings: set})
	if err != nil {
		return err
	}
	s.mu.Lock()
	if !s.enabled {
		s.mu.Unlock()
		return nil
	}
	if s.haveSettings && !s.pendingSettings && s.lastSettings.sameAs(set) {
		s.mu.Unlock()
		return nil
	}
	s.lastSettings, s.haveSettings, s.pendingSettings = set, true, false
	s.sendLocked(b)
	s.mu.Unlock()
	return nil
}

// offer hands a marshalled line to the writer, or drops it.
func (s *FileStore) offer(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled {
		return
	}
	// A store that was emptied writes the settings in force again before the
	// first record that follows, so that a record is never left on disk with
	// nothing to read it against. Not at the moment of clearing: Clear leaves
	// the directory empty, and a file that appeared as it was pressed would be
	// a file the operator did not ask for.
	if s.pendingSettings && s.haveSettings {
		s.pendingSettings = false
		set := s.lastSettings
		set.At = s.now().UTC().Unix()
		if sb, err := json.Marshal(settingsLine{V: SchemaVersion, Kind: KindSettings, Settings: set}); err == nil {
			s.lastSettings = set
			s.sendLocked(sb)
		}
	}
	s.sendLocked(b)
}

// sendLocked offers a line to the writer without ever waiting for it. Callers
// must hold s.mu, which is what makes the send safe against the close in stop.
func (s *FileStore) sendLocked(b []byte) {
	select {
	case s.ch <- storeEntry{line: b}:
	default:
		s.statusMu.Lock()
		s.status.Dropped++
		s.statusMu.Unlock()
	}
}

// Flush writes out everything buffered and waits for it, so a reader sees what
// has been recorded rather than what happens to have reached the disk.
func (s *FileStore) Flush() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	ch, done := s.ch, s.done
	on := s.enabled
	s.mu.Unlock()
	if !on {
		return nil
	}
	// Through the writer's own queue rather than past it: everything already
	// offered has to reach the file before the flush answers, or a reader that
	// flushed first would still miss the record it had just made.
	//
	// And with s.mu released, because this waits for the writer: holding the
	// lock every record takes would turn one slow disk into a stall of every
	// request goroutine. The writer stopping underneath is not an error — the
	// records were flushed as it closed.
	ack := make(chan error, 1)
	select {
	case ch <- storeEntry{ack: ack}:
	case <-done:
		return nil
	}
	select {
	case err := <-ack:
		return err
	case <-done:
		return nil
	}
}

// Status is what the panel shows beside the retention figures.
func (s *FileStore) Status() StoreStatus {
	if s == nil {
		return StoreStatus{}
	}
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	return s.status
}

// Clear removes every file the store wrote and nothing else, and leaves it
// recording. It is the only thing that removes a record: switching the switch
// off stops new records and leaves the old ones where they are.
func (s *FileStore) Clear() error {
	if s == nil {
		return nil
	}
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()

	// The settings in force are carried across the stop, so that the emptied
	// store can state them again before the first record that follows. Without
	// that, every record made between a Clear and the next restart would sit
	// in a file with nothing to read it against.
	s.mu.Lock()
	was, last, have := s.enabled, s.lastSettings, s.haveSettings
	s.mu.Unlock()
	s.stop()
	if was {
		s.mu.Lock()
		s.lastSettings, s.haveSettings, s.pendingSettings = last, have, have
		s.mu.Unlock()
	}

	if s.opts.duringClear != nil {
		s.opts.duringClear()
	}

	root, err := openStoreRoot(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			err = nil
		}
		if was {
			if e := s.setEnabled(true); err == nil {
				err = e
			}
		}
		return err
	}
	files, listErr := listStoreFiles(root)
	// The summary of what was already dropped goes with the detail: it is one
	// of the store's own files, and a Clear that left it would leave a record
	// of the traffic behind.
	if err := removeSummary(root); err != nil && listErr == nil {
		listErr = err
	}
	for _, f := range files {
		// A file that is already gone is a file this was asked to remove: two
		// clears at once must not make the second one report a failure.
		if e := root.Remove(f.name); e != nil && !errors.Is(e, fs.ErrNotExist) && listErr == nil {
			listErr = e
		}
	}
	root.Close()

	s.statusMu.Lock()
	// Every figure, the losses included: they described records that are no
	// longer there, and a panel that still reported them after a Clear would
	// be describing nothing.
	s.status = StoreStatus{}
	s.statusMu.Unlock()

	if was {
		if e := s.setEnabled(true); listErr == nil {
			listErr = e
		}
	}
	return listErr
}

// storeEntry is one thing for the writer to do: a line to append, or a flush
// to answer. Both ride the same queue so that they happen in the order they
// were asked for.
type storeEntry struct {
	line []byte
	ack  chan error
	// prune asks for retention to be applied now, which is what a lowered
	// limit means: the operator typed a smaller number and expects the store
	// to be smaller, not to become smaller the next time something else
	// happens.
	prune bool
}

// run is the writer. It owns the open file for as long as the store is on, and
// nothing else ever writes to it.
func (s *FileStore) run(ch chan storeEntry, quit chan struct{}, done chan struct{}, w *storeWriter) {
	defer close(done)
	t := time.NewTicker(w.opts.FlushEvery)
	defer t.Stop()
	p := time.NewTicker(w.opts.PruneEvery)
	defer p.Stop()
	for {
		select {
		case e := <-ch:
			s.apply(w, e)
		case <-t.C:
			if err := w.flush(); err != nil {
				s.writeFailed(w, err)
			}
			s.publish(w)
		case <-p.C:
			// Retention on a store nothing is happening to. Without this the
			// horizon would only ever be applied when a file rotated, so a Mac
			// serving a few dozen requests a day would keep records for years
			// against a figure that says months.
			if err := s.retain(w); err != nil {
				s.log.Warn("the statistics store could not apply its retention limits", "err", err)
			}
			s.publish(w)
		case <-quit:
			// Everything already queued is written before the file is closed:
			// switching recording off must not lose the records of the last
			// few seconds it was on.
			for {
				select {
				case e := <-ch:
					s.apply(w, e)
				default:
					if err := w.close(); err != nil {
						s.log.Warn("the statistics store could not be closed cleanly", "err", err)
					}
					s.publish(w)
					return
				}
			}
		}
	}
}

// apply does one thing the writer was asked for: append a line, or answer a
// flush.
func (s *FileStore) apply(w *storeWriter, e storeEntry) {
	if e.prune {
		if err := s.retain(w); err != nil {
			s.log.Warn("the statistics store could not apply its retention limits", "err", err)
		}
		s.publish(w)
		return
	}
	if e.ack != nil {
		err := w.flush()
		s.publish(w)
		e.ack <- err
		return
	}
	if w.opts.beforeWrite != nil {
		w.opts.beforeWrite()
	}
	if err := w.write(e.line); err != nil {
		s.writeFailed(w, err)
	} else {
		w.failing = false
	}
	s.publish(w)
}

// writeFailed is what a store that cannot be written costs: the record, and
// nothing else.
//
// The record is counted as lost, because a figure that is quietly missing is
// worse than one that says it is missing. The open file is dropped without
// being flushed — a bufio.Writer keeps its first error forever, so a single
// full disk would otherwise mean nothing is ever written again, however much
// room is freed afterwards; dropping it makes the next record open the file
// again. And it is logged once for the spell rather than once per request,
// which is the reason the recorder gives for logging nothing at all: a line
// per failed write would be a second unbounded record of the traffic the first
// one is meant to bound.
func (s *FileStore) writeFailed(w *storeWriter, err error) {
	s.statusMu.Lock()
	s.status.Dropped++
	s.statusMu.Unlock()
	if !w.failing {
		w.failing = true
		s.log.Warn("a request statistics record could not be written; the figures stay in memory", "err", err)
	}
	w.discardFile()
}

// retain applies both bounds to a store that is sitting still, flushing first
// so that what is on disk is what the horizon is judged against.
func (s *FileStore) retain(w *storeWriter) error {
	if err := w.flush(); err != nil {
		return err
	}
	return w.prune()
}

// publish copies the writer's own view of the store into the figures the panel
// reads.
func (s *FileStore) publish(w *storeWriter) {
	files, bytes, oldest := w.figures()
	s.statusMu.Lock()
	s.status.Files, s.status.Bytes, s.status.Oldest = files, bytes, oldest
	s.status.RetentionWedged = w.wedged
	s.status.Unsummarized = w.unsummarized
	s.status.Refused = false
	s.statusMu.Unlock()
}

// storeFile is one file in the store.
type storeFile struct {
	name string
	day  int // YYYYMMDD, from the name
	n    int // the counter within the day, from the name
	size int64
	// folded says this file's records are already counted in the summary and
	// its removal did not go through. It must never be appended to again: the
	// next fold would count what was added to it a second time, and the fold
	// skips it only for as long as it is byte for byte the file that was
	// counted.
	folded bool
}

// storeWriter owns the store's directory handle and its open file.
type storeWriter struct {
	root *os.Root
	opts StoreOptions
	now  func() time.Time
	loc  *time.Location

	// limits reads the retention bounds, which the operator can change while
	// the writer is running.
	limits func() (int, int64)

	files []storeFile // oldest first, including the open one
	// day and n are the last file name used, so a counter never goes backwards
	// within a run: retention can empty the directory, and a new file taking a
	// removed file's name would make two different files share one name in a
	// reader's notes.
	day int
	n   int
	f   *os.File
	buf *bufio.Writer
	// failing says the last write or flush did not work, so that a spell of
	// failure costs one line in the log rather than one per request.
	failing bool
	cur     int64 // bytes in the open file, buffered ones included
	oldest  int64 // the oldest record still held, or 0
	// summaryBytes is the room the summary of what has already been dropped
	// takes, which counts toward the cap along with the detail.
	summaryBytes int64
	// wedged says retention cannot run because what it would drop cannot be
	// summarized first. wedgedLogged keeps a spell of that to one line in the
	// log rather than one per attempt.
	wedged       bool
	wedgedLogged bool
	// unsummarized counts the records dropped without a summary, which is what
	// holding the size limit through a wedged fold costs.
	unsummarized int64
}

// openWriter checks the store's directory, creates it if it is not there, and
// opens the file to append to.
func (s *FileStore) openWriter() (*storeWriter, error) {
	if err := ensureStoreDir(s.dir); err != nil {
		return nil, err
	}
	root, err := openStoreRoot(s.dir)
	if err != nil {
		return nil, err
	}
	w := &storeWriter{root: root, opts: s.opts, now: s.now, loc: s.opts.loc, limits: s.limits}
	// Before anything is listed: a fold whose removal a crash interrupted is
	// finished here, so the file it already counted cannot be counted again and
	// cannot be listed as though it were still held.
	if err := w.reconcileSummary(); err != nil {
		// The same answer as at any other moment: a fold that cannot be done
		// costs retention and nothing else. Refusing the store here would make
		// a file planted under one name the difference between recording and
		// not, and the panel would say nothing is on disk when the records are
		// perfectly safe.
		w.retentionWedged(err)
	}
	w.summaryBytes = w.summaryFileBytes()
	if w.files, err = listStoreFiles(root); err != nil {
		root.Close()
		return nil, err
	}
	if err := w.prune(); err != nil {
		root.Close()
		return nil, err
	}
	// No file is opened here. A store that is switched on and then serves
	// nothing leaves no empty file behind, and Clear followed by a switch that
	// is still on leaves the directory as empty as it says it is.
	w.oldest = w.oldestRecord()
	return w, nil
}

// ensureStoreDir creates the store's directory 0700 and refuses a location
// this account does not have to itself.
//
// The refusal is the whole of the location rule. A store is one account's own
// record of what its Mac served, kept at 0600; in a directory another account
// can write to, that account can plant a file under the name the store is
// about to use, read what is written into a directory it controls, or remove
// the lot. So every directory from the store up to the filesystem root is
// checked for group- and other-write, and the store's own name must be a real
// directory rather than a link to one — the same discipline
// internal/runtime/launcher.go applies to a model server's log, applied to the
// directory as well, because here it is the directory that is created.
func ensureStoreDir(dir string) error {
	parent := filepath.Dir(dir)
	// The account's own data folder may not exist at all. In shared-cache mode
	// the layout is created under the shared root, and this store is the one
	// thing Gropius keeps under the account's own — so for an account that has
	// never run Gropius per-user there is nothing above the store yet. Created
	// owner-only, and then held to the same rule as any other ancestor.
	if _, err := os.Stat(parent); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return err
		}
	}
	if err := checkAncestors(parent); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return os.Mkdir(dir, 0o700)
	}
	if err != nil {
		return err
	}
	return checkStoreDir(dir, fi)
}

// openStoreRoot opens a handle to the store's directory, having first held it
// to the same rule the writer does.
//
// Reading and clearing check what writing checks. os.Root refuses a symbolic
// link *inside* the root but not one standing where the root itself should be,
// so without this a store directory replaced by a link would have Clear delete
// every matching file in whatever directory the link pointed at — this
// account's real store, or any directory it can write.
func openStoreRoot(dir string) (*os.Root, error) {
	if err := checkAncestors(filepath.Dir(dir)); err != nil {
		return nil, err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if err := checkStoreDir(dir, fi); err != nil {
		return nil, err
	}
	return os.OpenRoot(dir)
}

// checkStoreDir refuses a store directory that is a link, is not a directory,
// or is one another account on this Mac can write.
func checkStoreDir(dir string, fi os.FileInfo) error {
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("%s is a symbolic link, not a directory", dir)
	case !fi.IsDir():
		return fmt.Errorf("%s is not a directory", dir)
	case fi.Mode().Perm()&0o022 != 0:
		return fmt.Errorf("%s can be written by other accounts on this Mac (mode %04o)", dir, fi.Mode().Perm())
	}
	return nil
}

// checkAncestors refuses a directory that is, or sits under, one another
// account can write to. It follows symbolic links deliberately: what matters
// is the mode of the directory the store would really be in.
func checkAncestors(dir string) error {
	for {
		fi, err := os.Stat(dir)
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			return fmt.Errorf("%s is not a directory", dir)
		}
		if fi.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("%s can be written by other accounts on this Mac (mode %04o)", dir, fi.Mode().Perm())
		}
		// And owned by this account or by the administrator. A directory
		// another account owns is one they can replace between this check and
		// the open, however tight its mode looks now; the mode check alone
		// would let a store sit under a path only its owner can move.
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != uint32(os.Geteuid()) && st.Uid != 0 {
			return fmt.Errorf("%s belongs to another account on this Mac", dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

// listStoreFiles returns the store's own files, oldest first. Anything else in
// the directory is not the store's and is never touched.
func listStoreFiles(root *os.Root) ([]storeFile, error) {
	d, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer d.Close()
	ents, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var out []storeFile
	for _, e := range ents {
		m := storeFilePattern.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		day, _ := strconv.Atoi(m[1])
		n, _ := strconv.Atoi(m[2])
		out = append(out, storeFile{name: e.Name(), day: day, n: n, size: info.Size()})
	}
	// By day and then by counter, numerically: a counter that has run past
	// three digits would sort before, not after, its predecessors as text.
	sort.Slice(out, func(i, j int) bool {
		if out[i].day != out[j].day {
			return out[i].day < out[j].day
		}
		return out[i].n < out[j].n
	})
	return out, nil
}

// fileFlags is how every store file is opened: created if absent, append-only,
// owner-only, following no symbolic link and blocking on nothing. It is the
// discipline internal/runtime/launcher.go uses for a model server's log, for
// the same reason — a predictable name is a name someone else can get to
// first.
const fileFlags = os.O_CREATE | os.O_WRONLY | os.O_APPEND | syscall.O_NONBLOCK | syscall.O_NOFOLLOW

// open opens the file to append to: the newest one if it has room, a new one
// otherwise.
func (w *storeWriter) open(fresh bool) error {
	day := w.now().UTC().Format("20060102")
	dayNum, _ := strconv.Atoi(day)
	name := ""
	if n := len(w.files); n > 0 && !fresh {
		last := w.files[n-1]
		// Never a file already counted in the summary: appending to one would
		// have the next fold count what was appended a second time.
		if last.day == dayNum && last.size < w.opts.RotateBytes && !last.folded {
			name = last.name
			w.cur = last.size
		}
	}
	if name == "" {
		next := 1
		for _, f := range w.files {
			if f.day == dayNum && f.n >= next {
				next = f.n + 1
			}
		}
		if w.day == dayNum && w.n >= next {
			next = w.n + 1
		}
		name = fmt.Sprintf("stats-%s-%03d.jsonl", day, next)
		w.cur = 0
		w.day, w.n = dayNum, next
	}
	f, err := w.root.OpenFile(name, fileFlags, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return fmt.Errorf("%s is not a regular file", name)
	}
	if len(w.files) == 0 || w.files[len(w.files)-1].name != name {
		// Recorded only once the file is really open: a failed open must not
		// leave a name in the list for the panel to count.
		w.files = append(w.files, storeFile{name: name, day: dayNum, n: numOf(name)})
	}
	w.f, w.buf = f, bufio.NewWriterSize(f, 32<<10)
	// A file that does not end in a newline was torn by a crash part-way
	// through a record. Close the frame before appending, or the half-record
	// and the next whole one would join into a line that parses as neither and
	// the crash would cost two records instead of one.
	if w.cur > 0 {
		if ended, err := endsInNewline(w.root, name); err == nil && !ended {
			if err := w.buf.WriteByte('\n'); err != nil {
				return err
			}
			w.cur++
		}
	}
	return nil
}

// numOf reads a store file's counter back out of its name.
func numOf(name string) int {
	m := storeFilePattern.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[2])
	return n
}

// endsInNewline reports whether a file's last byte is a newline.
func endsInNewline(root *os.Root, name string) (bool, error) {
	f, err := openStoreFileForReading(root, name)
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return true, err
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], info.Size()-1); err != nil {
		return false, err
	}
	return last[0] == '\n', nil
}

// write appends one line, opening a file if there is not one and rotating
// first if the line would not fit in it.
func (w *storeWriter) write(b []byte) error {
	if w.f == nil {
		if err := w.open(false); err != nil {
			return err
		}
	}
	if w.cur > 0 && w.cur+int64(len(b))+1 > w.opts.RotateBytes {
		if err := w.rotate(); err != nil {
			return err
		}
	}
	n, err := w.buf.Write(b)
	w.cur += int64(n)
	if err != nil {
		return err
	}
	if err := w.buf.WriteByte('\n'); err != nil {
		return err
	}
	w.cur++
	w.files[len(w.files)-1].size = w.cur
	if w.oldest == 0 {
		// Read off the line being written rather than off the file: the file
		// is what the buffer has not reached yet, so an empty store would go
		// on reporting that it holds nothing for as long as it was buffering.
		if l, ok := parseLine(b); ok {
			w.oldest = l.At
		}
	}
	return nil
}

// rotate closes the open file, applies retention, and opens the next.
func (w *storeWriter) rotate() error {
	if err := w.closeFile(); err != nil {
		return err
	}
	if err := w.prune(); err != nil {
		return err
	}
	// A fresh file, always: the rotation is pre-emptive, so the file just
	// closed is still under the limit and reusing it would put the record that
	// triggered the rotation past it.
	if err := w.open(true); err != nil {
		return err
	}
	w.oldest = w.oldestRecord()
	return nil
}

// prune enforces both bounds, oldest file first. The size cap is the hard one:
// while the store is within one file's growth of it, the oldest file goes,
// whatever the horizon says.
// The horizon then removes what is left over from before it, so a quiet Mac
// still loses records it promised to forget.
//
// It removes whole files, never lines: a file is the unit the format rotates
// in, and rewriting one to drop its first half would mean holding two copies
// of it on a disk the operator asked to keep a limit on.
func (w *storeWriter) prune() error {
	// Around the pass rather than inside it, because folding what is dropped
	// makes the summary larger and the summary counts toward the cap: a pass
	// that took the store to its limit can leave it a little over, and the next
	// one takes the next file. Each pass drops at least one file or stops, and
	// a pass that drops nothing ends the loop.
	for {
		dropped, err := w.pruneOnce()
		if err != nil || dropped == 0 {
			w.oldest = w.oldestRecord()
			return err
		}
	}
}

// pruneOnce is one pass of retention: it decides which files go, summarizes
// them, and removes them. It reports how many it removed.
func (w *storeWriter) pruneOnce() (int, error) {
	months, maxBytes := w.limits()
	total := w.totalBytes()
	// Which files go is decided before any of them is removed, because what is
	// about to be dropped has to be summarized first and a summary written a
	// file at a time would rewrite the summary once per file.
	doomed := 0
	// Room for the file about to be opened, not just for what is already
	// there: pruning to exactly the cap and then opening a new file would put
	// the store over it for as long as that file took to fill, and the cap is
	// meant to be a bound rather than an average.
	for doomed < len(w.files)-1 && total+w.opts.RotateBytes > maxBytes {
		total -= w.files[doomed].size
		doomed++
	}
	cutoff := w.now().UTC().AddDate(0, -months, 0).Unix()
	for doomed < len(w.files) {
		newest := w.newestRecordIn(w.files[doomed].name)
		if newest == 0 || newest >= cutoff {
			break
		}
		doomed++
	}
	if doomed == 0 {
		return 0, nil
	}
	// The file being written can be dropped too, on a Mac quiet enough that it
	// never fills: close it first, so what is buffered is counted and the next
	// record starts a new file.
	if doomed == len(w.files) && w.f != nil {
		if err := w.closeFile(); err != nil {
			return 0, err
		}
		w.files[len(w.files)-1].size = w.cur
	}
	// Counted before it goes, and on disk before it goes: this is the whole of
	// itd-2609061602043757. A crash between the two leaves the summary and the
	// detail both, which the next start reconciles; a crash the other way round
	// would lose the records with nothing to show they had ever been there.
	//
	// A fold that cannot be done stops retention and nothing else. It must not
	// reach the caller: on the rotation path a prune error travels into the
	// write path, where it is counted as a lost record and the file is dropped
	// — so one unreadable file would cost every record from then on. The store
	// holds what it cannot summarize, says so on the panel, and carries on
	// recording.
	if err := w.fold(w.files[:doomed]); err != nil {
		w.retentionWedged(err)
		return w.dropUnsummarized(maxBytes), nil
	}
	w.retentionRan()
	// The seam stands for the removals not happening: a crash after the rename,
	// or a removal that fails. Both leave the same state, and it is the state
	// the next pass and the next start have to survive.
	skip := false
	if w.opts.afterSummary != nil {
		skip = w.opts.afterSummary() != nil
	}
	removed := 0
	var stuck []storeFile
	for _, f := range w.files[:doomed] {
		err := error(nil)
		if skip {
			err = fs.ErrPermission
		} else if e := w.root.Remove(f.name); e != nil && !errors.Is(e, fs.ErrNotExist) {
			err = e
		}
		if err != nil {
			// Counted but still here. It stays in the list so its bytes are
			// still accounted for and the next pass tries again; it is marked
			// so that nothing appends to it in the meantime, and the fold skips
			// it for as long as it is the file that was counted.
			if !skip && !w.wedgedLogged {
				w.wedgedLogged = true
				w.opts.Log.Warn("a summarized statistics file could not be removed; it is kept until it can be", "err", err)
			}
			f.folded = true
			stuck = append(stuck, f)
			continue
		}
		removed++
	}
	w.files = append(stuck, w.files[doomed:]...)
	return removed, nil
}

// dropUnsummarized holds the size limit when the summary cannot be written.
//
// The limit is the hard bound (adr-2609061610107154) and it still wins. Keeping
// records that cannot be summarized is the right first answer — a record
// deleted uncounted is gone twice over — but it cannot be the last one: a full
// disk is exactly the case that makes the fold fail, and a store that could
// not then free its own space would be a store that made a full disk
// permanent. So once it is over the limit by more than the file it is about to
// open, the oldest file goes without a summary, the loss is counted where the
// panel can see it, and the log says so.
//
// It never takes the last file, and the wedge stands until a fold succeeds.
func (w *storeWriter) dropUnsummarized(maxBytes int64) int {
	// What the store really holds now, not what the pass above expected to be
	// holding once it had dropped what it could not: nothing was dropped.
	total := w.totalBytes()
	if total <= maxBytes+w.opts.RotateBytes {
		return 0
	}
	dropped := 0
	for len(w.files) > 1 && total > maxBytes {
		f := w.files[0]
		lost := w.recordsIn(f.name)
		if err := w.root.Remove(f.name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return dropped
		}
		w.opts.Log.Warn("a statistics file was removed without being summarized first: the store is over its size limit and cannot summarize what it drops",
			"file", f.name, "bytes", f.size, "records", lost)
		w.unsummarized += lost
		total -= f.size
		w.files = w.files[1:]
		dropped++
	}
	return dropped
}

// recordsIn counts the records a file still holds, for the figure the panel
// shows when one is dropped without a summary. A file too damaged to read
// counts nothing, which is the truth: what it held cannot be known.
func (w *storeWriter) recordsIn(name string) int64 {
	lines, err := readRawLines(w.root, name)
	if err != nil {
		return 0
	}
	n := int64(0)
	for _, b := range lines {
		if _, ok := parseLine(b); ok {
			n++
		}
	}
	return n
}

// retentionWedged records that retention cannot run, once per spell.
func (w *storeWriter) retentionWedged(err error) {
	w.wedged = true
	if w.wedgedLogged {
		return
	}
	w.wedgedLogged = true
	w.opts.Log.Warn("the statistics store is keeping records past its limits: what it would drop cannot be summarized first", "err", err)
}

// retentionRan records that retention is working again.
func (w *storeWriter) retentionRan() {
	w.wedged, w.wedgedLogged = false, false
}

func (w *storeWriter) flush() error {
	if w.buf == nil {
		return nil
	}
	return w.buf.Flush()
}

func (w *storeWriter) closeFile() error {
	if w.f == nil {
		return nil
	}
	err := w.buf.Flush()
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	w.f, w.buf = nil, nil
	return err
}

// discardFile lets go of the open file without trying to write what is
// buffered, which is the only way past a write error a bufio.Writer will
// otherwise repeat forever.
func (w *storeWriter) discardFile() {
	if w.f == nil {
		return
	}
	_ = w.f.Close()
	w.f, w.buf, w.cur = nil, nil, 0
}

func (w *storeWriter) close() error {
	err := w.closeFile()
	if cerr := w.root.Close(); err == nil {
		err = cerr
	}
	return err
}

// figures is what the panel is shown: how many files, how many bytes, and how
// far back the store reaches.
func (w *storeWriter) figures() (int, int64, int64) {
	return len(w.files), w.totalBytes(), w.oldest
}

// totalBytes is how much room the store is using, the summary of what it has
// already dropped included: the cap is the room the operator gave the records,
// and the summary is one of them.
func (w *storeWriter) totalBytes() int64 {
	total := w.summaryBytes
	for _, f := range w.files {
		total += f.size
	}
	return total
}

// oldestRecord is the timestamp of the first record still held.
func (w *storeWriter) oldestRecord() int64 {
	for _, f := range w.files {
		if at := firstRecordIn(w.root, f.name); at != 0 {
			return at
		}
	}
	return 0
}

// maxStoreFileBytes bounds a read of one store file. Rotation keeps a file to
// RotateBytes, so anything far past that is not a file this store wrote; the
// bound is what stops a hand-placed file of any size being read into memory.
const maxStoreFileBytes = 64 << 20

// maxLatestBytes bounds one read through the store, so a caller that asks for
// everything is bounded in bytes as well as in records. It is comfortably over
// the largest cap a person can set, and is here for the store a hand-edit or
// another build left larger than this one would write.
const maxLatestBytes = 16 << 30

// firstRecordIn returns the timestamp of the first record in a file, or zero.
func firstRecordIn(root *os.Root, name string) int64 {
	lines, err := readRawLines(root, name)
	if err != nil {
		return 0
	}
	for _, b := range lines {
		if l, ok := parseLine(b); ok {
			return l.At
		}
	}
	return 0
}

// newestRecordIn returns the timestamp of the last record in a file, or zero.
// A file is kept or dropped on its newest record rather than on its name,
// because a quiet Mac writes across days into one file.
func (w *storeWriter) newestRecordIn(name string) int64 {
	f, err := openStoreFileForReading(w.root, name)
	if err != nil {
		return 0
	}
	_ = f.Close()
	lines, err := readRawLines(w.root, name)
	if err != nil {
		return 0
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if l, ok := parseLine(lines[i]); ok {
			return l.At
		}
	}
	return 0
}

// Files lists the store's own files, oldest first. It is the reader's way in
// for anything that wants to walk the store itself.
func (s *FileStore) Files() ([]string, error) {
	if s == nil {
		return nil, nil
	}
	root, err := openStoreRoot(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer root.Close()
	files, err := listStoreFiles(root)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.name)
	}
	return out, nil
}

// ReadOptions bounds one read through the store.
//
// The bounds are on the reading, not on what the reading yields, because those
// are different quantities the moment a line does not parse: a store written by
// a newer Gropius, or one a crash tore, hands back nothing while costing every
// byte of itself. A caller that bounded only the records it accepted would have
// bounded nothing at all.
type ReadOptions struct {
	// Limit is the most records handed to fn. Zero or less means every record
	// the other bounds allow.
	Limit int
	// MaxLines is the most lines read, whether or not they parse. Zero or less
	// means every line the byte budget allows.
	MaxLines int
	// MaxBytes is the most file content read. Zero or less means the store's
	// own ceiling, which is comfortably past the largest store Settings can be
	// asked for.
	MaxBytes int64
}

// ReadStats is what one read through the store cost and what it met.
//
// It is returned rather than published on the store, because two readings at
// once would otherwise each report the other's figures, and a reader shown "so
// many lines could not be read" deserves the number from the reading in front
// of them.
type ReadStats struct {
	// Lines is how many lines were read, parsed or not; Records how many were
	// handed to fn; Skipped how many could not be used.
	Lines   int64
	Records int
	Skipped int64
	// Bytes is how much file content was read.
	Bytes int64
	// Bounded reports a read that a bound stopped rather than one that reached
	// the end of the store or was stopped by fn; BoundedBy names which of them
	// did it — "records", "lines" or "bytes" — so a caller reporting the stop
	// to a reader can name the figure that actually applied rather than the
	// one it happens to know.
	Bounded   bool
	BoundedBy string
}

// Read walks the store newest record first, calling fn with each record until
// it returns false or a bound is reached.
//
// It is the store's read primitive: every bounded, cancellable reading goes
// through here, and a new reader — the per-model per-day summaries of
// itd-2609061602043757 among them — should take this rather than Latest.
//
// Newest first and bounded, because that is how every reader of this store
// wants it: the panel wants the last hour, and the dashboard
// (itd-2609061521159233) walks the range it is drawing. A line that does not
// parse is skipped, so a file torn by a crash costs the line it was torn in and
// nothing else — and is counted, both in what is returned and in the store's
// own status, so a great many of them cannot pass for a quiet store.
//
// The context is checked as lines are read rather than as records are accepted,
// for the same reason the bounds are: a caller cannot be left waiting on a
// store this build cannot read a single line of.
func (s *FileStore) Read(ctx context.Context, opts ReadOptions, fn func(Line) bool) (ReadStats, error) {
	var got ReadStats
	if s == nil {
		return got, nil
	}
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
	files, err := listStoreFiles(root)
	if err != nil {
		return got, err
	}
	budget := opts.MaxBytes
	if budget <= 0 {
		budget = maxLatestBytes
	}
	// seen counts everything the split produced, lines and blanks alike, and
	// is only ever used to decide when to ask whether the caller is still
	// there.
	var seen int64
	defer func() {
		s.statusMu.Lock()
		s.status.Skipped = got.Skipped
		s.statusMu.Unlock()
	}()
	for i := len(files) - 1; i >= 0; i-- {
		if got.Bytes >= budget {
			got.Bounded, got.BoundedBy = true, "bytes"
			break
		}
		lines, err := readRawLines(root, files[i].name)
		if err != nil {
			return got, err
		}
		got.Bytes += files[i].size
		// Backwards, parsing one line at a time: a bound of ten must cost ten
		// records decoded, not a whole file of them thrown away.
		for j := len(lines) - 1; j >= 0; j-- {
			// Asked now and then rather than per line, and counted separately
			// from the lines: the answer is a channel read, and a caller who
			// has gone away must be let go of even over a file that is nothing
			// but newlines, which yields no line to count.
			seen++
			if seen%4096 == 0 && ctx != nil {
				if err := ctx.Err(); err != nil {
					return got, err
				}
			}
			// A file ends in a newline, so splitting it leaves a last element
			// that is not a line at all. It is neither counted nor bounded
			// against, or a bound of four would cost a caller one of its four.
			raw := bytes.TrimSpace(lines[j])
			if len(raw) == 0 {
				continue
			}
			if opts.MaxLines > 0 && got.Lines >= int64(opts.MaxLines) {
				got.Bounded, got.BoundedBy = true, "lines"
				return got, nil
			}
			got.Lines++
			l, ok := parseLine(raw)
			if !ok {
				got.Skipped++
				continue
			}
			if !fn(l) {
				return got, nil
			}
			got.Records++
			if opts.Limit > 0 && got.Records >= opts.Limit {
				got.Bounded, got.BoundedBy = true, "records"
				return got, nil
			}
		}
	}
	return got, nil
}

// Latest is a thin wrapper over Read for a caller that wants records and has
// nothing to say about the cost: every record, newest first, up to limit.
//
// It is deliberately the weaker of the two. It passes no context and no line
// bound, so a store this build cannot read a line of costs it every byte and no
// caller can stop it — which is exactly the shape the dashboard's own read had
// before it was fixed. It suits a test and a one-off, and it does not suit
// anything on a request path: a new reader takes Read, with a line bound and
// the caller's context.
func (s *FileStore) Latest(limit int, fn func(Line) bool) error {
	_, err := s.Read(context.Background(), ReadOptions{Limit: limit}, fn)
	return err
}

// readRawLines reads one whole file and splits it into lines, oldest first,
// without parsing any of them.
//
// Whole, because a file is bounded by rotation and because reading it
// backwards line by line would mean seeking about inside a file another
// goroutine may be appending to. Splitting is cheap; it is decoding that is
// not, which is why the caller decides how many lines to decode.
func readRawLines(root *os.Root, name string) ([][]byte, error) {
	f, err := openStoreFileForReading(root, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxStoreFileBytes))
	if err != nil {
		return nil, err
	}
	return bytes.Split(raw, []byte{'\n'}), nil
}

// openStoreFileForReading opens one of the store's files to read, under the
// same discipline the writer opens one to write: following no link, blocking on
// nothing, and a regular file or nothing at all.
//
// The blocking half is the point. A named pipe left under a name the store
// reads — summary.jsonl above all, which is a fixed name in a folder any
// unsandboxed process running as this account can write — parks whoever opened
// it forever. That is the store's lifecycle lock on the way in, the writer
// goroutine at the next rotation, and a control-plane goroutine every time the
// panel polls: an app that looks alive while its settings, its shutdown and its
// figures are all stuck. O_NONBLOCK makes the open return, and the check on the
// handle — not on the name, which could be swapped in between — makes it
// refuse. A refusal wedges retention honestly; a hang says nothing at all.
func openStoreFileForReading(root *os.Root, name string) (*os.File, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	return f, nil
}

// parseLine turns one line into a record, reporting whether it was one.
//
// Two passes over the line, the first for the envelope and the second for the
// kind it names. It is the plainest way to keep the three record shapes
// separate structs rather than one struct that is mostly absent fields, and
// the store's own read is bounded by the caller.
func parseLine(b []byte) (Line, bool) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return Line{}, false
	}
	var head struct {
		V    int    `json:"v"`
		Kind string `json:"kind"`
		At   int64  `json:"at"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return Line{}, false
	}
	// A version this build does not know is a line it must not read: the
	// version is bumped only for a change an older reader could not survive,
	// so decoding it with these field names would be reading a shape that is
	// no longer this one. Skipped and counted, like any other line that cannot
	// be used.
	if head.V > SchemaVersion {
		return Line{}, false
	}
	l := Line{V: head.V, Kind: head.Kind, At: head.At}
	switch head.Kind {
	case KindRequest:
		if err := json.Unmarshal(b, &l.Request); err != nil {
			return Line{}, false
		}
	case KindLoad, KindRemoved:
		if err := json.Unmarshal(b, &l.Event); err != nil {
			return Line{}, false
		}
		l.Event.Kind = EventRemoved
		if head.Kind == KindLoad {
			l.Event.Kind = EventLoad
		}
	case KindSettings:
		if err := json.Unmarshal(b, &l.Settings); err != nil {
			return Line{}, false
		}
	case KindSummary:
		if err := json.Unmarshal(b, &l.Summary); err != nil {
			return Line{}, false
		}
	case KindSummaryIndex:
		// The head of the summary file, which carries no record at all: read as
		// a line so that a reader walking the file recognizes it rather than
		// counting it as one it could not use.
	default:
		return Line{}, false
	}
	return l, true
}

// jsonFields lists a struct's fields under the names they are written down as.
func jsonFields(v any) []string {
	rt := reflect.TypeOf(v)
	out := make([]string, 0, rt.NumField())
	for i := range rt.NumField() {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}
