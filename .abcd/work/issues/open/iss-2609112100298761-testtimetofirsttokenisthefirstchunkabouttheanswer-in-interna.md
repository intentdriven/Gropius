---
schema_version: 1
id: "iss-2609112100298761"
slug: "testtimetofirsttokenisthefirstchunkabouttheanswer-in-interna"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "merge queue for PR 45, 2026-09-11"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/gateway/stats_test.go"
---

TestTimeToFirstTokenIsTheFirstChunkAboutTheAnswer in internal/gateway/stats_test.go failed once in a merge-queue CI run (merge_group run for PR 45) and passed on the pull-request and push runs of the same commit and locally; a timing assertion on the first-token measurement under a loaded runner. Same shape as the other timing flakes (iss-2609091705185072, iss-2609091950213211, iss-2609111104112814): needs an ordering the fake controls rather than a wall-clock bound.
