---
id: itd-2609061521082551
slug: local-request-statistics-opt-in-alice-switches-on-statistics
spec_id: spc-2609061822383782
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609061441228998]
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Local request statistics, opt-in: Alice switches on statistics in Settings and the control panel shows, per model, token counts, time to first token, tokens per second, queue waits and evictions, with no prompt text and nothing leaving the Mac

## Press Release

Alice opens Settings and turns on "Record request statistics on this Mac".
Under the switch a fixed list says exactly what is recorded: model, outcome,
token counts and timings, never a prompt or a completion. From then on the
control panel shows, per model, how many requests it served, prompt and
completion tokens, time to first token, tokens per second, how long requests
waited for it, and how often it was evicted; Alice compares two quantisations
of the same model by their numbers instead of by feel. While the switch is
off, the panel shows nothing worked out from a request, exactly as it does
today. Bob's client sees no change: nothing about his prompts is kept, and
nothing leaves the Mac.

## Why This Matters

Every local LLM tool people actually use shows tokens per second and time to
first token, because that is how models and quantisations are compared, and
Gropius shows nothing, so those choices are made blind. The boundary the
figures sit inside is already settled by adr-2609061503319212: local only,
opt-in, off by default, and never prompt text, completions or keys.

Evidence: research note 2026-09-06-model-bench-evidence (the figures the lab had to measure by hand: first-token time, prefill and generation rates, swap time, same-model concurrency).

## Mechanism

We expect per-request token counts and timings to cost under a millisecond of
added time to first token because the gateway already parses every streamed
line and re-encodes every request body, so recording is one extra field and
one lookup rather than a second pass over the stream.

## Scope Conditions

- One Gropius process on one Apple Silicon Mac serving the pinned mlx-lm <!-- cond: cond-2609061822387125 -->
  0.31.3 model server; the token counts are the ones that server reports and
  their accuracy is its own.
- Completions requested through the gateway; a model server reached directly, <!-- cond: cond-2609061822382803 -->
  and the pool's own readiness probe, are outside the count.
- Request volume is bounded by the pool's per-model concurrency rather than by <!-- cond: cond-2609061822387098 -->
  any rate limit, so how far back the live view reaches follows from traffic.
- Streaming clients accept that Gropius asks the model server for the token <!-- cond: cond-2609061822383900 -->
  counts on their behalf.

## Acceptance Criteria

- Given a fresh install, when Alice opens Settings, then the statistics switch
  is off and the control panel shows no figure worked out from a request.
- Given the switch is on, when a streaming completion finishes, then the panel
  carries that request's model, outcome, token counts, time to first token and
  duration.
- Given the switch is on, when a client that did not ask for token counts
  sends a streaming completion, then the bytes it receives carry no chunk it
  did not ask for.
- Given the switch is on, when a request ends in any outcome other than
  success, then it is recorded with the class of that outcome and no token
  counts.
- Given the switch is on, when a request carrying a sentinel string in its
  prompt and a bearer token in its headers completes, then neither the
  sentinel nor the token appears in anything the panel shows or the process
  writes.
- Given the switch is on, when Alice turns it off, then the panel shows no
  figure worked out from a request and every model server keeps the log level
  it already had.
- Given the documentation, when a reader opens the page for the switch, then
  it names each recorded field, states that prompts, completions and keys are
  never recorded, and states what the process still logs while the switch is
  off.

## Open Questions

- Resolved: what the panel shows while the switch is off — nothing worked out
  from a request, so "off" is identical to today.
- Resolved: three states, not two — off; statistics on; and a separate
  per-model debug action, which never shares the statistics switch.
- Resolved: streamed token counts — the gateway asks the model server for them
  when it relays a streamed request, and removes the extra chunk when the
  client did not ask for it. The pinned server supports this, and the test
  fake gains the usage chunks so the behaviour can be exercised.
- Resolved: outcomes are classified by what is observable — the status the
  client received, whether the client cancelled, and whether the upstream body
  completed.
