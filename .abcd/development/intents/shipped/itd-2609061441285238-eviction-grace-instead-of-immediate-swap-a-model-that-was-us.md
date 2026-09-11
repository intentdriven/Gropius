---
id: itd-2609061441285238
slug: eviction-grace-instead-of-immediate-swap-a-model-that-was-us
spec_id: spc-2609061822385417
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609061441241254]
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Eviction grace instead of immediate swap: a model that was used moments ago is not evicted the instant another model is requested; the new request waits a bounded time for a model to fall idle past a grace interval, and clients are told when they waited

## Press Release

Bob switches eviction grace on in Settings, because two people share his Mac.
Now, when Alice's agent finishes a turn and pauses to think, and Carol asks in
that pause for a different model, Carol's request waits a little rather than
tearing Alice's model out of memory, and the answer she gets back tells her she
waited and for how long. Alice's next turn finds her model where she left it.
If nothing frees up in time, Carol gets a clear refusal naming the wait instead
of a silent swap under someone else. Bob decides how long a wait is acceptable,
and on the Macs where nobody turns this on, requests are served exactly as they
are today.

## Why This Matters

The least-recently-used rule treats a model idle for one second the same as one
idle for an hour. Between turns an agent is idle, so on a shared machine models
evict each other in exactly the pattern the 2026-09-05 model-bench lab measured
and called preemption. The lab's answer was an external queueing broker with a
reservation interface. A grace interval inside the pool, plus a bounded wait and
a note in the response, gives the same fairness with no reservation interface
and no second process. Pinned models (itd-2609061441241254) cover the always-on
case; grace covers the everything-else case.

Evidence: research note 2026-09-06-model-bench-evidence (swaps measured at 7.7 s to 38 s between the loop's two models; same-model concurrency nearly free; the lab's broker queued by model to avoid exactly the eviction this intent prevents).

## Mechanism

We expect a model that finished a request moments ago to survive a competing
request because the eviction rule will skip it as it already skips models with
work in flight and the competing request will wait rather than evict; we are
wrong if inter-turn pauses are usually longer than any wait clients will
tolerate, in which case grace only delays the same swaps.

## Scope Conditions

- Installs whose operator has switched grace on; it is off by default and the <!-- cond: cond-2609061822386054 -->
  pool behaves exactly as it does today until somebody enables it.
- Two or more clients alternating between models that cannot all sit in the <!-- cond: cond-2609061822384266 -->
  memory budget at once; where they all fit, grace never engages.
- Unpinned models, which are the only eviction candidates in the first place. <!-- cond: cond-2609061822385597 -->
- Clients whose own request timeout is longer than the configured maximum wait; <!-- cond: cond-2609061822389052 -->
  a client that gives up first sees its own error and never sees the wait it
  was told about.

## Acceptance Criteria

- Given grace is switched off, when a model is requested and there is no room
  for it, then a model is evicted at once and no request waits.
- Given grace is on and a resident model finished a request less recently than
  the grace interval allows, when another model is requested with no room for
  it, then the resident model stays loaded and the new request is not yet
  answered.
- Given grace is on and a protected model falls idle for longer than the grace
  interval before the maximum wait runs out, when the wait ends, then the
  requested model loads and the response reports how long the request waited.
- Given grace is on and a resident model keeps receiving a trickle of short
  requests, when a waiting request has waited longer than the grace interval
  and that model is between requests, then the waiting request is served rather
  than left waiting indefinitely.
- Given grace is on and freeing every unpinned model would still not make room
  for the requested one, when the request arrives, then it is refused at once
  without waiting.
- Given grace is on and as many requests are already waiting for room as the
  configured cap allows, when one more arrives, then it is refused immediately
  instead of joining them.
- Given a request is waiting for room, when the operator raises the memory
  budget so that the request now fits, then it proceeds without waiting for
  anything else to finish.

## Open Questions

- Resolved: the grace interval and the maximum wait are Settings fields,
  defaulting to 120 seconds and 300 seconds, behind an enable switch that is
  off, so an install that leaves it alone is unaffected.
- Resolved: protection is measured from the moment a model last finished a
  request, and a waiting request's own age bounds that protection, so a trickle
  of small requests to one model cannot deny another client indefinitely.
- Resolved: a request that could never fit, because the pinned models plus what
  it needs exceed the budget, is refused at once rather than made to wait.
