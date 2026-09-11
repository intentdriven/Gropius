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
resolution: "Rewrote the test in the ordering shape the download-lock test was given: the fake announcer gained a gated serve step, so the assertions about what the lifecycle had done between the second failure and the first successful announcement are taken while the responder is held at the door, and the closing fixed sleep was replaced by Stop, which waits for the refresh goroutine to exit and makes the recorder's contents final. The two failing serves cannot be gated — a responder held at the door is one the loop reads as healthy — so the test also pins its own 20 ms tick, long against the time a freshly spawned goroutine takes to reach its first statement. The underlying loop defect was captured as iss-2609111013499513 and fixed with it."
impact: internal
---

TestAResponderThatFailsIsServedAgainOnTheSameRegistration in internal/discovery/lifecycle_test.go failed once under -race on CI's pull_request run for the posture PR, while the push run of the same commit and four local full-race runs passed. The test counts failed serves, registrations and outage reports against a fake responder and ends on a fixed 30 ms sleep, so its assertions ride on scheduler timing on a loaded runner; the assertion text of the failed attempt was lost to a rerun. It needs the ordering-not-duration shape the download-lock test was given in iss-2609091705185072.

## Grounds

- pursued: the test's verdict rests on the order of the lifecycle's steps wherever an ordering is available — the gated third serve, and the Stop that makes the recorder's contents final — and, where none is, on a tick long enough that scheduling cannot be mistaken for survival. The two failing serves are the where-none-is: holding one would be read by the loop as a responder that is up, so the retry window can only be widened, not closed. At the shared 1 ms tick the test still failed 4 times in 200 runs under -race -cpu 1 against a 32-way CPU load; at 20 ms, 700 runs under the same load were green. What would show it wrong: a runner on which a freshly spawned goroutine cannot reach its first statement within 20 ms.
