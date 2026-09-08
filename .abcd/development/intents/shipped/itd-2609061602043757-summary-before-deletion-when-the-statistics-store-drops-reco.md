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

<!-- abcd-review: OWED receipt=rcp-849f518f44a0 -->
Fidelity review OWED (receipt rcp-849f518f44a0). The audit compares this
promise against what was delivered, so it runs after the branch merges; the
receipt's request lives in the per-machine tier and does not travel, so
whoever runs it re-emits the request with `abcd intent audit`.

**2026-09-07.** What shipped diverges from the spec's Approach in five places
and answers two of its three open design points: see the spec's "As built"
section and the 2026-09-07 lines in `.abcd/work/DECISIONS.md`, which also carry
the three review rounds this change went through. None of those five touches an
acceptance criterion.

**Diverged, at the level of this intent's press release.** The press release
says of the oldest records that "what she still has is a short summary written
before they went". That is what happens, except in one case the press release
does not imagine: when the summary itself cannot be written — a full disk, a
file planted under its name, a folder Gropius may no longer read — and the
store is over the size limit `adr-2609061610107154` makes the hard bound. The
first answer is to keep the records rather than delete them uncounted, and the
store says on the Settings page that its limits are not being applied. But that
cannot be the last answer: a full disk is exactly what stops a summary being
written, so a store that would not then free its own room would make a full
disk permanent. Past the limit by more than one file's growth, the oldest file
goes without a summary; the log names it, and Settings counts the records lost
that way separately from every other figure. Alice can therefore, in that one
case, lose a period with neither its detail nor its summary — and she is told
so rather than left to find out. Flagged here rather than quietly closed,
because the promise is the maintainer's to release. The reasoning is the
2026-09-07 ledger line answering the independent security review.

**Also worth the maintainer's eye.** The intent's Mechanism reasons that a
summary is "roughly a thousandth of the bytes the detail occupied". Measured,
it is better than that on a busy Mac and worse on a quiet one: a day of ten
thousand requests is about 2.3 MB of detail against one line per model, so a
thousandth is about right at ten models, while a Mac serving a hundred requests
a day collapses 23 KB into the same line. The claim's conclusion — that the
summary can outlive the detail by years at negligible cost — holds either way;
the reference page states the measured figure rather than the ratio.


2026-09-08 — **Adopted.** The maintainer adopts the press-release divergence:
the promise that a short summary outlives the detail does not hold in the one
case the press release does not imagine, where the summary itself cannot be
written and the store is over its hard bound. Past the limit the oldest file
goes without a summary, and Alice can lose a period with neither its detail nor
its summary.

The adoption rests on the disclosure, which an independent review (receipt
rcp-849f518f44a0) verified rather than took on trust: the log names the file,
the bytes and the records lost (`internal/stats/store.go:1332`), and Settings
renders both the sentence saying the limits are not being applied and a separate
count of records removed without a summary (`internal/ui/static/app.js:913`,
`:917`). A promise that degrades loudly on a full disk is the promise being
adopted; a silent one would not be.
## Grounds

- pursued: we want to learn how local models are actually used on this Mac, which models, how many tokens, what latencies, and we expect a month of records to change which models we keep and how we set the memory budget; we are wrong if, after a month with the dashboard, no such decision has changed
