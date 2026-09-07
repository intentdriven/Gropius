---
id: adr-2609070004056820
slug: the-app-s-three-locks-have-one-order-the-pool-s-mutex-then-t
status: accepted
date: 2026-09-07
supersedes: null
superseded_by: null
related_intents: [itd-2609061441241254]
related_rfcs: []
related_adrs: []
---

# ADR-2609070004056820: The app's three locks have one order: the pool's mutex, then the configuration's, and a settings save serialises above both

## Context

Three mutexes guard state that a settings save and a model load both touch.

`runtime.Pool.mu` guards the loaded entries, the pinned set and the eviction
paths. `app.App.cfgMu` guards the running configuration. Between them runs one
call that is easy to miss: `startLocked` launches a model server while holding
`p.mu`, and it calls `PoolOptions.SamplingFor` to read that model's sampling
defaults — which is `App.Config()`, taking `cfgMu.RLock`. So `p.mu → cfgMu` is
an established order, created by a seam whose whole purpose is to read the
configuration live at the moment a process starts.

Pinned models (itd-2609061441241254) made that order load-bearing. A settings
save is now three writes that must not interleave with another save's: the file,
the running value, and the pool's protected set. Two overlapping saves left the
pool enforcing a pin that `config.json`, the control panel and `/v1/models` all
said did not exist — enforcement diverging from every surface that reports it
(iss-2609062318466201). The obvious fix is to hold `cfgMu` across all three
steps. That inverts the order above and deadlocks: a save holding `cfgMu` waits
for `p.mu` inside `Pool.SetPinned` while a load holding `p.mu` waits for
`cfgMu` inside `SamplingFor`. Two independent reviewers reached for that fix
before finding the constraint, which is why it is written down here rather than
in a comment on one function.

`internal/runtime` and `internal/app` are trust-boundary packages, and a
deadlock in either stops the machine serving.

## Decision

We will keep exactly one lock order in the app and the pool:

1. **`runtime.Pool.mu` may be held while taking `app.App.cfgMu`.** That is what
   `SamplingFor` does, and it is allowed.
2. **`app.App.cfgMu` is never held while calling into the pool.** `SetConfig`
   releases it before `Pool.SetPinned`; anything else that applies a saved value
   to the pool does the same.
3. **`app.App.saveMu` serialises a whole settings save and is ordered under
   nothing.** It is taken at the top of `SetConfig` (and by `adoptPinnedSpelling`,
   the other writer of the running pinned set) and released at the end. No code
   holding `p.mu` or `cfgMu` may take it, so it adds no order to the two above.

A new lock that must be held across both a pool call and a configuration read
belongs above `saveMu` in this list, or it does not belong.

## Alternatives Considered

- **Hold `cfgMu` for the whole save.** One lock instead of two, and it closes the
  same divergence. Rejected: it inverts `p.mu → cfgMu` and deadlocks against any
  concurrent model load, which is every request for a model that is not resident.
- **Make `SamplingFor` not read the live configuration** — pass a snapshot into
  `PoolOptions` at construction. That removes the `p.mu → cfgMu` edge entirely.
  Rejected: reading live at launch is the whole point of the seam, so that a
  sampling default saved in Settings reaches the next load of every model without
  the pool being rebuilt. Buying lock simplicity with a restart is the wrong
  trade.
- **A separate save lock, ordered under nothing** — the choice. It serialises the
  sequence without touching either existing lock or creating a new edge, and it
  costs one uncontended mutex on a path a human drives.
- **Leave the divergence.** Rejected: a pin the panel says does not exist while
  eviction honours it is unexplainable to the operator, and it persists for the
  life of the process.

## Consequences

- A settings save is serialised end to end. Concurrent saves no longer lose the
  Hub token write either (iss-2), which was the same shape.
- Anything that applies configuration to the pool must copy what it needs out
  from under `cfgMu` first and call the pool after releasing it. That is a
  positive obligation on every future setter — the configurable memory budget
  (itd-2609061441261073) and eviction grace (itd-2609061441285238) both add one.
- The constraint is now findable. It was previously derivable only by following
  `SamplingFor` from `PoolOptions` into `App.Config()`, which is why two reviews
  proposed the deadlocking fix.
- Held by `app.TestOverlappingSavesLeaveThePoolAgreeingWithTheSettings` under
  `-race`, which fires concurrent saves and asserts the pool, the running
  configuration and the file agree afterwards. There is no test that would catch
  the inverted order — a deadlock is not a failing assertion — so this record is
  the guard.
