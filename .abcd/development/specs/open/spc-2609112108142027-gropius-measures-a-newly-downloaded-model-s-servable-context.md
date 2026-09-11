---
id: spc-2609112108142027
slug: gropius-measures-a-newly-downloaded-model-s-servable-context
intent: itd-2609091301112705
origin: researcher-authored
production_mode: hand-written
---
# Gropius measures a model's real context window

## Summary

A probe harness in a new package, `internal/contextprobe`, measures the largest
prompt this Mac will actually serve a model, by driving the same OpenAI endpoint
a client drives. It runs only when the operator has asked — a global switch that
defaults to off, or a per-model "Measure now" — and only at a sustained idle; it
yields the machine to a real request and stops the server it was driving rather
than merely stopping waiting for it; it stores the figure with the provenance
staleness needs; it publishes the figure beside the declared and the served
window; and it changes no charge and refuses no request until the operator
adopts it as that model's served window.

Because the probe is a client of the gateway rather than an exception inside it,
the largest step it can take is bounded by the gateway's own limits — the
prefill deadline `prefillBudget` derives, and the served-window check
`overServedContext` applies. A reading those bounds stopped is published as a
floor under the model's window, naming the bound; it is never published as the
model's limit. On the 2026-09-06 evidence that will be the common case: three
models of four were bounded by the gateway, not by themselves.

## Scope

In scope: the `internal/contextprobe` package and its scheduler; the global
switch and the per-model "Measure now" across all three surfaces (Go,
`config.json`, the control panel) with the progress and the result; the measured
window and its provenance in `registry.json`; the measured field in
`GET /v1/models`; adopting a measured figure into `served_context`; two knobs on
the `internal/mlxtest` fake; the docs pages and the changelog entry.

Out of scope, each for its reason:

- **A probe path exempted from the gateway's checks, and any relaxation of the
  prefill timeout.** Open question 5, settled at interview: the published figure
  should be what a client can actually get, and a client gets the derived bound.
  This is also what keeps the change out of `internal/gateway`'s request path.
- **The model's own window above whatever bound stops the probe first.** This
  design cannot measure it. What is published instead is a verified floor and
  the name of the bound; the manual campaign script remains the only instrument
  that could go higher, and only with the timeout raised by hand.
- **Recall.** The campaign's needle check is not automated here: it is another
  full prefill per depth, and no acceptance criterion rests on it. The probe
  verifies what the server accepts and returns usage for; the manual script
  stays the authority on whether the answer is still found at that size. Scope
  condition cond-2609112108143953 (text models) therefore bears on the filler
  the probe generates, not on a recall check it does not run.
- **Reuse of a reading between accounts on one Mac.** Deferred, not declined:
  it means a new writeable file in a group-writable directory, which is a
  trust-boundary change with an adversarial security review of its own.
- **Sampling memory to keep `capability`'s safety factors of 5 and 7 under
  continuous evidence.** A follow-on; that package is another lane's.
- **Preemption or priority in the pool.** Yielding here is the probe cancelling
  itself. Any change to the pool is a coordinated follow-up.

**Lane.** `internal/runtime`, `internal/gateway`, `internal/stats` and
`internal/capability` belong to another session until it says otherwise. This
design changes none of them, with one named exception below; calling their
exported methods is not changing them.

## Approach

### The package, and the seam it holds

`internal/contextprobe` owns the method, the scheduler and the result. It talks
to the rest of Gropius through one small interface it declares itself, which
`*runtime.Pool` already satisfies:

```
type poolView interface {
    Waiting() int                 // queue depth, as any caller is counted
    Residency() runtime.Residency // per model: State, InFlight, LastUsed
    Unload(repoID string) error   // ErrBusy while a request is in flight
}
```

Three existing exported methods, nothing added to the pool. A fake satisfying
this interface is what the tests drive, which is why the package is testable
without a real Mac.

### The method, from the campaign

The 2026-09-06 script is the reference implementation and the Go probe is
checked against it (see "What is verified by hand"). Per model:

1. **Sweep** at fixed sizes, growing, up to the smaller of the declared cap and
   the served window.
2. **Bisect** between the largest size that came back and the smallest that did
   not, to a stated tolerance.