- Resolved: the request log on stderr drops the client's network address
  (iss-2609061546490357), so the documentation describes a log that records
  method, path and duration only.
- Deferred: the context-fill figure needs each model's context length and
  ships with itd-2609061431463108 rather than with this intent.
- Deferred: how far back the live view reaches, and where its records are
  served from, are the spec's, once the cost of carrying them to the panel is
  measured.
- Deferred: the separate per-model debug action ships as its own intent,
  captured as the draft itd-2609062346072707, so the rule that it never shares
  the statistics switch has a home. Nothing in this intent raises a model
  server's log level, and nothing in it can.
- Depends on: adr-2609061503319212 (no public telemetry; local only; strict
  opt-in, off by default).

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-e6806ab7063d -->
Fidelity review — receipt rcp-e6806ab7063d (verifier abcd:intent-auditor claude-opus-5[1m]).

Provenance: abcd:intent-auditor@claude-opus-5[1m] · rubric_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5 · prompt_hash sha256:1a7a01f7ad6360976a807ed694f2f2076d5d178a701ace8a89bd08693c8b5bf3
Input attestations: diff:working tree at HEAD 18f1a4e (history rewritten during a rename; no reliable per-spec commit range, so the tree as shipped was audited)@-; intent:.abcd/development/intents/shipped/itd-2609061521082551-local-request-statistics-opt-in-alice-switches-on-statistics.md@-; spec:.abcd/development/specs/closed/spc-2609061822383782-local-request-statistics-opt-in-alice-switches-on-statistics.md@-; test-run:go test ./... — all 15 packages ok (cmd/gropius, internal/app, internal/archtest, internal/config, internal/gateway, internal/runtime, internal/stats, internal/ui among them), 0 failures; internal/gateway, internal/stats, internal/archtest and internal/ui re-run with -count=1@-;

Acceptance rollup: MET 7 · MET_WITH_CONCERNS 0 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: config.Default() sets no Statistics field, so a fresh install carries the switch off; the state snapshot omits the counters and /api/stats answers enabled:false with empty lists, and the panel renders the no-statistics body — asserted end to end by a control test.
  evidence: internal/config/config.go:816 — "func Default() Config {"
  evidence: internal/config/config.go:443 — "Statistics bool `json:"statistics,omitempty"`"
  evidence: internal/gateway/control_stats_test.go:46 — "func TestAFreshInstallShowsNothingWorkedOutFromARequest(t *testing.T) {"
  evidence: internal/gateway/control.go:290 — "if view.Enabled {"
  evidence: internal/ui/static/index.html:315 — "< input id="setStats" type="checkbox">"
  evidence: internal/ui/static/app.js:1024 — "$('statsOff').hidden = on;"
- ac-2 — MET: The gateway opens an observation on entry to handleCompletions and closes it on the way out, folding in the resolved model, the outcome class, the model server's usage counts, the first generation event's time and the whole duration; a gateway test against the fake asserts each of the five, and the panel test asserts every recorded figure reaches a row.
  evidence: internal/gateway/gateway.go:343 — "obs := g.observe(cfg.Statistics, started) defer obs.finish(r)"
  evidence: internal/gateway/observe.go:150 — "o.record.DurationMS = time.Since(o.started).Milliseconds()"
  evidence: internal/gateway/gateway.go:796 — "out.firstToken = time.Now()"
  evidence: internal/gateway/stats_test.go:136 — "func TestAStreamedRequestIsRecordedWithItsCountsAndTimings(t *testing.T) {"
  evidence: internal/gateway/control_stats_test.go:80 — "func TestTheCountersRideTheSnapshotAndTheRowsDoNot(t *testing.T) {"
  evidence: internal/ui/stats_test.go:170 — "func TestEveryRecordedFigureReachesThePanel(t *testing.T) {"