- Resolved: the grace interval is never longer than the idle timeout when one
  is set, so the idle reaper cannot unload the model a wait is protecting.
- Resolved: waiting requests are served oldest first; serving the
  quickest-to-load first would starve large models.
- Resolved: waiting requests have their own cap, separate from the per-model
  queue depth, because a queue that drains only when a model is evicted drains
  far more slowly than one that drains at decoding speed.
- Resolved: a change to the memory budget or to the pinned list wakes waiting
  requests, so a change that makes room is acted on immediately.
- Resolved: the wait is reported in two response headers, `X-Gropius-State` and
  `X-Gropius-Queue-Time`, because the response body has no place for it.
- Resolved: the impact is additive because the feature is off by default; the
  docs state that switching it on means a request may wait where it would once
  have swapped immediately.
- Resolved: no reservation interface and no second process are adopted; if
  reservations are ever wanted, that is a new record.
- Resolved: the eviction-timing race recorded in iss-6 is unchanged by grace,
  which makes it rarer without closing it.
- Depends on: itd-2609061441241254, the pinned models whose skip rule grace
  extends.
- Depends on: itd-2609061441261073, the configurable budget whose live changes
  wake waiting requests.
- Depends on: itd-2609061441228998, which lets clients see what is warm and so
  avoid the wait rather than only be told about it.

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-5c77e5d702ce -->
Fidelity review — receipt rcp-5c77e5d702ce (verifier intent-auditor claude-opus-5[1m]).

Provenance: intent-auditor@claude-opus-5[1m] · rubric_hash sha256:4c74f3e107c9c556e166e99f28e9e3ec975f96554690b04f8ea72c4080239917 · prompt_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5
Input attestations: diff:working tree at HEAD 18f1a4e286fd85f6e94bb2ae1d2ecba40e13cc77 (history rewritten during the rename; no reliable per-spec commit range, so the tree as shipped was audited)@sha256:unknown;

Acceptance rollup: MET 5 · MET_WITH_CONCERNS 2 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: With the grace at zero the wait verdict is waitNoGrace before any queue is consulted and every resident model is an eviction candidate, so the swap is immediate and nothing parks; the default config ships the switch off and a test asserts the eviction and Waiting()==0.
  evidence: internal/runtime/pool.go:1078 — "if !mayWait || p.grace <= 0 { return waitNoGrace"
  evidence: internal/runtime/pool.go:1025 — "if p.grace <= 0 || waited >= p.grace { return true"
  evidence: internal/config/config.go:410 — "EvictionGrace bool `json:"eviction_grace,omitempty"`"
  evidence: internal/runtime/grace_test.go:53 — "func TestWithGraceOffAModelIsEvictedAtOnceAndNothingWaits"
  evidence: internal/config/grace_test.go:40 — "func TestAFreshInstallHasEvictionGraceOffWithTheDefaultIntervals"
- ac-2 — MET: A model whose lastUsed is inside the grace is dropped from the candidate set before the least-recently-used sort, so the load finds no plan and parks as a loadWaiter rather than being answered; the test holds the resident model in memory and asserts the competing Acquire has not returned.
  evidence: internal/runtime/pool.go:995 — "if !p.graceElapsedLocked(e, waited) { continue"
  evidence: internal/runtime/pool.go:1039 — "return p.opts.now().Sub(e.lastUsed) >= p.grace"
  evidence: internal/runtime/pool.go:624 — "w = &loadWaiter{arrived: time.Now(), need: noRoom.need, signal: make(chan struct{}, 1)}"
  evidence: internal/runtime/grace_test.go:78 — "func TestAModelThatJustFinishedIsNotEvictedAndTheRequestWaits"
  evidence: internal/runtime/grace_test.go:99 — "t.Fatalf("Acquire(org/b) returned %v inside the grace; it must still be waiting", err)"
- ac-3 — MET_WITH_CONCERNS: The load half is fully realised — the waiter is woken when the protected model falls past its grace and the acquisition carries the wait it paid — but the reporting half is gated on the install having an API key configured, so on the shipping default (no key) the response reports nothing; concern: 'the response reports how long the request waited' holds only on a keyed install. I reached this independently and agree with the intent's own Audit Note that criterion 3 shipped narrower than written.
  evidence: internal/runtime/pool.go:736 — "QueueWait: waited + p.opts.now().Sub(loaded),"
  evidence: internal/gateway/gateway.go:439 — "setWaitHeaders(w.Header(), g.admittedKeyed(r), up.Waits.LoadWait+up.Waits.QueueWait)"
  evidence: internal/gateway/gateway.go:565 — "func setWaitHeaders(h http.Header, keyed bool, waited time.Duration) { if !keyed { return }"
  evidence: internal/runtime/grace_test.go:125 — "func TestARequestIsServedOnceTheProtectedModelFallsPastItsGrace"
  evidence: internal/gateway/waitheaders_test.go:277 — "t.Run("an open server tells a network client nothing", func(t *testing.T) {"
