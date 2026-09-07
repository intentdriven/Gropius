---
schema_version: 1
id: "iss-2609070654589719"
slug: "the-never-fits-fast-path-is-a-timing-oracle-for-the-aggregat"
severity: "nitpick"
category: "security"
source: "user-observation"
found_during: "adversarial security review of feat/eviction-grace"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/runtime/pool.go"
---

The never-fits fast path is a timing oracle for the aggregate charged size of the pinned set. A load that can never fit is refused at once while one that might is parked until the maximum wait, so a client can binary-search over models of publicly known size to solve for the sum of LoadCost over the pinned resident models, against the budget the refusal already prints. Names are not disclosed and the oracle exists only when something is pinned, but the stated rule is that which models this Mac is protecting stays off the network.
