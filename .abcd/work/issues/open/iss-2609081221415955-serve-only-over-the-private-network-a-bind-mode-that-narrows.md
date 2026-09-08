---
schema_version: 1
id: "iss-2609081221415955"
slug: "serve-only-over-the-private-network-a-bind-mode-that-narrows"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
---

Serve only over the private network: a bind mode that narrows exposure to the mesh-VPN address so the local network cannot reach the gateway at all. Admissible under adr-2609081118587999 rule 3 precisely because it is a bind rather than an inference, and it must fail closed when the interface is absent at launch. Inherits iss-7 as a prerequisite: a specific-address bind currently leaves the control panel unreachable and needs a hand-edited configuration file. Deferred from the itd-2609081015545349 decomposition and captured so it is not left living only as a sentence in an ADR consequence.
