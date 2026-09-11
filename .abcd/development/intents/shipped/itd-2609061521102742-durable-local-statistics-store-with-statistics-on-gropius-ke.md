---
id: itd-2609061521102742
slug: durable-local-statistics-store-with-statistics-on-gropius-ke
spec_id: spc-2609061822381335
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609061521082551]
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Durable local statistics store: with statistics on, Gropius keeps the per-request records on disk with a stated retention and a documented format, so months of local model use can be analysed later on that Mac

## Press Release

Alice has had statistics on for three months and the records are still there:
one line of JSON per record, in plain text files under her own account's data
folder, so she can point any tool she likes at them. In Settings she says how
many months to keep and sets a size limit, and the size limit always wins;
beside them the panel shows the date of the oldest record she still has, so
she can see how far back her own history reaches. Turning statistics off stops
new records; Clear removes what is there. Nothing in the store is a prompt, a
completion, a key, or a client's network address.

## Why This Matters

A live view forgets on restart, and the point of turning statistics on is to
learn how local models are actually used over time: which models, how many
tokens, what latencies, and how that changes. That needs records that outlive
the process, kept on the Mac that produced them, under the same opt-in the
live view has (adr-2609061503319212). Files on other people's machines are
hard to change once they exist, which is why the format, the retention rule
and the location were decided before the spec, in adr-2609061610107154.

## Mechanism

We expect a size-capped JSON Lines store to hold months of local use on one
Mac without a database because a record is about 150 bytes, so ten thousand
requests a day is about 1.5 MB a day, and a single sequential pass over a
store at the default cap answers the dashboard's aggregates within two seconds
on Apple Silicon.

## Scope Conditions

- One serving process writes one store, under the account that runs it, on an <!-- cond: cond-2609061822385818 -->
  Apple Silicon Mac.
- In the shared-cache install mode one process serves every account on the <!-- cond: cond-2609061822386584 -->
  Mac, so the store holds every local account's requests under the serving
  account's opt-in.
- The store holds only what was recorded while the switch was on; the size cap <!-- cond: cond-2609061822382727 -->
  is the hard bound and the months figure prunes within it.
- A crash loses at most the few seconds of records not yet written out. <!-- cond: cond-2609061822382003 -->
- Record volume is bounded by the pool's per-model concurrency, so hundreds of <!-- cond: cond-2609061822385461 -->
  records a minute at most rather than thousands a second.

## Acceptance Criteria

- Given the switch is on, when ten requests complete and Gropius is restarted,
  then the ten records are readable from the store's files, each carrying the
  documented fields and a schema version.
- Given the switch is off, when requests complete, then no store file is
  created and no existing store file changes.
- Given a store at its size limit, when further records are written, then the
  total stays at or under the limit and the panel shows the date of the oldest
  record still held.
- Given records on disk, when Alice presses Clear, then the store holds no
  record files and the per-model logs are untouched.
- Given a request carrying a sentinel string in its prompt, a bearer token in
  its headers and a non-loopback client address, when it is recorded, then a
  byte scan of every file in the store finds none of the three.
- Given a store location that resolves inside a group-writable directory, when
  Gropius starts, then it refuses to create the store there.
- Given the reference page, when it is compared with a record the current
  build writes, then every field in the record is described on the page and no
  described field is missing from the record.

## Open Questions

- Resolved: format — append-only JSON Lines, one object per line, rotated by
  size, every file carrying a schema version so a later Gropius reads older
  files. No database and no new dependency.
- Resolved: retention — a months figure and a size cap, both in Settings, with
  the size cap as the hard bound; the panel shows the date of the oldest
  record beside them.
- Resolved: location and permissions — per account under the per-user data
  root, files owner-only and opened without following symlinks, in every
  install mode.
- Resolved: record schema — request lines; load and eviction events each
  carrying their reason; a startup record of the effective settings; UTC
  timestamps to the second.
- Resolved: shared-cache mode — the store belongs to the account running the
  serving process and holds every local account's requests under that
  account's opt-in; the documentation says so in one sentence.
- Resolved: the session label reserved for the telemetry pack is not written;
  that intent (itd-2609061521134968) is retired as contradicting
  adr-2609061503319212.
