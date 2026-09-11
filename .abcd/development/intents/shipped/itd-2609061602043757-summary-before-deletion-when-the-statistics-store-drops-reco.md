---
id: itd-2609061602043757
slug: summary-before-deletion-when-the-statistics-store-drops-reco
spec_id: spc-2609061822388014
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609061521102742]
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Summary before deletion: when the statistics store drops records that have reached its retention limit, Gropius first writes a coarse summary of them, per model and per day, so Alice keeps a record of long-ago use after the detailed lines are gone

## Press Release

Alice's oldest records have reached the limit she set and the detailed lines
for them are gone. What she still has is a short summary written before they
went: for each model, for each day, how many requests it served and how many
tokens it produced. A year on she can still see that last spring's work ran
mostly on one model and that her use of another tailed off in June, even
though no single request from that period survives.

## Why This Matters

The store is bounded on purpose: a size limit is the hard bound and detail is
dropped as the store fills (adr-2609061610107154). Without a summary, the long
view that statistics were turned on for (itd-2609061521082551) disappears
exactly as it becomes interesting, and the oldest and most informative
comparison — how use has changed — is the first thing lost. A per-model
per-day summary is a few hundred bytes a day, so it can outlive by years the
detail it replaces.

## Mechanism

We expect a per-model per-day summary to preserve the long view at negligible
cost because summarising records as they are dropped collapses tens of
thousands of lines into one line per model per day, roughly a thousandth of
the bytes the detail occupied.

## Scope Conditions

- Only records the store is about to drop are summarised; nothing is <!-- cond: cond-2609061822385374 -->
  summarised while the detail is still held.
- The summary is coarse by construction: counts and totals per model per day, <!-- cond: cond-2609061822381938 -->
  never a per-request figure.
- The summary is kept under the same opt-in, in the same place and in the same <!-- cond: cond-2609061822385249 -->
  form as the store it summarises, and holds nothing the detailed records did
  not.
- Apple Silicon Macs running one serving process; in the shared-cache install <!-- cond: cond-2609061822380528 -->
  mode the summary belongs to the serving account, as the store does.

## Acceptance Criteria

- Given a store holding records for two models over three days, when retention
  drops the oldest of those records, then a summary line for each model and
  each day among them is written first, with request counts and token totals
  equal to the records dropped.
- Given a summary already written for a day, when a later drop removes more
  records for that same day, then the existing summary is extended to cover
  them rather than written a second time.
- Given the switch is off, when Gropius runs, then no summary is written.
- Given a request whose prompt carries a sentinel string, when its record is
  summarised and dropped, then a byte scan of the summary finds nothing beyond
  the counts and totals for its model and day.
- Given a month for which only summaries remain, when Alice looks at that
  month, then it shows the per-model daily totals and states that the detail
  for it is gone.
- Given the documentation, when a reader looks up retention, then it states
  that a coarse per-model per-day summary is kept when detail is dropped, and
  what that summary holds.

## Open Questions

- Resolved: what is summarised — request counts and token totals, per model
  and per day, taken from the records that are about to be dropped.
- Resolved: when it happens — immediately before retention removes those
  records, never on a schedule of its own.
- Resolved: what it may hold — nothing the detailed records did not, so no
  prompt, completion, key or client address, under the same opt-in.
- Deferred: how long summaries are kept, whether they are themselves bounded,
  and how they are named on disk are the spec's, within the store's size
  limit.
- Depends on: itd-2609061521102742, whose retention triggers the summary.
- Depends on: adr-2609061610107154, which names this summary as what preserves
  history beyond the store's cap.
- Depends on: adr-2609061503319212 (no public telemetry; local only; strict
  opt-in, off by default).

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-849f518f44a0 -->
Fidelity review — receipt rcp-849f518f44a0 (verifier intent-auditor claude-opus-5[1m]).

Provenance: intent-auditor@claude-opus-5[1m] · rubric_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5 · prompt_hash sha256:8495cf6a068e8857c414ee68a0f779a35aac9824dc97509f193ab797b298864e
Input attestations: tree:HEAD 2f77cf5882fb04ac70c0ca7a24fb038649658ab2 (clean working tree; history rewritten during a rename, so no per-spec commit range exists — the tree as shipped was audited)@sha256:unknown; test-run:go test ./internal/stats/ ./internal/gateway/ ./internal/archtest/ ./internal/ui/ — all ok; the five summary tests and TestNothingFromTheRequestReachesTheSummary re-run verbose, all PASS@sha256:unknown;

