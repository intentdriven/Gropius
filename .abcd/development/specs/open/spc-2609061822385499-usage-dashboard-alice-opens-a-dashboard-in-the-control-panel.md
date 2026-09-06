---
id: spc-2609061822385499
slug: usage-dashboard-alice-opens-a-dashboard-in-the-control-panel
intent: itd-2609061521159233
origin: researcher-authored
production_mode: hand-written
---
# usage-dashboard-alice-opens-a-dashboard-in-the-control-panel

## Summary

The control panel's Statistics view gains three historical tables over a
chosen range: tokens per day by model with each model's share, request latency
by model, and evictions and reloads by hour of the local day. They are
computed in Go by a single pass over the durable statistics store and served
over the loopback-only control plane as aggregates, never as records. Tables
only in this cut: no chart code, no charting library, no new dependency.

## Scope

In scope:

- An aggregate reader in `internal/stats` that walks the store's files for a
  date range and returns per-model, per-day and per-hour totals.
- A loopback-only endpoint on the control plane returning those aggregates.
- Three tables plus a range selector (7, 30, 90 days and all) in the existing
  Statistics view, and an empty state.
- A benchmark for the single-pass cost at the store's size cap.
- An explanation page describing each view and what it cannot show.

Out of scope:

- Writing anything. The dashboard reads the store and nothing else, so
  nothing appears that was not recorded under the opt-in.
- Charts of any kind, and any vendored charting library. Sign-off for one is
  not requested.
- Export; the store's files are already the export.
- Any per-session or per-person breakdown; the telemetry-pack intent is
  retired.
- Correlating the figures with a change of memory budget, which needs the
  budget setting itd-2609061441261073 introduces.
- Renaming the view. The recorder named it Statistics; this spec extends it.

## Approach

Aggregation runs in Go, in the store's own package, as
`Aggregate(from, to time.Time, loc *time.Location) (Aggregates, error)`. It
walks the store's files in name order, skips a file whose name-encoded date
falls wholly outside the range, decodes each line, and buckets by local day,
model and hour of day; a line that does not parse is skipped, as every reader
of this store does. Summary lines from itd-2609061602043757 supply days whose
detail has been dropped, and those days are marked summary-only so a view can
say so.

Caching. Aggregates are cached per closed file, keyed by name and size, and
the active file is scanned live on every request. Keying the cache on the
whole store's file set would invalidate continuously, because the active file
grows with every record while the switch is on.

The endpoint is registered in `internal/gateway/control.go`'s `Routes` and
sits behind the existing `loopbackOnly` guard, which already checks the remote
address, the Host header and the Origin header. It takes a range and returns
aggregates only: counts, sums, buckets and model ids. No record, no header, no
address ever appears in the body.

The three tables.

- Tokens per day by model: for each day in the range, each model's prompt plus
  completion tokens, with the model's share of the range's total beside it.
- Latency by model: for each model, the median, ninetieth and ninety-ninth
  percentiles of time to first token and of generation rate, over the requests
  in the range, with a bucket histogram beside them so the spread is visible
  in a table.
- Evictions and reloads by hour: for each hour of the local day, the count of
  eviction records whose reason is "evicted" and of load records. A crash or
  an idle reap is not an eviction, which is why the store records a reason.

Rendering. `app.js` fetches the aggregates when the Statistics view becomes
visible and on a range change, never on the two-second event tick, and draws
three tables under the live view. Every value goes through the existing
`escapeHtml`, model ids included, since a repo id is a name the store took
from a directory. With the switch never turned on, or an empty store, the
view states that nothing is recorded, links to Settings, and shows no figure.

Performance. The two-second bound is held as a benchmark over a synthetic
store at the size cap, run by hand on Apple Silicon at release, and its number
is recorded against adr-2609061610107154. It is deliberately not a test
assertion: continuous integration runs on shared machines whose wall-clock
varies run to run, and a flaky performance test is exactly the half-maintained
statistics the research note warns cost trust. The acceptance tests are
correctness tests over a fixture.

