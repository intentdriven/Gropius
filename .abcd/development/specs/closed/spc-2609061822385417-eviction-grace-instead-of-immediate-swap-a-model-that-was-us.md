---
id: spc-2609061822385417
slug: eviction-grace-instead-of-immediate-swap-a-model-that-was-us
intent: itd-2609061441285238
origin: researcher-authored
production_mode: hand-written
---
# eviction-grace-instead-of-immediate-swap-a-model-that-was-us

## Summary

This spec adds a bounded wait to the pool. With grace switched on, a request for
a model that does not fit does not evict a model that finished work moments ago:
it joins a first-in, first-out queue of load-waiters and takes an eviction
victim only once a candidate has been idle for the grace interval — or once the
waiter's own age has passed it, so a trickle of small requests cannot starve
anyone. The wait is bounded by a maximum, capped in number, and reported to the
client in two response headers. Switched off, which is the default, the pool
behaves exactly as it does today.

## Scope

In scope:

- Three settings: an enable switch (off), a grace interval (120 s) and a maximum
  wait (300 s), plus a load-waiter cap with its own `PoolOptions` field.
- A waiter queue inside `Pool`, its feasibility pre-check, its wake-up sources,
  `Pool.Waiting()`, and `entry.lastFinished` — the clock grace reads.
- `Upstream.Waited` and the two response headers on the success and refusal
  paths, and a no-grace path for start-up loaders.

Out of scope:

- Any reservation interface and any second process; if reservations are ever
  wanted, that is a new record.
- Closing iss-6 (eviction credits memory before the victim exits); grace makes it
  rarer without closing it.
- Changing what happens when grace is off. That path is untouched.

## Approach

**Two clocks, and which one governs.** `entry.lastUsed` is refreshed on every
`Acquire` *and* on every `release`, so it is a liveness marker, not an idleness
one. A new `entry.lastFinished`, set in `release` when `inFlight` drops to zero,
records when the model last finished work. Protection is read from
`lastFinished`; **eligibility is read from the waiter's own age**. A waiter may
take a victim when the candidate has been idle for at least the grace interval,
**or** when the waiter itself has waited at least that long and the candidate is
between requests. That second clause is what stops a client sending a one-token
request to model A every few seconds from denying model B indefinitely — the
starvation the least-recently-used rule does not have and grace must not
introduce.

**Feasibility before waiting.** Under `p.mu`, before a waiter is admitted, the
pool asks whether evicting every unpinned, ready entry would free enough for
`need`. If not — because the pinned sum plus `need` exceeds the budget, or the
model alone does — the request is refused at once with the pinned refusal, never
made to wait. Without this a request that can never fit would hold a connection
and a buffered body for the whole maximum wait, which is exactly the backlog
hazard `MaxQueueDepth`'s comment describes.

**The queue.** Waiters are held in an arrival-ordered slice under `p.mu`, each
with its own signal channel, its arrival time and its `need`. The head is served
first: serving the quickest-to-load first would starve large models, and the
existing `e.sem` queue is already arrival-ordered, so this is the same
discipline. `MaxLoadWaiters` is its **own** `PoolOptions` field with its own
default and its own rationale — `MaxQueueDepth`'s 64 was chosen for a queue that
drains at decoding speed, while this one drains only on eviction events, and each
waiter is a goroutine holding a buffered request body of up to `maxRequestBody`
(32 MiB). A default in single digits is right, and the field's comment carries
that arithmetic. Past the cap, `Acquire` refuses immediately rather than joining.

**Wake-ups.** A waiter is re-evaluated when: `release` drops an entry's
`inFlight` to zero; an entry is stopped; the oldest protected candidate's grace
elapses (a timer, not a poll); `SetMaxResidentBytes` changes the budget; or
`SetPinned` changes the pinned set. The last two are why this record is
sequenced after itd-2609061441261073 and itd-2609061441241254 — without them a
budget raise that makes room would leave waiters asleep until the next release.

**Grace and the reaper.** `reapIdle` unloads any idle, unpinned model after the
idle timeout, so a grace longer than the idle timeout would have the reaper
unload the very model a wait is protecting, and a grace shorter than it makes
the wait pointless in the other direction. `App.SetConfig` therefore **refuses**
a save whose grace exceeds a non-zero idle timeout, naming both figures, and
`config.Save` is not called.

**Reporting the wait.** `runtime.Upstream` gains `Waited time.Duration`.
`handleCompletions` sets `X-Gropius-State` (`warm` when nothing waited, `waited`
otherwise) and `X-Gropius-Queue-Time` (milliseconds) on `w.Header()` at two
sites: before `copyResponseHeaders`/`WriteHeader` on the success path, and
before `writeError` on the refusal path, since `writeError` calls `WriteHeader`
immediately. Both paths, streaming and not, take the same code.

