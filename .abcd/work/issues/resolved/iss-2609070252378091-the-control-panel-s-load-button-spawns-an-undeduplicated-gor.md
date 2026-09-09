---
schema_version: 1
id: "iss-2609070252378091"
slug: "the-control-panel-s-load-button-spawns-an-undeduplicated-gor"
severity: "nitpick"
category: "bug"
source: "user-observation"
found_during: "adversarial security re-review of feat/eviction-grace"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/gateway/control.go"
resolution: "handleLoad claims a per-model slot (folded repo id) before spawning its goroutine, so repeated clicks join the load in progress and are answered with the same status."
impact: fix
---

The control panel's Load button spawns an undeduplicated goroutine per click, and with eviction grace on each can occupy one of the eight load-waiter slots for the whole maximum wait. Loopback-only, so operator-inflicted, but eight impatient clicks fill the queue and every cold load from the network is refused until they drain.

## Grounds

- pursued: N clicks on Load for one model now take one place in the pool's queue for memory rather than N; what would show it wrong is a load that fails and cannot be retried, or two spellings of one repo id both getting a goroutine
