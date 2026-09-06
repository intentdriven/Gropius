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
---

The pool keys its loaded models by the exact string handed to Acquire, while the registry keys models case-insensitively and reports one canonical spelling, so the two key spaces disagree. Two callers reach the pool with an id that never went through the registry: App.preload, which takes the ids straight from config.Preload (hand-edited, since no UI writes it) and guards them only with ValidRepoID, which does not fold case; and the /api/models/load and /api/models/unload endpoints, which pass the request body's raw value. A preload entry typed MLX-Community/Qwen3-8B-4bit therefore launches a server keyed under that spelling while the registry lists mlx-community/Qwen3-8B-4bit. The consequences are a second mlx server for the same weights when a real request arrives and canonicalises (charged 1.2x against the same memory budget, which is the multi-minute swap the residency work exists to avoid), and a models list that reports the model as not loaded while it is loaded and warm. Reachable today by hand-editing config.json; the control panel's own Load button posts the registry's canonical id and does not trigger it.