- Deferred: rotation thresholds, file naming and exact field names are the
  spec's, within the format the decision record fixes.
- Deferred: the measured scan time at the cap is taken during the spec rather
  than before the format was fixed. The reviewer asked for that measurement
  first; the maintainer fixed the format now and the decision record names the
  measurement as the condition for reconsidering a database instead.
- Depends on: adr-2609061610107154 (statistics store format, retention and
  location), minted this session.
- Depends on: adr-2609061503319212 (no public telemetry; local only; strict
  opt-in, off by default).
- Depends on: itd-2609061521082551, whose records this store keeps.

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-799061841f58 -->
Fidelity review — receipt rcp-799061841f58 (verifier intent-auditor claude-opus-5[1m]).

Provenance: intent-auditor@claude-opus-5[1m] · rubric_hash sha256:c3846cbf07114884cd475f6340e66bdbe4cd5e39611205b4889a9b898806d268 · prompt_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5
Input attestations: tree:HEAD 2f77cf5882fb04ac70c0ca7a24fb038649658ab2 (clean working tree; history rewritten during a rename, so no per-spec commit range exists and the tree as shipped is what was audited)@sha256:unknown; test-run:go test ./internal/stats/... ./internal/archtest/... ./internal/app/... ./internal/config/... ./internal/gateway/... ./internal/ui/... — all ok@sha256:unknown;

Acceptance rollup: MET 4 · MET_WITH_CONCERNS 3 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET_WITH_CONCERNS: Ten records written, the store closed, a second FileStore opened over the same directory and all ten read back with their documented fields and SchemaVersion — the durability the criterion asks for is demonstrated; the concern is that the restart is a store-level reopen fed by direct appends rather than the spec's own promised test of ten completions against the fake model server, and the gateway-to-store leg is proven separately by a single real completion.
  evidence: internal/stats/store_test.go:86 — "func TestTheRecordsAreStillThereAfterARestart(t *testing.T) {"
  evidence: internal/stats/store_test.go:109 — "again := NewStore(dir, StoreOptions{Months: 1200, MaxBytes: 1 << 20, RotateBytes: 64 << 10})"
  evidence: internal/stats/store_test.go:139 — "if requests != 10 || loads != 1 || settings != 1 {"
  evidence: internal/stats/store.go:48 — "const SchemaVersion = 1"
  evidence: internal/gateway/control_store_test.go:175 — "func TestNothingFromTheRequestReachesTheStore(t *testing.T) {"
- ac-2 — MET: With the switch off nothing is created — the store directory does not exist at all — and a store written while on is byte-identical by name, size and mtime after the switch goes off and further records are offered; the same holds at app level across a settings save.
  evidence: internal/stats/store_test.go:200 — "if _, err := os.Stat(dir); !os.IsNotExist(err) {"
  evidence: internal/stats/store_test.go:226 — "if after := snapshotDir(t, dir); fmt.Sprint(after) != fmt.Sprint(before) {"
  evidence: internal/app/stats_store_test.go:30 — "t.Fatalf("a fresh install has a statistics store directory (%v)", err)"
  evidence: internal/gateway/control_stats_test.go:195 — "func TestRecordingWritesOnlyToTheStore(t *testing.T) {"
- ac-3 — MET_WITH_CONCERNS: Three hundred records into an 8 KB cap leave the total under it with the oldest file gone and Status().Oldest equal to the oldest surviving record, and the panel prints that date beside the two retention fields; the concern is a bounded, documented overshoot the criterion does not allow for — while a summary fold is wedged the store is left alone until it is over the cap by more than one file's growth (5 MB by default) before the oldest file is dropped without a summary.
  evidence: internal/stats/store_test.go:272 — "if total > cap {"
  evidence: internal/stats/store_test.go:286 — "if got := s.Status().Oldest; got != oldest.At {"
  evidence: internal/stats/store.go:1231 — "for doomed < len(w.files)-1 && total+w.opts.RotateBytes > maxBytes {"
  evidence: internal/gateway/control_store_test.go:70 — "if got := store["oldest"]; got != float64(oldest) {"
  evidence: internal/ui/static/app.js:907 — "parts.push(`Records from ${new Date(store.oldest * 1000).toLocaleDateString()} onwards`);"
  evidence: internal/ui/static/index.html:346 — "< p id="statsStoreLine" class="hint">< /p>"
  evidence: internal/stats/store.go:1323 — "if total <= maxBytes+w.opts.RotateBytes {"
