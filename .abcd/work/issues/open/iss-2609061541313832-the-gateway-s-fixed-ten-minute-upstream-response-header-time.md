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
---

The gateway's fixed ten-minute upstream response-header timeout silently truncates the usable context of slow-prefill models and reports it as a misleading upstream failure. Measured on 2026-09-06: a dense 27B model at 8-bit prefills at about 180 tokens per second, so prompts above roughly 105K tokens cannot finish prefill inside 600 s; two such requests died at exactly 600.0 s with the gateway's 502 'the model server did not respond', while the model's real window was unproven between 93K verified and the cap. Hybrid-attention MoE models prefill four to five times faster and reached 120K without failure. The timeout comment says a slowly generating model is not a stalled connection, but a slowly prefilling one is treated as exactly that. Candidates: a configurable timeout, a prefill-aware bound scaled from prompt size, or progress keepalives on the upstream hop; the error text must distinguish a timeout from an upstream crash either way.

Evidence: research note 2026-09-06-model-bench-evidence and evidence/2026-09-06-model-bench/context-probe.json (both failures at the 106,688-token target after exactly 600.0 s; prefill 180 to 202 tokens per second).
