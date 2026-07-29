---
schema_version: 1
id: "iss-2"
slug: "concurrency-races-in-app-setconfig-writes-hub-token-unsynchr"
severity: "minor"
category: "bug"
source: "agent-finding"
found_during: "2026-07 audit round 2 (deferred)"
found_at: "internal/app/app.go"
---

Concurrency races in app: SetConfig writes Hub.Token unsynchronized while download goroutines read it (-race flags it); Download calls dlWG.Add(1) after releasing dlMu so Close can Wait on a zero counter; Delete vs concurrent Download of the same repo is not atomic (RemoveAll can race a live writer). Fix directions: mutex/atomic SetToken or pass token in DownloadRequest; move Add(1) under dlMu plus a closed flag; per-repoID serialization/tombstone.