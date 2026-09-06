---
schema_version: 1
id: "iss-2609062232492926"
slug: "testrestoredreadymodelkeepsitscontextlength-in-internal-app"
severity: "minor"
category: "bug"
source: "user-observation"
found_during: "2026-09-06 residency record review verification"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/app/app.go"
resolution: "Duplicate: the same ordering gap in App.Download (ready state published before the in-flight entry cleared) is iss-2609062216114318, fixed on main in the system-message-merging change; rebasing this branch onto that main carries the fix."
impact: internal
---

TestRestoredReadyModelKeepsItsContextLength in internal/app fails intermittently under load with 're-download: already downloading'. App.Download registers the model in a.downloads and clears it in finishDownload, which runs as a deferred call in the download goroutine — after that goroutine has already written the ready state to the registry. The test waits for the registry to report the model ready and then immediately starts a second download, so it can observe ready while the in-flight entry is still present and get ErrAlreadyDownloading. The window is normally microseconds, which is why the test passes alone (0 failures in 25 runs) and passes the package alone (0 in 10), and only shows up when the whole suite runs its packages in parallel and widens it. Pre-existing: the ordering and the test both predate this branch, and the only change to internal/app on it swaps one identical fold expression for the shared one. Two candidate fixes: have the test tolerate ErrAlreadyDownloading by retrying briefly, or have the download goroutine clear the in-flight entry before it publishes the ready state, which would also close the same window for a real client that polls the registry and re-downloads on what it sees.

## Grounds

- pursued: the flake stops on this branch once rebased onto main; we are wrong if TestRestoredReadyModelKeepsItsContextLength fails again under -race -count=50
