---
schema_version: 1
id: "iss-2609070252377294"
slug: "the-eviction-grace-load-waiter-queue-is-one-queue-for-the-wh"
severity: "major"
category: "security"
source: "user-observation"
found_during: "adversarial security re-review of feat/eviction-grace"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/runtime/pool.go"
---

The eviction-grace load-waiter queue is one queue for the whole machine and its cap counts requests, not sources. One client holding a long generation on a resident model plus one request needing that memory puts a waiter at the head that cannot progress, and every other client's load that needs a model unloaded is refused — not queued behind it, refused — until that waiter's maximum wait runs out. On the shipping default of eight waiters that costs an attacker eight connections and, with the maxima the settings allow, denies model loading to the whole network for an hour a cycle.

Amended 2026-09-07 after an adversarial security review measured it, and raised from minor to major on that measurement. Two things the first wording got wrong, both since fixed on the same branch and neither of them what this issue is now about: loads that needed nothing unloaded were refused as well (they are served now, whatever the queue is doing), and each refusal ran the launcher's preconditions — two filesystem calls — under the pool's one lock (the eviction plan is consulted first now, so a refused arrival does no filesystem work at all).

What stays open is the design point: the queue is global and counts requests rather than sources, so a client that fills it denies cold loads that need an eviction to everyone else. Requests for models already in memory, and loads that fit in memory nobody is using, are unaffected throughout. Bounded, self-healing, off by default and operator-configurable, and stated in the how-to's "What it costs" — but the gateway is unauthenticated by default. A per-source or per-model cap is the obvious answer and needs a notion of "source" this codebase does not have on an open endpoint: an address is not a client and the API key is optional. The alternatives are a shorter maximum wait, which trades the hazard for the feature, and requiring a key before grace may be switched on.
