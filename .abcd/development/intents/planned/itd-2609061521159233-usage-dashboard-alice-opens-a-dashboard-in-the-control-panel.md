---
id: itd-2609061521159233
slug: usage-dashboard-alice-opens-a-dashboard-in-the-control-panel
spec_id: spc-2609061822385499
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609061521102742]
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Usage dashboard: Alice opens a dashboard in the control panel that reads the durable statistics store and shows token use, latency and model choice over days and months, so she can learn how local models are actually used on her Mac

## Press Release

Alice opens the statistics view in the control panel and picks the last month.
She gets three tables. The first gives tokens per day for each model, with
each model's share of the total. The second gives how long requests took,
spread across the range, model by model. The third gives when models were
evicted and reloaded, hour by hour. She learns that the model she thought was
her workhorse serves a fifth of the tokens, and that most evictions fall in
one hour of the day. Nothing appears that was not recorded while she had
statistics on.

## Why This Matters

The reason for keeping local statistics at all is to learn how local models
are actually used. The store (itd-2609061521102742) keeps the records; this is
what turns them into that learning. It reads the store and nothing else, so it
is honest about its input: nothing appears that was not recorded under the
opt-in adr-2609061503319212 requires. It is filed now so the store's design
knows its consumer, and planned once the recorder and the store exist and have
something to show.

Evidence: research note 2026-09-06-model-bench-evidence (the comparisons a dashboard should make possible: local against hosted per model, quantisation against quantisation, swap cost against concurrency).

## Mechanism

We expect on-demand aggregates over the store to answer which model carries
the work and when evictions cluster because those questions are sums and
counts grouped by model, day and hour, and a single sequential pass over a
store at the default size limit yields them within two seconds on Apple
Silicon.

## Scope Conditions

- The store on the same Mac, read over the control plane that only that Mac <!-- cond: cond-2609061822388443 -->
  can reach.
- Records still within the store's retention; beyond it only the coarse <!-- cond: cond-2609061822380954 -->
  per-model per-day summaries survive.
- One operator reading their own Mac's use; the views are per model and per <!-- cond: cond-2609061822383763 -->
  period, never per person.
- The figures are only as good as what was recorded: the model server's token <!-- cond: cond-2609061822385281 -->
  counts and Gropius's own timings.

## Acceptance Criteria

- Given a fixture of records for two models over three days, when Alice views
  tokens per day, then each day's per-model figure equals the sum of that
  day's prompt and completion tokens for that model.
- Given that fixture, when she views model share, then each model's share
  equals its tokens as a fraction of the range's total.
- Given a fixture whose requests have known durations, when she views the
  latency table, then the reported distribution matches the fixture.
- Given a fixture with eviction and reload events at known hours, when she
  views the eviction and reload table, then each hour's count matches the
  fixture in the Mac's local time.
- Given statistics have never been on, when she opens the view, then it states
  that nothing is recorded and shows no figure.
- Given a request for the aggregates that arrives from anywhere but the Mac
  itself, when it is handled, then it is refused.
- Given the documentation, when a reader opens the page for these views, then
  each shipped view is described together with what it cannot show.

## Open Questions

- Resolved: which views ship — all three in the first cut: tokens per day by
  model with model share; latency distributions; and the eviction and reload
  timeline.
- Resolved: how they are drawn — tables only in the first cut, with no chart
  code, so no charting library and no new dependency is asked for.
- Resolved: where they live — in the control panel's existing statistics view,
  extended rather than given a second surface, so the name that view already
  carries is the one users learn.
- Resolved: the input — the durable store only; nothing is shown that was not
  recorded under the opt-in.
- Resolved: no per-session breakdown, since the telemetry pack intent
  (itd-2609061521134968) is retired.
- Resolved: correlating the numbers with a change of memory budget is out of
  this intent; it needs a budget setting that itd-2609061441261073 introduces.
- Deferred: the two-second render bound is held as a benchmark and checked by
  hand at release rather than asserted in a test on shared build machines,
  where a wall-clock assertion would flake.
- Deferred: how far back a single view reads, and how aggregates are cached
  between requests, are the spec's.
- Deferred: hand-aggregating a first month of records with a command-line tool
  is a cheaper test of the same expectation and may be run before this intent
  is planned.
- Depends on: itd-2609061521102742, the store this view reads, and through it
  itd-2609061521082551, the recorder that fills it.
- Depends on: adr-2609061610107154 (store format and retention) and
  adr-2609061503319212 (no public telemetry; local only; strict opt-in).

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._

## Grounds

- pursued: we want to learn how local models are actually used on this Mac, which models, how many tokens, what latencies, and we expect a month of records to change which models we keep and how we set the memory budget; we are wrong if, after a month with the dashboard, no such decision has changed