- ac-3 — MET: Gropius sets include_usage on the upstream body only, and the relay drops the usage-only event when the client did not ask for it; the test compares the client's bytes with the switch on against the same request with it off and requires them equal, byte for byte.
  evidence: internal/gateway/gateway.go:478 — "relay := relayOptions{observing: obs.recording()} if obs.recording() && streamRequested(payload) { relay.dropUsage = !clientWantsUsage(payload) mergeIncludeUsage(payload)"
  evidence: internal/gateway/gateway.go:799 — "if parsed && opts.dropUsage && isUsageOnly(ev) {"
  evidence: internal/gateway/stats_test.go:173 — "func TestAClientThatDidNotAskForUsageReceivesTheSameBytes(t *testing.T) {"
  evidence: internal/gateway/stats_test.go:620 — "func TestOnlyTheCountsOnlyEventIsRemoved(t *testing.T) {"
  evidence: internal/gateway/stats_test.go:673 — "func TestRemovingTheCountsEventTakesOnlyItsOwnTerminator(t *testing.T) {"
- ac-4 — MET: Eleven outcome classes are named by what was observable, finish() zeroes both token counts for anything but ok, and a table test plus four sibling tests drive client error, busy, refused, launch failed, not ready, upstream status, unreachable and a mid-stream cancellation, asserting the class, the model and the absent counts for each.
  evidence: internal/gateway/observe.go:143 — "if o.record.Class != stats.ClassOK { o.record.PromptTokens = 0 o.record.CompletionTokens = 0"
  evidence: internal/stats/stats.go:90 — "func OutcomeClasses() []Class {"
  evidence: internal/gateway/stats_test.go:277 — "func TestEveryOtherOutcomeIsRecordedWithItsClass(t *testing.T) {"
  evidence: internal/gateway/stats_test.go:383 — "func TestAClientThatGoesAwayMidStreamIsRecordedAsCancelled(t *testing.T) {"
  evidence: internal/gateway/stats_test.go:584 — "func TestAnAnswerCutShortIsNotRecordedAsOne(t *testing.T) {"
