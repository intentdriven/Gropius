---
schema_version: 1
id: "iss-2609061429558510"
slug: "verify-whether-mlx-lm-0-31-3-s-server-honours-a-per-request"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
resolution: "Verified on 2026-09-06 by a sampling probe against the live server (five probes per model on four models: two hybrid-attention MoE, two dense): seed 42 and seed 999 at temperature 0.7 produced identical outputs on every model, so the pinned mlx-lm ignores the seed parameter entirely; temperature reaches the model on all four (temperature 1.5 diverges) and temperature 0 is deterministic. Sampling is deterministic per prompt and temperature. Gropius sets no sampler flags at launch and relays the field unchanged, so the behaviour is upstream. Consequence: reproducibility comes free at any fixed temperature, and the seed-in-Settings intent cannot deliver anything via seed."
impact: internal
---

Verify whether mlx-lm 0.31.3's server honours a per-request seed. The model-bench lab (2026-09-05, results outside the repo) reports temperature reaches the model but seed is not reliably honoured. The gateway relays the field unchanged, so the question is the upstream server. Probe: two identical non-streaming requests with the same seed and temperature above zero against one loaded model; compare completions. Outcome gates the seed-in-Settings intent.

## Grounds

- pursued: we expected the model server to honour a per-request seed and the probe was to confirm it; it showed the seed never has any effect, which would have been wrong if any of the four models had diverged between seeds

Evidence: research note 2026-09-06-model-bench-evidence and evidence/2026-09-06-model-bench/sampling-probe.json (outputs per probe, per model).
