---
id: itd-2609061431481936
slug: effective-context-managed-per-model-gropius-serves-each-mode
spec_id: spc-2609091431038544
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609061431463108]
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Effective context managed per model: Gropius serves each model only up to the context it can actually hold on this Mac, reports that effective limit to clients, and answers an over-long prompt with a clear error instead of a crash or a thrash

## Press Release

Alice runs a dense 27B model whose configuration claims a one-million-token
window, on a Mac whose memory budget can hold a fraction of that in KV cache.
Gropius tells her client the window it will actually serve, and when a prompt
exceeds it the request is refused at once with an error that names the limit
and the prompt's size. The model server never crashes under an oversized
prompt, and other clients on the machine never lose their model to a thrash.

## Why This Matters

The architectural maximum is not what the machine can serve. The effective
window is the smaller of that cap and what the memory budget leaves for the
KV cache, and the cost per token differs sharply by architecture: the 2026-09-05
model-bench lab estimates about 0.25 GB per thousand tokens for a dense 27B
at f16 and far less for hybrid-attention MoE models. The pool's budget
currently charges a flat 1.2x of disk size per model and ignores the KV cache
(iss-3), so a long prompt can push a Mac past its budget with nothing in
Gropius having said no.

Builds on itd-2609061431463108, which reports the architectural window.
Refines iss-3. Depends on iss-2609061431537735, the measurement of effective
windows on a real machine, for the numbers that make the claim testable.

Evidence: research note 2026-09-06-model-bench-evidence (context probe: three MoE models verified past 120K; the dense 27B verified 93K with failures caused by the gateway timeout, not the window).

## Mechanism

We expect a per-model served window, enforced at the gateway from a byte
estimate and charged in the memory budget, to keep the machine off swap,
because the four measured models' peaks were linear in prompt size and the
estimate over-counts. Shown wrong if a prompt under the window still pushes the
process past its charge.

## Scope Conditions

- macOS with mlx-lm as the model server. <!-- cond: cond-2609091431038721 -->
- The window is per model and static, with the declared cap as its default. <!-- cond: cond-2609091431030549 -->
- The byte estimate is conservative, so a refusal may fire on a prompt that would have fitted. <!-- cond: cond-2609091431031658 -->
- The estimate counts the prompt plus max_tokens. <!-- cond: cond-2609091431030215 -->
- The model server's own rejection remains the backstop. <!-- cond: cond-2609091431033452 -->

## Acceptance Criteria

- Given a model with a served window set, when a prompt plus max_tokens is estimated above it, then a 400 in the OpenAI error shape names the window and the estimated size, for streaming and non-streaming alike.
- Given no window set, when the model loads, then the declared cap is served and charged.
- Given a window, when the model is admitted, then the charge uses that window and the panel shows it.
- Given the models list, when an entry is returned, then it carries the served window beside the declared cap.
- Given config.json and the settings pane, when the window is changed, then the next load charges the new figure and a save never refuses an untouched field.
- Given the docs, when a reader opens the memory-budget page, then it explains the window and the charge in words.

## Hold

Lifted on 2026-09-09 at interview: the maintainer chose to charge the served window and enforce it at the gateway as the KV-budget rule, which is this intent; the budget rule now lives in the KV-budget change and the measurement record is resolved with its evidence.

Held on 2026-09-06 by the maintainer's decision in the planning interview: not planned until iss-2609061541313832 (the gateway's ten-minute upstream timeout, which confounds long-prompt measurement), the budget rule ADR that the configurable-budget intent's spec and iss-3 produce, and iss-2609061431537735 (effective-window measurement) have landed. The context-window reporting intent ships alone first.

Depends on, updated 2026-09-06 after the context-window campaign (research note 2026-09-06-context-windows): iss-2609061431537735 is satisfied as far as the timeout allows. Nominal, verified and usable windows are measured for all four local models (Nemotron 253,106 verified at its cap; Qwen3-Coder-Next 221,743; GLM-4.7-Flash 81,100; Qwen3.8-27B 91,673; 34 needle runs recalled out of 34 at sizes up to those), and the per-architecture memory cost iss-3 needs is measured (12, 115, 201 and 353 KB per token with a 1 to 3.4 GB intercept, plus cache retention across requests). Still outstanding: iss-2609061541313832, which bounds three of the four windows and must land before the windows above 222K, 81K and 92K can be verified; and the budget rule ADR. The issue records stay open for the fixing changes.

## Open Questions

All four resolved on 2026-09-09 at interview: the prompt's size is estimated from bytes with a safety margin; the served window is a static per-model setting defaulting to the declared cap; an over-long prompt gets a 400 in the OpenAI error shape naming the window and the estimated size, streaming included; max_tokens counts against the same window.

- How does the gateway know a prompt's token count without a tokenizer? Count
  in the model server and rely on its rejection, estimate from bytes with a
  safety margin, or load the model's tokenizer in Go?
- Is the effective limit a static per-model figure computed from the budget,
  or does it move with what else is resident and with decode concurrency
  (each concurrent sequence holds its own KV cache)?
- What does "clear error" look like on the wire: a 400 with the OpenAI error
  shape naming the limit and the prompt size, and the same for streaming?
- Does the limit also cap max_tokens, since generated tokens occupy the same
  cache?

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._

## Grounds

- pursued: we expect long-context models to become usable beside each other once their charge reflects the window Alice chose rather than the declared maximum; shown wrong if operators leave every window at the default and the budget refuses the same models it did before.