- ac-4 — MET: Clear leaves the store with no record files while a planted bystander file survives, and a dedicated app-level test plants both a model log and a file named like a record in the log directory and asserts both read back unchanged afterwards.
  evidence: internal/app/stats_store_test.go:171 — "func TestClearLeavesThePerModelLogsAlone(t *testing.T) {"
  evidence: internal/stats/store_test.go:357 — "func TestClearRemovesOnlyTheStoresOwnFiles(t *testing.T) {"
  evidence: internal/stats/store.go:617 — "func (s *FileStore) Clear() error {"
  evidence: internal/stats/store.go:204 — "var storeFilePattern = regexp.MustCompile(`^stats-(\d{8})-(\d{3,})\.jsonl$`)"
- ac-5 — MET: A real streamed chat completion is driven through the gateway carrying the sentinel prompt, a Bearer API key and RemoteAddr 192.0.2.44:51820, the store is closed, and every file in its directory is read whole and scanned for all three strings — with the test failing first if the directory is empty or any file is.
  evidence: internal/gateway/control_store_test.go:175 — "func TestNothingFromTheRequestReachesTheStore(t *testing.T) {"
  evidence: internal/gateway/control_store_test.go:233 — "for _, secret := range []string{sentinel, token, "192.0.2.44"} {"
  evidence: internal/stats/stats.go:112 — "type Record struct {"
- ac-6 — MET: Starting an app whose data root is 0777 leaves the server serving with no store directory created and the store reporting itself refused, and the store-level test refuses a 0770 directory, a world-writable ancestor and a symlink planted under the store's own name.
  evidence: internal/app/stats_store_test.go:206 — "func TestAStoreThatCannotBeCreatedLeavesTheServerServing(t *testing.T) {"
  evidence: internal/app/stats_store_test.go:230 — "if _, err := os.Stat(paths.Stats); !os.IsNotExist(err) {"
  evidence: internal/stats/store_test.go:543 — "func TestTheStoreRefusesADirectoryOthersCanWrite(t *testing.T) {"
  evidence: internal/stats/store.go:1050 — "const fileFlags = os.O_CREATE | os.O_WRONLY | os.O_APPEND | syscall.O_NONBLOCK | syscall.O_NOFOLLOW"
- ac-7 — MET_WITH_CONCERNS: The page-versus-record comparison holds today in both directions — every StoreFields(), RemovalReasons(), OutcomeClasses() and StoreKinds() entry is named on the reference page, and an independent sweep of every backticked lowercase name on that page found none that is not a real field, enum value or kind — but the guard for the reverse half only flags a described-but-absent name when it contains an underscore, so a single-word field named on the page and gone from the record (a `status` or an `endpoint`) would pass unnoticed.
  evidence: internal/archtest/statistics_docs_test.go:81 — "func TestTheStatisticsPageNamesEveryFieldTheStoreWrites(t *testing.T) {"
  evidence: internal/archtest/statistics_docs_test.go:83 — "for _, field := range stats.StoreFields() {"
  evidence: internal/stats/store.go:129 — "func StoreFields() []string {"
  evidence: internal/archtest/statistics_docs_test.go:30 — "if strings.Contains(name, "_") && !recorded(name) && !knownOther(name) {"
  evidence: internal/archtest/docs_test.go:197 — "var backtickRE = regexp.MustCompile("`([a-z_]+)`")"