3. Every prompt is filler generated from a seed and **starts with its own
   nonce**, with its own marker order, so no two requests share a prefix and the
   server's prompt cache cannot shorten a later prefill. This is the mistake
   that put GLM-4.7-Flash at 122,347 tokens in the earlier record and 81,100 in
   the campaign.
4. **One output token, temperature 0**; the window is the `usage.prompt_tokens`
   the response carries, not the size the generator aimed at.
5. **Unload and start again before each long step**, through `Unload`, so no
   retained cache flatters the reading.
6. **A memory guard**: each step's peak is projected from the steps before it,
   and a step projected within the campaign's margin of this Mac's available
   memory is skipped and recorded as skipped, with its projection.
7. **What stopped the largest step** is recorded as one of `model`,
   `prefill_deadline`, `served_window` or `memory_guard`. Only the first makes
   the figure a limit; the rest make it a floor.

### How it reaches the model

Over loopback, to this Mac's own OpenAI endpoint, with the client-facing model
name — the gateway rewrites the `model` field into the load instruction the
model server needs, which is decision 1 in the development decision record and
not something a probe may reproduce for itself. It sends the configured API key
when one is set, even though the gateway admits a loopback connection with a
loopback host without one, so a later tightening of that rule does not silently
break the probe.

### Idle, and yielding

**Idle** is all three of: `Waiting()` is zero, no resident model has a request
in flight, and every resident model's `LastUsed` is older than the probe's idle
threshold. A probe that cannot start says which of the three held it back.

**Yielding** is a poll of the same two readings on a short interval while a step
runs. The probe holds exactly one request, on one model, so a second in-flight
request on that model, any in-flight request on another, or any rise in
`Waiting()` is a real caller. On seeing one the probe cancels its own request
context and then calls `Unload` on the model it was driving: the gateway giving
up and the model server stopping are different events, and after the first the
server keeps prefilling the abandoned prompt — the campaign watched a process
idle at 74 to 76 GB and the next request run at less than half speed. If
`Unload` returns `ErrBusy`, a real request has already taken that model; the
probe leaves it alone and **discards** the step's reading, because a server that
kept an abandoned cache measures the cache.

Resumption keeps the bisection bounds already established, which is what makes
yielding cheap enough to do at the first sign of a caller.

**The minimal seam, if yielding ever needs the pool's cooperation.** Nothing
above asks the pool for anything it does not already offer: the probe reads
`Residency()` and `Waiting()` and cancels its own request, which is what the
model self-test does. What it cannot do is make a real request *beat* a probe
that is parked — the pool has no preemption and no priority, and queue fairness
is round-robin by source key. The probe therefore never parks: it acquires only
when the room is already free and gives up otherwise. Should a later design want
a genuinely preemptible hold, that is a pool change, a coordinated follow-up
with the lane that owns `internal/runtime`, and it already has a capture in that
lane (iss-2609100526194406, not yet on `main`).

### What is stored, and when it goes stale

`registry.Model` gains the measured window and the provenance staleness needs:
the runtime version the reading was taken under, the memory budget and decode
concurrency in force, the served window at the time, what stopped the largest
step, and when. Staleness is then a comparison made at load between that
provenance and what is in force now — and the comparison's result is stored, so
that a reader of the file learns it rather than inferring it.

Every persisted numeric on `registry.Model` is re-bounded on load, because in
shared-cache mode another local account can write `registry.json`; these fields
take the same treatment. A window outside the registry's bounds, a bound string
outside the closed set, or an unparseable provenance clears the measurement
rather than being repaired into something plausible.

A re-download re-derives the model's config-derived facts in the startup
rescan; the measurement is cleared on the same path.

### Adoption, and the charge

Adoption is never automatic. The panel offers the measured figure with a "Use
this window" action that writes `served_context` for that model — the setting
that already exists, already charged by the budget and already enforced at the
gateway. So nothing in this change touches the charge or the refusal: an adopted
measurement is an operator setting like any other, and an unadopted one is a
published fact with no effect on what the machine does.

### Publication — the one edit outside the new package's lane

`GET /v1/models` gains the measured window and its bound beside `context_length`
and `served_context`. That handler lives in `internal/gateway`, so this is the
single additive edit this change needs in another session's lane: two keys
written next to the two already there, in the same block, with no check, no
path and no bound altered. It is agreed with that lane before it lands, or the
field waits and the panel carries the figure alone; the probe's own request path
needs nothing from the package either way.

