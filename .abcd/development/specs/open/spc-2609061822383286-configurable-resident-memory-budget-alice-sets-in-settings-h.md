---
id: spc-2609061822383286
slug: configurable-resident-memory-budget-alice-sets-in-settings-h
intent: itd-2609061441261073
origin: researcher-authored
production_mode: hand-written
---
# configurable-resident-memory-budget-alice-sets-in-settings-h

## Summary

This spec gives the resident memory budget a home in Settings. Alice types
gigabytes, the value is stored in bytes, and the percentage of her Mac's memory
is shown beside it along with each downloaded model's charged size and the
current resident total. A saved budget takes effect immediately through a setter
on the pool. Lowering it applies to future loads only — nothing is unloaded on
her behalf — and the panel reports the machine as over budget until the models
settle. The ceiling is checked when Settings is saved, never when settings are
read at start-up.

## Scope

In scope:

- `config.Config.MaxResidentBytes`, zero meaning the 60% default.
- One exported machine-memory reading, shared by `internal/runtime`,
  `internal/capability` and `internal/app`, injectable for tests, and
  `Pool.SetMaxResidentBytes`, applied live and evicting nothing.
- The ceiling check and the high-budget warning at `App.SetConfig`.
- A `machine` object on the control plane's `State`, making the Settings claims
  testable in Go, and the search tab reading the configured budget live.

Out of scope:

- Evicting to fit a lowered budget. Nothing is unloaded on save.
- Changing the 1.2x charge, the KV-cache accounting (iss-3) or the
  eviction-timing race (iss-6). Both are documented; neither is a prerequisite,
  because lowering the budget evicts nothing.
- The pinned-sum floor, owned by itd-2609061441241254 and cited here.
- Enumerating which combinations fit. Settings shows the numbers.

## Approach

**Configuration.** `config.Config` gains
`MaxResidentBytes int64 \`json:"max_resident_bytes,omitempty"\``. Zero — the
default, and what a fresh install stores — means "60% of physical memory", which
is exactly what `PoolOptions.MaxResidentBytes` already means, so no second
representation is introduced. `config.Validate` gains one machine-independent
rule: the value must not be negative. It deliberately gains **no** ceiling
check, because `Load` returns `Default()` on a validation error and `main`
treats that as "lock down to loopback": a `config.json` written on a 128 GB Mac
must still load on a 64 GB one rather than silently resetting the host, the API
key and every other field with it.

**One machine reading.** `physicalMemory` exists unexported in both
`internal/runtime` and `internal/capability`. It moves to one exported place,
`capability.PhysicalMemory()`, and both callers read it. `capability.Assess`
becomes `Assess(modelsDir string, budget int64) Machine`, where a zero budget
resolves to the 60% default so a default config does not hide every model. `App`
holds the machine size behind a field set at construction, so tests inject a
size instead of shelling to `sysctl`.

**The pool.** `Pool.SetMaxResidentBytes(n int64)` takes `p.mu`, writes
`p.opts.MaxResidentBytes` and returns. It evicts nothing: `startLocked` and
`evictForLocked` both read the field under the same lock, so the next load sees
the new figure and the currently resident set is left alone. That is the whole
mechanism — admission is a single comparison against the budget and nothing else
in the pool constrains residency.

**Save-time checks in `App.SetConfig`,** in this order, all before
`config.Save`, so a refusal leaves the stored settings unchanged:

1. **Ceiling.** If the machine size is known and the incoming budget exceeds it,
   refuse, naming the machine's memory. If the machine size is unknown (`0`),
   skip the check entirely.
2. **Floor.** The pinned-sum invariant owned by itd-2609061441241254: the sum of
   `loadCost` over pinned, downloaded models must fit the incoming budget. When
   one save changes both fields, both are judged at their incoming values.
3. **Warning.** A budget above a high share of physical memory returns a warning
   alongside the successful save, not a refusal. It is advice: the charge
   excludes the KV cache, so a machine that is fully committed on paper can still
   run out under long-context load.

On success, `App.SetConfig` calls `Pool.SetMaxResidentBytes`, and
`handleSetSettings` does **not** add the field to its restart list.

**Making the UI claims testable.** `control.State` gains
`Machine struct { TotalRAM, Budget int64; BudgetIsDefault bool; WarnAbove int64;
ResidentBytes int64; OverBudget bool }`, built from the injected machine size,
the effective budget and the sum of `loadCost` over `Pool.Resident()`. Every
Settings claim below is then a `control_test` assertion rather than untested UI
text. The form posts gigabytes and converts to bytes; the percentage is rendered
from `TotalRAM` and is omitted when it is zero.

**The search tab.** `handleSearch` calls `capability.Assess(modelsDir, budget)`
with the effective budget resolved in `App`, so a budget change immediately
changes which models the search shows as fitting. No restart, no second source
of truth: the filter and the pool read one number.

## How each acceptance criterion is satisfied

1. _Given a fresh install, when Alice opens Settings, then the budget shows the
   default in gigabytes with its share of the machine's memory beside it, and the
   stored settings hold no explicit value._ `Default()` leaves
   `MaxResidentBytes` zero and `omitempty` keeps it out of the written file;
   `State.Machine` reports the resolved default with `BudgetIsDefault: true`.
   Test (`control_test`, injected machine size): assert the JSON's budget, the
   flag, and that the saved `config.json` has no `max_resident_bytes` key.