**Start-up loaders.** `App.preload` is sequential on purpose; five models that do
not co-fit would otherwise stall start-up by five maximum waits, so preload
passes a no-grace option. The control plane's `handleLoad` keeps waiting: the
operator asked for the load and can unload something to force it.

## How each acceptance criterion is satisfied

1. _Given grace is switched off, when a model is requested and there is no room
   for it, then a model is evicted at once and no request waits._ With
   `GraceEnabled` false, `evictForLocked` runs exactly as today and no waiter is
   ever created. Test (`internal/runtime`, fake launcher, injected `now`): the
   existing eviction tests are re-run with grace off and must not change, and
   `Pool.Waiting()` stays zero.
2. _Given grace is on and a resident model finished a request less recently than
   the grace interval allows, when another model is requested with no room for
   it, then the resident model stays loaded and the new request is not yet
   answered._ The candidate's `lastFinished` is inside the grace and the waiter's
   age is not yet past it, so no victim is eligible. Test: advance the injected
   clock inside the grace, `Acquire` in a goroutine, assert the resident entry is
   still present and `Pool.Waiting()` is 1, with `Acquire` not yet returned.
3. _Given grace is on and a protected model falls idle for longer than the grace
   interval before the maximum wait runs out, when the wait ends, then the
   requested model loads and the response reports how long the request waited._
   The grace timer wakes the head waiter, which takes the now-eligible victim.
   Test (`runtime`): assert `Acquire` returns an `Upstream` whose `Waited` is the
   elapsed injected time. Test (`gateway`): a streaming completion through a stub
   pool reporting `Waited`, asserting both headers are present on the SSE
   response — the streaming path specifically, because the JSON path is the easy
   one.
4. _Given grace is on and a resident model keeps receiving a trickle of short
   requests, when a waiting request has waited longer than the grace interval and
   that model is between requests, then the waiting request is served rather than
   left waiting indefinitely._ The waiter-age clause. Test: a loop that acquires
   and releases the resident model every fraction of the grace while a waiter is
   queued; assert the waiter is served once its own age passes the grace, and
   that it is served in a bounded number of cycles.
5. _Given grace is on and freeing every unpinned model would still not make room
   for the requested one, when the request arrives, then it is refused at once
   without waiting._ The feasibility pre-check. Test: pinned entries filling the
   budget, `Acquire` a third; assert the error returns immediately, that
   `Pool.Waiting()` never rises, and that the message names no model.
6. _Given grace is on and as many requests are already waiting for room as the
   configured cap allows, when one more arrives, then it is refused immediately
   instead of joining them._ The `MaxLoadWaiters` check runs under `p.mu` before
   the waiter is enqueued. Test: fill the cap, assert the next `Acquire` returns
   `ErrBusy` at once and `Pool.Waiting()` equals the cap.
7. _Given a request is waiting for room, when the operator raises the memory
   budget so that the request now fits, then it proceeds without waiting for
   anything else to finish._ `SetMaxResidentBytes` signals every waiter. Test: a
   waiter parked with no idle candidate, call `SetMaxResidentBytes` with a figure
   that fits, assert `Acquire` returns without any entry being stopped.

Additionally, and drawn out of the intent's fourth scope condition into a bar:
a waiter whose client context is cancelled returns `ctx.Err()` and is removed
from the queue, with `Pool.Waiting()` dropping — the client sees its own error
and never sees the headers.

## Trust-boundary review notes

`internal/runtime`, `internal/gateway` and `internal/config` are touched.

- **`internal/runtime` (subprocess management).** The waiter queue is the new
  resource. It is bounded by `MaxLoadWaiters`, each waiter is removed on context
  cancellation, and the feasibility pre-check refuses the cases that could never
  drain. The memory arithmetic is stated in the field's comment: waiters times
  `maxRequestBody` is the worst case a hostile client can pin, and the cap is
  chosen against that, not inherited from `MaxQueueDepth`. Every wake-up path
  takes `p.mu`; the timer fires outside the lock and re-checks under it, so a
  woken waiter never acts on a stale view. Nothing here starts or stops a process
  that `evictForLocked` and `startLocked` did not already.
- **`internal/gateway` (network input).** Two new response headers and a new 503
  message are API surface clients will depend on; they are documented. The
  refusal names no model, keeping the disclosure rule
  spc-2609061822370978 sets. `X-Gropius-Queue-Time` reveals only that this
  machine was busy — the same fact a timing measurement already gives — and
  carries no model name and no count.