- ac-4 — MET: graceElapsedLocked's first clause lets a waiter whose own age has reached the grace take any candidate, and the candidate set already excludes entries with work in flight, so a trickle renews idleness but cannot starve the waiter; the test trickles requests at 40 ms and asserts the waiter is served far short of the maximum.
  evidence: internal/runtime/pool.go:1025 — "if p.grace <= 0 || waited >= p.grace {"
  evidence: internal/runtime/pool.go:989 — "if e.inFlight > 0 || !isReady(e) { continue"
  evidence: internal/runtime/pool.go:1033 — "if e.inFlight > 0 { return false }"
  evidence: internal/runtime/grace_test.go:159 — "func TestATrickleOfRequestsCannotStarveAWaitingRequest"
- ac-5 — MET: canEverFitLocked sums only the pinned entries against the budget — i.e. what would remain after freeing every unpinned model — and a load that cannot fit that way is given waitNeverFits before any waiter is created, so it is refused without queueing; the test asserts the refusal is under a second and Waiting() stayed zero.
  evidence: internal/runtime/pool.go:1092 — "if !p.canEverFitLocked(noRoom.need) { return waitNeverFits"
  evidence: internal/runtime/pool.go:1136 — "if p.isPinnedLocked(e.repoID) { protected += LoadCost(e.bytes)"
  evidence: internal/runtime/grace_test.go:218 — "func TestARequestThatCanNeverFitIsRefusedWithoutWaiting"
  evidence: internal/runtime/grace_test.go:245 — "t.Errorf("the refusal took %s; a request that can never fit must not wait", took)"
- ac-6 — MET_WITH_CONCERNS: Past MaxLoadWaiters a new arrival gets waitQueueFull and is refused without joining the queue, as promised — but admissionLocked grants such an arrival admitFreeRoom, so a request that needs no eviction (its model already resident, or it fits in unused memory) is served instead of refused; concern: the criterion's 'refused immediately' holds only for arrivals that would need a victim. I reached this independently and agree with the intent's own Audit Note that criterion 6 shipped narrower than written.
  evidence: internal/runtime/pool.go:1097 — "return waitQueueFull"
  evidence: internal/runtime/pool.go:1176 — "if w == nil && len(p.waiters) >= p.opts.MaxLoadWaiters { return admitFreeRoom"
  evidence: internal/runtime/pool.go:959 — "case admitFreeRoom: return len(victims) == 0"
  evidence: internal/runtime/grace_test.go:253 — "func TestPastTheLoadWaiterCapARequestIsRefusedAtOnce"
  evidence: internal/runtime/grace_test.go:1008 — "func TestAFullQueueDoesNotRefuseALoadThatNeedsNoEviction"
  evidence: internal/runtime/grace_test.go:1061 — "func TestAQueueFullArrivalTakesNoVictimAndCostsNoSyscall"
- ac-7 — MET: SetMemoryBudget wakes every parked waiter under the same lock the eviction paths read the figure under, and a woken waiter whose need now fits takes the early 'enough room' return of the eviction plan with an empty victim list, so it proceeds with nothing unloaded and no request ended; four parked waiters are all served on one raise while every one of them is still held open.
  evidence: internal/runtime/pool.go:352 — "p.wakeWaitersLocked()"
  evidence: internal/runtime/pool.go:974 — "if used+need <= p.maxResident { return nil, true }"
  evidence: internal/runtime/grace_test.go:343 — "func TestRaisingTheMemoryBudgetWakesAWaitingRequest"
  evidence: internal/runtime/grace_test.go:1116 — "func TestRaisingTheMemoryBudgetServesEveryWaiterItFits"

