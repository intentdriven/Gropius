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

<!-- abcd-review: INGESTED receipt=rcp-7d79f2254a0d -->
Fidelity review — receipt rcp-7d79f2254a0d (verifier abcd:intent-auditor claude-opus-5[1m]).

Provenance: abcd:intent-auditor@claude-opus-5[1m] · rubric_hash sha256:e7648463491f667fff8c9d938501d8d63ab9f2de586f391a3179595eba4c3f56 · prompt_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5
Input attestations: tree:worktree at HEAD (main) — history rewritten during the rename, so no per-spec commit range exists; the tree as shipped was audited@sha256:18f1a4e286fd85f6e94bb2ae1d2ecba40e13cc77; request:.abcd/.work.local/reviews/rcp-7d79f2254a0d.request.md@sha256:e7648463491f667fff8c9d938501d8d63ab9f2de586f391a3179595eba4c3f56; prompt:/Users/dev/.claude/plugins/marketplaces/abcd-marketplace/agents/intent-auditor.md@sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5;

Acceptance rollup: MET 5 · MET_WITH_CONCERNS 2 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: A six-record fixture over two models and three local days is aggregated and every day/model cell is asserted against a sum written out by hand in the test, not against a second run of the same code; the panel renders those same fields per row.
  evidence: internal/stats/dashboard_test.go:48 — "func TestTokensPerDayAndShareAreTheFixturesOwnSums(t *testing.T) {"
  evidence: internal/stats/dashboard_test.go:74 — "{Day: "2026-09-01", Model: "org/alpha", Requests: 2, PromptTokens: 150, CompletionTokens: 225},"
  evidence: internal/stats/dashboard.go:132 — "func (d DayTokens) Tokens() int64 { return addTokens64(d.PromptTokens, d.CompletionTokens) }"
  evidence: internal/ui/history_test.go:16 — "func TestTheTokensPerDayRowShowsTheDaysFiguresAndTheRangesShare(t *testing.T) {"
- ac-2 — MET: The same fixture test asserts each model's share as its tokens over the range's total (378/703 and 325/703, computed in the test) and separately asserts the two shares add to one within rounding.
  evidence: internal/stats/dashboard_test.go:86 — "{Model: "org/alpha", Requests: 3, PromptTokens: 151, CompletionTokens: 227, Tokens: 378, Share: 378.0 / 703.0},"
  evidence: internal/stats/dashboard_test.go:93 — "if got := h.Models[0].Share + h.Models[1].Share; got < 0.999999 || got > 1.000001 {"
  evidence: internal/stats/dashboard.go:151 — "Share is Tokens over the range's total tokens, between 0 and 1."
- ac-3 — MET_WITH_CONCERNS: A fixture of ten answers with known first-token times (100..1000 ms) and a fixed 100 tok/s rate asserts every percentile and every histogram bucket against figures readable off the fixture, and excludes a refusal and a non-streamed request from the distribution; the concern is that the criterion's single 'latency table' ships as two tables.
  evidence: internal/stats/dashboard_test.go:107 — "func TestTheLatencyDistributionIsTheFixturesOwn(t *testing.T) {"
  evidence: internal/stats/dashboard_test.go:137 — "FirstTokenMS: Percentiles{P50: 500, P90: 900, P99: 1000},"
  evidence: internal/stats/dashboard_test.go:146 — "FirstTokenBuckets: []int{0, 2, 2, 5, 1, 0, 0, 0},"
  evidence: internal/ui/static/index.html:135 — "< h3>Time to first token, spread< /h3>"
  evidence: .abcd/development/specs/closed/spc-2609061822385499-usage-dashboard-alice-opens-a-dashboard-in-the-control-panel.md:202 — "5. **The spread is a view of its own, so four tables ship where this spec says three.**"
- ac-4 — MET: Events are written at fixed UTC seconds either side of a local midnight and the hourly counts are asserted twice, in a zone two hours east and one five hours west, so the buckets are shown to move with the Mac's own location; only a removal whose reason is 'evicted' is counted, with an idle reap and a shutdown in the fixture to prove it.
  evidence: internal/stats/dashboard_test.go:162 — "func TestEvictionsAndReloadsAreCountedByTheLocalHour(t *testing.T) {"
  evidence: internal/stats/dashboard_test.go:182 — "{"east", east, 1, 12}, {"west", west, 18, 5},"
  evidence: internal/stats/dashboard.go:175 — "Evictions counts only the removals that were evictions"
  evidence: internal/ui/history_test.go:87 — "func TestTheHourRowShowsTheLocalHourAndItsCounts(t *testing.T) {"
