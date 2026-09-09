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
resolution: "config.Validate bounds api_key at 512 bytes and preload at 256 models, naming the field it refuses; Load trims both with a warning instead of refusing the file."
impact: fix
---

The api_key and preload settings fields are unbounded: neither Validate nor the settings endpoint limits the key's length or the preload list's size. A loopback caller can grow config.json without limit through either. The saved-size check added for the sampling work stops the file becoming unreadable, so the remaining cost is a config.json far larger than it needs to be and a settings save that is refused rather than trimmed; bounding the two fields would let the refusal name the field instead.

## Grounds

- pursued: neither field can grow config.json without limit from the settings endpoint any more, and a file already carrying more still loads with the key shortened rather than cleared; what would show it wrong is an operator with a legitimate key longer than 512 bytes or a preload list of more than 256 models
