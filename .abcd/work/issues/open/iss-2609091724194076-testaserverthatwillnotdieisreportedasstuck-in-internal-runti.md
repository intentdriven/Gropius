---
schema_version: 1
id: "iss-2609091724194076"
slug: "testaserverthatwillnotdieisreportedasstuck-in-internal-runti"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "make test on feat/security-posture-page, 2026-09-09"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/runtime/drain_queue_test.go:355"
---

TestAServerThatWillNotDieIsReportedAsStuck in internal/runtime/drain_queue_test.go is timing-flaky under -race: it fails one to two runs in five on the integration branch and on a branch that touches only internal/ui, reporting 'the late exit was not reported' while the WARN line it looks for is in the captured log a moment later. The assertion reads the log before the reaper's goroutine has written the late-exit line; the test needs to wait on the event rather than on a fixed delay.
