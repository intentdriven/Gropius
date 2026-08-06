---
schema_version: 1
id: "iss-7"
slug: "binding-to-a-specific-lan-ip-makes-the-control-panel-unreach"
severity: "minor"
category: "bug"
source: "agent-finding"
found_during: "2026-07 bug-hunt round 2"
found_at: "cmd/gropius/main.go"
---

A config.json hand-edited to a specific interface address (e.g. Host "192.0.2.5") passes Validate, but after restart the control plane is unreachable from anywhere: the single listener binds only that address (cmd/gropius/main.go), so localhost is connection-refused, while browsing the bound LAN address from the same machine arrives with a non-loopback RemoteAddr and Host header and is 403'd by loopbackOnly (internal/gateway/control.go). The menu bar's "Open Control Panel" always opens localhost. The /v1 API keeps working, which makes the failure look like a UI bug. ExposedToLAN's own comment (internal/config/config.go) treats specific-interface binds as a supported configuration.

Scope: the settings UI only offers 0.0.0.0 or 127.0.0.1, so this needs a hand-edited config.json or hand-crafted POST, and the recovery is editing the same file back — a sharp edge, not a brick. Deferred because every fix is a design decision touching a declared trust boundary: a loopback co-listener changes the bind surface, refusing specific-IP hosts in Validate removes a documented configuration, and loosening loopbackOnly is exactly what its adversarial review exists to prevent. A startup warning is the minimal stopgap, but warn-vs-refuse is itself the decision.

Scope note (2026-08-06, bug-hunt round 16 verification): the same warn-vs-refuse decision should also cover IPv6 literals. Validate accepts "::1" (and ExposedToLAN classifies it — its test table pins the warning classification, not bindability), but cmd/gropius/main.go builds the listen address with fmt.Sprintf rather than net.JoinHostPort, so an unbracketed IPv6 host fails at startup with "too many colons in address" — fail-loud, hand-edited-config only, with the offending address in the error line. A bracketed "[::1]" listens today, but the singleton probe only contacts 127.0.0.1, so IPv6-only binds also feed the iss-5 handshake redesign. If specific hosts stay supported, switch to net.JoinHostPort; if they are refused, refuse IPv6 literals with the same message.
