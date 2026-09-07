---
id: spc-2609061822370978
slug: pinned-models-alice-marks-models-in-settings-that-stay-resid
intent: itd-2609061441241254
origin: researcher-authored
production_mode: hand-written
---
# pinned-models-alice-marks-models-in-settings-that-stay-resid

## Summary

This spec adds a pinned list to Settings. A pinned model is never chosen as an
eviction victim and is never reaped by the idle timeout; a request that would
need its memory is refused with the existing busy error, in a message that
names no model. Pins apply the moment Settings is saved, with no restart, and a
save whose pinned models' charged sizes exceed the memory budget is refused.
This record owns that fit invariant; the budget record cites it rather than
stating a second one.

## Scope

In scope:

- `config.Config.Pinned []string`, a list separate from `Preload`.
- `runtime.PoolOptions.Pinned` and `Pool.SetPinned`, applied live.
- The skip clause in `evictForLocked` and `reapIdle`.
- Canonicalisation of pinned ids against the registry, and case-insensitive
  matching in the pool as defence in depth.
- The pin-fit check at `App.SetConfig`, and the generic refusal text on the
  network.
- The Settings field, the remaining-budget figure beside it, and a pinned marker
  on the control panel's cards.

Out of scope:

- Loading pinned models at start-up. `Preload` is unchanged and stays the only
  start-up loader; pinning protects, it does not load.
- Pruning a pin on delete (`App.Delete` gains no `config.json` write; a stale
  pin is harmless), and reloading a crashed pinned server (`watchExit` stands).
- Waiting instead of refusing. That is itd-2609061441285238.

## Approach

**Configuration.** `config.Config` gains `Pinned []string
\`json:"pinned,omitempty"\``, alongside `Preload`. A slice is replaced, not
merged, when `handleSetSettings` decodes into a copy of the current config, so
an empty submission clears the list. `config.Validate` stays
machine-independent and checks only that each entry is a well-formed repo id.

**The pool.** `PoolOptions` gains `Pinned []string`. `Pool` holds
`pinned map[string]bool` keyed on the lower-cased repo id, guarded by `p.mu`.
`Pool.SetPinned(ids []string)` takes `p.mu`, rebuilds the map and returns; it
is the live seam. `evictForLocked`'s victim loop gains one clause beside the
existing `e.inFlight > 0 || !isReady(e)` skip:

    if p.isPinnedLocked(e.repoID) { continue }

and `reapIdle`'s guard gains the same clause, so a pinned model ignores the idle
timeout. `startLocked`'s own admission check (`need > MaxResidentBytes`) is
untouched: a model too large for the budget is still refused before anything is
evicted.

**Canonical ids.** The registry folds case and hands the pool its own spelling,
so a hand-edited `config.json` can name a pin the pool would never match.
`App.SetConfig` resolves each pinned id through the registry and stores the
canonical spelling when the model is downloaded, leaving an unknown id as typed
(a pin for a model not yet downloaded is accepted, and protects it once
something loads it). The pool's own lookup lower-cases both sides, so a pin that
escaped canonicalisation still matches.

**The fit invariant, owned here.** At every `App.SetConfig`, the sum of
`loadCost(size)` over the pinned ids that are downloaded must be no greater than
the effective memory budget. A save that breaks it is refused with a message
giving the sum and the budget, and `config.Save` is never called, so the stored
settings are unchanged. When one save changes both the pinned list and the
budget, the check runs against the *incoming* values of both: the operator's
whole intention is judged at once, and neither field wins by ordering.

**The refusal on the network.** `handleCompletions` writes a non-launch pool
error verbatim as the 503 body, so `evictForLocked`'s "no victim" message
reaches every LAN client. It is reworded to name nothing: "not enough memory to
load another model right now — the resident models are protected or busy". The
protected model names go to the machine's own log through a new
`PoolOptions.Log *slog.Logger` (defaulting to `slog.Default()`), at info level,
once per refusal. This is the same disclosure class spc-2609061822377049 gates
its residency fields on, and it is settled the same way: nothing about which
models the operator cares about reaches an unkeyed network client.

**Live application and the panel.** `App.SetConfig` calls `Pool.SetPinned` after
a successful save; `handleSetSettings` must **not** add `pinned` to its restart
list, whose four members stay host, port, decode concurrency and idle timeout.
`runtime.Resident` gains `Pinned bool`, which the control panel renders as a
marker on the card and which spc-2609061822377049 projects onto the keyed models
listing. The Settings page shows, beside the pinned field, the pinned charged
sum and what it leaves of the budget, so an impossible or nearly impossible set
is visible at pin time rather than at the first refused request.

## How each acceptance criterion is satisfied

