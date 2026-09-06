---
schema_version: 1
id: "iss-2609061334198154"
slug: "github-25-registry-open-loads-entries-without-the-validrepoi"
severity: "minor"
category: "security"
source: "user-observation"
found_during: "github issue triage 2026-09-06 (adversarially verified)"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/registry/registry.go"
resolution: "Fixed: registry.Open skips entries failing config.ValidRepoID, matching Rescan; test pins it."
impact: fix
---

GitHub #25: registry.Open loads entries without the ValidRepoID gate Rescan enforces, so a planted registry.json entry with an invalid id is advertised on /v1/models yet refused by Delete — an undeletable phantom that survives every restart because the victim's saves fail EPERM against the plant. Fix: skip invalid ids in Open.

## Grounds

- pursued: no app-written entry can fail the gate since every write path validates; an existing user index losing a legitimate entry would show it wrong
