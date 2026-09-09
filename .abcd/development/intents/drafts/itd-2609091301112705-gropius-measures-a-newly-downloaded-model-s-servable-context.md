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

# Gropius measures a model's real context window

## Press Release

Gropius now tells you how long a prompt each model on your Mac can actually
take. When a download finishes, Gropius measures the model rather than
believing its configuration: it sends prompts of growing length, bisects
between the last one that came back and the first that did not, and records
the window it verified beside the one the model declares.

Alice downloads a 262,144-token model in the evening. In the morning the
models list tells her that her Mac serves it to about 91,000 tokens — the
declared window was never reachable here — so her editor's long-file prompts
are sized to what works instead of failing halfway through the afternoon. The
memory budget charges the window the machine can serve rather than the one the
file advertises, and the figure comes from her own Mac rather than from someone
else's benchmark.

The measurement is not free, and the design has to carry that. Probing one
model took about forty minutes of GPU time at low load in the 2026-09-06
campaign, with an unload and a reload between steps so a retained prompt cache
cannot flatter the next reading, and it needs the machine not to be serving
anyone: a probe that runs while clients are asking for models measures the
queue rather than the model. So this runs when the Mac is idle, is
interruptible, and yields the machine to a real request the moment one arrives.

## Why This Matters

The declared window is a claim about the architecture, not about this Mac. The
2026-09-06 campaign found three of four models bounded by the gateway rather
than by the model, and one that reached only a third of its declared cap here;
that record exists because someone ran a script by hand for an evening, and it
goes stale the moment a model, a runtime or the machine changes. A window
nobody has measured is a number a client sizes its prompts from and then loses
an afternoon to.

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
