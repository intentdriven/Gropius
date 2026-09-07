---
schema_version: 1
id: "iss-2609070252377294"
slug: "the-eviction-grace-load-waiter-queue-is-one-queue-for-the-wh"
severity: "minor"
category: "security"
source: "user-observation"
found_during: "adversarial security re-review of feat/eviction-grace"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/runtime/pool.go"
---

The eviction-grace load-waiter queue is one queue for the whole machine and its cap counts requests, not sources. One client holding a long generation on a resident model plus one request needing that memory puts a waiter at the head that cannot progress, and every load for every other client queues behind it until its maximum wait runs out. Requests for models already in memory are unaffected. Bounded, off by default and operator-configurable, but the gateway is unauthenticated by default, so a per-source or per-model waiter cap is worth considering.
