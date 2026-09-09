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
---

TestAServerThatWillNotDieIsReportedAsStuck in internal/runtime fails intermittently on its own (the late-exit warning is not observed), independent of the internal/app download-lock test.