Acceptance rollup: MET 5 · MET_WITH_CONCERNS 1 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: A test writes five requests for each of two models on each of three days, lets the horizon drop the file, and asserts the summary holds exactly six lines with 5 requests and 50/100 tokens each; the fold runs on the doomed files and returns only once the summary is fsynced and renamed into place, before any detail is removed
  evidence: internal/stats/summary_test.go:135 — "if len(sums) != 6 { t.Fatalf("the summary holds %d lines, want one for each of two models on each of three days""
  evidence: internal/stats/summary_test.go:148 — "if got.PromptTokens != 50 || got.CompletionTokens != 100 {"
  evidence: internal/stats/store.go:1267 — "if err := w.fold(w.files[:doomed]); err != nil {"
  evidence: internal/stats/summary.go:528 — "return f.Sync()"
  evidence: internal/stats/summary.go:537 — "if err := w.root.Rename(name, summaryFileName); err != nil {"
- ac-2 — MET: A day is keyed by (local day, model) and dayOf returns the existing line rather than making a second one; the same test drives a later drop over an already-summarised day and asserts one line whose count went from 5 to 6
  evidence: internal/stats/summary.go:255 — "if s := set.days[key]; s != nil { return s }"
  evidence: internal/stats/summary_test.go:181 — "t.Fatalf("the summary holds %d lines for org/a on 2026-01-11, want the one it already had", n)"
  evidence: internal/stats/summary_test.go:185 — "want the 5 from the first drop and the 1 from the second"
- ac-3 — MET: Switching recording off stops the writer outright, so no prune and therefore no fold can run; the test asserts the store directory is never created with the switch off and that a store switched off after it has written writes no summary on a later retention change
  evidence: internal/stats/store.go:401 — "if !on { s.stop(); return nil }"
  evidence: internal/stats/summary_test.go:246 — "t.Fatalf("the store directory exists with recording off: %v", names(t, dir))"
  evidence: internal/stats/summary_test.go:261 — "t.Error("a summary was written while recording was off")"
- ac-4 — MET: An end-to-end gateway test drives forty real chat completions whose prompt carries a sentinel, with a bearer token and a LAN address, forces the drop that writes the summary, then byte-scans summary.jsonl for the sentinel, the token, the address and the model's own reply, and additionally holds every key in the file to the closed field set; the Summary type itself is counts, totals and a repo id only
  evidence: internal/gateway/control_store_test.go:341 — ""messages":[{"role":"user","content":"`+sentinel+`"}]"
  evidence: internal/gateway/control_store_test.go:368 — "for _, secret := range []string{sentinel, token, "192.0.2.51", "GROPIUS OK"} {"
  evidence: internal/gateway/control_store_test.go:365 — "the summary does not name the model whose requests it counts, so this scan proves nothing"
  evidence: internal/stats/summary_test.go:313 — "for _, gone := range []string{`"streamed"`, `"first_token_ms"`, `"duration_ms"`, `"queue_wait_ms"`} {"
  evidence: internal/stats/summary.go:85 — "type Summary struct {"
- ac-5 — MET_WITH_CONCERNS: The Statistics tab does show the per-model daily totals for summary-only days and does say the detail is gone — SummaryDay.DetailHeld is computed at read time, /api/stats carries the days, and the Earlier days table renders day, model, requests and tokens with a 'gone' marker and the sentence 'The detailed records for these days are no longer held'; the concern is where it is shown
  evidence: internal/stats/summary.go:796 — "fn(SummaryDay{Summary: *all[i], DetailHeld: detailHeld(oldest, all[i], s.opts.loc)})"
  evidence: internal/gateway/control.go:301 — "view.Summaries = append(view.Summaries, d)"
  evidence: internal/ui/static/app.js:1070 — "The detailed records for ${gone === days.length ? 'these days' : ...} are no longer held"
  evidence: internal/ui/static/app.js:1086 — "< td>${d.detail_held ? 'partly kept' : 'gone'}< /td>"
  evidence: internal/ui/static/index.html:153 — "< h2>Earlier days< /h2>"
  evidence: internal/ui/static/index.html:110 — "< p>Nothing was recorded in this range.< /p>"
  evidence: internal/stats/dashboard.go:558 — "a.addTokens(when.Format("2006-01-02"), r.Model, 1, int64(r.PromptTokens), int64(r.CompletionTokens), false)"
- ac-6 — MET: The reference page's retention section states that a file's records are folded into summary.jsonl before it is removed, one line per model per day, what that line holds, that an already-summarised day is extended, and what bounds the summary; the user-facing page says the same in plain words and names the Earlier days table
  evidence: docs/statistics-store-reference.md:183 — "Before a file is removed, whichever limit removes it, its records are folded into `summary.jsonl`: one line per model per day, holding the counts and totals described above and nothing else."
  evidence: docs/statistics-store-reference.md:189 — "A day already summarised is extended rather than written a second time"
  evidence: docs/statistics-store-reference.md:128 — "## `kind: "summary"` — one model's day, after its detail has gone"
  evidence: docs/request-statistics.md:90 — "What is dropped is counted before it goes. For every model and every day among the records being removed, Gropius keeps one short line"
  evidence: internal/archtest/statistics_docs_test.go:81 — "func TestTheStatisticsPageNamesEveryFieldTheStoreWrites(t *testing.T) {"
  evidence: internal/archtest/statistics_docs_test.go:238 — "func TestTheSummarySizeIsTheSameFigureEverywhere(t *testing.T) {"

