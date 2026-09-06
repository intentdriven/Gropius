---
id: adr-2609061610102325
slug: the-gateway-may-rewrite-prompt-content-only-to-merge-system
status: accepted
date: 2026-09-06
supersedes: null
superseded_by: null
related_intents: [itd-2609061441310453]
related_rfcs: []
related_adrs: [adr-2609061503319212]
---

# ADR-2609061610102325: The gateway may rewrite prompt content only to merge system messages, per model, opt-in, and never logs, retains or counts what it reads

## Context

The gateway has always parsed only the model field of a request and relayed
everything else. That is a deliberate stance, recorded in code comments and
in the project's privacy posture: the process that faces the network does
not read prompts. It is not a recorded invariant, so it could be eroded one
feature at a time without anyone deciding to.

Some chat templates reject any system message that is not the first message,
and agent clients re-send their system prompt on every turn. The 2026-09-05
model-bench lab hit this with a dense 27B model and worked around it with an
external proxy that merges all system content into one leading message. The
intent itd-2609061441310453 proposes doing that inside Gropius. The
planning interview of 2026-09-06 put the question plainly: may the gateway
read and rewrite prompt content at all? The maintainer answered yes, for
this one purpose, opt-in per model, with the boundary written down.

## Decision

We will allow the gateway to read and rewrite prompt content for exactly one
purpose: merging all system-role messages of a request into a single leading
system message, and only for models the operator has switched this on for
in Settings. For every other model, and for every other field, the gateway
relays prompt content unchanged.

We will never log, retain, count, index or otherwise keep any prompt content
the gateway reads for this purpose. The merged request exists only for the
duration of the relay. No statistics record, log line, error message or
control-panel view may carry any of it. Equality of relayed content is
judged on decoded values, never bytes, because the gateway re-encodes the
request it buffers.

We will treat any further reading of prompt content by the gateway as a new
decision that supersedes this record.

## Alternatives Considered

1. **Opt-in per model with this ADR (chosen).** Template-strict models work
   with agent clients; the trust boundary widens by one documented rule that
   a test can hold.
2. **Decline, and document the client-side workaround.** The gateway stays
   blind to prompts; users of such models carry a proxy or a client setting.
   Rejected: the problem is real for the models people actually run, and a
   documented rule is a smaller cost than a proxy on every client.
3. **A machine-wide opt-in.** One switch for all models. Rejected: it
   rewrites requests for models that did not need it, widening the boundary
   without cause.
4. **A server-side template override at launch.** No gateway change if the
   model server accepts a tolerant template. Not verified for the pinned
   version; recorded as worth a research capture, and this decision stands
   until such an override is shown to work.

## Consequences

- The merging intent's spec implements exactly this and nothing more; its
  pull request gets the trust-boundary security review, and the review
  checks the never-retain rule against every log and store path.
- A test in the gateway package asserts that a request for a model without
  the switch is relayed with decoded-value equality, and that a merged
  request leaves no trace in logs or statistics.
- The docs name the setting, say what it does to a request, and say that
  nothing read is kept.
- The no-public-telemetry ADR is unaffected: nothing here leaves the machine
  or is written anywhere.
