---
schema_version: 1
id: "iss-2609061443332414"
slug: "docs-do-not-state-the-resident-memory-budget-or-the-eviction"
severity: "minor"
category: "bug"
source: "user-observation"
found_during: "2026-09-06 model-bench lab review"
origin: researcher-authored
production_mode: hand-written
found_at: "docs/getting-started.md"
---

Docs do not state the resident memory budget or the eviction rule. The README promises an LRU memory budget and the getting-started page covers idle timeout and preload, but neither says that the budget defaults to 60% of physical RAM, that each loaded model is charged 1.2x its disk size, that a request for a model that does not fit evicts the least-recently-used idle model at once, or that preload loads at startup without pinning. The 2026-09-05 model-bench lab read the resulting behaviour as one-model-resident-with-swap and built a proxy around it.
