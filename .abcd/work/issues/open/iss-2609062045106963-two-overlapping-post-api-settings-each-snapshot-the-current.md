---
schema_version: 1
id: "iss-2609062045106963"
slug: "two-overlapping-post-api-settings-each-snapshot-the-current"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "2026-09-06 sampling defaults security review"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/gateway/control.go"
---

Two overlapping POST /api/settings each snapshot the current config, decode the posted body into a clone of it and call SetConfig, so the last writer wins for every field and a field only one of them touched can be silently reverted by the other. The reload_models list has the same shape of staleness: it compares the incoming config against a snapshot the other save may already have superseded, so it can name a model that no longer needs reloading or miss one that does. Loopback-only and single-user in practice, and the save-versus-launch race is closed separately (startLocked holds the pool lock from SamplingFor through the entry being published, and Resident takes the same lock), so this is the settings-write half of iss-2 rather than a new class.
