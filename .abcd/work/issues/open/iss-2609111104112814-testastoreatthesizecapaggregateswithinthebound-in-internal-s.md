---
schema_version: 1
id: "iss-2609111104112814"
slug: "testastoreatthesizecapaggregateswithinthebound-in-internal-s"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "building the lifecycle verbs, 2026-09-11"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/stats"
---

TestAStoreAtTheSizeCapAggregatesWithinTheBound in internal/stats fails under plain 'go test ./...' on a loaded Mac (12.3 s against a 10 s wall-clock bound; 11.4 s on an untouched main checkout) and passes under -race, so its bound measures the machine rather than the property. It is the duration-not-ordering shape iss-2609091705185072 and iss-2609091950213211 were given; it needs the same treatment.
