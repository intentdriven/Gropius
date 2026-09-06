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
---

TestRestoredReadyModelKeepsItsContextLength is flaky: roughly 2-3 runs in 8 fail with 'app_test.go:650: re-download: already downloading'. The test calls Download for a model whose previous download has just been cancelled, and the in-flight map has not always cleared by then, so Download returns ErrAlreadyDownloading instead of starting. Introduced with the test in 27f9e97 (context length), reproduces identically on a tree with the system-message-merging changes to app.New neutralized, so it is not an interaction with them. It will fail CI intermittently. The neighbouring tests wait for a state change with waitFor before acting; this one does not.
