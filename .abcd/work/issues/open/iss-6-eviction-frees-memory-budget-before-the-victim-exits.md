---
schema_version: 1
id: "iss-6"
slug: "eviction-frees-memory-budget-before-the-victim-exits"
severity: "minor"
category: "bug"
source: "agent-finding"
found_during: "2026-07 bug-hunt round 1"
found_at: "internal/runtime/pool.go"
---

Eviction frees the memory budget on paper before the victim process has released memory: stopEntryLocked deletes the entry (so evictForLocked's recount immediately credits the bytes) and stops the process asynchronously with up to ~10s SIGTERM grace plus 5s SIGKILL, while startLocked launches the replacement at once. Victim and replacement can transiently coexist up to the victim's full footprint — the GPU-memory blowup the budget exists to prevent. Distinct from iss-3 (steady-state loadCost formula); this is eviction timing.

Mitigations in practice: the victim is guaranteed idle (inFlight == 0) and normally dies to SIGTERM well under a second, the replacement's weights materialise over seconds, and the default budget is 60% of RAM. Worst case needs a wedged idle process holding memory for ~15s. Fix direction: keep dying entries in a drain tally that evictForLocked counts until proc.Done() fires, retrying admission instead of crediting the budget at delete time; any change must keep loadCost in lockstep with capability.runFootprint (see iss-3).
