---
schema_version: 1
id: "iss-2609070042568257"
slug: "a-memory-budget-can-be-saved-that-is-too-small-to-load-any-m"
severity: "minor"
category: "bug"
source: "agent-finding"
found_during: "2026-09-07 configurable-budget security review"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/app/app.go"
resolution: "The shipped App.MemoryBudgetWarning is the fix: no floor is added, because refusing a save over an untouched field is the wedge this setting has twice been taken through and the pinned-sum floor already exists."
impact: fix
---

A memory budget can be saved that is too small to load any model, and nothing says so. config.Validate refuses only a negative figure and App.SetConfig checks only the ceiling and the pinned floor, so a save of max_resident_bytes: 1048576 is accepted with HTTP 200 and no warning; every load is then refused with "needs about X but the limit is 1.0 MB", the search tab hides every model, and the control panel gives no advice on the low side the way it does above the high-budget threshold. Self-inflicted and loopback-only, but the control plane deliberately answers every local account, so under the shared-cache install any account on the Mac can stop the server serving with one POST. Found by the adversarial security review of the configurable-budget branch (itd-2609061441261073); the record settles no floor other than the pinned sum, so this is captured rather than fixed there. A low-side warning beside the existing high-budget one, or a floor at the smallest downloaded model's charge, would close it.

Update (2026-09-07, from the design review of the same branch): the warning half
is now shipped. `App.MemoryBudgetWarning` reports a budget below the smallest
ready model's charge, on the control panel and in the answer to the save that
set it, using the walk that already charges the pinned set. The issue stays open
for the floor question it was filed about — whether a save of such a figure
should be refused at all — which is a design decision the record does not
settle, and refusing it would be the wedge this setting has twice been taken
through. The review also corrected this record's own reasoning: the shared-mode
angle is not a new privilege, since the same endpoint already lets any local
account rewrite the bind address, the API key and the pinned set (iss-1,
iss-11); the case for a floor is symmetry with the high end of the range, not
escalation. What the same review's security counterpart asked for separately —
that enforcement be bounded by physical memory — is done, and is not this issue.

## Grounds

- pursued: operators will read the low-budget warning — on the panel and in the answer to the save — and raise the budget themselves, so no refusal is needed; shown wrong if a support report arrives from someone who saved a tiny budget, did not see the warning, and could not tell why every request was refused.
