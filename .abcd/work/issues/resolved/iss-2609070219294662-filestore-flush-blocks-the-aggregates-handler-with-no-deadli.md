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
resolution: "FileStore.Flush now takes the reader's context and a FlushWait bound, so a reading of the store cannot hold the aggregates handler or its reading slot open on a writer that has stopped."
impact: fix
---

FileStore.Flush blocks the aggregates handler with no deadline: reading the store for the usage dashboard calls Latest, which flushes the writer through its own queue and waits on the acknowledgement with no context and no timeout, so a stuck or very slow disk holds an HTTP handler open. The request's context cannot interrupt it, because the cancellation check lives in the aggregation's per-record callback, which does not run until the flush has returned. It also occupies a queue slot for the duration, so a dashboard read can cost a concurrent request its record, and it holds one of the two concurrent-reading slots meanwhile. Bounding the flush is the store's write path, which the dashboard intent does not own.

## Grounds

- pursued: a stuck writer no longer pins an HTTP handler or one of the two reading passes; a cancelled reader gets context.Canceled and a patient one gets a flush timeout after ten seconds. Wrong if a healthy store on a slow Mac legitimately needs longer than that to flush, which would refuse to draw the dashboard.
