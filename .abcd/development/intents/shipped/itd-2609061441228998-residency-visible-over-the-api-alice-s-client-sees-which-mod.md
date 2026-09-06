---
id: itd-2609061441228998
slug: residency-visible-over-the-api-alice-s-client-sees-which-mod
spec_id: spc-2609061822377049
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Residency visible over the API: Alice's client sees which models are loaded right now from the models list, so it can pick the warm one instead of triggering a swap

## Press Release

Alice's agent holds the key Bob handed out for his Mac. It lists the models and
sees, next to each one, whether that model is loaded right now, how much work
it is already handling, and when it was last used. It sends its work to the
warm model and never forces a multi-minute reload just because it did not know.
Carol's agent, holding the same key, sees the same picture and makes the same
choice, so the two of them share a resident model instead of evicting each
other's. On a Mac that Bob has left open to the network with no key, the list
is exactly what it is today, and nobody learns from it what he is running.

## Why This Matters

The models list shows every downloaded model with an id, a timestamp and an
owner, and nothing about whether it is resident. A client cannot tell a warm
model from one that will take minutes to load, so it picks blind, and a blind
pick is what triggers the swaps the 2026-09-05 model-bench lab measured as the
expensive event. This intent puts that fact where entitled clients can read it.
It is the cheapest change on the lab's list and removes the need for the lab's
status proxy. The field is meaningless without the eviction rule beside it, so
the docs defect iss-2609061443332414 is settled in the same change.

Evidence: research note 2026-09-06-model-bench-evidence (same-model concurrency against swap cost: six concurrent requests at 3.6 times the throughput of one; a cold swap between the two workhorse models at 7.7 s).

## Mechanism

We expect a keyed client that reads residency before choosing to stop
triggering load-time swaps because a request naming a resident model takes the
pool's fast path and evicts nothing, while a request naming a cold model
evicts under a full budget; we are wrong if swap counts on a two-agent Mac are
unchanged once clients can see what is warm.

## Scope Conditions

- Installs with an API key configured; where no key is set the list is <!-- cond: cond-2609061822377016 -->
  unchanged and the claim does not apply.
- Clients that list the models before each request, or on a schedule short <!-- cond: cond-2609061822376834 -->
  enough that the snapshot is still true; a client that lists once at start-up
  gains nothing after the first change.
- Machines holding more downloaded models than the memory budget can keep <!-- cond: cond-2609061822373573 -->
  resident at once, or running an idle timeout, so residency actually varies.
- The pool's process-per-model design and its least-recently-used eviction <!-- cond: cond-2609061822377503 -->
  rule as they stand today.

## Acceptance Criteria

- Given an API key is configured and a downloaded model is not loaded, when a
  client lists the models with the key, then that model's entry reports it as
  not loaded and the entry's pre-existing fields are unchanged.
- Given an API key is configured and a model's server has answered its
  readiness probe, when a client lists the models with the key, then that
  model's entry reports it as loaded.
- Given an API key is configured and a model's server has been started but has
  not yet answered its readiness probe, when a client lists the models with the
  key, then that model's entry reports it as loading.
- Given an API key is configured, when a client lists the models with the key,
  then each entry also reports how many requests are in flight for that model
  and when that model was last used.
- Given no API key is configured, when a client lists the models, then no entry
  carries residency, in-flight, last-used or pinned information.
- Given any models listing, when the response is inspected, then it carries no
  loopback port and no filesystem path.
- Given the reference page for the models list, when a reader looks it up, then
  the residency values, the keyed-install condition and the fact that the
  listing is a snapshot rather than a reservation are all stated.

## Open Questions

- Resolved: residency is reported on each entry of the models list; no separate
  status endpoint is added.
- Resolved: the residency information appears only when an API key is
  configured, so an open server discloses nothing it does not already disclose
  to anyone willing to time a request.
- Resolved: three states are reported — loaded, loading, not loaded — because a
  model mid-load is neither warm nor cold.
- Resolved: the in-flight count and the last-used time appear alongside the
  state on a keyed install, matching what the control panel shows on loopback.
- Resolved: pinned status follows the same rule and appears only on a keyed
  install; the pin list itself belongs to itd-2609061441241254.
- Resolved: this record owns the shape of the models-list extension — top-level
  fields under common names, no namespaced object — and itd-2609061431463108
  follows the same shape for the context length.
- Resolved: the reported state is a snapshot taken when the list is built, not
  a reservation; nothing holds a model warm on a client's behalf.
- Depends on: iss-2609061443332414, the docs defect that states the memory
  budget and the eviction rule the residency values are read against.

## Audit Notes

<!-- abcd-review: OWED receipt=rcp-251602005fba -->
Fidelity review OWED (receipt rcp-251602005fba).

## Grounds

- pursued: we expect a shared Mac to serve several agents without their models evicting each other once the operator can pin, budget and grace, and once keyed clients can see what is warm; we are wrong if model swaps stay as frequent with those controls set as they were without them, measured by the statistics store
