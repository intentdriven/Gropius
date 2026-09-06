---
id: itd-2609062346072707
slug: debug-logging-for-one-model-until-it-is-restarted-alice-pick
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

# Debug logging for one model, until it is restarted: Alice picks a model in the control panel and asks Gropius to run it with its own logging turned all the way up, in a panel that says in plain words what that writes down. From then until that model's server is restarted, its log holds every request sent to it and every answer it produced, prompts and completions included, so Alice can see what a client is actually sending when a model answers strangely. It is per model, it is never on for a model she did not choose, and it is a separate action from recording request statistics: turning statistics on never raises a log level, and this never turns statistics on. Bob's clients see no change, and nothing leaves the Mac either way.

## Press Release

> _Seeded from a quoted-text intent capture. Expand into the full press-release narrative before planning._

## Why This Matters

Debug logging for one model, until it is restarted: Alice picks a model in the control panel and asks Gropius to run it with its own logging turned all the way up, in a panel that says in plain words what that writes down. From then until that model's server is restarted, its log holds every request sent to it and every answer it produced, prompts and completions included, so Alice can see what a client is actually sending when a model answers strangely. It is per model, it is never on for a model she did not choose, and it is a separate action from recording request statistics: turning statistics on never raises a log level, and this never turns statistics on. Bob's clients see no change, and nothing leaves the Mac either way.

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
