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
resolution: "Per-model sampling, pinning and system-message merging are one config.Config.Models map with one ceiling, sanitiser, settings guard and canonicalisation; the three superseded config.json keys are reported as ignored on load and dropped on the next save."
impact: breaking
---

config.Config carries two per-model maps, model_sampling and per_model, held to the same rules by prose rather than by a type. Each has its own ceiling, its own sanitiser on load, its own namesField guard in the settings handler and its own canonicalisation path. spc-2609061822383838 fixed one per-model structure for both records; the sampling-defaults branch landed first with its own map and merging added a second beside it rather than rewrite a shipped settings file's shape (see .abcd/work/DECISIONS.md, 2026-09-06). A third per-model setting must not add a third map. Unifying them is a settings-file migration, not a tidy-up: config.json in the field carries both keys.

## Grounds

- pursued: we expect a third per-model setting to be a field on config.ModelSettings rather than a fourth section of config.json, held there by internal/archtest's one-map count; we are wrong if the archtest has to be relaxed, or if operators lose per-model settings without seeing the start-up line that names the keys that were ignored.
