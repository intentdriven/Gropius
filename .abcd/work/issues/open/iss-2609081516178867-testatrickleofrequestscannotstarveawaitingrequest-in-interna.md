---
schema_version: 1
id: "iss-2609081516178867"
slug: "testatrickleofrequestscannotstarveawaitingrequest-in-interna"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
---

TestATrickleOfRequestsCannotStarveAWaitingRequest in internal/runtime/grace_test.go failed a merge-queue run with 'the waiter took 20.311390083s to be served, want it bounded by its own age passing the grace'. Reported to me as a load-sensitive timing flake on a contended runner. It may be, but the figure does not fit that reading and the record should not settle it as one. The test configures grace 300ms and MaxEvictionWait 20s, and fails above a deliberately wide 5s bound. A merely slow runner produces a value between the grace and the bound; 20.31s is the configured MAXIMUM WAIT, which means the waiter was not served by its own age passing the grace at all — it waited the whole maximum and was served at the boundary. Acquire returned no error, so this is not the refusal path. Being served at the maximum instead of at the grace is the precise starvation this test exists to detect, and this repository has already corrected two defects in that clause: one where free room was taken by a request needing no eviction while the head waiter was refused at its maximum, and one where a maximum shorter than the grace disabled the waiter-age clause entirely. Neither applies here on the configured figures, which is what makes it worth investigating rather than dismissing. Evidence: the identical commit passed the push run and failed the pull_request run, so it is not a code difference. Not reproduced locally in 40 runs with the race detector — 20 ordinary and 20 at -cpu=1 to simulate contention — so it is rare. Surfaced by a peer session whose change touched only install.sh, README.md and internal/archtest, nothing in internal/runtime; captured here rather than there because it is outside that change's scope and would otherwise be rediscovered as 'the installer work broke the tests'. What would settle it: instrument the waiter's own age at the moment it is served, so a future failure distinguishes a waiter served late by a slow machine from a waiter whose age clause never fired.