- **`internal/config` (file parsing).** Three fields: one boolean and two
  non-negative integers, validated machine-independently. The grace-versus-idle
  refusal lives in `App.SetConfig`, not in `Validate`, so a config file is never
  made unloadable by it.
- **Denial of service.** Grace converts some swaps into waits, and a wait holds a
  connection. The cap, the maximum wait and the feasibility check are the three
  bounds; the docs say switching grace on means a request may wait where it once
  swapped. iss-6 is unchanged — rarer, not closed.

## Docs to change

- `docs/getting-started.md`: section 6 gains eviction grace — that it is off by
  default, what the two intervals mean, that a request may wait where it once
  swapped, that the grace may not exceed a set idle timeout, and the two
  response headers a client can read.
- `README.md`: one present-tense sentence naming grace beside pinning and the
  budget.
- `docs/models-list.md` (created by spc-2609061822377049): a pointer noting that
  reading residency before choosing a model is how a client avoids the wait
  rather than only being told about it.

## Dependencies and sequencing

- Depends on itd-2609061441241254 (spc-2609061822370978): the pinned skip rule
  defines the eligible-victim set this record waits on, and the pinned sum is
  what the feasibility check measures against.
- Depends on itd-2609061441261073 (spc-2609061822383286): `SetMaxResidentBytes`
  is a wake-up source and criterion 7 is written against it.
- Depends on itd-2609061441228998 (spc-2609061822377049), so clients can see
  what is warm and avoid the wait rather than only be told about it.
- Sequenced last of the four pool records. It is the only one that adds a
  blocking path to `Acquire`, and it wants the other three seams to exist first.

## Open design points

- The default value of `MaxLoadWaiters`: single digits, justified by the
  waiters-times-body arithmetic, written into the field's comment.
- Whether `X-Gropius-State` carries a third value for a request served without
  loading at all. Two values satisfy the criteria.
- Whether the control panel renders `Pool.Waiting()`. Presentation only.

## As built (2026-09-07)

Appended on closing, so a reader reconciling this record against the tree does
not find the differences unexplained. Nothing above this section is edited.

- **Zero means the default for the two intervals.** The Scope names three
  settings; as built, `EvictionGraceSec` and `EvictionMaxWaitSec` treat zero as
  "the default" rather than as a figure `Validate` refuses. The panel's number
  field posts `parseInt(...) || 0`, so a strict rule would have refused the
  whole settings save — the API key with it — over an interval the operator was
  clearing. `Config.GraceSeconds` and `Config.MaxWaitSeconds` resolve it, and
  everything that acts on the figures reads them there.
- **The grace-versus-idle refusal lives in `config.Validate`**, not in
  `App.SetConfig` as the Approach says. `Load` sanitises before it validates and
  `sanitizeGrace` clamps the grace to the idle timeout, so a config file is
  never made unloadable by the rule — the Approach's only reason for keeping it
  out of `Validate`. It applies only while the switch is on.
- **`Upstream.Waited` was not added.** The wait is carried on the existing
  `Upstream.Waits.QueueWait`: the statistics recorder already has
  `queue_wait_ms` and `load_wait_ms`, and a wait for room is a queue wait by
  that schema's own definition — the machine was busy, nothing was loading. The
  refusal path, which has no `Upstream`, carries it on `NoRoomError.Waited`.
- **The two headers are defined against every wait**, not only the grace wait.
  `X-Gropius-Queue-Time` is `LoadWait + QueueWait` in whole milliseconds and
  `X-Gropius-State` is `waited` exactly when that is non-zero. Defining them on
  the grace wait alone would have reported a cold start as `warm`. The open
  point about a third value is therefore settled as no: two values and one
  number that agree by construction.
- **`Pool.SetEvictionGrace` and `Pool.EvictionGrace()`**, not the Approach's
  unnamed setter. Off is a grace of zero rather than a flag of its own, so the
  load path has one question to answer; switching it off wakes every parked
  request.
- **`Pool.AcquireNow`** is the "no-grace option" the Approach names for
  start-up loaders, and it waives the grace as well as the wait — waiving only
  the wait left a preload refused by the grace alone, with the model cold for
  nothing.
- **`evictForLocked` is plan-then-execute under grace and incremental with
  grace off.** The Approach does not say how the pre-check and the eviction
  compose; as built, a request about to join the queue evicts nothing, while
  with grace off the victim set and its order — including the case where
  eviction cannot free enough and everything evictable goes before the refusal —
  are exactly what they were.
