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
---

TestAResponderThatFailsIsServedAgainOnTheSameRegistration in internal/discovery/lifecycle_test.go failed once under -race on CI's pull_request run for the posture PR, while the push run of the same commit and four local full-race runs passed. The test counts failed serves, registrations and outage reports against a fake responder and ends on a fixed 30 ms sleep, so its assertions ride on scheduler timing on a loaded runner; the assertion text of the failed attempt was lost to a rerun. It needs the ordering-not-duration shape the download-lock test was given in iss-2609091705185072.