2. _Given two models whose charged sizes together exceed the default but fit the
   machine, when Alice raises the budget above their sum and saves, then both
   models load and stay in memory together without Gropius being restarted._ Two
   tests, because no single test can hold both bars: an `internal/runtime` test
   with a fake launcher that `SetMaxResidentBytes` then `Acquire`s both models
   and asserts neither was evicted; and a `control_test` asserting the settings
   response carries `"restart": false` when only the budget changed.
3. _Given a budget larger than the machine's physical memory, when Alice saves,
   then the save is refused with a message naming the machine's memory, and the
   stored settings are unchanged._ Check 1 above. Test (`internal/app`, injected
   machine size): assert the error text carries the machine figure and that the
   file on disk is byte-identical to before.
4. _Given two pinned models are resident, when Alice lowers the budget below the
   sum of their charged sizes, then the save is refused with a message naming
   that sum._ Check 2 above, the invariant owned by itd-2609061441241254. Test:
   a registry fixture with known sizes, two pins, a lowering save, assert the
   sum appears in the message and the file is unchanged.
5. _Given resident models whose charged sizes exceed a lowered budget, when Alice
   saves it, then no model is unloaded on her behalf and the control panel reports
   the machine as over budget until those models unload._ `SetMaxResidentBytes`
   evicts nothing. Test: load two models, lower the budget, assert both entries
   are still in `Pool.Resident()` and that `State.Machine.OverBudget` is true
   with `ResidentBytes` above `Budget`.
6. _Given the machine's physical memory cannot be determined, when Settings
   renders, then the budget is shown without a percentage and an explicit value
   is accepted with no ceiling check._ With an injected machine size of zero,
   `State.Machine.TotalRAM` is zero (the UI omits the percentage) and check 1 is
   skipped. Test: inject zero, save a very large budget, assert it succeeds and
   is stored. This is the `sysctl`-unavailable path, testable for the first time
   because the reading is injected.
7. _Given the search tab, when the budget changes, then models the pool would now
   refuse are hidden and models it would now admit are shown, with no restart._
   `handleSearch` passes the effective budget into `Assess`, and `runFootprint`
   already mirrors `loadCost`, so the filter and the pool agree. Test
   (`control_test`): a fixture search result at a size that straddles two
   budgets; assert it is hidden at the lower budget and shown at the higher one
   after a save, with no new server.

## Trust-boundary review notes

`internal/config`, `internal/runtime` and `internal/gateway` are touched.

- **`internal/config` (file parsing).** One integer field. Keeping `Validate`
  machine-independent is the security-relevant choice: a machine-dependent check
  there would make a config carried between Macs invalid, and an invalid config
  reverts to `Default()` — host `0.0.0.0`, empty API key — which is the exact
  fail-open this repository already guards against. Negative values are refused
  so no arithmetic below can go negative.
- **`internal/runtime` (subprocess management).** `SetMaxResidentBytes` takes
  `p.mu`, the lock every reader of the field already holds, so there is no new
  ordering and no torn read. It starts and stops nothing. Raising the budget
  admits more models; the honest residual is that the 1.2x charge excludes the
  KV cache (iss-3) and eviction credits memory before the victim exits (iss-6),
  so a raised budget can admit more than the Mac holds under long-context load.
  The high-budget warning exists for exactly that, and the docs say so.
- **`internal/gateway` (network input).** Only the loopback-only control plane
  changes. The `machine` object carries three integers and two booleans — no
  path, no port, no model name — and it is served only on the control plane,
  never on `/v1`.
- **Shared-cache mode.** `config.json` is single-writer (iss-10) and the budget
  applies to whichever account runs the server. The Settings copy therefore says
  "this Mac's memory", not "your Mac's memory", because the server may be
  another account's.

## Docs to change

- `docs/getting-started.md`: section 6 gains the memory budget — what it is, the
  60% default, that Alice types gigabytes, that a change applies immediately and
  to future loads only, that the budget can be exceeded briefly while a replaced
  model exits (iss-6), and that the charge excludes the KV cache (iss-3), which
  is why a high budget draws a warning.
- `README.md`: the settings paragraph names the budget beside decode
  concurrency and the idle timeout.
- `docs/models-list.md` (created by spc-2609061822377049): the budget and
  eviction rule stated there stay the single reference; this record's docs link
  to it rather than restating it.

## Dependencies and sequencing

- Depends on itd-2609061441241254 (spc-2609061822370978) for the pinned-sum
  floor. The invariant is stated once, there, and checked here.
- Sequencing: the pinned record and this one share `App.SetConfig`'s check block
  and the live-setter seam. Whichever lands first builds that block; the second
  adds its check to it.
- itd-2609061441285238 (eviction grace) depends on this record: a budget change
  wakes waiting requests, which requires `SetMaxResidentBytes` to exist as a
  signal point.
- iss-3 and iss-6 are refined, not blocked: the docs state the KV-cache
  exclusion and the brief over-budget window. Neither is a prerequisite.

## Open design points

- The warning threshold's value. It is advice; the implementer picks a share and
  exposes it as `WarnAbove`, so the UI text stays untested and the number checked.
- Whether the field accepts a fractional gigabyte figure. Storage is bytes either
  way.
- Whether `capability.Assess` takes a budget parameter or an `App`-supplied
  `Machine`. Both satisfy criterion 7; the budget parameter is recommended, as it
  keeps the zero-means-default rule in one place.
