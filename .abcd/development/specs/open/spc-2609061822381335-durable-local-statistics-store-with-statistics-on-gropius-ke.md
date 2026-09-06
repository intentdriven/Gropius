---
id: spc-2609061822381335
slug: durable-local-statistics-store-with-statistics-on-gropius-ke
intent: itd-2609061521102742
origin: researcher-authored
production_mode: hand-written
---
# durable-local-statistics-store-with-statistics-on-gropius-ke

## Summary

With the statistics switch on, the records the recorder produces are also
written to disk as append-only JSON Lines, one file at a time, rotated by
size, under the serving account's own data directory, owner-only and opened
without following symlinks. Settings gains a months figure, a size cap — the
hard bound — and a Clear button, and shows the date of the oldest record
still held. The format, the retention rule and the location are fixed by
adr-2609061610107154; this spec settles field names, file naming, rotation
thresholds and the write path.

## Scope

In scope:

- A store in `internal/stats` beside the recorder: writer, rotation,
  retention, Clear, and a reader the dashboard later uses, in a `stats`
  directory resolved per account and refused inside a group-writable one.
- Record kinds: request lines; load and eviction events carrying their
  reason; a startup record of the effective settings. Every record carries a
  schema version and a UTC timestamp to the second.
- Settings fields for the months figure and the size cap, the Clear action,
  and the oldest-record date beside them.
- A reference page describing every field, the file naming and the retention
  rule, plus the shared-mode and uninstall sentences.

Out of scope:

- The coarse summary written before records are dropped
  (itd-2609061602043757) and the historical views (itd-2609061521159233),
  both of which build on this spec.
- Any session or client label. The telemetry-pack intent is retired, and no
  field is reserved for it.
- SQLite or any other new dependency; adr-2609061610107154 names the measured
  condition under which that is reconsidered.

## Approach

Location. `config.Paths` gains `Stats`. It is the `stats` directory under the
per-user data root in every install mode, including shared-cache mode, because
the shared root is group-writable and a per-account record file has no
business there. Under an explicit root override the store lives under that
root, so the rule is total. Before the first write, the resolved directory and
each parent up to the root are checked: if the directory exists and is
group- or other-writable, or is not a directory, the store refuses to create
itself, logs once, and statistics stay in memory. The directory is created
0700.

Files. Names match `stats-YYYYMMDD-NNN.jsonl`, with the date in UTC and a
per-day counter. Each file is opened with create, write-only, append,
no-follow and non-blocking, mode 0600, followed by an fstat regular-file
check — the discipline `internal/runtime/launcher.go` already applies to the
per-model log. Rotation closes the active file and opens the next when it
passes 5 MB. Retention removes whole files: while the total exceeds the size
cap, the oldest file goes, and a file whose newest record is older than the
months figure goes even when the total is under the cap. Rotation and Clear
only ever remove names matching the store's own pattern in its own directory,
never whatever a directory listing happens to return.

Records. One JSON object per line: `v` (schema version), `ts` (UTC seconds),
`kind`, and `model` where a model applies.

