---
schema_version: 1
id: "iss-2609091950213211"
slug: "testaresponderthatfailsisservedagainonthesameregistration-in"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "CI on PR 36, 2026-09-09"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/discovery/lifecycle_test.go"
resolution: "Rewrote the test in the ordering shape the download-lock test was given: the fake announcer gained a gated serve step, so the assertions about what the lifecycle had done between the second failure and the first successful announcement are taken while the responder is held at the door, and the closing fixed sleep was replaced by Stop, which waits for the refresh goroutine to exit and makes the recorder's contents final. The underlying cause was the loop defect captured as iss-2609111013499513 and fixed with it."
impact: internal
---

TestAResponderThatFailsIsServedAgainOnTheSameRegistration in internal/discovery/lifecycle_test.go failed once under -race on CI's pull_request run for the posture PR, while the push run of the same commit and four local full-race runs passed. The test counts failed serves, registrations and outage reports against a fake responder and ends on a fixed 30 ms sleep, so its assertions ride on scheduler timing on a loaded runner; the assertion text of the failed attempt was lost to a rerun. It needs the ordering-not-duration shape the download-lock test was given in iss-2609091705185072.

## Grounds

- pursued: the test's verdict now depends on the order of the lifecycle's steps rather than on how fast this Mac is — 50 runs green under -race and 30 more across -cpu 1,2,4, with the shared 1 ms interval restored once the loop stopped miscounting a dead responder as a recovery. What would show it wrong: a failure on a runner so loaded that a responder goroutine cannot record its outcome within one refresh interval, which no ordering in the test can rule out.