### Three surfaces

- **Go** carries the whole of it: the scheduler, the switch, the per-model run,
  the progress snapshot and the result.
- **`config.json`** carries the switch and the idle threshold as ordinary
  settings; a save that names neither leaves both alone, which is the wedge this
  repository has built three times.
- **The control panel** carries the switch, a "Measure now" per downloaded
  model, the progress of a run in words (which model, which step, the bounds so
  far, what stopped the last step), the result with its bound, and the "Use this
  window" action.

The sync is armed as a test, not as a habit:
`internal/archtest/per_model_settings_test.go` is the pattern, and
`internal/archtest` also gains the new package to its layering entries.

## How each acceptance criterion is tested

The fake in `internal/mlxtest` is faithful to the real server's quirks but knows
nothing about prompts; it gains exactly two knobs, both small and both useful
beyond this change:

- a prefill delay honoured on the **non-streaming** path (today only the
  streaming path has one), so a step can be made to outlast a deadline;
- `usage.prompt_tokens` derived from the request body rather than the constant
  3, with an optional size above which the fake refuses or hangs — which is what
  gives a bisection something to converge on.

| Criterion | Test |
| --- | --- |
| Switch off: a finished download measures nothing and queues nothing | `internal/contextprobe`: `TestNothingIsMeasuredWhileTheSwitchIsOff`; `internal/app`: `TestADownloadDoesNotStartAProbe` |
| A probe queued does not start while a model served recently, a caller waits, or anything is in flight — and says which | `internal/contextprobe`: `TestAProbeWaitsForIdleAndNamesWhatHeldItBack` (fake pool view: each of the three conditions in turn) |
| A real request makes the probe cancel, unload, and record the step as yielded | `internal/contextprobe`: `TestAProbeYieldsToARealRequest` (fake pool view raises `Waiting`; asserts the request context is cancelled and `Unload` is called for that model) |
| An unload refused with `ErrBusy` discards the reading | `internal/contextprobe`: `TestAReadingIsDiscardedWhenTheServerCouldNotBeStopped` |
| A yielded probe resumes from the bounds already established | `internal/contextprobe`: `TestAResumedProbeKeepsItsBisectionBounds` |
| Stop, quit or sleep writes no partial figure and leaves the model unloaded | `internal/contextprobe`: `TestAnInterruptedProbeWritesNoFigure`; `internal/app`: `TestQuitLeavesNoProbeRunning` |
| A step projected within the margin of available memory is skipped and recorded | `internal/contextprobe`: `TestAStepTooLargeForThisMacIsSkippedWithItsProjection` |
| Each step unloads, starts again, and sends its own nonce | `internal/contextprobe`: `TestEveryStepReloadsAndSharesNoPrefix` (the fake records every body: no two prompts share a prefix, and an `Unload` falls between them) |
| The probe never evicts a pinned model, acquires only when the room is free, and is counted by the pool's waiting count | `internal/contextprobe`: `TestAPinnedModelIsNeverEvictedForAProbe`; `internal/archtest`: the probe acquires through the ordinary path, so it is queued like any caller |
| `GET /v1/models` carries the measured window beside the declared and served ones | `internal/gateway`: `TestModelsListCarriesTheMeasuredContext` (the one additive edit in that lane) |
| Adoption charges the figure; an unadopted measurement charges nothing | `internal/app`: `TestAdoptingAMeasurementSetsTheServedContext`, `TestAnUnadoptedMeasurementChangesNoCharge` |
| A gateway-bounded reading is published as a floor, naming its bound | `internal/contextprobe`: `TestAStepStoppedByTheDeadlineIsAFloor` (fake prefills past the deadline); `internal/ui`: `TestTheResultSaysWhatBoundedIt` |
| The switch and "Measure now" exist in Go, `config.json` and the panel, and a save that names neither leaves both alone | `internal/config`: `TestProbeSettingsAreValidated`; `internal/app`: `TestSavingAnUnrelatedSettingKeepsTheProbeSettings`; `internal/archtest`: the three-surfaces test in the per-model-settings pattern |
| A measurement goes stale on a runtime change, a re-download, or a budget, concurrency or served-window change, and says which | `internal/registry`: `TestAMeasurementGoesStaleWhenItsProvenanceMoves` (one case per trigger), `TestARescanClearsAMeasurementForARedownloadedModel` |
| Provenance is compared at start-up, so staleness is stored rather than assumed | `internal/registry`: `TestStalenessIsRecordedAtLoad`; the bounds test that every persisted numeric already takes |

