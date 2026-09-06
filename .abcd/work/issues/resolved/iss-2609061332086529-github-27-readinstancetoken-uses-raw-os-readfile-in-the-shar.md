---
schema_version: 1
id: "iss-2609061332086529"
slug: "github-27-readinstancetoken-uses-raw-os-readfile-in-the-shar"
severity: "minor"
category: "security"
source: "user-observation"
found_during: "github issue triage 2026-09-06 (adversarially verified)"
origin: researcher-authored
production_mode: hand-written
found_at: "cmd/gropius/singleton.go"
resolution: "Fixed: readInstanceToken reads through config.ReadRegular capped at 4 KiB; any refusal reads as no token (holderForeign). Round-trip, FIFO and symlink tests."
impact: fix
---

GitHub #27: readInstanceToken uses raw os.ReadFile in the shared root. Reachable only when a peer already holds the port and answers /api/instance; a planted FIFO then turns HEAD's fast holderForeign refusal (exit 1) into a silent hang past acquireListener's deadline, a symlink is followed into a constant-time compare (a one-bit oracle, not disclosure), and no cap. Failure-mode quality, not a new capability. Fix: hardened capped read; any error reads as no token.

## Grounds

- pursued: the probe can no longer hang past acquireListener's deadline on a planted token; iss-5's replay/impersonation semantics are untouched
