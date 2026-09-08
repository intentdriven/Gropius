---
schema_version: 1
id: "iss-2609081722274276"
slug: "testasocketclosedondonestillcountsasananswer-is-flaky-and-re"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
---

TestASocketClosedOnDoneStillCountsAsAnAnswer is flaky and reds CI on unrelated pull requests. Observed on a records-only pull request that touches no Go code beyond one comment: internal/gateway failed with 'stats_test.go:964: its counts are 0 completion tokens, want 4', while a second run of the same commit passed. The test drives a streaming completion, closes the socket on the [DONE] frame, and asserts the recorder still credits the whole answer; the assertion reads the record before the recorder has finished writing it under load. Locally it passes 20 consecutive times under -race on an idle Mac, which is why it survives the usual check. The cost is not the test: a red check blocks the merge queue for changes that cannot have caused it, and the next person to see it will reasonably assume their own change is at fault. waitForRecord blocks until exactly one request is recorded, so the gap is between the class being settled and the token counts being attached.
