---
id: itd-2609061431481936
slug: effective-context-managed-per-model-gropius-serves-each-mode
spec_id: null
kind: null
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

> _Prompted (the claim-recording gradient): why the authors expect this to work, as a falsifiable "we expect X because Y" — not the outcome restated. Replace this line with the claim, or with the exact token `None stated.` alone on its line to record the claim as considered and declined._

## Scope Conditions

> _Required (the claim-recording gradient): the population, platform, scale, or assumptions this claim holds under, one per top-level bullet — `abcd intent plan` stamps each with a persistent identity. Replace this line with those bullets, or with the exact token `None stated.` alone on its line._

## Acceptance Criteria

> _Required (the itd-1 discipline): add at least one Given-When-Then bullet describing the verifiable bar for "shipped" before this draft can be planned._

## Hold

Held on 2026-09-06 by the maintainer's decision in the planning interview: not planned until iss-2609061541313832 (the gateway's ten-minute upstream timeout, which confounds long-prompt measurement), the budget rule ADR that the configurable-budget intent's spec and iss-3 produce, and iss-2609061431537735 (effective-window measurement) have landed. The context-window reporting intent ships alone first.

Depends on, updated 2026-09-06 after the context-window campaign (research note 2026-09-06-context-windows): iss-2609061431537735 is satisfied as far as the timeout allows. Nominal, verified and usable windows are measured for all four local models (Nemotron 253,106 verified at its cap; Qwen3-Coder-Next 221,743; GLM-4.7-Flash 81,100; Qwen3.8-27B 91,673; 34 needle runs recalled out of 34 at sizes up to those), and the per-architecture memory cost iss-3 needs is measured (12, 115, 201 and 353 KB per token with a 1 to 3.4 GB intercept, plus cache retention across requests). Still outstanding: iss-2609061541313832, which bounds three of the four windows and must land before the windows above 222K, 81K and 92K can be verified; and the budget rule ADR. The issue records stay open for the fixing changes.

## Open Questions

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
