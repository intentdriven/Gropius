---
schema_version: 1
id: "iss-2609061546490357"
slug: "the-request-log-on-stderr-records-every-api-client-s-network"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "2026-09-06 planning review"
origin: researcher-authored
production_mode: hand-written
found_at: "cmd/gropius/main.go"
---

The request log on stderr records every API client's network address. The logging wrapper in the main package writes the remote address for each request on the LAN-facing API, so a client address reaches the app's log output on every call, while the telemetry drafts and the 2026-09-06 local-telemetry research note state that Gropius records no client address. Either the log line drops the address (or coarsens it) so the statement holds, or the ADR on local telemetry and the docs say plainly that the operational log carries addresses and the statistics store must not. Decide before the local-statistics intent is specced, since its 'off means identical to today' criterion depends on what today writes.


Decision (2026-09-06, planning interview): drop the address from the log line. The operational log keeps method, path, status and duration; debugging a specific client goes through the per-model debug action. This makes "Gropius records no client address" true and is a precondition for the local-statistics intent's "off is identical to today" criterion.
