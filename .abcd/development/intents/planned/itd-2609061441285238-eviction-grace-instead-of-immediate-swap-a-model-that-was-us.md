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

_Empty. Populated by intent-auditor when intent moves to shipped/._

## Grounds

- pursued: we expect a shared Mac to serve several agents without their models evicting each other once the operator can pin, budget and grace, and once keyed clients can see what is warm; we are wrong if model swaps stay as frequent with those controls set as they were without them, measured by the statistics store
