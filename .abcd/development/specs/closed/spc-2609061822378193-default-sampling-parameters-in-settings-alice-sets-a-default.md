---
id: spc-2609061822378193
slug: default-sampling-parameters-in-settings-alice-sets-a-default
intent: itd-2609061429508050
origin: researcher-authored
production_mode: hand-written
---
# default-sampling-parameters-in-settings-alice-sets-a-default

## Summary

Settings gains a set of machine-wide sampling defaults, plus an optional
override per model. The defaults are applied where the pinned mlx-lm server
takes them — as launch flags on the model server process — so a request that
omits a parameter is served with Alice's value and a request that carries one
still wins, without the gateway touching a client's body. Changing a default
takes effect the next time a model loads, and the panel names the loaded
models that must load again.

## Scope

In scope:

- A `sampling` block on `config.Config`, with pointer fields so that "blank"
  and "zero" differ, and an optional per-model override map keyed by repo id.
- Only the parameters the pinned mlx-lm 0.31.3 server exposes as launch
  flags. The candidate set is `--temp`, `--top-p`, `--top-k`, `--min-p` and a
  completion-token default; the exact set, spellings, types and the server's
  own accepted ranges are read from the pinned server's argument parser at
  the start of implementation and recorded in a dated research note that this
  spec's tests cite.
- Range validation at the settings endpoint, and a documented rule for a
  hand-edited configuration file that carries an out-of-range value.
- Launch-time application in `internal/runtime`, live reporting of which
  resident models need reloading, and the Settings form.
- Documentation of the parameter list, the precedence rule, what a blank
  field means, and that reproducibility comes from a fixed temperature.

Out of scope:

- Injecting sampling fields into a client's request body in
  `internal/gateway`. The completion path is unchanged by this spec.
- Any mode in which a saved value overrides a value a request carried, and
  any ceiling or floor on a client's value.
- A seed field. The pinned server ignores the seed a request carries
  (iss-2609061429558510); the documentation states the fact instead.
- Parameters the server accepts per request only (repetition penalty unless a
  flag exists for it, `repetition_context_size`, `xtc_*`, `logit_bias`,
  `logprobs`).
- Per-request context or token budgets, which belong to itd-2609061431481936.

## Approach

`config.Config` gains `Sampling *Sampling` and `ModelSampling
map[string]Sampling`. `Sampling` holds one pointer field per launch flag
(`*float64` for temperature, top-p and min-p; `*int` for top-k and the
completion-token default), so an absent field is unset and reaches no flag.
Override keys are validated with `config.ValidRepoID` and matched by the same
case-folding rule the registry uses, keeping the first-seen spelling; a model
with no override is served with the global set.

`internal/runtime` carries the values to the process. `runtime.Spec` gains a
`Sampling` struct; `PoolOptions` gains `SamplingFor func(repoID string)
Sampling`, read at `startLocked` time from the live config, so a saved change
reaches the next load without rebuilding the pool.
`ExecLauncher.Launch` appends a flag for each set field only, rendered through
`strconv`, immediately after the existing `--log-level` argument. Nothing a
client sends ever reaches an argument vector.

Precedence falls out of the mechanism: the server takes the flags as its
start-up defaults and a request's own field replaces them for that request
alone. `handleCompletions` is untouched, so the decoded value of every field
the client sent still reaches the model server unchanged.

Two hazards in the settings path are fixed here because this spec is the
first to put pointer fields on `Config`. First, `handleSetSettings` decodes
into `incoming := current`, which shares pointees with the live config, so a
posted value would mutate the running configuration before `Validate` ran and
would stay there when it was rejected; this spec adds `Config.Clone`, a deep
copy, and decodes into that. Second, `config.Load` today returns `Default()`
plus an error when `Validate` fails, and `cmd/gropius/main.go` then fails
closed to loopback, so one out-of-range temperature in a hand-edited file
would lock the whole server. This spec settles the intent's deferred question:
sampling values are preferences, not serving invariants, so `Load` drops any
out-of-range sampling value through a new `Sampling.sanitise` and returns the
dropped field names for `main.go` to log as a warning; the strict check, which
refuses with the field named, applies at `/api/settings` only. The Go ranges
are never wider than the ranges recorded from the pinned server, and a table
test pins them to the recorded set: a value Go accepts but the server rejects
would make every model fail to launch, which is a machine-wide outage from one
save.