Each test is watched failing before the change and passing after, as this
repository's definition of done requires.

### What is verified by hand, and why

`internal/mlxtest` is a **fake**. It can be made to prefill slowly, to time out,
and to report a prompt size — which arms idle, yielding, interruption,
skipping, staleness and the bounded reading — but it cannot produce a real
window, because there is no model behind it. The real check therefore stays a
manual run, as the campaign was: on at least one local model, the Go probe's
figure is compared with the campaign script's figure for the same model on the
same Mac and the same runtime, and the comparison is recorded beside the
campaign's evidence. No measured figure is published from a run that has not
been checked this way at least once. This is stated here so that a green suite
is never mistaken for a verified window.

## What this costs, stated for the maintainer

- **About forty minutes of GPU time per model at low load** — the press
  release's own figure and the campaign's budget for one measurement. Not forty
  minutes of idling: sustained full-GPU prefill.
- **A single step can be half an hour.** `prefillBudget` allows
  `tokens / 150 + 1 minute`; at a declared 262,144 tokens that is about thirty
  minutes before the gateway gives up, and a sweep plus a bisection is several
  such steps.
- **An unload and a reload between steps**, or a retained prompt cache flatters
  the next reading. Each reload re-reads the weights from disk: up to 44.9 GB
  for the largest local model.
- **The Mac must not be serving anyone.** A probe that runs while clients are
  asking measures the queue, and while it runs the GPU is nobody else's.
- **Memory.** Peaks at the verified windows were 68, 59, 49 and 28 GB on a
  128 GB Mac; the campaign's script skipped any step projected within 24 GB of
  available memory, and the one time two long requests overlapped, the machine
  swapped 2.5 GB.
- **On a laptop**, forty minutes of full GPU is battery and heat, and the lid
  may not close: a sleeping Mac loses the run, and it already looks down to
  clients.
- **On a shared Mac**, "idle" for the probing account is not idle for the
  machine, and each account keeping its own registry means one measurement can
  be paid for more than once on one GPU.
- **On a Mac with many models**, forty minutes each and serialised: six models
  is an evening, and running them together is not an option.
- **An interrupted probe that only stops waiting** leaves the server prefilling
  an abandoned prompt and its cache resident. That is why yielding unloads.

## Docs

- `docs/models-list.md` — the measured field in the field table, and a section
  beside the declared and served ones saying what the three figures mean, what a
  floor means, and that a measurement charges nothing until it is adopted.
- `docs/memory-budget-explained.md` — the measured window beside the served one:
  where the figure comes from, and why adopting it is a separate act.
- `docs/context-probe.md` — a new how-to, one Diátaxis type: switch the probe
  on, measure one model, read the result, adopt it, and what it costs while it
  runs.
- `README.md` — one line in the feature list.
- `CHANGELOG.md` — an `### Added` entry under `[Unreleased]`, in the shape this
  file already uses: a bold lead sentence saying what the operator gets, then
  what it costs, that nothing is measured unless asked, and that a figure the
  gateway bounded is published as a floor. Present tense, British English, and
  no claim the figure is the model's limit when it is not.

## Sequencing

1. Nothing blocks the work. The served window it publishes beside, the charge it
   feeds and the campaign it reproduces are all on `main`.
2. The one additive edit in `internal/gateway`'s models list is agreed with the
   lane that owns the package before it lands; without that agreement the panel
   carries the figure and the list field follows.
3. The model self-test intent (itd-2609100457007827) is not on `main` yet and
   also runs at idle. Whichever of the two lands second reconciles the two ideas
   of "idle" into one.
4. The manual comparison against the campaign script happens before any figure
   is published, not after.

## Grounds

- pursued: a probe that drives the same OpenAI path a client drives recovers the
  window a person recovered by hand, and publishing it beside the declared and
  the served window — charged only when the operator adopts it — is enough to
  size a client's prompts to what this Mac can actually serve. Shown wrong if a
  repeat probe on the same Mac, models and runtime returns a materially
  different window; or if the figure is so often a gateway-bounded floor that
  operators learn nothing about their models from it, in which case the useful
  change was to the gateway's bounds and not to the measurement.
