---
id: itd-2609091301112705
slug: gropius-measures-a-newly-downloaded-model-s-servable-context
spec_id: null
kind: null
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Gropius measures a newly downloaded model's servable context window on this Mac and records it beside the cap the model's configuration declares, so the window an operator and their clients can rely on is a measured figure rather than an architectural claim. When a download finishes, Gropius probes the model the way the 2026-09-06 campaign did by hand — prompts of growing length at one output token, bisecting between the last success and the first failure, at low load — and stores the verified window with the model, showing it beside the nominal cap wherever the cap is shown today. Alice downloads a model in the evening and finds in the morning that her Mac will serve it to 91,000 tokens and not the 262,144 its configuration advertises; the memory budget charges the window it can actually serve, and the record no longer depends on someone running the probe script by hand.

## Press Release

> _Seeded from a quoted-text intent capture. Expand into the full press-release narrative before planning._

## Why This Matters

Gropius measures a newly downloaded model's servable context window on this Mac and records it beside the cap the model's configuration declares, so the window an operator and their clients can rely on is a measured figure rather than an architectural claim. When a download finishes, Gropius probes the model the way the 2026-09-06 campaign did by hand — prompts of growing length at one output token, bisecting between the last success and the first failure, at low load — and stores the verified window with the model, showing it beside the nominal cap wherever the cap is shown today. Alice downloads a model in the evening and finds in the morning that her Mac will serve it to 91,000 tokens and not the 262,144 its configuration advertises; the memory budget charges the window it can actually serve, and the record no longer depends on someone running the probe script by hand.

## Mechanism

> _Prompted (the claim-recording gradient): why the authors expect this to work, as a falsifiable "we expect X because Y" — not the outcome restated. Replace this line with the claim, or with the exact token `None stated.` alone on its line to record the claim as considered and declined._

## Scope Conditions

> _Required (the claim-recording gradient): the population, platform, scale, or assumptions this claim holds under, one per top-level bullet — `abcd intent plan` stamps each with a persistent identity. Replace this line with those bullets, or with the exact token `None stated.` alone on its line._

## Acceptance Criteria

> _Required (the itd-1 discipline): add at least one Given-When-Then bullet describing the verifiable bar for "shipped" before this draft can be planned._

## Open Questions

_None recorded yet._

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._