1. _Given two pinned models are resident and the memory budget cannot also hold a
   third, when a client requests the third model, then the request is refused at
   once, both pinned models stay resident, and the refusal names no model._
   `evictForLocked` finds no eligible victim and returns the reworded error;
   `startLocked` returns before `Launch`, so nothing is torn down. Test
   (`internal/runtime`, fake launcher, injected `now`): two pinned entries at the
   budget, `Acquire` a third, assert the error, assert both entries still
   present, and assert the message contains neither repo id. A gateway test
   asserts the 503 body carries no model name.
2. _Given one pinned and one unpinned model are resident and both idle, when a
   third model is requested and freeing the unpinned one makes room, then the
   unpinned model is unloaded and the pinned one stays, even though the pinned
   one was used more recently._ The skip clause removes the pinned entry from the
   candidate set before the least-recently-used comparison runs. Test: set
   `lastUsed` explicitly through `PoolOptions.now` so the pinned model is the
   more recent one — no sleeps — then `Acquire` the third and assert which entry
   was stopped.
3. _Given an idle timeout is set, when a pinned model has been idle for longer
   than that timeout, then it stays resident while an unpinned idle model is
   unloaded._ The same clause in `reapIdle`'s guard. Test: an injected clock
   advanced past the timeout, one pinned and one unpinned idle entry, assert only
   the unpinned one is stopped.
4. _Given a resident model that is not pinned, when Alice pins it in Settings,
   then it is protected from the next eviction without Gropius being restarted._
   `App.SetConfig` → `Pool.SetPinned` under `p.mu`. Two tests: a `runtime` test
   that `SetPinned` changes the next `evictForLocked` outcome with no new pool;
   and a `control_test` asserting the settings response carries
   `"restart": false` when only `pinned` changed.
5. _Given a settings save whose pinned models' charged sizes together exceed the
   memory budget, when Alice submits it, then the save is refused with a message
   giving that sum and the budget, and the stored settings are unchanged._ The
   fit check runs before `config.Save`. Test (`internal/app`): a registry fixture
   with known sizes, a save that overshoots, assert the error text contains both
   figures, and assert the file on disk is byte-identical to before.
6. _Given a pinned model written with different letter case from the one the
   registry holds, when a competing request would need its memory, then the model
   is still protected._ Canonicalisation at `SetConfig` plus the lower-cased
   lookup in the pool. Two tests: `SetConfig` rewrites a case-variant id to the
   registry's spelling; and a `runtime` test that `SetPinned` with a case-variant
   id still skips the entry.
7. _Given a pinned model is resident, when the operator unloads it from the
   control panel, then it is unloaded and it remains pinned._ `Pool.Unload` is
   untouched — it refuses only on `inFlight > 0` — and nothing writes
   `config.json` on that path. Test: pin, load, `Unload`, assert the entry is
   gone and `App.Config().Pinned` still names it.

## Trust-boundary review notes

`internal/config`, `internal/runtime` and `internal/gateway` are all touched.

- **`internal/config` (file parsing).** One new field, a string slice, bounded
  by the existing `MaxConfigBytes` read through `ReadRegular`. Each entry is
  validated as a repo id, so a pinned entry cannot become a path. `Validate`
  stays machine-independent, so a `config.json` carried from another Mac still
  loads rather than resetting every other field with it.
- **`internal/runtime` (subprocess management).** The skip clause only *removes*
  candidates from an eviction set; it starts no process and changes no launch
  argument. The failure mode it introduces is refusal, not eviction, and
  criterion 1 holds it. `SetPinned` takes `p.mu`, the same lock every reader
  uses, so there is no new ordering.
- **`internal/gateway` (network input).** The 503 body is the disclosure
  surface. It is reworded to name no model, and criterion 1's gateway test is
  the standing guard. The settings handler gains no new authorisation path; it
  is loopback-only as it already is.
- **Denial of service.** A pinned set that fills the budget makes the machine
  refuse work it would previously have served by swapping. The fit check bounds
  that at save time and the Settings page shows the remaining budget; a pinned
  set that fits but leaves little room is the operator's choice, and documented.
- iss-2 (unsynchronised `App.SetConfig`) is unchanged and not worsened:
  `SetPinned` takes the pool's own lock, and no new writer of `config.json` is
  added.

## Docs to change

- `docs/getting-started.md`: section 6 gains a paragraph on pinning — what it
  protects against, that it is separate from preloading (a pinned model is not
  loaded at start-up), that a pinned model ignores the idle timeout, and that a
  request needing a pinned model's memory is refused rather than served.
- `README.md`: the settings paragraph names the pinned list beside the preload
  list, in present tense.
- `docs/models-list.md` (created by spc-2609061822377049): the `pinned` field.

## Dependencies and sequencing

- Depends on itd-2609061441261073 (spc-2609061822383286), the configurable
  budget, which makes pinning practical where the preferred pair does not fit
  the default. This record's fit check reads the *effective* budget, so it works
  against the fixed default too and does not block on that record landing first.
- spc-2609061822383286 cites this record's fit invariant as its floor rather
  than stating a second check.
