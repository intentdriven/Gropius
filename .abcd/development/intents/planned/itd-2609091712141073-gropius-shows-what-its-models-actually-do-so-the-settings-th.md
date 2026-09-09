---
id: itd-2609091712141073
slug: gropius-shows-what-its-models-actually-do-so-the-settings-th
spec_id: spc-2609091737253961
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609061521082551, itd-2609061431481936]
severity: minor
impact: additive
origin: researcher-authored
production_mode: dictated-and-formatted
---

# Gropius shows what its models actually do, so the settings that matter can be seen before they are chosen. For every request the usage dashboard records the prompt's size against the window the model declares and the window it serves, the time to the first token, the memory the model server held while answering, how many requests were in flight, the sampling values in force and whether the client overrode them, and the class of any refusal; per model it shows their distributions and how often a limit bit. Alice reads it to decide the served window and the concurrency; nothing in it is a prompt or an answer, and it can be exported as a file.

## Press Release

Alice opens the usage dashboard and sees, per model, what its requests did:
how large the prompts were against the window the model declares and the one
it is set to serve, how long the first token took, how many requests were in
flight, how the model server's footprint moved, which sampling values each
server was launched with and how often clients set their own, and which
refusals happened. Nothing in it is a prompt or an answer, and the files are
the plain JSON Lines the reference page names.

## Why This Matters

The request record today holds the class, the token counts and four
durations; it cannot say how a prompt sat against a window, what was in flight,
or whether a client set its own sampling. Those are the facts that decide a
served window and a concurrency, and today they are guessed. Refines the
context-probe draft (itd-2609091301112705), which measures a model once at
download, by measuring every request in use. No export: the statistics
endpoint keeps handing the panel aggregates only, and the reference page says
where the files are.

## Mechanism

We expect fields on the existing request and load records, plus a periodic
footprint sample, to show which settings matter, because each fact already
passes through the gateway or the pool where recording it costs a lookup.
Shown wrong if deciding a window or a concurrency turns out to need
per-request memory attribution the sample cannot give.

## Scope Conditions

- Apple Silicon, one Mac, with the statistics opt-in on. <!-- cond: cond-2609091737259918 -->
- The served window is the per-model setting. <!-- cond: cond-2609091737257664 -->
- Memory is the model server's footprint at completion from a periodic sample, not an attributable per-request cost. <!-- cond: cond-2609091737256053 -->
- Shared-cache mode records every local account's traffic under the serving account's opt-in. <!-- cond: cond-2609091737259099 -->
- The sampling parameters are mlx-lm's launch-flag set. <!-- cond: cond-2609091737254282 -->

## Acceptance Criteria

- Given a request completes, when its record is written, then it carries the model's declared window, its served window and the in-flight count at admission.
- Given a request is refused for prompt size, when its record is written, then it still carries the gateway's estimated size.
- Given a client sets a sampling parameter, when the request is recorded, then that parameter is marked overridden and no client-supplied number is stored.
- Given a model server is launched, when the load event is written, then it carries the sampling values the server was launched with.
- Given a model server is running, when the sampler runs, then its footprint is recorded periodically and a request record carries the footprint at completion, named as such.
- Given the usage dashboard, when a model is shown, then it shows the prompt-size distribution against both windows, the override rate and the footprint over time.
- Given a new field, when it ships, then the statistics reference page names it, the record-size constant is re-measured, and the docs say where the files are.

## Open Questions

- Resolved 2026-09-09 at interview: no export button (the aggregates-only rule of the statistics endpoint stands); memory is a periodic per-model sample joined by time; launch sampling values go on the load event and a per-parameter override flag on the request, argued in the docs as the one field derived from a client body.

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._

## Grounds

- pursued: we expect the numbers to decide the served window and concurrency defaults instead of guesses; shown wrong if after a month the distributions separate no setting choice, or the added fields push the store's retention below a useful span.
