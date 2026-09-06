---
id: spc-2609061822383782
slug: local-request-statistics-opt-in-alice-switches-on-statistics
intent: itd-2609061521082551
origin: researcher-authored
production_mode: hand-written
---
# local-request-statistics-opt-in-alice-switches-on-statistics

## Summary

One switch in Settings, off by default, turns on content-free per-request
recording on this Mac. While it is on, the gateway records each completion's
model, outcome class, token counts and timings, the pool reports loads and
removals with their reason, and a Statistics view in the control panel shows
the figures per model. While it is off, nothing derived from a request is
recorded or shown, which is exactly today's behaviour. Nothing leaves the Mac,
and no prompt, completion, key or client address is ever recorded.

## Scope

In scope:

- A `Statistics bool` field on `config.Config`, default false, applied live,
  and a new `internal/stats` package holding an in-memory recorder: a ring of
  recent request records, per-model counters and per-minute rollups.
- Gateway instrumentation of `handleCompletions`, including asking the model
  server for token counts on a streamed request and removing the extra chunk
  when the client did not ask for it.
- A pool observer reporting load start, load finish and entry removal with a
  reason, and an acquire result that separates load wait from queue wait.
- Per-model counters in the control state; the ring served from a
  loopback-only endpoint fetched while the Statistics view is open.
- Dropping the client's network address from the request log line
  (iss-2609061546490357), and extending `internal/mlxtest` so the streaming
  behaviour has something to exercise.

Out of scope:

- Anything on disk (itd-2609061521102742) and the long-horizon views
  (itd-2609061521159233).
- Any per-client or per-session identity; the telemetry-pack intent is
  retired as contradicting adr-2609061503319212.
- The per-model debug action, which never shares this switch, and the
  context-fill column, which waits on itd-2609061431463108.

## Approach

`internal/stats` owns a `Recorder` with a mutex, a ring of the last 1,000
request records, per-model counters and 24 hours of per-minute rollups. It is
handed values, never bodies: a resolved repo id, an outcome class, integer
counts and durations. It has no access to the request, its headers or its
remote address, which makes the redaction criterion a structural property
rather than a filtering rule. The gateway consults the switch through the
existing `ConfigFunc` per request, so it applies live as the API key does.

Token counts on a streamed request. The pinned mlx-lm 0.31.3 server reads
`stream_options` from the body and, when `include_usage` is true, emits a
final usage-only event whose `choices` array is empty. When the switch is on
and the request is streaming, `handleCompletions` sets `include_usage` in the
payload it re-marshals. `stream_options` is a `json.RawMessage` like every
other field, so a client-sent object is parsed and the key set explicitly
rather than replaced: the upstream raises on a `stream_options` object that
lacks the key, so it is always written, never assumed. On the way back,
`streamRewriteSSE` recognises the usage-only event, hands the counts to the
recorder, and forwards it only when the client's own request asked for it.
Non-streaming responses already carry `usage` and need no injection.

To classify outcomes honestly, `streamRewriteSSE` and `relayRewritingModel`
return whether the upstream body completed, the time of the first content
event and the usage object. Classes are defined by what is observable — the
status sent to the client, whether the request's context was cancelled, and
whether the body completed — and are: ok; client error (the gateway's own
400, 404, 408 and 413 branches); upstream error status (a relayed non-2xx);
busy (`runtime.ErrBusy`); refused (any other pool error, including a budget
refusal and `ErrClosed`); launch failed (`*runtime.LaunchError`); not ready
(the readiness error); upstream unreachable (502); cancelled. Only "ok"
carries token counts.

Timings. The clock starts on entry to `handleCompletions`, before the body is
read, so a slow upload counts against the client. `Pool.Acquire` returns an
`AcquireStats` alongside the upstream, separating the wait on the entry's
`ready` channel from the wait on its semaphore, which the caller cannot tell
apart today. Time to first token is the first data event whose content is
non-empty, absent for a non-streaming request; generation rate is completion
tokens minus one over duration minus time to first token, the definition
vLLM, the OpenTelemetry GenAI conventions and LM Studio share.

