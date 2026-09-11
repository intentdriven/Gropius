---
schema_version: 1
id: "iss-2609091709537421"
slug: "testaserverthatwillnotdieisreportedasstuck-in-internal-runti"
severity: "minor"
category: "tech-debt"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/runtime/drain_queue_test.go"
resolution: "The test asserted the warning at the instant the stuck counter moved, but the counter is published under the pool's lock and the warning written after the unlock; the test now waits for the log line the way it waits for the counter, ordering not duration. Green at -race -count=60 and -cpu=1 -count=30."
impact: internal
---

TestAServerThatWillNotDieIsReportedAsStuck in internal/runtime fails intermittently on its own (the late-exit warning is not observed), independent of the internal/app download-lock test.

## Grounds

- pursued: we expect the test to be deterministic once it waits for both the counter and the line; shown wrong by any further failure of this test on a merge-queue run, which would point at the pool rather than the test
