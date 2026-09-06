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
resolution: "The models-list reference states the resident memory budget (60% of physical RAM by default), the 1.2x charge on each loaded model, the least-recently-used eviction of an idle model when a request does not fit, the idle timeout, and that preload does not pin. Written there because that page is where a reader meets the residency values the rules govern."
impact: additive
resolved_by:
  intent: "itd-2609061441228998"
  spec: "spc-2609061822377049"
---

Docs do not state the resident memory budget or the eviction rule. The README promises an LRU memory budget and the getting-started page covers idle timeout and preload, but neither says that the budget defaults to 60% of physical RAM, that each loaded model is charged 1.2x its disk size, that a request for a model that does not fit evicts the least-recently-used idle model at once, or that preload loads at startup without pinning. The 2026-09-05 model-bench lab read the resulting behaviour as one-model-resident-with-swap and built a proxy around it.

## Grounds

- pursued: we expect an operator and a LAN client reading the residency values to also need the rules those values move under, so stating the budget and the eviction rule beside them removes the guesswork the 2026-09-05 lab compensated for with a status proxy; we are wrong if readers still model the server as one-model-resident-with-swap after reading the page.
