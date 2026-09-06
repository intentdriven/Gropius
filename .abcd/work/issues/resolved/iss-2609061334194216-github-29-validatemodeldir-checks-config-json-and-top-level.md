---
schema_version: 1
id: "iss-2609061334194216"
slug: "github-29-validatemodeldir-checks-config-json-and-top-level"
severity: "minor"
category: "security"
source: "user-observation"
found_during: "github issue triage 2026-09-06 (adversarially verified)"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/app/fsutil.go"
resolution: "Fixed: validateModelDir calls the exported registry.CheckShards, the same rule Rescan uses; test pins a missing index-named shard."
impact: fix
---

GitHub #29: validateModelDir checks config.json and top-level weights but not model.safetensors.index.json shard completeness, unlike its Rescan sibling shardsPresent. A repo whose weight_map names a shard absent from the tree (or hidden, or in a subdirectory) downloads and validates as ready, then 503s forever on load. Needs a malformed upstream repo; no data loss. Fix: export the registry shard check and call it from validation.

## Grounds

- pursued: a repo whose index names an absent shard is now marked failed with the shard named, instead of ready-then-503; a legitimate single-file or complete sharded model failing validation would show it wrong
