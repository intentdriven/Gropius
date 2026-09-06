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
---

The 1.2x charge a loaded model costs the memory budget has three homes: runtime.LoadCost, capability.runFootprint (byte-identical, with a comment saying it mirrors the pool so the fits filter and the pool agree), and the control panel's pinnedCharge in app.js. Exporting LoadCost for the pinned-models fit check made the Go duplication removable — internal/capability imports no internal package and internal/runtime does not import it, so calling runtime.LoadCost introduces no cycle — and the change declined it rather than widen its scope. The JavaScript copy is now bound to LoadCost by a test in internal/ui; capability.runFootprint is held to it by prose only, so a change to the charge can still leave the fits filter and the pool disagreeing about which models this Mac can run. Found by the code review of the pinned-models branch (itd-2609061441241254).
