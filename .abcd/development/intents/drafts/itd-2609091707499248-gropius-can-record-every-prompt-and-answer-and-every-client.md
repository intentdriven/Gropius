---
id: itd-2609091707499248
slug: gropius-can-record-every-prompt-and-answer-and-every-client
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

# Gropius can record every prompt and answer, and every client is told so first. Alice turns recording on in the server's settings; nothing turns it on by itself. From then on the first answer any client receives opens with a notice that this server records every prompt and answer, every response carries the same fact, and the models list says so, so no client can talk to the server without having been told. The recording lives in Alice's own account on the Mac, readable by nobody else, until she turns it off.

## Press Release

> _Seeded from a quoted-text intent capture. Expand into the full press-release narrative before planning._

## Why This Matters

Gropius can record every prompt and answer, and every client is told so first. Alice turns recording on in the server's settings; nothing turns it on by itself. From then on the first answer any client receives opens with a notice that this server records every prompt and answer, every response carries the same fact, and the models list says so, so no client can talk to the server without having been told. The recording lives in Alice's own account on the Mac, readable by nobody else, until she turns it off.

## Mechanism

> _Prompted (the claim-recording gradient): why the authors expect this to work, as a falsifiable "we expect X because Y" — not the outcome restated. Replace this line with the claim, or with the exact token `None stated.` alone on its line to record the claim as considered and declined._

## Scope Conditions

> _Required (the claim-recording gradient): the population, platform, scale, or assumptions this claim holds under, one per top-level bullet — `abcd intent plan` stamps each with a persistent identity. Replace this line with those bullets, or with the exact token `None stated.` alone on its line._

## Acceptance Criteria

> _Required (the itd-1 discipline): add at least one Given-When-Then bullet describing the verifiable bar for "shipped" before this draft can be planned._

## Hold

Held on 2026-09-09 by the maintainer at interview, after the design review of
the draft: the promise "every client is told first, and no client can talk to
the server without having been told" cannot be made honestly by a stateless
OpenAI-compatible server. There is no session it can know: the one API key is
shared by every client and loopback clients carry none; a remote address hides
everyone behind a proxy and reconnects per request; a user-agent is
client-chosen; an acknowledgement field is a client-controlled opt-out; and an
"already told" memory is unbounded state keyed on client strings inside the
gateway, a declared trust boundary. The only stateless rule is a notice on
every answer, which the maintainer declined for now. Also found: prepending
text to an answer is a new right to WRITE generated content that the
prompt-content boundary record does not grant, so the mode needs a superseding
decision record naming two readers and one writer; the store must be its own,
never the statistics store; and "readable by nobody else" is false under the
shared-cache mode, where the serving account would hold every local account's
prompts. Lifted when a client-side contract exists that can show a person the
notice, or the maintainer adopts the every-answer rule. The per-model debug
draft itd-2609062346072707 stays its own record meanwhile.

## Open Questions

_None recorded yet._

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._
