---
id: spc-2609061822388014
slug: summary-before-deletion-when-the-statistics-store-drops-reco
intent: itd-2609061602043757
origin: researcher-authored
production_mode: hand-written
---
# summary-before-deletion-when-the-statistics-store-drops-reco

## Summary

Before the statistics store removes a file that has reached its retention
limit, it folds that file's request records into a coarse summary: one line
per model per day, carrying a request count and token totals and nothing
else. The summary is kept in the same directory, under the same opt-in, in the
same format and with the same permissions as the store it summarises, and it
outlives the detail by years at a few hundred bytes a day. A month for which
only summaries remain still shows its per-model daily totals, and says that
the detail for it is gone.

## Scope

In scope:

- A summary file in the store directory, written only from records the store
  is about to drop.
- A fold that extends an existing model-and-day line rather than writing a
  second one, and that cannot double-count a file across a crash.
- The summary's own bound within the store's size cap.
- Marking a period as summary-only wherever the store's aggregates are read,
  so a view can say the detail is gone.
- The retention paragraph in the store's reference page.

Out of scope:

- Any summarising while the detail is still held. Nothing is summarised on a
  schedule; the only trigger is a drop.
- Any figure the detailed records did not already hold: no per-request value,
  no latency, no prompt, completion, key or client address.
- The layout of the views themselves, which are itd-2609061521159233's.
- A second opt-in. The statistics switch gates this exactly as it gates the
  store.

## Approach

The store's retention step today removes a whole file once the total exceeds
the size cap or the file's newest record is older than the months figure. This
spec puts a fold in front of that removal.

The summary lives in `summary.jsonl` in the store directory, opened with the
same create, no-follow, non-blocking, owner-only discipline as every other
store file. Its first line is a `kind: "summary_index"` record listing the
names of the detail files already folded in. Each following line is
`{"v", "kind": "summary", "day", "tz_offset_min", "model", "requests",
"prompt_tokens", "completion_tokens"}` and nothing else — the fields the
criterion's byte scan is written against.

Days are local days, with the offset that was in force recorded beside them,
because the views bucket by the Mac's local day and a summary that disagreed
with the detail either side of the boundary would be worse than useless. The
store's timestamps stay UTC seconds, as adr-2609061610107154 fixes them.

The fold is a read-modify-rename, not an append, because a day's line must be
extended rather than repeated when a later drop removes more records for the
same day. Drops are rare and the file is small, so the whole file is read into
a map keyed by day and model, updated with the counts from the file about to
go, and written to a temporary file created in the same directory with
`os.CreateTemp` (random name, exclusive create, mode 0600, as `config.Save`
already does), then renamed into place. Only after the rename succeeds is the
detail file removed.

That order is what makes the fold crash-safe in the right direction: a crash
between the rename and the removal leaves a summary that already counts a file
still present. The `summary_index` closes it — the retention step folds a file
only when its name is absent from the index, and a startup pass removes any
file the index names that is still on disk. A crash before the rename leaves
the summary and the detail both untouched, and the next retention pass folds
the file again from scratch.

The summary's own bound: it counts toward the store's size cap and is never
chosen as the file to drop, but if it ever exceeds a twentieth of the cap its
oldest days are dropped, oldest first, so a decade of daily lines cannot crowd
out the detail. Clear removes the summary with everything else.

Reading. The store's aggregate reader already walks the detail files for a
range; it now also reads the summary lines that fall inside the range and
marks each day it supplies as summary-only, so the caller can render the
totals and state that the detail for that day is gone. Where a day has both —
a partial drop — the detail is authoritative for the records still held and
the summary supplies the rest, which is exactly why the fold subtracts
nothing: a summary line always describes records that no longer exist.

## How each acceptance criterion is satisfied

- "Given a store holding records for two models over three days, when
  retention drops the oldest of those records, then a summary line for each
  model and each day among them is written first, with request counts and
  token totals equal to the records dropped." A test builds that store with a
  tiny cap, forces a drop, and asserts one line per model per day with counts
  and totals equal to the sum of the dropped file's records.
- "Given a summary already written for a day, when a later drop removes more
  records for that same day, then the existing summary is extended to cover
  them rather than written a second time." The same test forces a second drop
  covering the same day and asserts one line still exists for that model and
  day, with counts equal to both drops together.
- "Given the switch is off, when Gropius runs, then no summary is written." A
  test with the switch off runs requests and a retention pass and asserts no
  summary file exists; retention itself only runs while the writer runs.
- "Given a request whose prompt carries a sentinel string, when its record is
  summarised and dropped, then a byte scan of the summary finds nothing beyond
  the counts and totals for its model and day." A test drives such a request
  through the fake model server, forces the drop, and asserts the summary
  file's bytes contain no sentinel and no key outside the documented set.
- "Given a month for which only summaries remain, when Alice looks at that
  month, then it shows the per-model daily totals and states that the detail
  for it is gone." A reader test over a store whose detail for a month has
  been dropped asserts the aggregates carry that month's per-model daily
  totals and that each such day is marked summary-only; the view renders that
  mark as a sentence.
- "Given the documentation, when a reader looks up retention, then it states
  that a coarse per-model per-day summary is kept when detail is dropped, and
  what that summary holds." The reference page's field table gains the summary
  record, and the existing documentation test that compares the table with the
  record structs covers it.

## Trust-boundary review notes

- The store's file handling is the boundary this spec touches. The fold adds a
  temporary-file create and a rename inside the store directory: the temporary
  name is random and created exclusively, so a planted name cannot be written
  through, and the rename is within one directory. The removal it precedes is
  unchanged and still restricted to the store's own name pattern.
- A crash mid-fold must never double-count and never lose the detail silently.
  The rename-then-remove order plus the `summary_index` is the mechanism, and
  a test kills the fold between the two steps and asserts the startup pass
  reconciles it.
- `internal/config`: nothing new. No setting is added; the months figure and
  size cap already exist.
- `internal/gateway`: Clear covers the summary file, and nothing about the
  summary is reachable other than over the loopback control plane.
- Privacy: the summary can hold nothing the detail did not, and holds far
  less. The documented field set is closed, and the byte-scan test is what
  keeps it closed.

## Docs to change

- `docs/getting-started.md`: the retention sentence says that when detail is
  dropped a coarse per-model per-day summary is kept.
- The store's reference page under `docs/`: the summary record's fields, the
  local-day rule, that summaries are extended rather than duplicated, that
  they are bounded within the size cap, and that Clear removes them too.
- `README.md`: no change; the feature line the store added already covers it.

## Dependencies and sequencing

- Builds on itd-2609061521102742, whose retention step this spec hooks into;
  that spec ships first.
- Binding: adr-2609061610107154, which names this summary as what preserves
  history beyond the cap, and adr-2609061503319212 for the opt-in and the
  content rules.
- itd-2609061521159233 renders summary-only periods; if the dashboard has not
  yet shipped, the statistics view the recorder added shows the totals and the
  sentence.

## Open design points

- The summary's share of the size cap; a twentieth is the starting figure.
- Whether a day that is partly dropped is marked summary-only or partial in
  the aggregates the reader returns.
- Whether the fold also summarises load and eviction events, which the intent
  does not ask for and this spec does not do; the counts and token totals are
  the whole of it.
