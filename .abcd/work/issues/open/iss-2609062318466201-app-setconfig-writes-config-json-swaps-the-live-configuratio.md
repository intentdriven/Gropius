---
schema_version: 1
id: "iss-2609062318466201"
slug: "app-setconfig-writes-config-json-swaps-the-live-configuratio"
severity: "minor"
category: "tech-debt"
source: "agent-finding"
found_during: "2026-09-06 pinned-models security review"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/app/app.go"
---

App.SetConfig writes config.json, swaps the live configuration and calls Pool.SetPinned as three separate steps, so two overlapping settings saves can leave the pool protecting a model the stored settings say is unpinned. Unlike the lost update in iss-2609062045106963, which loses a field in a file, this divergence is in live pool state: the panel and /v1/models report the pin as gone while eviction still honours it, and it persists until the next save or a restart. Same shape as the unsynchronised a.Hub.Token write recorded in iss-2, now with a second observer. Found by the adversarial security review of the pinned-models branch (itd-2609061441241254); the fix belongs with whatever synchronises SetConfig rather than with the pinned list alone.
