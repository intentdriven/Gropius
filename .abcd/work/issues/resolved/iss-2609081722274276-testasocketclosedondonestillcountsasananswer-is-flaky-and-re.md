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
resolution: "The relay now reports that the terminal [DONE] event reached the client, and a failure after it no longer downgrades the record or discards its token counts."
impact: fix
---

TestASocketClosedOnDoneStillCountsAsAnAnswer is flaky and reds CI on unrelated pull requests. Observed on a records-only pull request that touches no Go code beyond one comment: internal/gateway failed with 'stats_test.go:964: its counts are 0 completion tokens, want 4', while a second run of the same commit passed. The test drives a streaming completion, closes the socket on the [DONE] frame, and asserts the recorder still credits the whole answer; the assertion reads the record before the recorder has finished writing it under load. Locally it passes 20 consecutive times under -race on an idle Mac, which is why it survives the usual check. The cost is not the test: a red check blocks the merge queue for changes that cannot have caused it, and the next person to see it will reasonably assume their own change is at fault. waitForRecord blocks until exactly one request is recorded, so the gap is between the class being settled and the token counts being attached.

## Grounds

- pursued: a client that hangs up on [DONE] is recorded as ok with the model server's counts, so the test is deterministic and the dashboard stops undercounting the requests that went best. Wrong if a model server puts something a client needs after the [DONE] sentinel.

## Notes

- 2026-09-09 — Two trade-offs the review named, accepted deliberately. First, `complete` is keyed on the write of the `[DONE]` line returning nil, so anything that goes wrong after that — an upstream error, or the model server crashing mid-cleanup — reads as a clean run. That is the point of the flag rather than a gap in it: the client already has every byte of the answer and the sentinel saying there is no more of it, so nothing that happens afterwards changes what it got. What it costs is that a model server failing straight after `[DONE]` leaves no mark on the record, and would have to be noticed from its own log. Second, a usage chunk sent *after* `[DONE]` with the client already gone would be recorded as 0/0 with no sign that anything was missed — a silent undercount rather than a visible one. It does not fire against the pinned server, which puts the usage event before the sentinel (see `internal/mlxtest/fake.go`, faithful to mlx-lm 0.31.3), and it is the failure mode to look for first if a future server puts the counts last.
