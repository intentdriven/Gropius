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

## Amendment: both sides stalled to the maximum and unblocked together

The surrounding log lines were captured before the rerun overwrote them, and
they change the reading from "one waiter served late" to "the pool stalled":

    refused a model load: no model in memory could be freed
      model=org/a protected=[] limit="250 B" waited=20.003911208s
    --- FAIL: TestATrickleOfRequestsCannotStarveAWaitingRequest (20.33s)
        grace_test.go:208: the waiter took 20.311390083s to be served

So the trickle's own `Acquire(org/a)` was refused after waiting 20.003s — its
whole maximum — and `org/b` was served 0.311s after that. Both sides sat until
the maximum and came unstuck together.

Two things follow, and the second is the sharper one.

The refusal is for `org/a`, which the test makes resident with `warm` before the
trickle starts. A resident model needs no eviction, so by the time that acquire
ran, `org/a` was no longer in memory and could not be reloaded into a pool whose
room `org/b` had taken. That is the ordinary consequence of the eviction the
waiter was asking for, not a defect by itself.

But the trickle goroutine returns on any error from its acquire — the `else`
branch of the `if _, release, err := p.Acquire(...)`. So the moment that refusal
landed, the trickle STOPPED, and `org/b` was served 0.311s later. Read forward
rather than backward, that says `org/b` was starved for the entire maximum wait
by a trickle it was supposed to be protected from, and was released only when
its competitor gave up. The waiter-age clause, which exists precisely so that a
waiter older than the grace may evict regardless of how recently the resident
model was touched, did not fire at 300ms, at 5s, or at any point before the
maximum. That is the property the test is named for.

It also means the test's own premise ends mid-run, which is worth knowing before
anyone reads a future failure: after that refusal there is no trickle, so the
final measurement is taken in a condition the test did not intend to create.

Instrumentation that would settle it, revised: the waiter's own age at the moment
it is served is still the right probe, and a second is needed beside it — why the
trickle's acquire was refused at that same instant. Without both, a waiter served
late by a slow machine, a waiter whose age clause never fired, and a pool that
stalled and released everything at its boundary all produce the same line.

Evidence supplied by the peer session that hit the failure; the figures and the
`grace_test.go` line numbers were checked here against the tree.
