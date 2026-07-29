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