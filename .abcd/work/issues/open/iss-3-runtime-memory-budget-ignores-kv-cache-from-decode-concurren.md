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

Evidence (2026-09-06 context-window campaign, research note 2026-09-06-context-windows, memory from top's per-process footprint sampled every three seconds, least-squares growth line per model over clean probes): the KV-cache cost per token, measured, is Nemotron-3.5-Lightning 4bit 12 KB (0.011 GB per thousand tokens, intercept 3.4 GB), Qwen3-Coder-Next 4bit 115 KB (0.110 GB, intercept 1.0 GB), Qwen3.8-27B 8bit 201 KB (0.191 GB, intercept 3.4 GB), GLM-4.7-Flash 8bit 353 KB (0.337 GB, intercept 1.5 GB); the intercept is the working set prefill needs at any size. Every figure is two to seven times what the configuration's f16 KV arithmetic gives (6, 24, 64 and 53 KB), so the config figure is a floor. The earlier 0.25 GB per thousand for the 27B assumed full attention on every layer; the 27B is hybrid (16 full-attention layers of 64) and measures 0.19 GB, so the estimate was near the truth for the wrong reason. The flat 1.2x disk size misses in both directions: Nemotron 21 GB budgeted against a 28 GB peak at its 253K cap; the Coder 54 GB against 68 GB at 222K; GLM 38 GB against 59 GB at 81K and about 100 GB at its 202K cap (extrapolated); the 27B 35 GB against 49 GB at 92K. Two further facts for the budget rule: the process does not return to its idle footprint after a response (up to 17 GB above at the 27B's 92K, the prompt's cache kept) and the caches stack across requests (the Coder at 43 GB idle reached 76 GB after an abandoned 256K prompt, 103 GB with a following 105K prompt, and 108 GB with 2.5 GB of swap when two long requests overlapped); and two concurrent 64K prompts on Nemotron cost about 2 GB more than one, with first-token time 1.8 times the single request. A budget that fits these numbers is weights plus the per-model intercept plus the per-token slope times the window the pool intends to serve, times the sequences it admits.
