---
schema_version: 1
id: "iss-2609070219294662"
slug: "filestore-flush-blocks-the-aggregates-handler-with-no-deadli"
severity: "minor"
category: "bug"
source: "agent-finding"
found_during: "2026-09-07 dashboard build"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/stats/store.go"
---

FileStore.Flush blocks the aggregates handler with no deadline: reading the store for the usage dashboard calls Latest, which flushes the writer through its own queue and waits on the acknowledgement with no context and no timeout, so a stuck or very slow disk holds an HTTP handler open. The request's context cannot interrupt it, because the cancellation check lives in the aggregation's per-record callback, which does not run until the flush has returned. It also occupies a queue slot for the duration, so a dashboard read can cost a concurrent request its record, and it holds one of the two concurrent-reading slots meanwhile. Bounding the flush is the store's write path, which the dashboard intent does not own.
