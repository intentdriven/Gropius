---
schema_version: 1
id: "iss-1"
slug: "default-config-ships-host-0-0-0-0-with-empty-apikey-unauthen"
severity: "major"
category: "security"
source: "agent-finding"
found_during: "2026-07 security audit round 1 (deferred)"
found_at: "internal/config/config.go"
resolution: "A LAN-exposed bind with no API key now generates one, persists it and announces it before the gateway serves anything; a failure to generate or save drops the bind to loopback rather than continuing open."
impact: breaking
---

Default config ships Host 0.0.0.0 with empty APIKey — unauthenticated LAN exposure by default. Documented as intentional (stderr warning at startup); decision needed: default to 127.0.0.1 with LAN as explicit opt-in, or auto-generate an API key when ExposedToLAN() is true (fail closed).

## Grounds

- pursued: we expect failing closed to end unauthenticated LAN exposure without users noticing much, because the key is generated and shown rather than demanded; we are wrong if users routinely clear the key to get their clients working again