Gap audit:
- honoured:
  - A short summary is written before the oldest records go: for each model, for each day, how many requests it served and how many tokens it produced
    evidence: internal/stats/summary.go:384 — "func (w *storeWriter) fold(doomed []storeFile) error {"
    evidence: internal/stats/store.go:1256 — "Counted before it goes, and on disk before it goes: this is the whole of itd-2609061602043757."
    evidence: internal/stats/summary_test.go:135 — "want one for each of two models on each of three days"
  - The summary outlives the detail it replaces — it is not removed by the months horizon and is bounded only by a twentieth of the size cap, losing its oldest days first
    evidence: internal/stats/summary.go:59 — "const summaryShare = 20"
    evidence: internal/stats/summary.go:503 — "for int64(len(b)) > share && set.dropOldestDay() {"
    evidence: docs/statistics-store-reference.md:192 — "Summaries are not removed by the months limit — a summary of last spring is what makes the shape of last spring's use visible once its detail is gone."
  - The summary holds nothing the detailed records did not — no prompt, completion, key or client address — and stays under the same opt-in
    evidence: internal/gateway/control_store_test.go:368 — "for _, secret := range []string{sentinel, token, "192.0.2.51", "GROPIUS OK"} {"
    evidence: internal/stats/summary_test.go:261 — "t.Error("a summary was written while recording was off")"
  - A crash between the summary and the drop costs neither: the index at the head of the file resolves it and nothing is counted twice
    evidence: internal/stats/summary.go:583 — "func (w *storeWriter) reconcileSummary() error {"
    evidence: internal/stats/summary_test.go:321 — "func TestACrashBetweenTheSummaryAndTheDropCountsTheRecordsOnce(t *testing.T)"
- diverged:
  - The press release says of the oldest records, without qualification, that 'what she still has is a short summary written before they went'. Shipped, the summary can fail to be written — a full disk, a file planted under its name, a folder Gropius may no longer read — and once the store is over its size cap by more than one rotation file's growth the oldest file is removed with no summary at all. I reach the same divergence the intent's Audit Notes record and agree with it; I also verified the second half of that note independently: Alice is in fact told. The store's own log names the file, its bytes and the records lost, StoreStatus carries the wedge and a separate Unsummarized count, and the Settings page renders both in the store line
    evidence: internal/stats/store.go:1319 — "func (w *storeWriter) dropUnsummarized(maxBytes int64) int {"
    evidence: internal/stats/store.go:1332 — "a statistics file was removed without being summarized first: the store is over its size limit and cannot summarize what it drops"
    evidence: internal/stats/store.go:237 — "Unsummarized int64 `json:"unsummarized"`"
    evidence: internal/ui/static/app.js:913 — "what is being dropped cannot be summarised first — the records are kept while there is room for them, and once there is not the oldest go without a summary (its own log says why)"
    evidence: internal/ui/static/app.js:917 — "parts.push(`${store.unsummarized} removed without a summary`)"
    evidence: internal/stats/summary_test.go:1151 — "func TestTheSizeLimitStillWinsWhenTheSummaryCannotBeWritten(t *testing.T)"
  - 'A year on she can still see that last spring's work ran mostly on one model' is delivered through a separate always-listed 'Earlier days' table rather than inside the view where a span is actually chosen. The 'Over time' range picker (7/30/90/everything) is fed by an aggregation over records only, so a range whose detail has all been summarised renders the empty state 'Nothing was recorded in this range' with the summarised days shown in the block below it instead
    evidence: internal/ui/static/index.html:100 — "< label for="statsRange">Range< /label>"
    evidence: internal/gateway/control.go:351 — "var historySource = func(a *app.App) stats.RecordSource { return a.StatsStore }"
    evidence: internal/ui/static/app.js:1271 — "const anything = days.length > 0 || (h.latency || []).length > 0"
    evidence: internal/ui/static/index.html:110 — "< p>Nothing was recorded in this range.< /p>"
    evidence: internal/ui/static/app.js:1064 — "function renderStatsSummary(days) {"
  - The Mechanism's 'roughly a thousandth of the bytes the detail occupied' is repeated unqualified on the user-facing page, while the reference page states the measured figure instead (800 bytes a line, three years of daily lines for ten models at the 200 MB default). The intent's own Audit Notes record that the ratio is better than a thousandth on a busy Mac and worse on a quiet one
    evidence: docs/request-statistics.md:96 — "it takes a thousandth of the room the records did"
    evidence: docs/statistics-store-reference.md:197 — "A summary line is at most 800 bytes, so at the 200 MB default the summary's twentieth holds 3 years of daily lines for ten models"
    evidence: internal/stats/summary.go:71 — "const ApproxSummaryBytes = 800"