Gap audit:
- honoured:
  - On the Macs where nobody turns this on, requests are served exactly as they are today — grace is off by default and the pool takes its victim at once.
    evidence: internal/config/config.go:827 — "EvictionGraceSec: DefaultEvictionGraceSec,"
    evidence: internal/runtime/pool.go:1078 — "if !mayWait || p.grace <= 0 {"
    evidence: internal/runtime/grace_test.go:53 — "func TestWithGraceOffAModelIsEvictedAtOnceAndNothingWaits"
  - Carol's request waits a little rather than tearing Alice's model out of memory: a model that just finished work is skipped by the eviction rule and the competing request parks.
    evidence: internal/runtime/pool.go:995 — "if !p.graceElapsedLocked(e, waited) {"
    evidence: internal/runtime/grace_test.go:78 — "func TestAModelThatJustFinishedIsNotEvictedAndTheRequestWaits"
  - A trickle of small requests to one model cannot deny another client indefinitely — the waiting request's own age bounds the protection.
    evidence: internal/runtime/pool.go:1025 — "if p.grace <= 0 || waited >= p.grace {"
    evidence: internal/runtime/grace_test.go:159 — "func TestATrickleOfRequestsCannotStarveAWaitingRequest"
  - If nothing frees up in time, Carol gets a clear refusal instead of a silent swap; a request that could never fit is refused at once rather than made to wait.
    evidence: internal/runtime/pool.go:1092 — "if !p.canEverFitLocked(noRoom.need) {"
    evidence: internal/runtime/pool.go:1102 — "if time.Since(w.arrived) >= p.maxWait { return waitTimedOut"
    evidence: internal/runtime/grace_test.go:218 — "func TestARequestThatCanNeverFitIsRefusedWithoutWaiting"
    evidence: internal/runtime/grace_test.go:484 — "func TestAWaitingRequestGivesUpAfterTheMaximumWait"
  - Bob decides how long a wait is acceptable: the grace and the maximum wait are Settings fields behind an enable switch, bounded and never longer than the idle timeout.
    evidence: internal/config/config.go:741 — "if c.EvictionGrace && c.IdleTimeoutSec > 0 && c.GraceSeconds() > c.IdleTimeoutSec {"
    evidence: internal/config/config.go:755 — "if c.EvictionGrace && c.MaxWaitSeconds() < c.GraceSeconds() {"
    evidence: internal/ui/grace_test.go:15 — "func TestSettingsFormPostsTheEvictionGraceFields"
    evidence: internal/app/grace_test.go:54 — "func TestSetConfigAppliesTheEvictionGraceWithoutARestart"
  - A change to the memory budget or to the pinned list wakes waiting requests, so a change that makes room is acted on immediately.
    evidence: internal/runtime/pool.go:327 — "p.wakeWaitersLocked()"
    evidence: internal/runtime/pool.go:352 — "p.wakeWaitersLocked()"
    evidence: internal/runtime/grace_test.go:1116 — "func TestRaisingTheMemoryBudgetServesEveryWaiterItFits"
  - Waiting requests have their own cap, separate from the per-model queue depth, and are served oldest first.
    evidence: internal/runtime/pool.go:263 — "const defaultMaxLoadWaiters = 8"
    evidence: internal/runtime/pool.go:1174 — "if len(p.waiters) == 0 || (w != nil && p.waiters[0] == w) { return admitEvict"
    evidence: internal/runtime/grace_test.go:644 — "func TestARequestThatFitsDoesNotStepOverTheWaiterAtTheHead"
  - No reservation interface and no second process were adopted — the grace, the queue and the wait all live inside the existing pool.
    evidence: internal/runtime/pool.go:202 — "waiters []*loadWaiter"
    evidence: internal/runtime/pool.go:524 — "func (p *Pool) acquire(ctx context.Context, repoID string, mayWait bool) (*Upstream, func(), error) {"
