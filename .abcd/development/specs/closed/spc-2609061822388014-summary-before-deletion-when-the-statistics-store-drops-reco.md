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

## As built (2026-09-07)

Five places where the shipped code parts from the Approach above. A closed
spec is not edited, so they are listed here and argued in the dated
`.abcd/work/DECISIONS.md` line of the same date; none of them touches an
acceptance criterion.

1. **The summary line carries more than the fields named above.** Beside
   `requests`, `prompt_tokens` and `completion_tokens` it holds `by_class`
   (the outcome-class counts), the four latency sums
   (`duration_ms_total`, `queue_wait_ms_total`, `load_wait_ms_total`,
   `first_token_ms_total`) with `first_token_requests` as the divisor the last
   is a mean over, and `loads`, `failed_loads` and `removals` — which resolves
   the third open design point in favour of folding events. Every one is a
   count or a total over a whole day, so the intent's "coarse by construction,
   never a per-request figure" holds; the byte-scan criterion is written
   against this closed set.
2. **The summary is not removed by the months horizon**, only by its share of
   the size cap: a twentieth of `stats_max_bytes`, oldest days first. The
   intent's press release is a year on from the period it describes and the
   default horizon is six months, so a summary pruned by the horizon would go
   exactly when it became the only thing left.
3. **The most recent day is never dropped from the summary.** On a cap small
   enough that one day's lines and the index together exceed a twentieth of
   it, a summary that emptied itself would still take room while saying
   nothing.
4. **The index fingerprints a folded file by name, size and newest record**
   rather than by name alone, and the startup pass removes only names matching
   the store's own pattern. A store the horizon has emptied can hand a new file
   the name of one that is gone, and a pass keyed on the name alone would
   delete records nothing had counted.
5. **The fold runs once per retention pass rather than once per file**, and
   the pass is applied in a loop, because folding what is dropped makes the
   summary larger and the summary counts toward the cap.

The second open design point is answered by the reader rather than by a mark
in the data: every day `FileStore.Summaries` yields is a day whose detail has
been dropped, and where a day appears in `Summaries` and `Latest` both the
detail is what is still held and the summary is what is not — the fold never
subtracts, so a caller adds the two.
