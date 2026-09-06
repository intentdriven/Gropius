---
schema_version: 1
id: "iss-2609062216114318"
slug: "testrestoredreadymodelkeepsitscontextlength-is-flaky-roughly"
severity: "minor"
category: "bug"
source: "user-observation"
found_during: "2026-09-06 system-merging full-suite run"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/app/app_test.go"
resolution: "A download published its final registry state before deregistering itself, so a caller that saw the model finish and acted on it was refused with ErrAlreadyDownloading. The two now happen in one critical section, so the window cannot be observed: reaching the in-flight map means taking the same lock the final state is written under. The retry loop another test wrapped around the same symptom is now a straight assertion."
impact: fix
---

TestRestoredReadyModelKeepsItsContextLength is flaky: roughly 2-3 runs in 8 fail with 'app_test.go:650: re-download: already downloading'. The test calls Download for a model whose previous download has just been cancelled, and the in-flight map has not always cleared by then, so Download returns ErrAlreadyDownloading instead of starting. Introduced with the test in 27f9e97 (context length), reproduces identically on a tree with the system-message-merging changes to app.New neutralized, so it is not an interaction with them. It will fail CI intermittently. The neighbouring tests wait for a state change with waitFor before acting; this one does not.

## Grounds

- pursued: we expect a caller that sees a download's final state in the registry to be able to act on it at once, because the registry is what the control panel renders and what every waiter polls; we are wrong if a Retry or re-download issued the instant a model is shown ready is still refused, or if TestRestoredReadyModelKeepsItsContextLength fails again under -race -count=50
