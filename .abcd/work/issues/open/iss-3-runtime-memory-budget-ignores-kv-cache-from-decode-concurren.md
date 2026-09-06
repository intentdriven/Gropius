---
schema_version: 1
id: "iss-3"
slug: "runtime-memory-budget-ignores-kv-cache-from-decode-concurren"
severity: "minor"
category: "tech-debt"
source: "agent-finding"
found_during: "2026-07 audit round 3 (deferred)"
found_at: "internal/runtime/pool.go"
---

Runtime memory budget ignores KV cache from decode concurrency: loadCost charges flat 1.2x weights, but each batched sequence holds its own KV cache, so the pool can over-admit under long-context concurrent load (swap/OOM risk). Partially mitigated by the per-model semaphore. Any fix must keep loadCost in lockstep with capability.runFootprint.

Evidence (2026-09-06, from the model-bench lab of 2026-09-05, results outside the repo): KV-cache cost is architecture-dependent and large for dense models — about 0.25 GB per thousand tokens at f16 for a dense 27B, far less for hybrid-attention MoE models such as Qwen3-Coder-Next and Nemotron-3.5-Lightning. A flat 1.2x of weights therefore under-charges a dense model under long-context concurrent load and over-charges a hybrid one. The lab's measured co-residency table is in the dated research note 2026-09-06-model-bench-findings under the development record. See also iss-2609061431537735 (measure the effective context window per model).

Evidence: research note 2026-09-06-model-bench-evidence (prefill rates by architecture: 180 to 202 tokens per second for the dense 27B at 8 bits against 567 to 1,107 for hybrid-attention MoE models; runs.json for the per-task timings).