- ac-5 — MET: A control test on a fresh temp store with config.Default() (recording never on) asserts the endpoint reports enabled=false and every table empty, and the renderer test asserts the reader is shown the sentence 'Nothing is recorded' with the tables hidden, no row markup and no 1970 range.
  evidence: internal/gateway/control_history_test.go:127 — "func TestTheHistoryEndpointSaysNothingIsRecordedWithTheSwitchOff(t *testing.T) {"
  evidence: internal/ui/history_test.go:250 — "func TestTheOffStateDrawsNoFigureAndNoRange(t *testing.T) {"
  evidence: internal/ui/static/app.js:1300 — "if (!h || !h.from || !h.to) return 'Nothing is recorded, so there is nothing to show over time.';"
- ac-6 — MET: The history route is registered on the control mux that Handler() wraps in loopbackOnly, and a test drives the wrapped handler with a LAN remote address, a rebound Host and a foreign Origin, asserting 403 for each and 200 for the same request from loopback, so the refusal is the guard working rather than the endpoint being absent.
  evidence: internal/gateway/control.go:49 — "return loopbackOnly(mux)"
  evidence: internal/gateway/control.go:66 — "mux.HandleFunc("GET /api/stats/history", c.handleStatsHistory)"
  evidence: internal/gateway/control_history_test.go:153 — "func TestTheHistoryEndpointIsRefusedFromAnywhereButThisMac(t *testing.T) {"
  evidence: internal/gateway/control_history_test.go:172 — "{"from the LAN", "192.0.2.44:5555", "127.0.0.1:11535", ""},"
- ac-7 — MET_WITH_CONCERNS: docs/statistics-explained.md carries a section per shipped view plus a 'What none of the views can show' section, and an architecture test binds the page's headings to the panel's; the concern is that one of the page's limitation claims is false in this tree — it tells the reader Gropius writes no coarse daily totals, while the store now writes summary.jsonl and the reference page documents it.
  evidence: docs/statistics-explained.md:70 — "## Tokens per day"
  evidence: docs/statistics-explained.md:134 — "## What none of the views can show"
  evidence: internal/archtest/dashboard_docs_test.go:21 — "func TestTheHistoricalViewsAreEachDescribed(t *testing.T) {"
  evidence: docs/statistics-explained.md:150 — "mixed without saying so. Gropius writes no such totals, so no row carries the"
  evidence: docs/statistics-store-reference.md:31 — "Beside them is one file that is not a file of records: `summary.jsonl`, which"

Gap audit:
- honoured:
  - The Statistics view in the control panel gains historical tables over a chosen range, extending the existing view rather than adding a second surface.
    evidence: internal/ui/static/index.html:114 — "< h3>Tokens per day< /h3>"
    evidence: internal/ui/history_test.go:156 — "func TestTheHistoricalTablesLiveInTheStatisticsViewAndAreFetchedOnce(t *testing.T) {"
  - The browser is handed aggregates and never a record: no record-only field and no store path appears in the body.
    evidence: internal/gateway/control_history_test.go:97 — "func TestTheHistoryBodyCarriesNoRecords(t *testing.T) {"
    evidence: internal/stats/dashboard.go:14 — "one pass over the durable store, and handed to the control plane as sums"
  - Nothing appears that was not recorded: the dashboard reads the store and writes nothing.
    evidence: internal/stats/dashboard.go:19 — "It records nothing."
    evidence: internal/stats/dashboard.go:308 — "func Aggregate(ctx context.Context, src RecordSource, from, to time.Time, loc *time.Location) (History, error) {"
  - Tables only in this cut: no chart code, no charting library, no new dependency.
    evidence: go.mod:5 — "require ( fyne.io/systray v1.12.2 github.com/brutella/dnssd v1.2.14 golang.org/x/sys v0.21.0 )"
    evidence: CHANGELOG.md:172 — "Tables, not charts: the figures are exact and the panel carries no charting library."
  - An explanation page describes each shipped view and what it cannot show, and the pages that should point at it do.
    evidence: docs/statistics-explained.md:1 — "# Understanding the historical views"
    evidence: internal/archtest/dashboard_docs_test.go:107 — "func TestTheStatisticsExplanationStaysAnExplanation(t *testing.T) {"
  - The aggregation is bounded and says when a bound stopped it, so a table drawn short does not read as a quiet month.
    evidence: internal/stats/dashboard.go:232 — "StoppedBy names the bound that stopped the pass, when one did"
    evidence: internal/ui/history_test.go:371 — "func TestTheBoundsLineNamesTheBoundThatFired(t *testing.T) {"
