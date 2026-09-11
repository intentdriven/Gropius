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
resolution: "The flake was the test blocking on its own trickle goroutine after the measurement point; the service time is now read before the shutdown handshake and the trickle is stopped by cancelling its context."
impact: internal
---

TestATrickleOfRequestsCannotStarveAWaitingRequest is flaky under -race on a loaded runner. It asserts a waiter is served within a bound derived from the eviction grace, but the pool it builds sets MaxEvictionWait to 20 seconds and the observed failure was 20.312s: the waiter reached its maximum wait instead of being served, so the assertion reports the symptom rather than the cause. Two jobs on the same commit disagreed, one failing and its sibling passing, and a rerun of the failed job went green, which is what identifies timing sensitivity rather than a regression. Not caused by the per-source waiter cap added in the same branch: the runtime tests never set MaxLoadWaitersPerSource, so it is zero and that path is unreachable from them, verified by inspection and by five local runs under -race. The cost is that a red check here reads as a real break and must be investigated each time. Candidates: raise or remove the wall-clock bound, drive the pool clock from the injected now() the pool already accepts rather than from real time, or mark the test timing-sensitive so a rerun is the documented first response.

## Grounds

- pursued: the reported figure is the waiter's service time and can no longer include the trickle's own MaxEvictionWait, shown by the old shape reproducing 2.302s against a 301ms service at a 2s maximum and by 40 runs at -race plus 20 at -race -cpu=1 passing after; wrong if the test fails again with a figure at or near MaxEvictionWait, which the three probes now in its failure message would attribute.
