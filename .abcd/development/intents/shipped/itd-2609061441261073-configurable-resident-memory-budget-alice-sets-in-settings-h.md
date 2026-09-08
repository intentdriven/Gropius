---
id: itd-2609061441261073
slug: configurable-resident-memory-budget-alice-sets-in-settings-h
spec_id: spc-2609061822383286
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Configurable resident memory budget: Alice sets in Settings how much memory Gropius may fill with loaded models, sees the default and what it means for which models can co-reside, and changes it without a restart

## Press Release

Alice opens Settings and sees how much of her Mac's memory Gropius may fill
with loaded models: the figure in gigabytes, what fraction of the machine that
is, and what each of her downloaded models would cost against it. She raises it
because her Mac runs nothing else, saves, and her writer and reviewer models now
sit in memory together without a restart. Bob, on a laptop he also works on,
lowers his; nothing is pulled out from under the model he is using, and the
panel simply tells him he is over his new figure until the models settle.

## Why This Matters

The budget is fixed at 60% of physical memory and is not a field anyone can
set. On the 2026-09-05 model-bench lab's 128 GB machine that is about 77 GB,
and the lab's preferred pair (a 45 GB coder and a 29 GB reviewer, charged 1.2x
each) does not fit although the machine has room. A dedicated inference Mac and
a shared laptop want different answers, and today both get the same one. The
budget is a single number the pool already holds; this intent gives it a home
in Settings next to decode concurrency and idle timeout.

iss-3 and iss-6 concern how the budget is accounted; this intent concerns who
sets it. The budget half of the docs gap iss-2609061443332414 is settled by the
same change.

Evidence: research notes 2026-09-06-model-bench-evidence and 2026-09-06-model-bench-findings (co-residency arithmetic under the 60% default: the preferred author-and-reviewer pair does not fit although the machine has room).

## Mechanism

We expect a writer-and-reviewer pair charged at about 89 GB to sit in memory
together once the operator raises the budget above that figure, because
admission is a single comparison against the budget and nothing else in the
pool constrains residency; we are wrong if a raised budget admits more than the
Mac can hold, since the charge ignores the KV cache (iss-3) and eviction
credits memory before the victim exits (iss-6).

## Scope Conditions

- Apple Silicon Macs whose physical memory the system reports. <!-- cond: cond-2609061822382119 -->
- The 1.2x charge as the only accounting rule; the claim says nothing about <!-- cond: cond-2609061822387031 -->
  concurrent long-context load, which iss-3 covers.
- Operators who know what else the Mac runs, since the high-budget warning is <!-- cond: cond-2609061822380027 -->
  advice rather than enforcement.
- One Gropius server per machine; in shared-cache mode the budget lives in the <!-- cond: cond-2609061822380142 -->
  shared settings and applies to whichever account runs the server.

## Acceptance Criteria

- Given a fresh install, when Alice opens Settings, then the budget shows the
  default in gigabytes with its share of the machine's memory beside it, and
  the stored settings hold no explicit value.
- Given two models whose charged sizes together exceed the default but fit the
  machine, when Alice raises the budget above their sum and saves, then both
  models load and stay in memory together without Gropius being restarted.
- Given a budget larger than the machine's physical memory, when Alice saves,
  then the save is refused with a message naming the machine's memory, and the
  stored settings are unchanged.
- Given two pinned models are resident, when Alice lowers the budget below the
  sum of their charged sizes, then the save is refused with a message naming
  that sum.
- Given resident models whose charged sizes exceed a lowered budget, when Alice
  saves it, then no model is unloaded on her behalf and the control panel
  reports the machine as over budget until those models unload.
- Given the machine's physical memory cannot be determined, when Settings
  renders, then the budget is shown without a percentage and an explicit value
  is accepted with no ceiling check.
- Given the search tab, when the budget changes, then models the pool would now
  refuse are hidden and models it would now admit are shown, with no restart.

## Open Questions

- Resolved: Alice types gigabytes, the value is stored in bytes, and the
  percentage of the machine's memory is shown beside the field.
- Resolved: lowering the budget below what is resident applies to future loads
  only; nothing is unloaded on save, and the panel shows the over-budget state
  until the models unload by the usual rules.