- **Strict first-in, first-out.** Only the head waiter may take a victim, and
  the rule binds a request that has not queued at all, or a new arrival would
  step over everyone waiting. The cost is head-of-line blocking, bounded by the
  maximum wait.
- **`MaxLoadWaiters` defaults to 8** — the open design point. Eight waiters
  times the gateway's 32 MiB body limit is 256 MiB an unadmitted client can pin,
  and the field's comment carries that arithmetic.
- **The control panel renders `Pool.Waiting()`** — the other open design point,
  settled as yes. It reaches the panel as `State.Waiting` and a line on **My
  Models**: a waiting request holds no model, so it appears on no card, and a
  Mac with nothing loading looks identical to one with four clients queued.
- **The docs list differs.** `docs/getting-started.md` gains a subsection under
  section 4 rather than section 6 (loading models is the subject there, not
  hardening); the two headers get their own reference page,
  `docs/response-headers.md`, rather than a paragraph in a how-to; and a new
  how-to, `docs/eviction-grace.md`, carries the operator-facing rules.
  `docs/models-list.md` gains the pointer the record asks for and has its
  eviction bullet corrected, since it ended in "refused … rather than waiting",
  which grace makes conditional.
- **`entry.lastFinished` is kept although it does not yet differ from
  `lastUsed`** for an idle entry — `release` stamps both. Its comment says so:
  the two coincide only while the candidate filter skips every entry with a
  request in flight, so a grace read from `lastUsed` would be correct by that
  filter's leave rather than by its own rule.

## As built, addendum (2026-09-07, after the two adversarial reviews)

Two independent reviews — one adversarial security, one senior code — ran
against the branch and returned BLOCK and FIX FIRST. Three points above are
superseded; the section stands as written and this one governs where they
differ.

- **Point 8 is narrowed.** "Strict first-in, first-out — only the head waiter
  may take a victim" was the claim; the code gated the eviction and not the
  load, so a request needing no eviction took the free room as it appeared and
  a waiter at the head needing more than any one release frees was refused at
  its maximum. That is the "smallest to load first starves a large model"
  failure the queue exists to prevent, reached by the other door. `mayEvict`
  now refuses the load whether or not the plan needs a victim, and
  `mayEvictLocked`'s doc comment says so. Held by
  `TestARequestThatFitsDoesNotStepOverTheWaiterAtTheHead`.
- **The last point is withdrawn: `entry.lastFinished` is gone.** It was
  provably equal to `lastUsed` for every entry the candidate filter admits —
  `release` stamps both, `startLocked` stamps both, and nothing else writes
  either while an entry is idle — so keeping it was a second invariant to
  maintain in exchange for a comment. `graceElapsedLocked` reads `lastUsed` and
  carries the reasoning, including why the in-flight skip is part of that rule
  rather than merely part of its caller's. The Approach's `entry.lastFinished`
  is therefore not in the tree.
- **A waiter re-checks its context on every pass**, which the Approach does not
  mention. A woken waiter blocks on `p.mu`, the lock every `Acquire`,
  `Resident`, `Unload` and control-panel snapshot takes, and the client can
  disconnect while it is blocked; without the check the loop went on to evict a
  warm model and start a server for a request that no longer existed. Held by
  `TestACancelledWaiterDoesNotEvictOnItsWayOut`, which holds the window open by
  taking `p.mu` from the test.
- **A waiter that is not at the head does not call `startLocked`.** Every
  wake-up wakes every waiter, and `startLocked` resolves the model and stats the
  launcher's files before it reaches the eviction plan, so a release did
  filesystem work under `p.mu` once per parked request. `loadWaiter.need`
  remembers what the failed attempt asked for, which is all the re-check needs.
- **Six guards had no test that could fail.** The queue's fairness, the wake on
  `release`, on `stopEntryLocked` and on `leaveQueueLocked`, the plan's
  least-recently-used order, and the grace-off partial eviction were all
  satisfiable by a build that did nothing. Each now has a test watched to fail
  with the guard removed.
- **`copyResponseHeaders` skips the two Gropius headers.** It merges rather
  than replaces, so a model server emitting either name added a second value
  beside ours.
- **`docs/statistics-store-reference.md`'s `queue_wait_ms` row** now states the
  wait for room as well as the wait for a slot, which point 3 of the section
  above changed and that page did not carry.

## As built, second addendum (2026-09-07, after the security re-review)

The first review round saw a tree that had been poisoned by another agent's
in-flight mutation, so the branch was frozen and re-reviewed. The re-review
confirmed every fix above by mutation and found one thing they had introduced.

