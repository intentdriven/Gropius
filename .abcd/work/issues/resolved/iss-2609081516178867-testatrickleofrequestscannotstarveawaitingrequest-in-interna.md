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
resolution: "wakeDelayLocked now bounds a parked waiter's sleep by the eviction grace when the only candidate is in flight or still loading, instead of falling back to the whole maximum wait."
impact: fix
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

## Second amendment: the trickle loop is sequential, and a candidate mechanism

**Corrected timeline.** The trickle goroutine is a sequential loop — a select on
a 40ms timer, then a *blocking* `p.Acquire`, then round again. One acquire at a
time. So the acquire that reports `waited=20.003911208s` was blocked for those
twenty seconds, and while it was blocked the loop could not iterate: no timer,
no further acquires, nothing renewing `org/a`'s idleness clock.

That moves where the test's premise ended. It did not end at the refusal; it
ended roughly twenty seconds earlier, when that acquire first blocked — about
0.3s into a 20.33s run. For essentially the whole measured window there was no
trickle at all.

This makes the finding worse rather than milder, and it retires the earlier
reading above. `org/b` was not held off by competing traffic it should have been
protected from: there was no competing traffic. `org/a`'s clock stopped being
renewed almost immediately, its 300ms grace should have expired inside the first
second, and `org/b` still was not served until the maximum. The clause failed
with nothing to defeat it. It also retires the causal story in either direction
— neither acquire released the other; both were blocked on the same thing and
both came unstuck at the boundary, which is why they land 0.3s apart at the end
of a 20s window rather than anywhere during it.

**A mechanism that fits, from the code rather than from the logs.**
`Pool.wakeDelayLocked` computes how long a parked waiter may sleep before
something could have changed that nothing will signal. It takes the minimum of
three terms:

- the maximum wait remaining, `p.maxWait - waited`;
- the waiter's own age reaching the grace, `p.grace - waited`, applied only
  `if own > 0`;
- for each entry, its grace running out — but the loop `continue`s over any
  entry with `inFlight > 0`.

Once the waiter is older than the grace the second term is non-positive and
drops out, which is correct in itself: that moment has passed. But if the waiter
then re-checks at an instant when the only candidate is momentarily in flight —
which a 40ms-cadence trickle makes true a large fraction of the time — the third
term is skipped as well. Neither contributes, and `delay` falls back to
`maxWait - waited`. The waiter parks until the maximum.

It is rescued by an explicit signal, and releases, stops, budget and pin changes
all do signal.

**There is no lost-wakeup race, and saying there was understated this.** The
waiter is appended to `p.waiters` and its delay computed while `p.mu` is still
held, and only then is the lock released; `w.signal` is `make(chan struct{}, 1)`
and `wakeWaitersLocked` does a non-blocking send into that buffer. So a wake
arriving between the unlock and the select is not lost — it sits in the buffer
and the select takes it at once. The buffering exists for exactly this.

That makes the stall deterministic rather than a coin flip, and it explains the
pairing. The fallback to the maximum bites only when *nothing* calls
`wakeWaitersLocked` for the whole interval, and here that is entailed rather than
lucky: the trickle's own acquire was blocked, so it called no `release()`, and
the only other actor is the waiter itself, also parked. Two parked waiters, no
third party, therefore no wakes, therefore both sleep on timers derived from
`maxWait - waited`, therefore both come unstuck within a few hundred
milliseconds of each other at the boundary. The 20.003 / 20.311 pairing falls
out of the mechanism instead of needing a coincidence.

What is rare is *arriving* in the state — a waiter past its grace, the sole
candidate in flight at the instant of the check, and no other traffic to prod
anyone. Once there, the wait to the maximum follows. That distinction matters
for whoever fixes it: a lost-wakeup race would invite adding a signal somewhere,
and no signal is missing. The defect is that `wakeDelayLocked` treats "the only
candidate is momentarily in flight" as "nothing can change", when in flight is
the most transient state a candidate has.

**Status of these claims.** The sequential loop is checkable from
`internal/runtime/grace_test.go` and was checked. The `wakeDelayLocked`
arithmetic is checkable from `internal/runtime/pool.go` and was checked. That
the two combined produced *this* failure is a reading of the evidence, not a
demonstrated fact: nobody knows when that acquire first blocked, only that it
blocked for 20.003s. It is offered as the first place to look, not as a
diagnosis.

**Third probe.** Beside the waiter's own age at service and the reason the
trickle's acquire was refused, record the timestamp of the trickle's last
*successful* acquire. That is what separates "the premise held and the clause
lost" from "the premise collapsed and the clause was never tested" — and without
it a future reader cannot tell whether the test measured what its name says.

The sequential-loop observation and the corrected timeline came from the peer
session that hit the failure; the loop shape, the `wakeDelayLocked` terms and
the line references were checked here against the tree.

## Resolution note, 2026-09-09: the mechanism is real and it is not what failed

Both halves of the amendments above need correcting where they join.

The `wakeDelayLocked` arithmetic is exactly as described and is fixed. But it
could not have stalled anything on its own: every exit from in flight or from
loading calls `wakeWaitersLocked` — a release, a stop, a failed load, a crashed
process — and, as the amendment itself notes, the waiter is queued and its delay
computed under `p.mu` with a buffered signal channel, so no wake is lost. A
waiter behind a busy model was therefore always woken when that request ended.
The defect was that the delay RESTED on that wake rather than standing on its
own, which is a missing backstop and not a starvation. It is fixed as hardening.

The CI failure was the test blocking on itself. It read the service time AFTER
`close(stop); <-trickled`, while the trickle's `Acquire` ran on
`context.Background()` and so could not see `stop`; once the waiter had taken
the room, the trickle's next request wanted room held by a model the test
goroutine was still holding in flight, which can only end at `MaxEvictionWait`.
Reproduced at a 2s maximum: refusal at `waited=2.001111333s`, the waiter
actually served after `301.413417ms`, the old shape reporting `2.302508292s` —
the same 0.3s pairing as `20.003911208s` / `20.311390083s` above, scaled. The
waiter-age clause fired at the grace in every run. See iss-2609081020327017 and
the two dated lines of 2026-09-09 in `.abcd/work/DECISIONS.md`.

All three probes the amendments ask for are now in the test's failure message,
and the first is in the pool's debug logging.

## Grounds

- pursued: a waiter past its grace behind a single busy model re-checks within the grace, shown by TestAWaiterPastItsGraceDoesNotParkToTheMaximumBehindAnInFlightModel computing 19.4s before and under 300ms after; wrong if a pool with a very small grace and a large maximum wait shows the re-check cadence costing measurable contention on p.mu, in which case the fix is a floor under the re-check rather than a return to the fallback.
