---
id: itd-2609061429516182
slug: reproducible-generation-via-a-seed-set-in-settings-alice-set
spec_id: null
kind: null
kind_at_supersession: standalone
superseded_by: itd-2609061429508050
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609061429508050]
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Reproducible generation via a seed set in Settings: Alice sets a default seed in the control panel and two identical requests to the same model return the same completion, once it is established that the pinned mlx-lm honours a seed

## Press Release

Alice wants the same prompt to return the same answer twice, for a test suite
that runs against her Mac. She sets a default seed in Settings. Two identical
requests to the same loaded model now return the same completion, and a
request that carries its own seed still wins. Clearing the field returns the
machine to non-deterministic sampling.

## Why This Matters

Reproducibility is what turns a local model into something a test suite
can depend on. The gateway already relays a per-request seed unchanged, but
the model-bench lab of 2026-09-05 reports that the seed is not reliably
honoured by the MLX server, so a Settings field for it would be a lie until
that is established either way (iss-2609061429558510). This intent is
therefore gated: it proceeds only if the pinned mlx-lm honours a seed, or
after Gropius can make it honour one.

Builds on itd-2609061429508050, which puts the sampling defaults panel in
Settings; this intent adds one field to it.

## Mechanism

> _Prompted (the claim-recording gradient): why the authors expect this to work, as a falsifiable "we expect X because Y" — not the outcome restated. Replace this line with the claim, or with the exact token `None stated.` alone on its line to record the claim as considered and declined._

## Scope Conditions

> _Required (the claim-recording gradient): the population, platform, scale, or assumptions this claim holds under, one per top-level bullet — `abcd intent plan` stamps each with a persistent identity. Replace this line with those bullets, or with the exact token `None stated.` alone on its line._

## Acceptance Criteria

> _Required (the itd-1 discipline): add at least one Given-When-Then bullet describing the verifiable bar for "shipped" before this draft can be planned._

## Supersession

Retired on 2026-09-06. The sampling probe of the same day (iss-2609061429558510, resolved) showed the pinned model server ignores the seed parameter on every model tested while temperature 0 is deterministic, so a seed field could deliver nothing. The sampling-defaults intent carries the documented fact instead: reproducibility comes from a fixed temperature.

## Open Questions

- Does mlx-lm 0.31.3 honour a per-request seed at all? If not, is there a
  launch-time seed, and would a per-process seed give reproducibility only
  for the first request after load?
- Batched decoding: with decode concurrency above one, do concurrent
  requests sharing a batch still reproduce individually?
- Is reproducibility promised across model reloads and across Gropius
  versions, or only within one loaded process?

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._