- `kind: "request"` adds `status` (the recorder's outcome class), `stream`,
  `endpoint` (chat or completions), `prompt_tokens`, `completion_tokens`,
  `cached_tokens`, `ttft_ms` (absent when not streaming), `duration_ms`,
  `queue_ms` and `load_ms`.
- `kind: "load"` records a model becoming ready, with `load_ms`.
- `kind: "evict"` carries `reason`, one of the seven removal reasons the
  recorder enumerates, so the dashboard can tell an eviction from a crash or
  an idle reap.
- `kind: "settings"` is written at every startup with the effective values —
  budget bytes, decode concurrency, idle timeout, the months figure and the
  size cap — and again on a live change once itd-2609061441261073 makes the
  budget a setting. Effective, not saved: decode concurrency and idle timeout
  take a restart today, so a saved value that is not yet in force must not be
  recorded as though it were.

Readers ignore unknown fields and skip a line that does not parse, so a torn
last line after a crash costs that line and nothing more, and a later build
reads an older file.

Write path. A single goroutine owns the open file and is fed by a buffered
channel, flushed every two seconds, when the switch is turned off, and on
shutdown. A full channel drops records and increments a dropped counter shown
in the panel rather than blocking a request; a crash loses at most the few
seconds not yet flushed. The pool bounds concurrency rather than rate, so the
channel sees hundreds of records a minute at most.

Startup and the live view. Only the newest file is read at startup, to seed
the recorder's ring and counters; everything older is the dashboard's to read.
Turning the switch off stops new records and leaves the files alone; Clear
removes every store file and empties the ring. The panel shows the oldest
record's date beside the two retention fields, taken from the oldest file's
first record.

Settings. `Config` gains `StatsMonths int` and `StatsMaxBytes int64`, typed in
the panel in months and megabytes and stored in bytes, matching the unit
convention the memory-budget intent uses. Both are validated in
`config.Validate`; the size cap is the hard bound and the months figure prunes
within it.

## How each acceptance criterion is satisfied

- "Given the switch is on, when ten requests complete and Gropius is restarted,
  then the ten records are readable from the store's files, each carrying the
  documented fields and a schema version." A test runs ten completions against
  the fake model server, closes the store, reopens it and asserts ten request
  records with the documented fields and a version, plus the startup settings
  record.
- "Given the switch is off, when requests complete, then no store file is
  created and no existing store file changes." A test records with the switch
  on, notes each file's name, size and modification time, turns the switch
  off, runs further requests, and asserts the directory is byte-identical and
  that a fresh install with the switch off creates no directory at all.
- "Given a store at its size limit, when further records are written, then the
  total stays at or under the limit and the panel shows the date of the oldest
  record still held." A test with a small cap writes past it and asserts the
  total never exceeds the cap, that the oldest file is the one removed, and
  that the reported oldest date matches the oldest surviving record.
- "Given records on disk, when Alice presses Clear, then the store holds no
  record files and the per-model logs are untouched." A control test asserts
  the store directory holds no matching files, that an unrelated file planted
  in the directory survives (Clear removes only the store's own names), and
  that the per-model log files are unchanged.
- "Given a request carrying a sentinel string in its prompt, a bearer token in
  its headers and a non-loopback client address, when it is recorded, then a
  byte scan of every file in the store finds none of the three." A test issues
  such a request with a crafted `RemoteAddr` and scans every file in the
  directory for all three strings.
- "Given a store location that resolves inside a group-writable directory,
  when Gropius starts, then it refuses to create the store there." A test
  points the root override at a directory with group-write set and asserts no
  store is created, the refusal is logged, and the process serves normally
  with statistics held in memory.
- "Given the reference page, when it is compared with a record the current
  build writes, then every field in the record is described on the page and no
  described field is missing from the record." A test in the architecture test
  package parses the page's field table and compares it with the JSON tags of
  the record structs, in the pattern the existing architecture tests use.

## Trust-boundary review notes

- The store is a new file-handling boundary and is reviewed as one. It
  creates, appends to, rotates and deletes files under a path it computes; the
  deletions are the class the registry hardened in the 2026-09-06 triage, so
  they are restricted to the store's own name pattern in the store's own
  directory, and the directory handle is checked rather than the path
  re-walked.
- `internal/config`: `Paths` gains an entry and `Config` two fields, both
  parsed from a file another local account can write in shared mode. The
  group-writable refusal is the guard that keeps a hand-edited root from
  redirecting owner-only records into a shared directory.
- `internal/gateway`: Clear and the retention settings arrive over the control
  plane, which is loopback-only. Neither takes a path from the request; Clear
  operates on the resolved store directory only.
- `internal/runtime`: no change, but the file discipline is deliberately the
  launcher's, so there is one rule for owner-only files, not two.
- Shared-cache mode is a documented fact, not a hidden behaviour: one process
  serves every account on the Mac, so the store belongs to the serving account
  and holds every local account's requests under that account's opt-in.

## Docs to change

- `README.md`: one line under Features saying records are kept locally in
  JSON Lines with a retention Alice sets.
- `docs/getting-started.md`: "Sharing across user accounts" gains the sentence
  about whose store holds whose requests, and "Uninstalling" names the store
  directory when it is not under the root that section already deletes.
- A new reference page under `docs/`: every record kind and field, the file
  naming, the rotation and retention rules, the permissions, and that the size
  cap always wins.

## Dependencies and sequencing

- Binding: adr-2609061610107154 (format, retention, location) and
  adr-2609061503319212 (local only, strict opt-in, never prompt text,
  completions or keys).
- itd-2609061521082551 ships first; this spec persists what it records.
- itd-2609061602043757 hooks into the retention step this spec adds;
  itd-2609061521159233 reads these files.
- itd-2609061441261073 supplies the budget figure the settings record carries;
  until it lands the effective budget is the pool's computed default.
- Informs iss-10; not blocked by it, because the store never sits under the
  shared root.

## Open design points

- The per-file rotation size and the default cap and months figure; 5 MB,
  200 MB and six months are the starting values, and about 150 bytes a record
  makes 200 MB roughly four months at ten thousand requests a day.
- The measured single-pass scan time at the cap, taken during implementation
  and recorded against adr-2609061610107154 as the condition for
  reconsidering a database.
- Whether the dropped-record counter is shown in the panel or only logged.