- ac-5 — MET: The recorder is structurally content-free — a Record is numbers, a fixed class name and a registry-resolved repo id — and three byte scans (the live view and /api/stats and the captured process log; the store's files; the summary that outlives them) all assert that a sentinel prompt, a bearer token and a LAN address appear nowhere, with the process's own request line carrying method, path, status and duration only.
  evidence: internal/stats/stats.go:108 — "// Record is one request, as counts and timings."
  evidence: internal/gateway/stats_test.go:424 — "func TestNothingFromTheRequestReachesTheRecordOrTheLog(t *testing.T) {"
  evidence: internal/gateway/control_store_test.go:174 — "func TestNothingFromTheRequestReachesTheStore(t *testing.T) {"
  evidence: internal/gateway/control_store_test.go:311 — "func TestNothingFromTheRequestReachesTheSummary(t *testing.T) {"
  evidence: cmd/gropius/main.go:322 — ""method", r.Method, "path", r.URL.Path, "status", rec.status(), "took", time.Since(start).Round(time.Millisecond))"
  evidence: internal/stats/store.go:68 — "type Settings struct {"
- ac-6 — MET: Saving the switch off calls SetEnabled(false), which empties the ring, the counters and the rollups so the snapshot drops its counters and /api/stats returns no rows; separately the launcher names --log-level exactly once and always INFO, an argv-level test asserts it across three specs, and an architecture test fails the build if the switch ever reaches internal/runtime.
  evidence: internal/app/app.go:666 — "func (a *App) applyStatistics(c config.Config) {"
  evidence: internal/stats/stats.go:287 — "func (r *Recorder) SetEnabled(on bool) {"
  evidence: internal/gateway/control_stats_test.go:114 — "func TestTurningTheSwitchOffEmptiesThePanelAtOnce(t *testing.T) {"
  evidence: internal/runtime/launcher.go:219 — ""--log-level", "INFO","
  evidence: internal/runtime/launcher_test.go:111 — "func TestEveryModelServerIsLaunchedAtInfo(t *testing.T) {"
  evidence: internal/archtest/statistics_switch_test.go:46 — "func TestTheStatisticsSwitchDoesNotReachTheRuntime(t *testing.T) {"
- ac-7 — MET: docs/request-statistics.md is the page beside the switch: it lists what is recorded and points at a reference page whose field table is held to stats.RecordFields() by a build-failing test, states that no prompt, answer, API key or client address is recorded, and has a section saying what the process still logs whether or not the switch is on — each of the four assertions enforced by an architecture test.
  evidence: docs/request-statistics.md:31 — "## What is recorded"
  evidence: docs/request-statistics.md:155 — "## What is never recorded"
  evidence: docs/request-statistics.md:202 — "## What the process still logs, whether or not you switch this on"
  evidence: internal/archtest/statistics_docs_test.go:16 — "func TestTheStatisticsPageNamesEveryFieldThatIsRecorded(t *testing.T) {"
  evidence: docs/statistics-store-reference.md:70 — "The whole of what a request is recorded as. There is nothing else: no prompt, no answer, no key, no client address"

Gap audit:
- honoured:
  - Alice opens Settings and turns on "Record request statistics on this Mac"
    evidence: internal/ui/static/index.html:316 — "< span>Record request statistics on this Mac< /span>"
  - Under the switch a fixed list says exactly what is recorded: model, outcome, token counts and timings, never a prompt or a completion
    evidence: internal/ui/static/index.html:317 — "While it is on, Gropius records, for each request it serves: the model, when the request arrived, how it ended, whether it streamed, how many tokens went in and came out, how long the first token took"
    evidence: internal/ui/stats_test.go:214 — "func TestTheSwitchExplainsItselfInWholeSentences(t *testing.T) {"
  - The control panel shows, per model, how many requests it served, prompt and completion tokens, how often it was evicted
    evidence: internal/ui/static/app.js:1118 — "`${m.requests} request${m.requests === 1 ? '' : 's'}`,"
    evidence: internal/stats/stats.go:193 — "type ModelCounters struct {"
  - While the switch is off, the panel shows nothing worked out from a request, exactly as it does today
    evidence: internal/stats/stats.go:341 — "func (r *Recorder) clearLocked() {"
    evidence: internal/gateway/stats_test.go:497 — "func TestOffLeavesTheGatewayAsItWas(t *testing.T) {"
  - Bob's client sees no change: nothing about his prompts is kept
    evidence: internal/gateway/stats_test.go:173 — "func TestAClientThatDidNotAskForUsageReceivesTheSameBytes(t *testing.T) {"
  - Nothing leaves the Mac: the statistics endpoint is loopback-only and there is no export path
    evidence: internal/gateway/control.go:96 — "func loopbackOnly(next http.Handler) http.Handler {"
    evidence: internal/gateway/control_stats_test.go:171 — "func TestTheStatisticsEndpointIsLoopbackOnly(t *testing.T) {"
- diverged:
  - the control panel shows, per model, time to first token and tokens per second — delivered as the LAST request's first-token time and rate rather than as a figure over that model's traffic, so two quantisations are compared on their most recent request unless the reader reads the row table
    evidence: internal/stats/stats.go:210 — "LastFirstTokenMS int64 `json:"last_first_token_ms"`"
    evidence: internal/ui/static/app.js:1122 — "m.last_first_token_ms >= 0 ? `last first token ${millis(m.last_first_token_ms)}` : null,"
  - the control panel shows, per model, how long requests waited for it — ModelCounters carries no wait figure at all; queue and load waits are shown only on the per-request row, summed into one cell
    evidence: internal/stats/stats.go:193 — "type ModelCounters struct {"
    evidence: internal/ui/static/app.js:1145 — "const waited = (r.queue_wait_ms || 0) + (r.load_wait_ms || 0);"
  - the intent's own resolution said the request log would record "method, path and duration only"; the shipped line also records the response status (which the spec later settled on, and the documentation states)
    evidence: cmd/gropius/main.go:322 — ""status", rec.status(), "took", time.Since(start).Round(time.Millisecond))"
    evidence: docs/request-statistics.md:204 — "records the method, path, status and duration of the request, and nothing else"
- missing:
  - the Mechanism's measurable promise — "under a millisecond of added time to first token" — is nowhere measured: the only benchmark in the tree times the durable store's read path, and no test or benchmark compares time to first token with the switch on against with it off
    evidence: internal/stats/store_bench_test.go:24 — "func BenchmarkLatestAtTheCap(b *testing.B) {"
    evidence: internal/gateway/stats_test.go:136 — "if got.FirstTokenMS <= 0 {"

Scope-condition dispositions:
- cond-2609061822387125 — narrowed: The build is bound to the pinned mlx-lm 0.31.3 on macOS/arm64 and the counts recorded are the ones that server reports — except that the gateway clamps a negative count to zero rather than passing on what the server said.
  narrowing: holds for every non-negative count the pinned server reports; a negative prompt or completion count is recorded as zero rather than as reported, so accuracy is the server's own only within that clamp
  evidence: internal/runtime/provision.go:50 — "mlxLMVersion = "0.31.3""
  evidence: internal/gateway/gateway.go:754 — "Prompt: max(counts.PromptTokens, 0),"
  evidence: internal/runtime/provision.go:34 — "// Regenerate when bumping mlxLMVersion (needs the pinned uv, macOS/arm64):"
- cond-2609061822382803 — survived: The only place an observation is opened is handleCompletions, so nothing else is counted; the pool's readiness probe posts a one-token completion straight to 127.0.0.1 on the entry's port, never through the gateway, and holds no reference to a recorder.
  evidence: internal/gateway/gateway.go:343 — "obs := g.observe(cfg.Statistics, started)"
  evidence: internal/runtime/pool.go:918 — "base+"/v1/chat/completions", bytes.NewReader(body))"
  evidence: internal/archtest/statistics_switch_test.go:46 — "func TestTheStatisticsSwitchDoesNotReachTheRuntime(t *testing.T) {"
- cond-2609061822387098 — survived: There is no rate limit anywhere in the served path; a model's in-flight requests are bounded by a semaphore sized from decode concurrency, and the live view is a fixed 1,000-record ring, so how far back it reaches is set by traffic rather than by a clock.
  evidence: internal/runtime/pool.go:803 — "sem: make(chan struct{}, 2*p.opts.DecodeConcurrency),"
  evidence: internal/stats/stats.go:24 — "const RingSize = 1000"
  evidence: docs/request-statistics.md:47 — "the totals for the last hour and the last day, which reach back further than a thousand rows do on a busy Mac"
- cond-2609061822383900 — narrowed: Gropius does ask the model server for the counts on the client's behalf, but the delivery arranged that a streaming client has nothing to accept: the extra event is removed on the way back and the relayed bytes are asserted identical to those the switched-off build sends.
  narrowing: holds only on the upstream request body, only while recording is on, only for a streamed request, and only where the client's stream_options is absent, null or an object — a malformed stream_options is left exactly as sent; the client-visible stream is byte-for-byte unchanged, so no streaming client is asked to accept anything it can observe
  evidence: internal/gateway/observe.go:278 — "func mergeIncludeUsage(payload map[string]json.RawMessage) {"
  evidence: internal/gateway/gateway.go:480 — "relay.dropUsage = !clientWantsUsage(payload) mergeIncludeUsage(payload)"
  evidence: internal/gateway/stats_test.go:173 — "func TestAClientThatDidNotAskForUsageReceivesTheSameBytes(t *testing.T) {"
## Grounds

- pursued: we want to learn how local models are actually used on this Mac, which models, how many tokens, what latencies, and we expect a month of records to change which models we keep and how we set the memory budget; we are wrong if, after a month with the dashboard, no such decision has changed