Gap audit:
- honoured:
  - One line of JSON per record, in plain text files under her own account's data folder, so she can point any tool she likes at them
    evidence: internal/stats/store_test.go:153 — "func TestEveryLineIsOneJSONObject(t *testing.T) {"
    evidence: internal/config/config.go:150 — "if !sameDir(root, SharedRoot) {"
    evidence: docs/statistics-store-reference.md:46 — "| `kind` | Which of the six kinds below the line is: `request`, `load`, `removed`, `settings`, `summary` or `summary_index`."
  - In Settings she says how many months to keep and sets a size limit, and the size limit always wins; beside them the panel shows the date of the oldest record she still has
    evidence: internal/ui/static/index.html:332 — "< span>Keep records for (months)< /span>"
    evidence: internal/ui/static/index.html:346 — "< p id="statsStoreLine" class="hint">< /p>"
    evidence: internal/stats/store.go:1231 — "for doomed < len(w.files)-1 && total+w.opts.RotateBytes > maxBytes {"
  - Turning statistics off stops new records; Clear removes what is there
    evidence: internal/app/stats_store_test.go:27 — "func TestTheStoreFollowsTheStatisticsSwitch(t *testing.T) {"
    evidence: internal/app/stats_store_test.go:125 — "func TestClearEmptiesTheStoreAndTheView(t *testing.T) {"
  - Nothing in the store is a prompt, a completion, a key, or a client's network address
    evidence: internal/gateway/control_store_test.go:233 — "for _, secret := range []string{sentinel, token, "192.0.2.44"} {"
    evidence: internal/gateway/control_store_test.go:368 — "for _, secret := range []string{sentinel, token, "192.0.2.51", "GROPIUS OK"} {"
  - Resolved: location and permissions — per account under the per-user data root, files owner-only and opened without following symlinks, in every install mode
    evidence: internal/stats/store_test.go:605 — "func TestTheStoreIsOwnerOnly(t *testing.T) {"
    evidence: internal/stats/store.go:1050 — "const fileFlags = os.O_CREATE | os.O_WRONLY | os.O_APPEND | syscall.O_NONBLOCK | syscall.O_NOFOLLOW"
  - Resolved: format — append-only JSON Lines rotated by size, every file carrying a schema version. No database and no new dependency
    evidence: go.mod:5 — "require ( fyne.io/systray v1.12.2 github.com/brutella/dnssd v1.2.14 golang.org/x/sys v0.21.0 )"
    evidence: internal/stats/store_test.go:634 — "func TestFilesRotateAtTheirLimit(t *testing.T) {"
    evidence: internal/stats/store.go:48 — "const SchemaVersion = 1"
  - Resolved: the session label reserved for the telemetry pack is not written; no field is reserved for it
    evidence: internal/stats/stats.go:112 — "type Record struct { // Model, At, Class, Streamed, PromptTokens, CompletionTokens, FirstTokenMS, DurationMS, QueueWaitMS, LoadWaitMS — no session or client field"
    evidence: internal/stats/store.go:129 — "func StoreFields() []string {"
  - Resolved: shared-cache mode — the store belongs to the account running the serving process and holds every local account's requests under that account's opt-in; the documentation says so in one sentence
    evidence: docs/getting-started.md:194 — "Request statistics stay with the account that runs the server: if that account has recording on, its records cover every request the server handled, from any account on this Mac, and they are kept in that account's own folder rather than the shared one."
    evidence: internal/config/config.go:157 — "return filepath.Join(home, "Library", "Application Support", "Gropius", "stats")"
  - Deferred: the measured single-pass scan time at the cap is taken during the spec — the ADR's condition for reconsidering a database
    evidence: internal/stats/store_bench_test.go:24 — "func BenchmarkLatestAtTheCap(b *testing.B) {"
    evidence: .abcd/work/DECISIONS.md:57 — "fills the default cap to **206,342,511 bytes in 40 files, 901,059 records**, and reads the whole store in one sequential pass, aggregating by model and by day, in **1.436 s**"
