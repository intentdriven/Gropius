---
schema_version: 1
id: "iss-11"
slug: "control-plane-origin-check-does-not-cover-blind-cross-orig"
severity: "minor"
category: "security"
source: "agent-finding"
found_during: "2026-08 bug-hunt round 9 (merge-gate security review)"
found_at: "internal/gateway/control.go"
---

`loopbackOnly`'s Origin allow-list (internal/gateway/control.go, around the
`if origin := r.Header.Get("Origin"); origin != "" && !isLoopbackOrigin(origin)`
check) only ever runs when the request actually carries an `Origin` header.
Browsers omit `Origin` on simple cross-origin GET subresource requests (e.g.
`<img src="http://127.0.0.1:PORT/api/...">`), so a page the user's browser
visits can reach every GET route on the control plane blind — the request is
executed, though the response body cannot be read back since no CORS headers
are ever set (no exfiltration path), and every state-changing endpoint is
POST-only, where browsers do send Origin on cross-site submits (still
correctly refused).

Round 9 capped `/api/search`'s `limit` query parameter (previously unbounded)
because it was the one GET route with expensive, attacker-influenceable
per-request work — an uncapped limit let a blind cross-origin GET fan out an
unbounded number of outbound HuggingFace lookups. That is a symptom patch on
today's one instance, not a fix to the underlying gap: any future GET route
with real per-request work would reopen the same class of issue, since
`loopbackOnly` itself has no signal at all for "this GET did not originate
from the app's own UI."

Deferred rather than auto-patched because the durable fix is a trust-boundary
design decision, not a one-line change: options include requiring a custom
header or token on state-relevant GETs that a simple cross-origin request
cannot attach, or enforcing `Sec-Fetch-Site: same-origin` (not currently
checked anywhere in this codebase) for GET routes. Either changes what the
control-plane API contract requires of any client, including the app's own
UI, and is a call for the maintainer.
