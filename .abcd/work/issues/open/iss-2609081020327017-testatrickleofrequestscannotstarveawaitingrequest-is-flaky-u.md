---
schema_version: 1
id: "iss-2609081020327017"
slug: "testatrickleofrequestscannotstarveawaitingrequest-is-flaky-u"
severity: "minor"
category: "tech-debt"
source: "user-observation"
found_during: "CI on the critical-and-major security branch"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/runtime/grace_test.go"
---

TestATrickleOfRequestsCannotStarveAWaitingRequest is flaky under -race on a loaded runner. It asserts a waiter is served within a bound derived from the eviction grace, but the pool it builds sets MaxEvictionWait to 20 seconds and the observed failure was 20.312s: the waiter reached its maximum wait instead of being served, so the assertion reports the symptom rather than the cause. Two jobs on the same commit disagreed, one failing and its sibling passing, and a rerun of the failed job went green, which is what identifies timing sensitivity rather than a regression. Not caused by the per-source waiter cap added in the same branch: the runtime tests never set MaxLoadWaitersPerSource, so it is zero and that path is unreachable from them, verified by inspection and by five local runs under -race. The cost is that a red check here reads as a real break and must be investigated each time. Candidates: raise or remove the wall-clock bound, drive the pool clock from the injected now() the pool already accepts rather than from real time, or mark the test timing-sensitive so a rerun is the documented first response.