- diverged:
  - Resolved: record schema — load and eviction events each carrying their reason. A load carries no reason: Reason is documented as empty on a load and omitted from the line, and what a load carries instead is duration_ms and failed. I reach the same finding as the intent's own Audit Notes and agree with it at the level of this intent, where the wording is explicit ('each carrying their reason') and is contradicted outright.
    evidence: internal/stats/stats.go:162 — "// Reason is why the entry was removed, one of the reasons above; empty on // a load."
    evidence: .abcd/development/intents/shipped/itd-2609061521102742-durable-local-statistics-store-with-statistics-on-gropius-ke.md:91 — "- Resolved: record schema — request lines; load and eviction events each"
    evidence: docs/statistics-store-reference.md:95 — "A load carries no `reason`: nothing in Gropius knows why a model was loaded"
  - Whether adr-2609061610107154 is itself contradicted is weaker than the Audit Notes state, and I disagree on that point. The ADR does not say 'each': it names 'a load or eviction event with its reason', a single noun phrase that reads collectively as well as distributively, and the shipped reference page adopts the collective reading in as many words. The ADR is contradicted only under the distributive reading; the unambiguous contradiction lives in the intent.
    evidence: .abcd/development/decisions/adrs/2609061610107154-statistics-store-format-json-lines-size-rotated-per-account.md:39 — "load or eviction event with its reason, and a startup record of the"
    evidence: docs/statistics-store-reference.md:46 — "A load and a removal are two spellings of the one event kind the [decision record] (...) names"
  - A second ADR-level divergence the Audit Notes do not record: the ADR reserves the record kinds to itself and delegates only field names and thresholds to the spec, and it names the kind an 'eviction event'; the shipped kind is `removed`, covering seven removal reasons of which eviction is one. That is a change to a record kind, argued only in the ledger and the closed spec's 'As built' rather than against the ADR.
    evidence: .abcd/development/decisions/adrs/2609061610107154-statistics-store-format-json-lines-size-rotated-per-account.md:80 — "- The store's spec inherits the format, the record kinds and the location;"
    evidence: internal/stats/store.go:59 — "KindRemoved = "removed""
    evidence: docs/statistics-store-reference.md:104 — "| `reason` | One of `evicted`, `idle`, `unloaded`, `abandoned`, `load_failed`, `crashed`, `shutdown`. |"
  - Mechanism: 'a record is about 150 bytes, so ten thousand requests a day is about 1.5 MB a day'. Measured at 230 bytes, so 200 MB is roughly three months rather than over four; the Mechanism's conclusion — a size-capped JSON Lines store without a database, answering the dashboard's aggregates in one pass within two seconds — survives at 1.436 s.
    evidence: internal/stats/store.go:46 — "const ApproxRecordBytes = 230"
    evidence: internal/archtest/statistics_docs_test.go:195 — "func TestTheRecordSizeIsTheSameFigureEverywhere(t *testing.T) {"
  - Spec Approach: the store is 'the `stats` directory under the per-user data root in every install mode, including shared-cache mode'. Shipped, when the resolved root IS the shared root the store moves to the serving account's own Application Support directory instead — a divergence from the spec's Approach that is more faithful, not less, to the ADR's 'per account under the per-user data root'.
    evidence: internal/config/config.go:149 — "func StatsDir(root string) string {"
    evidence: internal/config/config.go:157 — "return filepath.Join(home, "Library", "Application Support", "Gropius", "stats")"
  - Spec Approach field names and record shape: `at` not `ts`, `class` not `status`, `queue_wait_ms`/`load_wait_ms`/`first_token_ms` not `queue_ms`/`load_ms`/`ttft_ms`; no `cached_tokens`, no `endpoint`; the ring is not re-seeded from the newest file at startup; retention is applied continuously rather than only at rotation and open. All six are disclosed in the closed spec's 'As built' and argued in the ledger, and none is one of the acceptance criteria.
    evidence: internal/stats/stats.go:117 — "At int64 `json:"at"`"
    evidence: .abcd/development/specs/closed/spc-2609061822381335-durable-local-statistics-store-with-statistics-on-gropius-ke.md:0 — "## As built (2026-09-07)"
    evidence: internal/stats/store.go:158 — "// PruneEvery is how often retention is applied to a store that is simply // sitting there."
- missing:
  - No promise of the press release is undelivered. The one thing the criteria imply and the tests do not reach is a whole-process restart: the durability evidence is a second FileStore opened over the same directory inside one process, and no test restarts an App over the same data root and reads its store back.
    evidence: internal/stats/store_test.go:109 — "again := NewStore(dir, StoreOptions{Months: 1200, MaxBytes: 1 << 20, RotateBytes: 64 << 10})"
    evidence: internal/app/stats_store_test.go:13 — "func statsApp(t *testing.T, c config.Config) (*App, config.Paths) { // every caller builds one app per root; none reopens a root"