- **A caller that will not wait is not held behind the queue either.** Making
  the fairness gate cover admission (first addendum, point 1) caught
  `AcquireNow` with it: `mayEvictLocked(nil)` is false whenever anything is
  queued, so a start-up preload was refused — with a message saying no model
  could be freed, when nothing needed freeing — and the model was left cold for
  the session. `acquire` now sets both the grace waiver and the queue waiver
  together for a non-waiting caller. Held by
  `TestAcquireNowIsNotHeldBehindTheQueue`, which is a behavioural test where
  `TestPreloadDoesNotWaitOutAnEvictionGrace` is only a source assertion.
- **A parked waiter asks for a load only when it could proceed**
  (`worthTryingLocked`). `startLocked` resolves the model and stats two files
  before it reaches the eviction plan, and every completed request wakes every
  waiter, so a waiter that could not have proceeded was doing filesystem work
  under the pool's one lock at the rate the machine serves requests. Held by
  `TestAParkedWaiterDoesNoFilesystemWorkOnEveryCompletedRequest`, which counts
  launch prechecks.
- **The `MaxLoadWaiters` arithmetic is corrected.** A waiter holds the request
  body twice — the bytes the gateway read and the decoded value, which copies
  rather than aliases — so the figure the default is chosen against is about
  64 MiB a waiter and half a gigabyte for eight, not 256 MiB.
- **`loadWaiter.need` can go stale** and its comment now says what that costs:
  only whether a waiter is woken early or parked a little longer, never what is
  admitted, since the load itself resolves the model again.
- **Two findings are captured rather than fixed.** The waiter queue is one
  queue for the whole machine and its cap counts requests rather than sources
  (iss-2609070252377294); the control panel's Load button can fill it from
  loopback (iss-2609070252378091). Both are bounded and behind a switch that is
  off; a per-source cap is a design decision this record does not settle. The
  first is stated in `docs/eviction-grace.md`'s "What it costs".

## As built, third addendum (2026-09-07, after the design review)

A design and correctness review against this record and the intent found one
blocking defect and three that shipped differently from what the record says.

- **Switching grace off released only the waiter at the head.** The Approach
  makes the off switch the operator saying "swap now", and three comments and
  the how-to promise that every parked request takes its victim. What shipped
  refused every waiter that reached the pool's lock while somebody else was
  still at the head: the fairness gate said it could not evict, so it never
  attempted, and the queue check then refused it because grace was off. With
  grace off there is no queue to be fair to — `mayEvictLocked` and
  `worthTryingLocked` both admit every caller — and each takes the ordinary
  path, which evicts what it can before refusing anything. Held by
  `TestSwitchingGraceOffReleasesEveryWaitingRequest`, which parks seven: with
  one waiter, which is what the first cut's test parked, the failing moment
  does not exist.
- **The grace/idle invariant was judged on the wrong figure.** `config.Validate`
  compares the grace with the idle timeout in the settings file, but the pool
  reaps on the one it was built with, and the idle timeout only reaches it at a
  restart while the grace is applied live. One save that raised the timeout and
  set a longer grace therefore left the reaper unloading the very model a
  request was waiting on — the one state this record wrote a refusal for.
  `App.enforcedGrace` now holds what the pool is given down to the idle timeout
  in force, following `enforcedBudget`: the operator's figures stay stored, the
  clamp is logged, and a restart gives them what they asked for. Refusing the
  save instead would have been the wedge this repository has now built three
  times. Held by
  `TestTheEnforcedGraceIsNeverLongerThanTheIdleTimeoutInForce`.
- **A refusal after a wait recorded `queue_wait_ms` 0** while reporting the wait
  in its header, so the statistics store — the surface an operator would use to
  ask what grace is costing — was blind to the outcome grace produces when it
  fails. `handleCompletions` now calls the observer on that path. Held by
  `TestARefusalAfterAWaitIsRecordedWithTheWaitItPaid`.
- **`stats.Record.QueueWaitMS`'s own comment** still described a slot on a
  loaded model. The as-built decision above rests on that definition, so the
  definition is corrected where it lives and not only on the reference page.
- Smaller, from the same review: `graceElapsedLocked` enforces its own
  in-flight premise rather than borrowing its caller's; a refusal because the
  queue is full says so in this Mac's log instead of reading like a machine
  whose memory is all spoken for (`waitVerdict`); `docs/response-headers.md`
  says which answers do *not* carry the two headers, since the overload and
  launch-failure 503s do not; and
  `TestARequestThatReallyWaitedSaysSoOnTheWire` drives a real parked waiter
  through the gateway to the header, which is the seam the stub-pool tests
  cannot hold.
