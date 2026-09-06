---
schema_version: 1
id: "iss-2609062150484966"
slug: "the-pool-keys-its-loaded-models-by-the-exact-string-handed-t"
severity: "minor"
category: "bug"
source: "user-observation"
found_during: "2026-09-06 design review of feat/residency-visible"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/runtime/pool.go"
resolution: "The pool keys entries through entryKey, which folds case the same way the registry does, so Acquire, Unload and every identity check and delete agree with the registry's key space and one model cannot occupy two entries. The models list folds both sides of its residency join as well, since an entry still carries the spelling that created it. Tests: acquiring org/m then ORG/M leaves one entry and one launched server; Unload finds the model under either spelling; the models list reports a model held under another spelling as loaded, end to end from a real pool. Not fixed here: App.preload and the load and unload endpoints still hand the pool an id that never went through the registry, so the control panel's resident pill still compares raw spellings and can miss for such a model — cosmetic, loopback only."
impact: fix
resolved_by:
  intent: "itd-2609061441228998"
  spec: "spc-2609061822377049"
---

The pool keys its loaded models by the exact string handed to Acquire, while the registry keys models case-insensitively and reports one canonical spelling, so the two key spaces disagree. Two callers reach the pool with an id that never went through the registry: App.preload, which takes the ids straight from config.Preload (hand-edited, since no UI writes it) and guards them only with ValidRepoID, which does not fold case; and the /api/models/load and /api/models/unload endpoints, which pass the request body's raw value. A preload entry typed MLX-Community/Qwen3-8B-4bit therefore launches a server keyed under that spelling while the registry lists mlx-community/Qwen3-8B-4bit. The consequences are a second mlx server for the same weights when a real request arrives and canonicalises (charged 1.2x against the same memory budget, which is the multi-minute swap the residency work exists to avoid), and a models list that reports the model as not loaded while it is loaded and warm. Reachable today by hand-editing config.json; the control panel's own Load button posts the registry's canonical id and does not trigger it.

## Grounds

- pursued: we expect the pool's key space and the registry's to be the same one, so that a model is loaded at most once and every consumer that joins the two — the models list today, the control panel next — gets the same answer; we are wrong if a model can still occupy two entries, or if a listing reports a resident model as cold, under any spelling a caller can reach the pool with.