- diverged:
  - The press release promises three tables; the panel draws four — the time-to-first-token spread is a view of its own rather than columns beside the percentiles. Recorded as a departure in the spec's As built and carried in the CHANGELOG, the panel and an architecture test.
    evidence: internal/ui/static/index.html:135 — "< h3>Time to first token, spread< /h3>"
    evidence: CHANGELOG.md:165 — "seven, thirty or ninety days, or everything still kept — as four tables under"
    evidence: .abcd/development/specs/closed/spc-2609061822385499-usage-dashboard-alice-opens-a-dashboard-in-the-control-panel.md:202 — "5. **The spread is a view of its own, so four tables ship where this spec says three.**"
  - The spec's Approach specified a per-closed-file aggregate cache; none was built. Every range costs the same full pass.
    evidence: .abcd/development/specs/closed/spc-2609061822385499-usage-dashboard-alice-opens-a-dashboard-in-the-control-panel.md:184 — "1. **No per-file aggregate cache.**"
    evidence: internal/stats/dashboard.go:299 — "so every range costs the same pass, which is 1.4 s over a store at its default size cap"
  - The Approach's `Aggregate(from, to, loc)` in the store's own package became a free function over a `RecordSource` interface taking a context, so a Mac that never recorded passes nil.
    evidence: internal/stats/dashboard.go:308 — "func Aggregate(ctx context.Context, src RecordSource, from, to time.Time, loc *time.Location) (History, error) {"
    evidence: .abcd/development/specs/closed/spc-2609061822385499-usage-dashboard-alice-opens-a-dashboard-in-the-control-panel.md:193 — "3. **A free function over a reader, not a method on the store.**"
  - A new endpoint `GET /api/stats/history` ships rather than an extension of `/api/stats`, so a pass over months is not put on the panel's two-second tick.
    evidence: internal/gateway/control.go:66 — "mux.HandleFunc("GET /api/stats/history", c.handleStatsHistory)"
    evidence: .abcd/development/specs/closed/spc-2609061822385499-usage-dashboard-alice-opens-a-dashboard-in-the-control-panel.md:198 — "4. **A new endpoint, `GET /api/stats/history`, rather than an extension of `/api/stats`.**"
  - The spec's Performance section declined a wall-clock assertion; one ships anyway, at ten seconds with the race build excluded — seven times the 1.4 s measured, so it catches an order-of-magnitude regression rather than the two-second bound the intent stated.
    evidence: internal/stats/dashboard_bound_test.go:34 — "const aggregateBound = 10 * time.Second"
    evidence: internal/stats/dashboard_bound_test.go:36 — "func TestAStoreAtTheSizeCapAggregatesWithinTheBound(t *testing.T) {"
  - The spec said the off/empty state 'links to Settings'; the shipped empty state is a bare sentence with no link, and the off-state renderer test does not assert one.
    evidence: internal/ui/static/index.html:109 — "< div id="statsHistoryEmpty" class="empty" hidden> < p>Nothing was recorded in this range.< /p>"
    evidence: .abcd/development/specs/closed/spc-2609061822385499-usage-dashboard-alice-opens-a-dashboard-in-the-control-panel.md:135 — "the view renders the sentence and the link to Settings"
  - The explanation page's account of the retention edge is stale in this tree: it tells the reader that days beyond the detail are simply absent and that Gropius writes no coarse daily totals, while the store folds dropped records into summary.jsonl and the dashboard reads and marks those rows. The architecture test does not catch it because it only checks that the phrase 'summary totals' appears.
    evidence: docs/statistics-explained.md:150 — "mixed without saying so. Gropius writes no such totals, so no row carries the"
    evidence: internal/stats/summary.go:384 — "func (w *storeWriter) fold(doomed []storeFile) error {"
    evidence: internal/stats/dashboard.go:117 — "It is false throughout while nothing writes those summaries."
    evidence: internal/archtest/dashboard_docs_test.go:56 — ""summary totals","
