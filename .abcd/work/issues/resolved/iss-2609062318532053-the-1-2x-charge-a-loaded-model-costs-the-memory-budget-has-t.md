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

Amendment (2026-09-09, iss-3): the charge is no longer one figure that every
surface can apply. A model Gropius holds is charged by capability.LoadCostOf,
which needs the model's declared window and the per-token cache cost its
config.json implies; a model in the search results is not on this Mac and has
neither, so the fits filter still applies the flat capability.LoadCost. The two
therefore answer different questions, and they agree on the filter's own
question — can this model load at all — only because no single model is charged
more than the whole budget: the ceiling makes the flat charge both the floor
under LoadCostOf and the test the filter applies. If the maintainer decides
against that ceiling, this record reopens: the filter would then show models
the pool refuses outright, and the filter would need the model's configuration,
which is on the hub and not on disk.

## Grounds

- pursued: there is exactly one Go definition of the 1.2x charge and internal/capability imports no internal package, so the fits filter and the pool cannot diverge; a second definition of the charge appearing anywhere in Go, or capability gaining an internal import, would show it wrong.
