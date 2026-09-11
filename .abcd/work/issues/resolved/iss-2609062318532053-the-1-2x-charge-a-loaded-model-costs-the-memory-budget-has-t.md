---
schema_version: 1
id: "iss-2609062318532053"
slug: "the-1-2x-charge-a-loaded-model-costs-the-memory-budget-has-t"
severity: "nitpick"
category: "tech-debt"
source: "agent-finding"
found_during: "2026-09-06 pinned-models code review"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/capability/capability.go"
resolution: "The 1.2x load charge now lives once, as capability.LoadCost; internal/runtime, internal/app, internal/gateway and the control panel's test all read that one function."
impact: internal
---

The 1.2x charge a loaded model costs the memory budget has three homes: runtime.LoadCost, capability.runFootprint (byte-identical, with a comment saying it mirrors the pool so the fits filter and the pool agree), and the control panel's pinnedCharge in app.js. Exporting LoadCost for the pinned-models fit check made the Go duplication removable — internal/capability imports no internal package and internal/runtime does not import it, so calling runtime.LoadCost introduces no cycle — and the change declined it rather than widen its scope. The JavaScript copy is now bound to LoadCost by a test in internal/ui; capability.runFootprint is held to it by prose only, so a change to the charge can still leave the fits filter and the pool disagreeing about which models this Mac can run. Found by the code review of the pinned-models branch (itd-2609061441241254).

Amendment (2026-09-07, itd-2609061441261073): the direction is now fixed. That
change moves the machine reading and the default budget share into
internal/capability and has internal/runtime read them, so internal/runtime
imports internal/capability and the remedy above — calling runtime.LoadCost
from capability — would create an import cycle. Collapsing the duplicate means
moving the charge into internal/capability and having internal/runtime call it,
not the other way round.

Amendment (2026-09-09, iss-3 and itd-2609061431481936): there is still exactly
one Go definition of the charge — capability.LoadCostOf — and every surface
reads it: the pool, the app's pinned-set check, the control panel through the
binding test in internal/ui, and the fits filter. What differs is what each can
tell it. A model Gropius holds carries the window it is served at and the
per-token cache cost its config.json implies; a model in the search results is
on the hub and has neither, so the filter calls the same function with neither
and gets its degenerate case, the flat weights-plus-a-fifth.

The ceiling that once made those two answers agree by construction is gone (the
maintainer's decision of 2026-09-09: a model charged the budget rather than
what it costs is a figure the machine does not support). So a model can now
pass the filter and be refused at load — and that refusal names the served
window and the decode concurrency that would fit, which is the honest answer to
a question the filter cannot answer from the hub. This record stays resolved:
the duplication it was filed about is gone and cannot return while the charge
has one home. What would reopen it is a second definition of the charge
appearing anywhere, not the two callers knowing different amounts about a
model.

## Grounds

- pursued: there is exactly one Go definition of the 1.2x charge and internal/capability imports no internal package, so the fits filter and the pool cannot diverge; a second definition of the charge appearing anywhere in Go, or capability gaining an internal import, would show it wrong.
