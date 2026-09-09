---
id: itd-2609091412177263
slug: an-api-client-is-told-what-gropius-cannot-do-never-why-the-w
spec_id: null
kind: null
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: dictated-and-formatted
---

# An API client is told what Gropius cannot do, never why; the why goes to the server's own log. When Bob's client asks for a model Gropius cannot load or does not have, the answer says so plainly: 'cannot load model X', 'model X not available'. The reason stays on the server, in a log that lives in the account running Gropius and is never shared with another account. The log is sparse by default and Alice can switch it to detailed when she is diagnosing something. A client on this Mac, or one holding the API key, keeps today's informative refusal.

## Press Release

> _Seeded from a quoted-text intent capture. Expand into the full press-release narrative before planning._

## Why This Matters

An API client is told what Gropius cannot do, never why; the why goes to the server's own log. When Bob's client asks for a model Gropius cannot load or does not have, the answer says so plainly: 'cannot load model X', 'model X not available'. The reason stays on the server, in a log that lives in the account running Gropius and is never shared with another account. The log is sparse by default and Alice can switch it to detailed when she is diagnosing something. A client on this Mac, or one holding the API key, keeps today's informative refusal.

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
