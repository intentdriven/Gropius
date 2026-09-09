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
---

GropiusChat cannot show which models are currently loaded, because a keyless install publishes no residency at all. The models list already carries residency — loaded state, in-flight count, last-used time — but internal/gateway/gateway.go:206 reports it only on an install that has an API key configured, and deliberately so: the three-state value is inferable by timing, but the in-flight count and last-used time say who is busy and when, and an open server discloses neither. The condition is the install's, not the request's. So on the common first-run setup (server and client on the same Mac, no key) the picker has nothing to indicate. The client-side work is trivial once the field is visible; the real question is a maintainer decision — whether a keyless install should publish residency to loopback clients, which the control panel already shows to loopback. Setting an API key makes this work today with no code change, and that is the honest workaround to document either way.