- itd-2609061441285238 (eviction grace) builds on this record: its eligible-
  victim set is exactly the set this record's skip clause defines, and its
  pre-wait feasibility check reads the pinned sum.
- spc-2609061822377049 projects `Pinned` onto the keyed models listing. Either
  order works.

## Open design points

- Whether `Pool` takes a `*slog.Logger` option or the app logs the protected set
  from the error's wrapped detail. The bar is only that the names reach the
  machine's log and not the network.
- Whether Settings offers a checkbox per downloaded model or a text list. Either
  holds; the remaining-budget figure beside the field is required.
- Whether a non-resident pinned model reads "pinned, not loaded" on its card.
  Recommended, because an operator reading "pinned" assumes it is running.

## As built (2026-09-07)

Six things shipped differently from the plan above. Recorded here because a
closed spec is the durable claim about what shipped, and the rest of this
document says otherwise. The reasoning is in `.abcd/work/DECISIONS.md`
(2026-09-06, the three pinned lines, and 2026-09-07); this section is the
inventory.

**`runtime.Resident` did not gain `Pinned`.** The Approach says it does, and
that spc-2609061822377049 projects it onto the keyed models listing. A
`Resident` record exists only for a model the pool is holding, so a
`Resident`-sourced field reports `pinned: false` for a pinned model nothing has
loaded — which is the entry a client most needs to tell apart from an ordinary
cold one, and which this spec's own Open Design Point asks the panel to show as
"pinned, not loaded". `Pool.Pinned() []string` answers instead, independent of
what is loaded, and the models list joins it on the folded id beside the
residency join.

**The fit check refuses only a save that adds a pin.** The Approach says "at
every `App.SetConfig`, the sum ... must be no greater than the effective memory
budget". As built, a save is refused when the resulting set does not fit **and**
the save adds a pin; a set that arrives already too large — a `config.json`
carried from a Mac with more memory, or a start-up where the RAM sysctl fell
back to its default — is applied and warned about, on the panel and in the log.
The literal rule refuses every settings change there is, the API key and the
bind address with it, over a set the operator did not choose on this machine;
that is the wedge `App.adoptPinned` exists to prevent, reached by a second door.
Acceptance criterion 5, scope condition `cond-2609061822378129` and the Open
Question that resolves the fit check are all stated in the literal form and are
therefore narrower as built — see the intent's Audit Notes for 2026-09-07.
**itd-2609061441261073 must read this invariant as advisory for an inherited
set**: its dependency on "floor at least the pinned sum" holds for a budget the
operator lowers, and warns rather than refuses for a set carried in.

**The 503 wording.** The Approach gives it as "not enough memory to load another
model right now — the resident models are protected or busy". As built it is
`runtime.NoRoomError.Error()`: "not enough memory to load another model, and no
model in memory can be freed (limit %s)". The word "protected" is deliberately
gone — it tells an unauthenticated LAN client that pinning is in use on this
install, which is the disclosure class this record gates its other fields on.

**Names the Scope does not list.** Shipped beyond the five in-scope items:
`App.adoptPinned` (folds a pinned list read from disk onto the registry's
spellings), `App.adoptPinnedSpelling` (the same when a model arrives),
`App.PinnedFitWarning` (a set can stop fitting with no save to refuse it),
`App.saveMu` (a settings save is one sequence, not three steps),
`runtime.NoRoomError`, `runtime.PoolOptions.Log`, and `State.Pinned` /
`State.MemoryBudget` on the control plane.

**The charge basis.** The Approach charges "`loadCost(size)` over the pinned ids
that are downloaded". As built, `chargeable` counts a model that is ready **or
still downloading** — a download charged nothing is how a pinned pair that can
never fit is accepted while the bytes are arriving — and a failed download is
charged nothing, because the pool refuses to load it at all. `chargedSize` is
`Bytes` when non-zero, else `SizeBytes`: the disk figure is what the pool
charges, and the declared figure stands in for it until the bytes land. A model
with neither is refused as it is pinned rather than skipped silently.

**iss-2 is closed on this path, not merely "not worsened".** The trust-boundary
notes say iss-2 (unsynchronised `App.SetConfig`) is unchanged. `saveMu` covers
the whole of `SetConfig`, including the `a.Hub.Token` write that issue names, so
that race is gone; iss-2609062318466201, raised on this branch for the pool's
own divergence, is resolved by the same lock.

**Criterion 2 as written is weaker than what shipped.** It says the pinned model
survives "even though the pinned one was used more recently" — which plain
least-recently-used eviction satisfies without any pin at all. The holding test,
`runtime.TestEvictionSkipsThePinnedModelEvenWhenItIsTheLeastRecentlyUsed`, makes
the pinned model the *older* of the two, so it fails without the skip clause.
The criterion is not rewritten (a shipped record is not rewritten); the test is
the statement of what holds.