- missing:
  - The dashboard's own history aggregation never reads the summaries. DayTokens.FromSummary and its 'from daily totals' pill exist and are exercised only by tests; the sole production call site passes fromSummary false, and the field's own comment still says it is 'false throughout while nothing writes those summaries' — a sentence the shipped fold makes stale
    evidence: internal/stats/dashboard.go:117 — "It is false throughout while nothing writes those summaries."
    evidence: internal/stats/dashboard.go:558 — "a.addTokens(when.Format("2006-01-02"), r.Model, 1, int64(r.PromptTokens), int64(r.CompletionTokens), false)"
    evidence: internal/ui/static/app.js:1344 — "const coarse = d.from_summary ? ' < span class="pill">from daily totals< /span>' : '';"

Scope-condition dispositions:
- cond-2609061822385374 — narrowed: The fold is only ever handed the files retention has already chosen to drop, so nothing live is summarised; but the assumption does not hold as an invariant over time, because a summarised file whose removal fails or is interrupted stays on disk already counted
  narrowing: Holds at the moment of the fold — only the doomed files are folded — not for the whole life of a record: a file whose removal fails, or a crash after the rename, leaves detail on disk that the summary already counts. Such a file is marked folded so nothing appends to it and the next fold skips it, and the startup pass removes what the index names, so it is never counted twice; but there is a window in which a record is both summarised and still held
  evidence: internal/stats/store.go:1267 — "if err := w.fold(w.files[:doomed]); err != nil {"
  evidence: internal/stats/store.go:1299 — "f.folded = true"
  evidence: internal/stats/store.go:1061 — "Never a file already counted in the summary: appending to one would have the next fold count what was appended a second time."
  evidence: internal/stats/summary.go:31 — "A crash in the middle therefore leaves a summary that counts a file still present, which the index at the head of the file resolves"
- cond-2609061822381938 — survived: Every field of the shipped Summary is a count, a sum over a whole day, or the model's repo id, and a test fails the build on any key outside that closed set or on any per-request field name appearing in the file
  evidence: internal/stats/summary.go:85 — "type Summary struct {"
  evidence: internal/stats/summary.go:111 — "A total and a count is what a mean is made of; a percentile would need the records themselves, which is precisely what is gone."
  evidence: internal/stats/summary_test.go:308 — "the summary line carries %q, which is not one of its documented fields"
  evidence: internal/stats/summary_test.go:314 — "the summary carries %s, which is a per-request figure"
- cond-2609061822385249 — narrowed: Same opt-in, same place and same form all hold — summary.jsonl sits in the store's own directory as JSON lines carrying v and kind like every other line, nothing is folded with recording off, and Clear takes it with the records; the last clause holds under its privacy reading but not literally
  narrowing: Holds for everything a client or a request could put there — the end-to-end scan proves no prompt, key, address or reply reaches the file — but the summary carries two fields no detailed record carries: the local day and the UTC offset in force on it, derived from this Mac's zone rather than from any record, which stores only whole UTC seconds
  evidence: internal/stats/summary.go:47 — "const summaryFileName = "summary.jsonl""
  evidence: internal/stats/summary.go:93 — "Day string `json:"day"`"
  evidence: internal/stats/summary.go:98 — "TZOffsetMin int `json:"tz_offset_min"`"
  evidence: internal/stats/stats.go:117 — "At is when the request arrived, in whole UTC seconds."
  evidence: internal/stats/summary_test.go:419 — "func TestTheSummaryIsOwnerOnlyAndClearRemovesIt(t *testing.T)"
- cond-2609061822380528 — survived: The summary is created inside the store's own root, owner-only and following no link, and that root is the one thing Gropius keeps under the account's own data folder rather than the shared cache, with every ancestor checked for group- and other-write; nothing in the delivery contradicts the one-serving-process half — the fold simply tolerates a file that vanished from under it rather than coordinating with a second writer
  evidence: internal/stats/summary.go:565 — "f, err := w.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0o600)"
  evidence: internal/stats/store.go:919 — "In shared-cache mode the layout is created under the shared root, and this store is the one thing Gropius keeps under the account's own"
  evidence: internal/stats/store.go:930 — "return os.Mkdir(dir, 0o700)"
  evidence: internal/stats/summary.go:402 — "Gone from under the writer: deleted by hand, restored from a backup, or taken by another process on the same directory."
## Grounds

- pursued: we want to learn how local models are actually used on this Mac, which models, how many tokens, what latencies, and we expect a month of records to change which models we keep and how we set the memory budget; we are wrong if, after a month with the dashboard, no such decision has changed
