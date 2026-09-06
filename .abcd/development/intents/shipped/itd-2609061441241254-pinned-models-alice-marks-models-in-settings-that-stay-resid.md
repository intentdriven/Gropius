---
id: itd-2609061441241254
slug: pinned-models-alice-marks-models-in-settings-that-stay-resid
spec_id: spc-2609061822370978
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Pinned models: Alice marks models in Settings that stay resident no matter what else is requested; a request that would need to evict a pinned model is refused with the existing busy error instead

## Press Release

Alice runs two models all day: one writes, one reviews. She ticks both in
Settings, and from then on nothing Bob or Carol requests can push them out of
memory. When Bob asks for a third model that would only fit by dropping one of
Alice's, his client is told straight away that the Mac has no room for it,
rather than Alice's workhorse vanishing mid-task and coming back minutes later.
Bob's refusal says nothing about what Alice is running. Alice can see which
models she has protected, and how much of the memory budget they leave for
everyone else, on the Settings page where she set them.

## Why This Matters

Preload brings a model up at startup but does not protect it: the
least-recently-used rule evicts any idle model the moment a different one is
requested and does not fit. Agentic clients are idle between turns, so a shared
machine evicts its most important models most often. The 2026-09-05 model-bench
lab named a keep-warm option the most valuable feature request for Gropius and
built a queueing proxy to approximate it. Pinning is the smaller change. Its
docs change also settles iss-2609061443332414 by stating the difference between
preloading a model and protecting one.

Evidence: research note 2026-09-06-model-bench-evidence (the writer-and-reviewer loop: two models alternating, swaps at 7.7 s and 38 s, the reviewer's generation as the bottleneck; the lab's stated top request was a keep-warm option).

## Mechanism

We expect a pinned model to survive every competing request because the pool
picks eviction victims only from the models its skip rule admits, and a pin is
one more clause in that rule; we are wrong if a pinned set leaves so little
room that the machine refuses more work than eviction used to cost it.

## Scope Conditions

- Shared Macs serving more than one client, where the operator knows which one <!-- cond: cond-2609061822373930 -->
  or two models matter most.
- Models that have been loaded; the pinned list is separate from the preload <!-- cond: cond-2609061822373978 -->
  list, so a pinned model nobody has loaded is protected only once something
  loads it.
- Pinned sets whose charged sizes fit within the memory budget together; a <!-- cond: cond-2609061822378129 -->
  settings save that breaks that is refused.
- The pool's single process per model: a crash of a model's own server, an <!-- cond: cond-2609061822373660 -->
  operator's own unload, and shutdown all still remove a pinned model from
  memory.

## Acceptance Criteria

- Given two pinned models are resident and the memory budget cannot also hold
  a third, when a client requests the third model, then the request is refused
  at once, both pinned models stay resident, and the refusal names no model.
- Given one pinned and one unpinned model are resident and both idle, when a
  third model is requested and freeing the unpinned one makes room, then the
  unpinned model is unloaded and the pinned one stays, even though the pinned
  one was used more recently.
- Given an idle timeout is set, when a pinned model has been idle for longer
  than that timeout, then it stays resident while an unpinned idle model is
  unloaded.
- Given a resident model that is not pinned, when Alice pins it in Settings,
  then it is protected from the next eviction without Gropius being restarted.
- Given a settings save whose pinned models' charged sizes together exceed the
  memory budget, when Alice submits it, then the save is refused with a message
  giving that sum and the budget, and the stored settings are unchanged.
- Given a pinned model written with different letter case from the one the
  registry holds, when a competing request would need its memory, then the
  model is still protected.
- Given a pinned model is resident, when the operator unloads it from the
  control panel, then it is unloaded and it remains pinned.

## Open Questions

- Resolved: pinned models are a separate list and preload is unchanged, so
  pinning adds a setting rather than altering an existing one.
- Resolved: pinned models ignore the idle timeout, and Settings says so beside
  that field.
- Resolved: the fit check runs when Settings is saved — the pinned models'
  charged sizes must sum to no more than the memory budget — so an impossible
  pin set is refused at pin time rather than discovered at request time.
  itd-2609061441261073 cites this invariant rather than stating a second one.
- Resolved: pins apply as soon as Settings is saved; no restart is needed.
- Resolved: pins are matched by the registry's canonical model id, so a
  hand-edited settings file with different letter case still protects the model
  the operator meant.
- Resolved: the message a network client receives says only that there is not
  enough memory; the protected models are named in the machine's own log.
- Resolved: pinning lives in Settings for the first cut; the control panel
  marks pinned models on their cards without offering a second place to change
  them.
- Resolved: an operator's own unload succeeds and leaves the pin in place;
  deleting a model leaves its pin behind rather than writing the settings file
  from a second code path.
- Resolved: a pinned model whose server exits is not reloaded automatically;
  the control panel shows it as pinned and not loaded until something loads it
  again.
- Depends on: itd-2609061441261073, the configurable memory budget, which makes
  pinning practical on machines where the preferred pair does not fit the
  default budget.

## Audit Notes

<!-- abcd-review: OWED receipt=rcp-4d0d60cfdaf8 -->
Fidelity review OWED (receipt rcp-4d0d60cfdaf8).

## Grounds

- pursued: we expect a shared Mac to serve several agents without their models evicting each other once the operator can pin, budget and grace, and once keyed clients can see what is warm; we are wrong if model swaps stay as frequent with those controls set as they were without them, measured by the statistics store
