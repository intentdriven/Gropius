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
Scope note (2026-07-30, bug-hunt round 3 verification): the Delete non-atomicity also covers request-triggered loads, not just Download. Registry.Remove deletes the index entry before RemoveAll, and Pool.Unload's ErrBusy check is point-in-time, so a request that resolved the model just before the index write can Acquire and launch onto a directory mid-removal. The outcome is contained (the child fails, probeReady sees the exit, waitReady removes the entry, the caller gets 503), but the recorded fix direction — per-repoID serialization/tombstone — must cover the Acquire/launch path as well as the Download writer, or a fast-loading model can briefly serve from unlinked file descriptors as a pool ghost after its registry record is gone.

Scope note (2026-08-06, bug-hunt round 16 verification): the dlMu registration window also has a liveness face. Download publishes the download handle (with its done channel) before the initial Registry.Put, and the Put-error path returns via finishDownload without close(done) or cancel() — so a Delete that snapshotted the handle inside that window parks forever on <-dl.done (one leaked control-plane handler goroutine; no lock held, and Close is unaffected since dlWG.Add only happens after a successful Put). The per-repoID serialization fix must guarantee done-closure on every Download exit path (or carry the two-line close(dl.done); cancel() in the Put-error branch). Trigger context: a registry-save failure is routine in shared-cache mode per iss-10, so the window is not merely a disk-full hypothetical there.
