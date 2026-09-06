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