- diverged:
  - "The answer she gets back tells her she waited and for how long" (ac-3): the two response headers are written only on an install that has an API key configured, so on the shipping default — no key — a network client is told nothing. The wait is still measured and still recorded in the statistics; only the disclosure is gated, on the residency rule the models list already follows.
    evidence: internal/gateway/gateway.go:565 — "func setWaitHeaders(h http.Header, keyed bool, waited time.Duration) { if !keyed { return }"
    evidence: internal/gateway/gateway.go:414 — "obs.waited(runtime.AcquireStats{QueueWait: noRoom.Waited})"
    evidence: internal/gateway/waitheaders_test.go:275 — "func TestTheWaitHeadersFollowTheResidencyRule"
  - "As many requests are already waiting as the cap allows, then one more is refused immediately" (ac-6): past the cap an arrival that needs nothing unloaded is served rather than refused. The concession is bounded to free memory — such a caller may not take a victim, because the waiter at the head is parked precisely because that free room is not enough for it.
    evidence: internal/runtime/pool.go:1176 — "if w == nil && len(p.waiters) >= p.opts.MaxLoadWaiters { return admitFreeRoom"
    evidence: internal/runtime/pool.go:959 — "case admitFreeRoom: return len(victims) == 0"
    evidence: internal/runtime/grace_test.go:1008 — "func TestAFullQueueDoesNotRefuseALoadThatNeedsNoEviction"
    evidence: internal/runtime/grace_test.go:1061 — "func TestAQueueFullArrivalTakesNoVictimAndCostsNoSyscall"
  - "It proceeds without waiting for anything else to finish" (ac-7) is delivered, but the several waiters a raise fits are served in queue order rather than simultaneously: the oldest loads and leaving the queue wakes the next. Nothing has to finish, so the criterion's words hold; the stronger reading the wording invites does not.
    evidence: internal/runtime/pool.go:456 — "p.wakeWaitersLocked()"
    evidence: internal/runtime/pool.go:1174 — "if len(p.waiters) == 0 || (w != nil && p.waiters[0] == w) {"
    evidence: internal/runtime/grace_test.go:1116 — "func TestRaisingTheMemoryBudgetServesEveryWaiterItFits"
- missing: (none)

Scope-condition dispositions:
- cond-2609061822386054 — narrowed: Grace is off by default and switching it on is the only gate on the pool-side behaviour, but it is not the only gate on the record's client-facing promise: the wait report additionally requires a configured API key, which this condition does not mention.
  narrowing: holds in full only for installs that have switched grace on AND have an API key configured; on a keyed-less install that switches grace on, the waiting and the protection happen but the response reports nothing
  evidence: internal/config/grace_test.go:40 — "func TestAFreshInstallHasEvictionGraceOffWithTheDefaultIntervals"
  evidence: internal/gateway/gateway.go:565 — "func setWaitHeaders(h http.Header, keyed bool, waited time.Duration) { if !keyed { return }"
  evidence: internal/gateway/waitheaders_test.go:277 — "t.Run("an open server tells a network client nothing", func(t *testing.T) {"
- cond-2609061822384266 — survived: The eviction plan returns 'enough room, no victims' before any candidate is considered when the load already fits the budget, and the queue is only ever joined off a NoRoomError, so where the models all fit nothing waits and grace never engages.
  evidence: internal/runtime/pool.go:974 — "if used+need <= p.maxResident { return nil, true }"
  evidence: internal/runtime/pool.go:1086 — "if !errors.As(err, &noRoom) { return waitNoGrace"
  evidence: internal/runtime/grace_test.go:1116 — "func TestRaisingTheMemoryBudgetServesEveryWaiterItFits"
- cond-2609061822385597 — survived: Pinned entries are removed from the candidate set before the least-recently-used comparison and are the only memory counted as permanently spoken for in the can-this-ever-fit test, so unpinned models remain the sole eviction candidates under grace exactly as they are without it.
  evidence: internal/runtime/pool.go:992 — "if p.isPinnedLocked(e.repoID) { continue"
  evidence: internal/runtime/pool.go:1136 — "if p.isPinnedLocked(e.repoID) { protected += LoadCost(e.bytes)"
  evidence: internal/runtime/grace_test.go:382 — "func TestPinningTheOnlyCandidateReleasesAWaitingRequestAtOnce"
- cond-2609061822389052 — survived: A client that gives up first cancels its context: the waiter leaves the queue and Acquire returns ctx.Err(), and the gateway returns on context.Canceled before any wait header or body is written, so that client sees only its own error.
  evidence: internal/runtime/pool.go:640 — "p.leaveQueueLocked(w) p.mu.Unlock() return nil, nil, ctx.Err()"
  evidence: internal/gateway/gateway.go:416 — "if errors.Is(err, context.Canceled) { obs.failed(stats.ClassCancelled) return // the client hung up while the model was loading"
  evidence: internal/runtime/grace_test.go:306 — "func TestACancelledWaiterGivesItsPlaceBack"
## Grounds

- pursued: we expect a shared Mac to serve several agents without their models evicting each other once the operator can pin, budget and grace, and once keyed clients can see what is warm; we are wrong if model swaps stay as frequent with those controls set as they were without them, measured by the statistics store