- missing:
  - The spec's gate before implementation — one month of the maintainer's own store records aggregated by hand, whose output was to be the fixture for the tokens-per-day and share criteria — did not run. The fixtures are synthetic and hand-computed instead.
    evidence: .abcd/development/specs/closed/spc-2609061822385499-usage-dashboard-alice-opens-a-dashboard-in-the-control-panel.md:214 — "the gate before implementation did not run."
    evidence: internal/stats/dashboard_test.go:50 — "// Three local days in `east`, two models."
  - The intent's own conjecture — that a month of records changes which models are kept or how the memory budget is set — is untested, and remains so until the maintainer runs the hand aggregation against their own records after adopting this.
    evidence: .abcd/development/intents/shipped/itd-2609061521159233-usage-dashboard-alice-opens-a-dashboard-in-the-control-panel.md:169 — "we are wrong if, after a month with the dashboard, no such decision has changed"
    evidence: .abcd/development/specs/closed/spc-2609061822385499-usage-dashboard-alice-opens-a-dashboard-in-the-control-panel.md:219 — "the fixtures are synthetic and hand-computed, every criterion is met against them, and the conjecture itself is untested"
  - The press release's 'She learns that the model she thought was her workhorse serves a fifth of the tokens, and that most evictions fall in one hour of the day' is delivered as figures a reader must compare by eye; nothing in the shipped tree surfaces the comparison itself, and no test or artefact demonstrates the learning outcome.
    evidence: internal/ui/static/app.js:1264 — "function renderHistory(h) {"
    evidence: docs/statistics-explained.md:74 — "so the question "which model actually does the work" is answered by one column"

Scope-condition dispositions:
- cond-2609061822388443 — survived: The aggregates are served only over the control plane, whose mux is wrapped in the loopbackOnly guard, and the refusal is demonstrated three ways — remote address, rebound Host, foreign Origin — with the same request from loopback answered 200.
  evidence: internal/gateway/control.go:49 — "return loopbackOnly(mux)"
  evidence: internal/gateway/control_history_test.go:153 — "func TestTheHistoryEndpointIsRefusedFromAnywhereButThisMac(t *testing.T) {"
- cond-2609061822380954 — survived: The store folds records into a coarse per-model per-day summary before retention drops them, and the aggregation adds those summary days into the table and marks the rows, which a test asserts.
  evidence: internal/stats/summary.go:384 — "func (w *storeWriter) fold(doomed []storeFile) error {"
  evidence: internal/stats/dashboard.go:522 — "d.FromSummary = d.FromSummary || fromSummary"
  evidence: internal/stats/dashboard_test.go:596 — "func TestTheSummaryFoldAddsToTheDayAndMarksIt(t *testing.T) {"
- cond-2609061822383763 — narrowed: The per-model, per-period half holds — no client address is recorded and no table carries a person — but the delivery found the reader is not one operator: the control panel asks for no password and answers every account on the Mac, and these tables read back the whole retained store rather than the last thousand requests, which the pages had to say outright and an architecture test now binds.
  narrowing: Holds as 'per model and per period, never per person'; it does not hold as 'one operator reading their own Mac's use' — on a shared Mac any account with the panel open reads months of the serving account's records, so the boundary is the Mac rather than the operator.
  evidence: docs/statistics-explained.md:24 — "The boundary is the Mac, not your account."
  evidence: internal/archtest/dashboard_docs_test.go:135 — "func TestThePagesSayTheViewsWidenWhatOtherAccountsCanRead(t *testing.T) {"
  evidence: docs/statistics-explained.md:153 — "model and per period and never says who: not which machine on your network,"
- cond-2609061822385281 — survived: The figures are carried as the model server's own token counts and Gropius's own timings, said so in the type that holds them and stated to the reader on the explanation page as a limit rather than a boast.
  evidence: internal/stats/dashboard.go:112 — "// PromptTokens and CompletionTokens are the model server's own counts."
  evidence: docs/statistics-explained.md:32 — "They are only as good as what was recorded. The token counts are the model"
  evidence: internal/stats/dashboard.go:161 — "Requests is how many requests the figures rest on: answers that streamed a first token."
## Grounds

- pursued: we want to learn how local models are actually used on this Mac, which models, how many tokens, what latencies, and we expect a month of records to change which models we keep and how we set the memory budget; we are wrong if, after a month with the dashboard, no such decision has changed