Scope-condition dispositions:
- cond-2609061822385818 — narrowed: One writer goroutine owns the open file and the store is resolved and moded per account, so the assumption holds where it is allowed to hold — but the shipped store refuses to exist at all in a group- or other-writable location, and nothing guards the directory against a second process of the same account.
  narrowing: Holds only where the resolved store directory and every ancestor up to the root are owner-writable alone; where they are not, the serving process writes no store at all and statistics stay in memory. The 'one process' half is relied on rather than enforced: no lock or exclusive open stops a second process of the same account opening the same directory.
  evidence: internal/stats/store.go:429 — "s.ch = make(chan storeEntry, s.opts.QueueSize)"
  evidence: internal/app/stats_store_test.go:230 — "if _, err := os.Stat(paths.Stats); !os.IsNotExist(err) {"
  evidence: internal/stats/store.go:1050 — "const fileFlags = os.O_CREATE | os.O_WRONLY | os.O_APPEND | syscall.O_NONBLOCK | syscall.O_NOFOLLOW"
- cond-2609061822386584 — survived: Shared-cache mode is handled exactly as assumed and is a documented fact rather than a hidden behaviour: the store belongs to the serving account, holds every local account's requests under that account's opt-in, and is deliberately moved out of the group-writable shared root into that account's own folder.
  evidence: internal/config/config.go:157 — "return filepath.Join(home, "Library", "Application Support", "Gropius", "stats")"
  evidence: docs/getting-started.md:194 — "Request statistics stay with the account that runs the server: ... they are kept in that account's own folder rather than the shared one."
- cond-2609061822382727 — narrowed: The store holds only what was recorded while the switch was on, and the cap is enforced ahead of the next file's growth with the months figure pruning within it — but the cap is not an unconditional hard bound in the shipped tree: while a summary fold cannot be written, retention stops and the store is left over its limit until the overshoot exceeds one rotation's worth.
  narrowing: The size cap is the hard bound in the normal path; while a summary fold is wedged the store may sit up to one file's growth (5 MB by default) above the cap before the oldest file is dropped without a summary and the loss is counted for the panel.
  evidence: internal/stats/store_test.go:189 — "func TestNothingIsWrittenWhileTheSwitchIsOff(t *testing.T) {"
  evidence: internal/stats/store_test.go:296 — "func TestFilesOlderThanTheHorizonArePruned(t *testing.T) {"
  evidence: internal/stats/store.go:1323 — "if total <= maxBytes+w.opts.RotateBytes {"
- cond-2609061822382003 — narrowed: The writer flushes every two seconds and on switch-off and shutdown, and a torn last line costs itself alone, so a process crash costs what the condition says — but no record file is ever fsynced, only the summary, so a power cut can cost more than the few seconds not yet written out.
  narrowing: Holds for a process crash, where the two-second flush bounds the loss; a power cut is not bounded by it, because nothing but summary.jsonl is forced to the disk and the rest sits in the OS page cache.
  evidence: internal/stats/store.go:192 — "defaultFlushEvery = 2 * time.Second"
  evidence: internal/stats/store_test.go:404 — "func TestATornLastLineCostsOnlyThatLine(t *testing.T) {"
  evidence: internal/stats/summary.go:528 — "return f.Sync()"
  evidence: docs/statistics-store-reference.md:57 — "Gropius never asks the disk to make sure they are really on it"
- cond-2609061822385461 — untested: Nothing in the delivery exercises real record volume against the pool's concurrency bound; the store instead hedges against the assumption failing with a 1024-entry queue that drops and counts rather than blocking a request, which is a guard against the condition rather than evidence for it, and the one high-rate exercise on the branch is a synthetic benchmark filling the cap as fast as it can.
## Grounds

- pursued: we want to learn how local models are actually used on this Mac, which models, how many tokens, what latencies, and we expect a month of records to change which models we keep and how we set the memory budget; we are wrong if, after a month with the dashboard, no such decision has changed