## How each acceptance criterion is satisfied

- "Given a fixture of records for two models over three days, when Alice views
  tokens per day, then each day's per-model figure equals the sum of that
  day's prompt and completion tokens for that model." A reader test builds the
  fixture store and asserts each cell against the sum computed independently
  in the test.
- "Given that fixture, when she views model share, then each model's share
  equals its tokens as a fraction of the range's total." The same test asserts
  the shares sum to one within rounding and that each matches its model's
  fraction.
- "Given a fixture whose requests have known durations, when she views the
  latency table, then the reported distribution matches the fixture." A test
  with known times to first token and durations asserts each percentile and
  each histogram bucket against the fixture.
- "Given a fixture with eviction and reload events at known hours, when she
  views the eviction and reload table, then each hour's count matches the
  fixture in the Mac's local time." A test fixes a location, writes events at
  known UTC seconds either side of a local midnight, and asserts the hourly
  counts; a second run in a different location asserts the buckets move with
  it. Only records whose reason is "evicted" are counted.
- "Given statistics have never been on, when she opens the view, then it
  states that nothing is recorded and shows no figure." A control test with no
  store directory asserts the endpoint returns an empty result and the view
  renders the sentence and the link to Settings.
- "Given a request for the aggregates that arrives from anywhere but the Mac
  itself, when it is handled, then it is refused." A control test invokes the
  handler with a crafted non-loopback remote address, and again with a foreign
  Host and a foreign Origin, asserting each is refused by the existing guard;
  a fourth asserts a successful body carries only aggregate fields.
- "Given the documentation, when a reader opens the page for these views, then
  each shipped view is described together with what it cannot show." A
  documentation test asserts the page names all three views; the prose states
  that nothing recorded while the switch was off appears, that the horizon is
  the store's cap, that days beyond it show summary totals only, and that the
  figures are the model server's token counts and Gropius's own timings.

## Trust-boundary review notes

- `internal/gateway`: one new endpoint on the control plane, inheriting
  `loopbackOnly`. It accepts two timestamps and nothing else — no path, no
  file name, no model filter that could be turned into a path — and returns
  aggregates. The refusal test covers the remote address, the Host header and
  the Origin header, because loopback includes other local accounts.
- The store package as a reader: files are opened without following symlinks
  and read with the same size discipline as the writer, and unparsable lines
  are skipped rather than failing the request, so a corrupt or truncated file
  cannot take the panel down.
- Rendering: model ids reach the browser and pass through `escapeHtml`, as
  every other model-derived string in the panel does.
- Nothing this spec adds writes to the store, so the opt-in's guarantee is
  unchanged: the dashboard can only show what was already recorded.

## Docs to change

- `README.md`: one line under Features saying the panel shows historical use
  per model from the local store.
- `docs/getting-started.md`: a sentence in the statistics walk-through
  pointing at the explanation page.
- A new explanation page under `docs/`: what each of the three views means,
  what it cannot show, that the range is bounded by the store's retention, and
  that days beyond the detail show summary totals only.

## Dependencies and sequencing

- Builds on itd-2609061521102742 (the store, including the reason on eviction
  records and the startup settings record) and through it itd-2609061521082551
  (the recorder and the view this spec extends). Both ship first.
- Binding: adr-2609061610107154 and adr-2609061503319212.
- itd-2609061602043757 supplies the summary-only days; if it has not shipped,
  those days are simply absent.
- Gate before implementation: one month of the maintainer's own store records
  aggregated by hand with a command-line tool, whose output is the fixture for
  the tokens-per-day and share criteria. It is the cheaper test of the same
  expectation and it comes first.
- itd-2609061441261073 is needed only for the budget correlation, which this
  spec does not ship.

## Open design points

- The default range; 30 days is the starting choice.
- The latency histogram's bucket edges, and whether the rate percentiles are
  weighted by completion tokens.
- Whether the per-file aggregate cache is bounded, and by what.
