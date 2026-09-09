---
schema_version: 1
id: "iss-2609091705185072"
slug: "testafinishingdownloaddoesnotholdupaloadofanothermodel-compa"
severity: "minor"
category: "tech-debt"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/app/concurrency_test.go"
resolution: "Replaced the wall-clock comparison with a blocking measureDir seam: the test now holds the sizing walk still and asserts another model's Resolve completes meanwhile."
impact: internal
---

TestAFinishingDownloadDoesNotHoldUpALoadOfAnotherModel compares wall-clock Acquire delays across two downloads and fails about one run in five on a loaded machine.

## Grounds

- pursued: the test's verdict now depends only on where the walk happens, not on how fast this Mac is — 50 runs green with -race and again with -race -cpu=1, and the same test fails on a ten-second bound when the sizing is moved inside finishDownload's publish closure. What would show it wrong: a failure on a machine where the download itself cannot reach the sizing step within the 30-second bound.
