---
schema_version: 1
id: "iss-2609081743176753"
slug: "gropiuschat-cannot-show-which-models-are-currently-loaded-be"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "maintainer walkthrough of the chat client on a standard account"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/gateway/gateway.go"
resolution: "GET /v1/models now publishes residency to a loopback client on a keyless install as well as to every client a key admits, so the chat application on the same Mac as a keyless server can show which models are loaded; a client off this Mac on a keyless install still gets nothing."
impact: fix
---

GropiusChat cannot show which models are currently loaded, because a keyless install publishes no residency at all. The models list already carries residency — loaded state, in-flight count, last-used time — but internal/gateway/gateway.go:206 reports it only on an install that has an API key configured, and deliberately so: the three-state value is inferable by timing, but the in-flight count and last-used time say who is busy and when, and an open server discloses neither. The condition is the install's, not the request's. So on the common first-run setup (server and client on the same Mac, no key) the picker has nothing to indicate. The client-side work is trivial once the field is visible; the real question is a maintainer decision — whether a keyless install should publish residency to loopback clients, which the control panel already shows to loopback. Setting an API key makes this work today with no code change, and that is the honest workaround to document either way.

## Grounds

- pursued: we expect this to disclose nothing new, because the control panel already shows the same state, in-flight counts and last-used times to loopback with no key involved, so the facts were reachable by the same person on the same machine already; the LAN half of the old rule is untouched and tested. We are wrong if a loopback connection can be made to stand for a network client. The browser case is closed: an adversarial review of the branch found that a bare source-address check was open to DNS rebinding here — withAuth returns before its own Host and Origin guards when no key is configured, so on a keyless install nothing upstream had looked at either header — and the admission is now fromThisMachine, the same RemoteAddr-plus-Host-plus-Origin rule loopbackOnly gates the control plane on, stated once and used by both. What remains is a proxy or container that terminates a network connection and re-originates it from loopback with a loopback Host, which would hand the clients behind it the listing this rule keeps from the network; nothing in Gropius creates one, it is documented on the reference page, and it is the shape to watch for. The record's 'honest workaround' (set an API key) is no longer needed and was not documented: docs/models-list.md now states who the fields are served to instead.
