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
---

Measure the effective context window per model on a real Mac. The architectural maximum in a model's config is not what the machine can serve: the effective window is the smaller of that cap and what the memory budget leaves for the KV cache, which differs by architecture (the 2026-09-05 model-bench lab estimates about 0.25 GB per thousand tokens at f16 for a dense 27B, far less for hybrid-attention MoE models). Probe: per loaded model, send prompts of growing token length with max_tokens 1, record where the server errors or degrades, bisect between last success and first failure, at low system load so memory pressure does not evict the model mid-probe. Feeds the effective-context intent and iss-3.


Evidence (2026-09-06 context probe, bisection between 1K and 128K target tokens at temperature 0, verified by the usage prompt-token count with the chat template included, on a 128 GB Mac): the three hybrid-attention MoE models tested (a 4-bit 80B-A3B coder, a 4-bit 30B-A3B, an 8-bit GLM Flash) all reached the 128K cap with no failure, largest verified prompts between 120,719 and 122,347 tokens, prefill 390 to 960 tokens per second. The dense 27B at 8-bit verified 93,222 tokens; its failures at 104K and 112K were the gateway's ten-minute response-header timeout (both at exactly 600.0 s), not a context limit, so its true window is unproven above 93K. See the capture on the gateway timeout filed the same day. The probe therefore cannot separate window from timeout for slow-prefill models until that is addressed; a repeat above 128K was not attempted.

Evidence: research note 2026-09-06-model-bench-evidence and evidence/2026-09-06-model-bench/context-probe.json (verified windows per model and the probe script to repeat it).
