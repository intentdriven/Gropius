---
schema_version: 1
id: "iss-2609061431537735"
slug: "measure-the-effective-context-window-per-model-on-a-real-mac"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
resolution: "Measured: the 2026-09-06 context-window campaign (research notes 2026-09-06-model-bench-evidence and 2026-09-06-context-windows, evidence in research/evidence/2026-09-06-context-windows/) recorded each model's nominal cap, the largest prompt verified through the gateway, recall at three depths, and the memory a long prompt costs — Nemotron-3.5-Lightning 253,106 tokens, Qwen3-Coder-Next 221,743, Qwen3.8-27B 91,673, GLM-4.7-Flash 81,100, with no recall degradation anywhere the gateway can serve; three of the four windows are bounded by the gateway's 600 s upstream header timeout rather than by the models, which is its own record (iss-2609061541313832)."
impact: internal
---

Measure the effective context window per model on a real Mac. The architectural maximum in a model's config is not what the machine can serve: the effective window is the smaller of that cap and what the memory budget leaves for the KV cache, which differs by architecture (the 2026-09-05 model-bench lab estimates about 0.25 GB per thousand tokens at f16 for a dense 27B, far less for hybrid-attention MoE models). Probe: per loaded model, send prompts of growing token length with max_tokens 1, record where the server errors or degrades, bisect between last success and first failure, at low system load so memory pressure does not evict the model mid-probe. Feeds the effective-context intent and iss-3.


Evidence (2026-09-06 context probe, bisection between 1K and 128K target tokens at temperature 0, verified by the usage prompt-token count with the chat template included, on a 128 GB Mac): the three hybrid-attention MoE models tested (a 4-bit 80B-A3B coder, a 4-bit 30B-A3B, an 8-bit GLM Flash) all reached the 128K cap with no failure, largest verified prompts between 120,719 and 122,347 tokens, prefill 390 to 960 tokens per second. The dense 27B at 8-bit verified 93,222 tokens; its failures at 104K and 112K were the gateway's ten-minute response-header timeout (both at exactly 600.0 s), not a context limit, so its true window is unproven above 93K. See the capture on the gateway timeout filed the same day. The probe therefore cannot separate window from timeout for slow-prefill models until that is addressed; a repeat above 128K was not attempted.

Evidence: research note 2026-09-06-model-bench-evidence and evidence/2026-09-06-model-bench/context-probe.json (verified windows per model and the probe script to repeat it).

Evidence (2026-09-06 context-window campaign, research note 2026-09-06-context-windows and evidence/2026-09-06-context-windows/, every request through the gateway, unique prompts so the model server's prompt cache could not help, one output token, temperature 0, prompt size from the usage object): nominal caps from each model's config.json are 262,144 for Qwen3-Coder-Next, Nemotron-3.5-Lightning and Qwen3.8-27B and 202,752 for GLM-4.7-Flash, none with a rope-scaling entry. Verified through the gateway: Nemotron 253,106 tokens (its cap, 469 s, no failure); Qwen3-Coder-Next 221,743 (591 s, nine seconds under the timeout; 244K and 256K died at 600.0 s, so the cap needs about 700 s); GLM-4.7-Flash 81,100 (432 s; 96K died at 600.0 s, so its earlier 122K figure held only with a shared prefix in the prompt cache); Qwen3.8-27B 91,673 (497 s, the largest under this campaign's 540 s budget; 92K to 105K unprobed, above that the timeout). Usable window from a needle-in-a-haystack check at 10%, 50% and 90% depth, thinking off, 64 output tokens: 34 runs, 34 recalled (Nemotron 3 of 3 at 16K, 64K and 253K; the Coder 1 of 1 at 16K, 3 of 3 at 64K and 189K, its largest needle at 189K because 222K sits nine seconds under the timeout; GLM 3 of 3 at 16K, 64K and 81K; the 27B 3 of 3 at 16K, 1 of 1 at 64K, 3 of 3 at 92K); no degradation anywhere the gateway can serve. Only Nemotron's nominal cap is servable today; the other three windows are bounded by iss-2609061541313832, not by the models.

## Grounds

- pursued: the effective window per model on this Mac is measured and recorded, and the figures fed the memory budget's charge (iss-3) — the per-token cache cost and the peak footprint at each verified window come from this campaign. What would show it wrong: a repeat probe on the same machine reaching a materially different window, or a model whose recall degrades inside the window recorded here.
