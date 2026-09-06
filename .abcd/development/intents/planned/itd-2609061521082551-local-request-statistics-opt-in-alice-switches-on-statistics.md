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
- Deferred: the separate per-model debug action has no record of its own yet.
  The reviewer asked for it to be captured before planning; it is captured as
  its own draft so the rule that it never shares the statistics switch has a
  home.
- Depends on: adr-2609061503319212 (no public telemetry; local only; strict
  opt-in, off by default).

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._

## Grounds

- pursued: we want to learn how local models are actually used on this Mac, which models, how many tokens, what latencies, and we expect a month of records to change which models we keep and how we set the memory budget; we are wrong if, after a month with the dashboard, no such decision has changed