The panel adds the fields to the Settings form. Each field's placeholder shows
the server's own default, so a blank field is honest about what applies. The
form sends `null` for a blank field rather than `parseInt(...) || 0`, and the
handler treats `null` as unset. `handleSetSettings` compares the effective
sampling for each entry in `Pool.Resident()` before and after the save and
returns a `reload_models` list beside the existing `restart` flag; `app.js`
names those models in the saved-settings message.

## How each acceptance criterion is satisfied

- "Given a default temperature saved in Settings, when a client sends a
  completion that omits the temperature, then that request is served with the
  saved value." The launcher appends the temperature flag for that model. A
  launcher test asserts the flag and value in the recorded argument vector,
  and a gateway test through `internal/mlxtest` asserts that the relayed body
  carries no `temperature` key, so the default lives in the process rather
  than in the request.
- "Given a default temperature saved in Settings, when a client sends its own
  temperature, then that request is served with the client's value." A gateway
  test asserts that the fake server's `LastBody` carries the client's value,
  and that the decoded value of every other field equals what the client sent.
  Byte identity is not asserted: `handleCompletions` re-marshals a
  `map[string]json.RawMessage`, which sorts keys and escapes output.
- "Given an override saved for one model, when that model has loaded again,
  then requests to it that omit the parameter are served with the override
  rather than the global default." A pool test with two models asserts that
  the override's flags appear in the argument vector of the overridden model
  only, and only on the load that follows the save.
- "Given a default is changed, when Alice saves it, then the panel names the
  loaded models that must load again before the new value applies to them." A
  control test posts a changed default with two models resident, one of them
  carrying an override that shadows the change, and asserts the
  `reload_models` list holds exactly the model whose effective value moved.
- "Given a value outside the range the model server accepts, when it is posted
  to Settings, then it is refused with the field named and neither the running
  nor the stored configuration changes." A control test posts an out-of-range
  temperature, asserts a 400 naming the field, asserts `App.Config()` is
  deep-equal to its prior value including pointees (which fails without
  `Config.Clone`), and asserts the configuration file's bytes are unchanged. A
  second test loads a hand-edited file with the same value and asserts the
  server starts, serves beyond loopback, and logs the dropped field.
- "Given the documentation, when a reader looks up sampling, then it states
  which parameters can be defaulted, that a request's own value wins, what a
  blank field means, and that reproducibility comes from a fixed temperature
  because the model server ignores a seed." A documentation test in the
  architecture test package compares the parameter table on the page with the
  fields on `config.Sampling`.

## Trust-boundary review notes

- `internal/config`: new fields are parsed from a file another local account
  can write in the shared-cache mode. Bounds live in `Validate`, never in the
  panel. The drop-with-a-warning rule keeps a malformed preference from
  turning into a fail-closed bind. `Config.Clone` removes the aliasing that
  let a rejected post mutate the live configuration; the existing `Preload`
  slice has the same aliasing and is fixed by the same clone.
- `internal/runtime`: values become subprocess arguments. They are numeric
  fields re-rendered by `strconv`, so no client or file string reaches the
  argument vector, and no rendered value can begin with a dash.
- `internal/gateway`: only the control plane changes, and it is already
  loopback-only. The completion path is deliberately untouched, so this spec
  adds nothing to the network-input surface.
- Named failure mode for the reviewer: a default that Go accepts and the
  server rejects makes every model fail to launch. The range table pinned to
  the recorded server ranges is what holds this, and the documentation names
  it.

## Docs to change

- `README.md`: one line under Features saying Settings holds machine-wide
  sampling defaults with optional per-model overrides.
- `docs/getting-started.md`: a sentence in the settings walk-through pointing
  at the new page.
- A new how-to page under `docs/`: the parameter list, that a request's own
  value wins, that a blank field means the model server's own default, that a
  change applies when the model loads again, and that reproducibility comes
  from a fixed temperature because the pinned server ignores a seed.

## Dependencies and sequencing

- Supersedes itd-2609061429516182; iss-2609061429558510 is the documented
  fact the page states.
- Nothing must ship first. The flag-set research note is written before the
  code, and its table is the test fixture.
- `Config.Clone` is shared ground with itd-2609061441241254,
  itd-2609061441261073 and itd-2609061441285238; whichever lands first adds
  it, and the others use it.
- Related, not blocking: iss-2 (races around `App.SetConfig`).

## Open design points

- The exact flag set and spellings, and whether a completion-token default
  ships, follow from reading the pinned server's parser; the lab's reasoning
  model needed an 8,192-token budget, so the field ships if a flag exists.
- Whether per-model overrides are edited on the model card or in a Settings
  table.
- Whether the launcher logs the effective sampling for each model at load, so
  the per-model log shows what a process is running with.
