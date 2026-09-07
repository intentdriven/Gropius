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
---

The control panel's Load button spawns an undeduplicated goroutine per click, and with eviction grace on each can occupy one of the eight load-waiter slots for the whole maximum wait. Loopback-only, so operator-inflicted, but eight impatient clicks fill the queue and every cold load from the network is refused until they drain.
