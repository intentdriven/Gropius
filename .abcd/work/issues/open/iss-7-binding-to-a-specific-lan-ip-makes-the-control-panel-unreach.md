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

## Decision (2026-09-08): Gropius always binds loopback as well

Taken at interview, and it resolves this issue's fault rather than working
around it: **loopback is always in the bind, whatever else is.** A specific
address bind becomes that address *and* loopback, not that address alone.

Three things follow, and they are why this was chosen over the alternatives.

- The control panel stops being unreachable. It is loopback-only by design, so
  a bind that always includes loopback can never strand it — which is the fault
  recorded above, removed at its cause.
- The endpoint list becomes true. itd-2609081015545349 criterion 4 requires the
  panel to list only addresses the server answers on, and the shipped list
  offers loopback under every bind. Today that is false under a specific
  non-loopback bind; once loopback is always bound it is true by construction,
  and the criterion needs no exception written into it. The alternative
  considered was to drop loopback from the list, which satisfies the criterion
  and leaves the operator unable to reach their own server.
- It is the invariant the maintainer stated when specifying the private-network
  bind (iss-2609081221415955): no other machine may reach the server, but *this*
  Mac must, including a different user account on it, which is loopback. Stated
  once for that mode, it is the same rule here, so it belongs to the bind
  generally rather than to one mode of it.

**This is enforcement, not presentation**, so it is out of scope for
spc-2609081222104376 by that spec's own Scope section, and adr-2609081118587999
rule 3 governs it: a bind is the narrowing Gropius owns end to end. It needs its
own record before it is built. The cost is structural — Gropius acquires a
single listener today (`cmd/gropius/singleton.go`), and this needs two, with the
port-ownership challenge and the singleton behaviour re-reasoned for a pair.
