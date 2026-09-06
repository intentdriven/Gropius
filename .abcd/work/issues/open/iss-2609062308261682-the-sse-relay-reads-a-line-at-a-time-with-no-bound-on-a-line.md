---
schema_version: 1
id: "iss-2609062308261682"
slug: "the-sse-relay-reads-a-line-at-a-time-with-no-bound-on-a-line"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "adversarial security review of feat/local-statistics"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/gateway/gateway.go"
---

The SSE relay reads a line at a time with no bound on a line's length, so a model server that never emits a newline makes the gateway buffer without limit. The request side is capped at 32 MiB and the buffered JSON response side at 64 MiB; the streamed side has no cap at all. The model server is a local child process, so this is a robustness gap rather than a reachable attack, but the two other limits exist for the same reason and this one is missing. Found while instrumenting the relay for request statistics (spc-2609061822383782).
