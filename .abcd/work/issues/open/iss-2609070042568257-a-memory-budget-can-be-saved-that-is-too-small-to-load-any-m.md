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
---

A memory budget can be saved that is too small to load any model, and nothing says so. config.Validate refuses only a negative figure and App.SetConfig checks only the ceiling and the pinned floor, so a save of max_resident_bytes: 1048576 is accepted with HTTP 200 and no warning; every load is then refused with "needs about X but the limit is 1.0 MB", the search tab hides every model, and the control panel gives no advice on the low side the way it does above the high-budget threshold. Self-inflicted and loopback-only, but the control plane deliberately answers every local account, so under the shared-cache install any account on the Mac can stop the server serving with one POST. Found by the adversarial security review of the configurable-budget branch (itd-2609061441261073); the record settles no floor other than the pinned sum, so this is captured rather than fixed there. A low-side warning beside the existing high-budget one, or a floor at the smallest downloaded model's charge, would close it.
