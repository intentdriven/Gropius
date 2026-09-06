---
id: itd-2609061431463108
slug: context-window-visible-per-model-alice-s-client-reads-each-m
spec_id: spc-2609061822377137
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Context window visible per model: Alice's client reads each model's context length from the models list and sizes its prompts before sending, instead of discovering the limit by failure

## Press Release

Alice points her agent at Gropius. Before its first request the agent lists
the models and reads, beside each one, how many tokens of context that model
accepts, under the name her tooling already looks for. It trims its history to
fit and never sends a prompt the model was not built to take. Bob, browsing
the control panel, sees the same figure on each model card, labelled as that
model's maximum, and picks the model whose window suits the job.

## Why This Matters

Today a client learns a model's limit by failing: the models list carries only
an id, a timestamp and an owner, and nothing about context. Publishing the
figure turns a class of failure into a number a client can plan against. It is
presented as the architectural maximum, because what a given Mac can actually
hold at once may be smaller; that smaller, effective figure is the subject of
itd-2609061431481936 and iss-2609061431537735.

Evidence: research note 2026-09-06-model-bench-evidence (context probe: verified prompt sizes per model, prefill rates, and the gateway-timeout confound for the dense model).

## Mechanism

We expect a client that trims its prompt to the published figure to stay
inside the range the model was trained or scaled for, because the figure is
the positional range the model's own configuration declares, which the server
does not enforce but beyond which generation extrapolates.

## Scope Conditions

- Models whose own configuration declares a positional range; architectures <!-- cond: cond-2609061822375202 -->
  that declare none are listed exactly as they are today, with no figure.
- The figure is the architectural maximum, not what a particular Mac can hold <!-- cond: cond-2609061822374039 -->
  at once.
- Clients that read the raw JSON of the models list; a typed SDK object that <!-- cond: cond-2609061822370244 -->
  discards fields it does not know never sees it.
- Models downloaded from the mlx-community organisation, which is where <!-- cond: cond-2609061822370207 -->
  Gropius downloads from.

## Acceptance Criteria

- Given a ready model whose configuration declares a positional range N, when
  a client requests the models list, then that model's entry carries N under
  both `context_length` and `max_model_len` and the fields already served are
  unchanged.
- Given a model whose configuration declares no positional range, when the
  models list is requested, then its entry carries no context figure and the
  model is still listed as ready.
- Given a configuration whose declared range is negative, zero, non-integer or
  above the documented ceiling, when the registry rescans, then the figure is
  omitted and the model's state is unaffected.
- Given a model already on disk from a build that predates the figure, when
  Gropius next starts, then that model's entry carries the figure without a
  re-download.
- Given the control panel, when Alice opens the Models tab, then each card
  shows the figure labelled as that model's maximum context.
- Given the documentation, when a reader looks up the models list, then it
  states the figure, both names it is published under, and that the window a
  given Mac can serve may be smaller.

## Open Questions

- Resolved: field name and shape — top-level fields on the models list, the
  context length published under both `context_length` and `max_model_len`;
  later extensions follow the same shape with names already common elsewhere.
- Resolved: labelling — the figure is the architectural maximum, and the
  documentation and the card say so.
- Resolved: the control panel shows the figure on each model card.
- Resolved: the shape of Gropius's extensions to the models list is recorded
  as a dated decision line rather than as an architecture decision record; the
  reviewer asked for the latter, and the maintainer chose the lighter record
  because the shape is one convention, not a structural commitment.
- Deferred: which configuration key is authoritative for each architecture,
  and what is published when a scaled range is declared without its
  pre-scaling figure. Sampled at spec time against the architectures actually
  in use, and recorded there.

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._

## Grounds

- pursued: we expect a machine-wide temperature default and a visible context length to remove the two commonest client misconfigurations on a shared Mac, wrong temperature and oversized prompts; we are wrong if clients keep sending their own values regardless, or if no client reads the context field within two releases of it shipping