- Resolved: the machine-dependent ceiling is checked when Settings is saved,
  not when settings are read at start-up, so a settings file carried from a
  larger Mac still loads rather than resetting every other field with it.
- Resolved: the floor is the sum of the pinned models' charged sizes, the
  invariant owned by itd-2609061441241254 rather than restated here.
- Resolved: a budget above the share of memory the rest of the machine needs
  draws a warning, not a refusal.
- Resolved: a saved budget takes effect immediately through a setter on the
  pool; the field is not restart-only.
- Resolved: Settings shows each downloaded model's charged size and the current
  resident total, and leaves the operator to add them up; it does not enumerate
  which combinations fit.
- Resolved: iss-6 is not a prerequisite, because lowering the budget evicts
  nothing; the docs state instead that the budget can be exceeded briefly while
  a replaced model exits.
- Resolved: iss-3 is refined rather than blocked; the docs state that the
  charge excludes the KV cache, which is why the high-budget warning exists.
- Depends on: itd-2609061441241254, the pinned models whose charged sum this
  budget's floor is measured against.

## Audit Notes

<!-- abcd-review: OWED receipt=rcp-0b2669b3be31 -->
Fidelity review OWED (receipt rcp-0b2669b3be31).

2026-09-07 — Acceptance criterion 3 is **diverged**, not met, and the divergence
is deliberate. As built, a save is refused when it raises the budget above this
Mac's memory; a budget that is merely inherited — a settings file carried from a
larger Mac, or one written during a start where the machine could not be
measured — is applied, warned about at start-up and on the control panel, and
left for the operator to change. The criterion as written refuses every settings
change there is, the API key that closes an open LAN endpoint included, over a
figure the operator never chose on this machine, while the pool enforces that
figure regardless — so the refusal blocks everything and protects nothing. Two
independent reviews of the branch demonstrated it end to end. This is the same
anti-wedge principle criterion 5 of itd-2609061441241254 was adopted as diverged
under on 2026-09-07; the criterion and the principle could not both stand, and
the principle won again.

2026-09-07 — Acceptance criterion 4 shipped **narrower** than it is written, for
the same reason, and the maintainer should adopt or reject it as such. The
criterion refuses, unconditionally, a save that lowers the budget below the sum
of the pinned models' charged sizes. As built, that refusal applies only when the
budget in force could hold the set (`App.lowersUnderAFittingSet`): where the
pinned set already does not fit — an inherited `config.json`, or a start where
this Mac's memory could not be read and the conservative default applied — a save
that lowers the budget further is accepted and warned about instead. Two things
forced the narrowing. The panel renders gigabytes, so a save that posts back what
it shows can carry a figure a few bytes under the one in force, which on the
unconditional rule read as "lowering" and refused every settings change over a
set the operator never chose here; and a pin whose model this Mac cannot measure
blocked every budget change with no way out but unpinning. The sibling intent's
own audit note asks this record to read the pinned sum as advisory for an
inherited set, which is what this is. Held by
`app.TestARoundedBudgetDoesNotTurnAWarningIntoARefusal` and
`app.TestAnUnmeasurablePinDoesNotBlockALowerBudget`, with
`app.TestSetConfigRefusesABudgetBelowThePinnedSum` holding the criterion's own
case.

2026-09-08 — **Correction: no adoption has happened.** The 2026-09-07 note on
criterion 3 above says the anti-wedge principle is the one "criterion 5 of
itd-2609061441241254 was adopted as diverged under on 2026-09-07". No such
adoption was made. That intent's own Audit Notes ask, in terms, that the
fidelity audit "record criterion 5 as diverged, for the maintainer to adopt or
reject", and that decision is still open. The sentence above describes a shared
argument, not a settled precedent, and a reader meeting this record before its
sibling could reasonably conclude the question is closed. It is not.

The same correction applies to every criterion in this family. Criterion 5 of
itd-2609061441241254 and criteria 3 and 4 here are all awaiting the maintainer's
adopt-or-reject; none of them has been adopted, and none may be cited as
precedent for the others until one is.

## Grounds

- pursued: we expect a shared Mac to serve several agents without their models evicting each other once the operator can pin, budget and grace, and once keyed clients can see what is warm; we are wrong if model swaps stay as frequent with those controls set as they were without them, measured by the statistics store
