---
schema_version: 1
id: "iss-2609062213413447"
slug: "config-config-carries-two-per-model-maps-model-sampling-and"
severity: "minor"
category: "tech-debt"
source: "user-observation"
found_during: "2026-09-06 system-merging record review"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/config/config.go"
---

config.Config carries two per-model maps, model_sampling and per_model, held to the same rules by prose rather than by a type. Each has its own ceiling, its own sanitiser on load, its own namesField guard in the settings handler and its own canonicalisation path. spc-2609061822383838 fixed one per-model structure for both records; the sampling-defaults branch landed first with its own map and merging added a second beside it rather than rewrite a shipped settings file's shape (see .abcd/work/DECISIONS.md, 2026-09-06). A third per-model setting must not add a third map. Unifying them is a settings-file migration, not a tidy-up: config.json in the field carries both keys.
