---
id: itd-2609061429508050
slug: default-sampling-parameters-in-settings-alice-sets-a-default
spec_id: spc-2609061822378193
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
supersedes: [itd-2609061429516182]
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Default sampling parameters in Settings: Alice sets a default temperature (and the other sampling parameters mlx-lm accepts) in the control panel, and every request that omits the parameter is served with that value

## Press Release

Alice runs Gropius for a team whose tools speak the OpenAI API but never set
a temperature. She opens Settings, sets the default temperature she wants the
whole machine to serve with, and saves; the panel tells her which models must
load again before the new value counts. From then on every request that omits
the parameter is served with her value. One model wants a different figure, so
she gives that model its own override, which takes over the next time it
loads. Bob's client, which sets its own temperature, is unaffected: what a
request carries still wins. Each field shows the value that applies when Alice
leaves it blank, so she can see what she is changing.

## Why This Matters

Sampling parameters a client sends are passed through untouched and the model
server starts with none of its own set, so today the only way to change what
"unspecified" means for the machine is to change every client. A shared
server needs one place to say it. Settings already holds the other
machine-wide serving choices, so this is where it belongs.

Reproducibility does not need a seed. The pinned model server ignores the
seed a request carries (iss-2609061429558510), and a fixed temperature is what
makes generation repeatable; the intent that would have exposed a seed
(itd-2609061429516182) is superseded by this one and by that documented fact.

Evidence: research note 2026-09-06-model-bench-evidence (sampling probe: seed inert on four models, temperature effective, temperature 0 deterministic; a reasoning model needed an 8,192-token completion budget).

## Mechanism

We expect a machine-wide default to reach every request that omits a sampling
parameter and none that carries one, because the model server takes those
values once when it starts and a request's own field replaces them for that
request alone.

## Scope Conditions

- Requests served to the pinned mlx-lm 0.31.3 model server; a later server may <!-- cond: cond-2609061822371570 -->
  take a different set of parameters when it starts.
- Only the parameters that server accepts when it starts; anything it accepts <!-- cond: cond-2609061822373761 -->
  per request alone is outside this claim.
- The values Gropius accepts are no wider than the ranges the model server <!-- cond: cond-2609061822370170 -->
  itself accepts.
- Clients follow OpenAI semantics, where an omitted or null parameter means <!-- cond: cond-2609061822377006 -->
  "use the default".
- One global set of defaults with at most one override per model; a model with <!-- cond: cond-2609061822372855 -->
  no override is served with the global set.

## Acceptance Criteria

- Given a default temperature saved in Settings, when a client sends a
  completion that omits the temperature, then that request is served with the
  saved value.
- Given a default temperature saved in Settings, when a client sends its own
  temperature, then that request is served with the client's value.
- Given an override saved for one model, when that model has loaded again,
  then requests to it that omit the parameter are served with the override
  rather than the global default.
- Given a default is changed, when Alice saves it, then the panel names the
  loaded models that must load again before the new value applies to them.
- Given a value outside the range the model server accepts, when it is posted
  to Settings, then it is refused with the field named and neither the running
  nor the stored configuration changes.
- Given the documentation, when a reader looks up sampling, then it states
  which parameters can be defaulted, that a request's own value wins, what a
  blank field means, and that reproducibility comes from a fixed temperature
  because the model server ignores a seed.

## Open Questions

- Resolved: where the default is applied — to the model server when it starts,
  so a change counts from that model's next load and the panel says so.
- Resolved: precedence — a request's own value always wins; the default only
  fills what a request omits.
- Resolved: scope — a global set of defaults plus an optional per-model
  override.
- Resolved: which parameters — only those the pinned mlx-lm server accepts as
  start-up options, the exact set confirmed against the 0.31.3 server at spec
  time; a default number of completion tokens is included if that server takes
  one, since a reasoning model in the lab needed 8,192.
- Resolved: the panel shows the value that applies when a field is left blank,
  so a blank field is honest about what it means.
- Resolved: seed — the pinned server ignores it (iss-2609061429558510), so no
  seed field is offered and itd-2609061429516182 is retired; the documentation
  states the behaviour instead.
- Deferred: what a hand-edited configuration file holding an out-of-range
  sampling value does at start-up. The reviewer asked for an explicit choice
  between refusing to serve beyond loopback and dropping the value with a
  warning; the interview did not reach it, and the spec decides it alongside
  the range check.

## Audit Notes

<!-- abcd-review: OWED receipt=rcp-e4c0369b6247 -->
Fidelity review OWED (receipt rcp-e4c0369b6247). The audit compares this
intent's acceptance criteria against the delivered code, so it runs after the
change merges, not at the moment the spec closes: until then this section is
expected to be empty of findings, and its absence is not an oversight.

## Grounds

- pursued: we expect a machine-wide temperature default and a visible context length to remove the two commonest client misconfigurations on a shared Mac, wrong temperature and oversized prompts; we are wrong if clients keep sending their own values regardless, or if no client reads the context field within two releases of it shipping
