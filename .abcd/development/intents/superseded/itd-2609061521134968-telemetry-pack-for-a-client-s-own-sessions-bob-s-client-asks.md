---
id: itd-2609061521134968
slug: telemetry-pack-for-a-client-s-own-sessions-bob-s-client-asks
spec_id: null
kind: null
kind_at_supersession: standalone
superseded_by: adr-2609061503319212
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609061521102742]
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Telemetry pack for a client's own sessions: Bob's client asks Gropius for a telemetry pack covering the sessions it identifies as its own and receives a file of token counts and timings for those requests, and no one else's

## Press Release

Bob runs an agent against Alice's Gropius for an afternoon. At the end he
asks Gropius for a telemetry pack for his session and receives a file: every
request his agent made, with model, token counts, time to first token,
tokens per second, queue wait and outcome. He keeps it with his run notes,
compares it with the pack from yesterday's run on a different model, and
never sees a line that was not his. Carol, running her own agent at the same
time, gets her own pack and nothing of Bob's.

## Why This Matters

The people who use a shared local model want the same numbers the operator
sees, scoped to their own work, in a file they can keep. The statistics
already exist once itd-2609061521082551 and itd-2609061521102742 are in
place; this intent adds a way for a client to claim a subset and download
it. The hard part is not the file but the boundary: the OpenAI API has no
session, Gropius deliberately records no client address, and a client must
not be able to fetch another client's pack. That boundary is a trust rule
and will be an ADR.

## Mechanism

> _Prompted (the claim-recording gradient): why the authors expect this to work, as a falsifiable "we expect X because Y" — not the outcome restated. Replace this line with the claim, or with the exact token `None stated.` alone on its line to record the claim as considered and declined._

## Scope Conditions

> _Required (the claim-recording gradient): the population, platform, scale, or assumptions this claim holds under, one per top-level bullet — `abcd intent plan` stamps each with a persistent identity. Replace this line with those bullets, or with the exact token `None stated.` alone on its line._

## Acceptance Criteria

> _Required (the itd-1 discipline): add at least one Given-When-Then bullet describing the verifiable bar for "shipped" before this draft can be planned._

## Supersession

Retired on 2026-09-06 by the maintainer's decision in the planning interview. Serving a client its usage records over the network contradicted adr-2609061503319212 (local telemetry is for the operator of that Mac); rather than amend the ADR or move the pack to the operator's panel, the intent was dropped. The store and the dashboard serve the stated purpose.

## Open Questions

- What is a session? A client-minted opaque label sent as a request header
  and echoed into the record, a per-client API key when keys are in use, or
  something else. A label the client chooses is content the client controls;
  the record must store it as an opaque token and never as an address or a
  name.
- Who may fetch whose pack? The proposal is: only a client presenting the
  same label (or key) that the records carry; the operator may fetch any
  pack from the control panel. Recorded as an ADR.
- Where the endpoint lives: under `/v1` on the LAN-facing gateway (a new
  trust-boundary surface, security review at PR) or only on the loopback
  control API. Bob is on another machine, so the LAN gateway is implied.
- Does the pack require the operator's statistics switch to be on, or does
  a client's request for its own pack imply consent for its own records
  only? The ADR says local telemetry is the operator's opt-in.
- Pack format: the same JSON Lines as the store, or a summary as well.

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._
