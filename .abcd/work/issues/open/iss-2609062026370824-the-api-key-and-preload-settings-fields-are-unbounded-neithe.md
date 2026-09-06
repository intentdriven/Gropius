---
schema_version: 1
id: "iss-2609062026370824"
slug: "the-api-key-and-preload-settings-fields-are-unbounded-neithe"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "2026-09-06 sampling defaults build"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/config/config.go"
---

The api_key and preload settings fields are unbounded: neither Validate nor the settings endpoint limits the key's length or the preload list's size. A loopback caller can grow config.json without limit through either. The saved-size check added for the sampling work stops the file becoming unreadable, so the remaining cost is a config.json far larger than it needs to be and a settings save that is refused rather than trimmed; bounding the two fields would let the refusal name the field instead.
