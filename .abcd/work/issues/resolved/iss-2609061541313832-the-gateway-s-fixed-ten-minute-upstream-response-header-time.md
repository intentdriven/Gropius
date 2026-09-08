---
schema_version: 1
id: "iss-2609061541313832"
slug: "the-gateway-s-fixed-ten-minute-upstream-response-header-time"
severity: "major"
category: "bug"
source: "user-observation"
found_during: "2026-09-06 context-window probe"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/gateway/gateway.go"
resolution: "The upstream header wait is now derived per request from the prompt's size, keeping ten minutes as the floor, with upstream_header_timeout_sec overriding it. The refusal is its own error saying the work may continue and that retrying makes it worse."
impact: fix
---

The gateway's fixed ten-minute upstream response-header timeout silently truncates the usable context of slow-prefill models and reports it as a misleading upstream failure. Measured on 2026-09-06: a dense 27B model at 8-bit prefills at about 180 tokens per second, so prompts above roughly 105K tokens cannot finish prefill inside 600 s; two such requests died at exactly 600.0 s with the gateway's 502 'the model server did not respond', while the model's real window was unproven between 93K verified and the cap. Hybrid-attention MoE models prefill four to five times faster and reached 120K without failure. The timeout comment says a slowly generating model is not a stalled connection, but a slowly prefilling one is treated as exactly that. Candidates: a configurable timeout, a prefill-aware bound scaled from prompt size, or progress keepalives on the upstream hop; the error text must distinguish a timeout from an upstream crash either way.

Evidence: research note 2026-09-06-model-bench-evidence and evidence/2026-09-06-model-bench/context-probe.json (both failures at the 106,688-token target after exactly 600.0 s; prefill 180 to 202 tokens per second).

Evidence (2026-09-06 context-window campaign, research note 2026-09-06-context-windows): the timeout now bounds three of the four local models, not one. Prefill rate falls with prompt length on every model (Qwen3-Coder-Next 1,292 tokens per second at 8K, 645 at 96K, 375 at 222K; Nemotron 1,501 at 8K, 540 at 253K; GLM-4.7-Flash 863 at 8K, 231 at 64K, 188 at 81K; Qwen3.8-27B 247 at 8K, 184 at 92K), so a bound sized from small prompts is wrong by two to four times where it matters. Deaths at exactly 600.0 s with the 502: Qwen3-Coder-Next at 256K twice (load averages 57 and 7; a 244K target died the same way but overlapped another request), GLM-4.7-Flash at 96K (load 4). Verified just under the limit: the Coder at 221,743 tokens in 591 s, GLM at 81,100 in 432 s, the 27B at 91,673 in 497 s; Nemotron's 256K needle prompts took 436 to 546 s, within a minute of it. The floor across the four models at their largest verified sizes is about 185 tokens per second, so a prefill-aware bound of prompt tokens over 150 tokens per second plus one minute, or today's ten minutes where that is larger, covers every probe (256K gets about 30 minutes; prompts under about 80K keep the ten). Also observed: after the gateway answers 502 the model server keeps prefilling the abandoned request, so the next request runs at less than half speed (two Coder probes at 224 and 258 tokens per second against 645 clean) and the abandoned prompt's cache stays resident (the Coder process at 74 to 76 GB idle afterwards, 103 GB with the next prompt); a client that retries on the 502 makes both worse. The timeout's fix should cancel upstream work when it gives up, or the error text should say the work continues.

## Grounds

- pursued: we expect a bound scaled below every measured prefill floor to stop killing large prompts while changing nothing under about 80K tokens; we are wrong if a model prefills slower than 150 tokens per second at size and still dies at the bound
