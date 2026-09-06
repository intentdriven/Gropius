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

_Empty. Populated by intent-auditor when intent moves to shipped/._

## Grounds

- pursued: we want to learn how local models are actually used on this Mac, which models, how many tokens, what latencies, and we expect a month of records to change which models we keep and how we set the memory budget; we are wrong if, after a month with the dashboard, no such decision has changed
