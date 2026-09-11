---
id: adr-2609091239058072
slug: the-app-s-four-locks-have-one-order-the-settings-handler-s-a
status: accepted
date: 2026-09-09
supersedes: adr-2609070004056820
superseded_by: null
related_intents: [itd-2609061441241254]
related_rfcs: []
related_adrs: [adr-2609070004056820]
---

# ADR-2609091239058072: The app's four locks have one order: the settings handler's, the save lock, then the pool's mutex and the configuration's

## Context

This supersedes adr-2609070004056820, which named three locks and said of
itself that it IS the list: "a new lock that must be held across both a pool
call and a configuration read belongs above `saveMu` in this list, or it does
not belong." There is now a fourth, and the list has to say so or it stops
being the thing a reviewer can check a change against.

The fourth is `gateway.Control.settingsMu`, added for iss-2609062045106963.
`App.saveMu` serialises `SetConfig`'s own sequence — the file, the running
value, the pool — but a settings save is larger than `SetConfig`. The control
handler reads the settings in force, decodes the posted body into a copy of
them, calls `SetConfig`, and then works out which loaded models the change
means to reload. The snapshot every save starts from is taken *before*
`SetConfig` is entered, so `saveMu` structurally cannot cover it: two
overlapping saves each wrote a configuration that had never seen the other's
change, and the second reverted a field it was never asked about — the form
posts a whole configuration, so the field need not appear in either body. The
`reload_models` list was computed against the same stale snapshot.

The obvious place for the fix is `App`, where the other three locks live. It
cannot go there without changing what `App` offers: what has to be atomic
starts in the caller, so `App` would need a read-modify-write of its own
(`UpdateConfig(func(Config) (Config, error))`) rather than a setter. That is a
real option and may become the right one — but there is exactly one caller of
`SetConfig` in the tree, the settings handler, so serialising the handler
serialises every settings write there is, and it does so without widening
`App`'s interface for a single consumer.

`internal/gateway` is a trust-boundary package and a deadlock in it stops the
machine serving, so the order has to be written down rather than inferred.

## Decision

We will keep exactly one lock order across the app, the pool and the control
plane. Outermost first:

1. **`gateway.Control.settingsMu`** serialises the whole settings write path:
   read the settings in force, decode into a copy, `SetConfig`, and the
   `reload_models` diff. It is taken only by the settings handler, and only
   from an HTTP handler goroutine. Nothing reached from under any lock below
   may take it — which is what makes it safe to hold across `SetConfig`.
2. **`app.App.saveMu`** serialises a whole settings save inside `App`. It is
   taken at the top of `SetConfig` (and by `adoptPinnedSpelling`, the other
   writer of the running pinned set) and released at the end. Nothing holding
   `p.mu` or `cfgMu` may take it.
3. **`runtime.Pool.mu` may be held while taking `app.App.cfgMu`.** That is what
   `SamplingFor` does — `startLocked` launches a model server under `p.mu` and
   reads that model's sampling defaults live — and it is allowed.
4. **`app.App.cfgMu` is never held while calling into the pool.** `SetConfig`
   releases it before `Pool.SetPinned`; anything else that applies a saved
   value to the pool does the same.

Two locks in `gateway.Control` are leaves and sit outside this order because
nothing is taken while they are held: `loadMu`, which claims the background
load of one model so repeated clicks on Load do not each take a place in the
pool's queue for memory, and `repairMu`, which guards the list of settings the
load had to repair. A leaf lock that starts calling into `App` or the pool
stops being a leaf and joins the list above.

A new lock that must be held across both a pool call and a configuration read
belongs above `saveMu` in this list, or it does not belong.

## Alternatives Considered

- **Amend adr-2609070004056820 in place.** Rejected on the maintainer's rule of
  2026-09-09: an ADR is never edited, it is superseded. The old record is what
  a reader of the branch that introduced `saveMu` will find; rewriting it would
  make that reader's context wrong.
- **A read-modify-write on `App` (`UpdateConfig`)**, with `saveMu` held across
  the whole of it and no fourth lock at all. Genuinely the cleaner shape, and
  the one to take the moment a second caller of `SetConfig` appears — a
  menu-bar action, a CLI, a start-up repair. Rejected for now: it widens
  `App`'s interface for a single consumer, and the change was made alongside
  concurrent work in `internal/app` that it would have collided with.
- **Hold `settingsMu` across reading the request body and writing the
  response.** Rejected: a client that uploads or reads slowly would then be
  something the next save waits behind. The body is read before the lock and
  the answer written after it.
- **Leave the save path unserialised.** Rejected: a setting the operator did
  not touch being reverted by a save they did not make is invisible — no error,
  no log line, and the panel shows the value that won.

## Consequences

- Every settings write is serialised end to end, and `reload_models` is
  computed against the settings the save actually started from.
- The serialisation now lives in the control plane, so a second caller of
  `SetConfig` outside it would not be covered. That is the trigger to move it
  into `App` as a read-modify-write, and it is the one thing to check before
  adding such a caller.
- Held by `gateway.TestConcurrentSavesEachKeepTheirOwnField` under `-race`,
  which fires paired saves that each touch a different field and asserts both
  survive, and by
  `app.TestOverlappingSavesLeaveThePoolAgreeingWithTheSettings`, which the
  superseded record already carried. As before, no test can catch an inverted
  order — a deadlock is not a failing assertion — so this record is the guard.