Loads and removals. `PoolOptions` gains an observer, nil by default, with
callbacks for load started, load finished and entry stopped with a reason.
The pool removes entries by seven paths, so the reason is one of evicted
(`evictForLocked`), idle (`reapIdle`), unloaded (`Unload`), abandoned (the
teardown in `Acquire`'s release), load-failed (`waitReady`), crashed
(`watchExit`) or shutdown (`Close`); only "evicted" counts as an eviction.
Callbacks run outside `p.mu`.

Panel. `State` carries only the per-model summary counters, because
`handleEvents` re-marshals the whole snapshot every two seconds and `app.js`
redraws on every event; the ring and the rollups would be well over a hundred
kilobytes a tick. The ring is served instead from a loopback-only `/api/stats`
endpoint fetched while the Statistics view is visible, which is also the
endpoint itd-2609061521159233 extends. The request table shows time, model,
outcome class, token counts, time to first token, rate and duration — no
message text and no client address.

Off. Turning the switch off stops recording, empties the ring, the counters
and the rollups, and returns the panel to its no-statistics rendering. It
never touches anything on disk; once itd-2609061521102742 exists, the panel
shows stored records only while the switch is on, and Clear removes them.
`internal/mlxtest` gains `stream_options.include_usage` handling and emits the
usage-only chunk, so the streaming behaviour has something to exercise.

## How each acceptance criterion is satisfied

- "Given a fresh install, when Alice opens Settings, then the statistics
  switch is off and the control panel shows no figure worked out from a
  request." `config.Default()` leaves the field false. A control test asserts
  the snapshot carries the configuration's false switch and no request-derived
  field, and that `/api/stats` returns an empty result.
- "Given the switch is on, when a streaming completion finishes, then the
  panel carries that request's model, outcome, token counts, time to first
  token and duration." A gateway test against the extended fake asserts the
  recorded values, including a time to first token that is greater than zero
  and less than the duration.
- "Given the switch is on, when a client that did not ask for token counts
  sends a streaming completion, then the bytes it receives carry no chunk it
  did not ask for." The same test scans the relayed stream for an event with
  an empty `choices` array; a sibling test with a client-sent
  `include_usage` of true asserts the chunk is forwarded, and a third sends
  `stream_options` without the key and asserts the relayed body carries it,
  which is what keeps the upstream from raising.
- "Given the switch is on, when a request ends in any outcome other than
  success, then it is recorded with the class of that outcome and no token
  counts." A table test drives each branch through a stub pool and the fake's
  404 and 503 paths, plus a client that cancels mid-stream, and asserts the
  class and absent counts for each.
- "Given the switch is on, when a request carrying a sentinel string in its
  prompt and a bearer token in its headers completes, then neither the
  sentinel nor the token appears in anything the panel shows or the process
  writes." A test issues such a request from a crafted non-loopback
  `RemoteAddr` (set on the request rather than through a listener) and scans
  the state snapshot, the `/api/stats` body and the process's captured log
  output for all three strings.
- "Given the switch is on, when Alice turns it off, then the panel shows no
  figure worked out from a request and every model server keeps the log level
  it already had." A control test records requests, posts the switch off, and
  asserts the counters and the ring are empty; a launcher test asserts the
  argument vector's `--log-level INFO` is identical either side of both
  transitions.
- "Given the documentation, when a reader opens the page for the switch, then
  it names each recorded field, states that prompts, completions and keys are
  never recorded, and states what the process still logs while the switch is
  off." A documentation test compares the field table on the page with the
  recorder's record struct, and the log sentence names method, path, status
  and duration only.

## Trust-boundary review notes

- `internal/gateway`: this is the first feature that merges a field other than
  `model` into a client's request body. The merge sets exactly one key inside
  `stream_options` and touches nothing else; a test asserts the decoded value
  of every other field is what the client sent. The extra chunk is removed on
  the way back, so a client that did not ask sees the stream it expects, and
  the new endpoint sits behind the existing loopback guard.
- `internal/runtime`: the observer adds no input parsing and no new file or
  process access. Callbacks run outside `p.mu`, and a slow or panicking
  observer must not stall an acquire; the recorder's callbacks take a mutex
  and return.
- `internal/config`: one new boolean, parsed from the configuration file and
  carried in the state snapshot. That value is configuration, not a
  request-derived figure, and the redaction criterion allows it.
- The recorder is structurally content-free: it is never handed a body, a
  header or an address, so there is no filter to get wrong.

## Docs to change

- `README.md`: one line under Features and one under Security saying
  statistics are local, opt-in and off by default.
- `docs/getting-started.md`: the request-log sentence describes a line
  carrying method, path, status and duration, with no client address.
- A new how-to page under `docs/`: what the switch records field by field,
  that prompts, completions and keys are never recorded, that nothing leaves
  the Mac, and what the process still logs while the switch is off.

## Dependencies and sequencing

- Binding: adr-2609061503319212 (no public telemetry; local only; strict
  opt-in, off by default; never prompt text, completions or keys).
- Builds on itd-2609061441228998, refined by adding figures beside residency
  rather than by changing residency.
- iss-2609061546490357 (drop the client address from the log line) lands with
  this spec.
- itd-2609061521102742 persists what this recorder produces;
  itd-2609061521159233 extends the view this spec adds, which is named
  "Statistics" here and is not renamed later.
- itd-2609061431463108 is needed only for the later context-fill column, and
  no new Go dependency is added.

## Open design points

- Ring size and rollup window; 1,000 records and 24 hours of per-minute
  rollups are the starting figures, and the pool bounds concurrency rather
  than rate, so at peak the ring is minutes rather than hours.
- Whether the per-model counters appear on the model cards as well as in the
  Statistics view, and whether a load wait borne by a waiter that did not
  trigger the load is reported apart from the triggering request's.
