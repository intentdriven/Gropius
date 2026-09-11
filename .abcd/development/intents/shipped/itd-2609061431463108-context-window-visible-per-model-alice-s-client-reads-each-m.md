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

<!-- abcd-review: INGESTED receipt=rcp-f32e2cb0c516 -->
Fidelity review — receipt rcp-f32e2cb0c516 (verifier intent-auditor claude-opus-5[1m]).

Provenance: intent-auditor@claude-opus-5[1m] · rubric_hash sha256:ddb20008870670e78835134e55948823c7d86602c2b33d41a0e12f0ec9dd4cbc · prompt_hash sha256:1821f36820edce83360e0b976685c64ef380ede219a2ee14bbd049451f227cfa
Input attestations: diff:worktree at HEAD 18f1a4e286fd85f6e94bb2ae1d2ecba40e13cc77 (tree 5f3256dd3718af34e459add3c3c30b5d631e90af); history was rewritten during a rename, so no per-spec commit range exists and the shipped tree was audited instead@sha256:9d9d3f6795a1a23511a90941c4a8bcca89a9623f1b31918d142665cd0adb134c;

Acceptance rollup: MET 5 · MET_WITH_CONCERNS 1 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: handleListModels adds context_length and max_model_len with the same registry figure onto the entry it already built from id/object/created/owned_by, and a gateway test asserts both keys carry N and that the entry's key set is exactly those four plus the two new ones; an end-to-end test carries a real model directory through Rescan to the wire
  evidence: internal/gateway/gateway.go:260 — "entry["context_length"] = m.ContextLength"
  evidence: internal/gateway/gateway.go:261 — "entry["max_model_len"] = m.ContextLength"
  evidence: internal/gateway/gateway_test.go:964 — ""context_length": true, "max_model_len": true}"
  evidence: internal/gateway/gateway_test.go:1136 — "func TestContextLengthReachesTheWireFromAModelDirectory(t *testing.T) {"
- ac-2 — MET: contextLengthFrom returns 0 when neither max_position_embeddings nor text_config.max_position_embeddings is declared, the field is omitempty and the gateway only emits the two keys when the figure is positive, so the entry is served with its four original fields and StateReady intact
  evidence: internal/registry/registry.go:765 — "return 0"
  evidence: internal/registry/registry.go:61 — "ContextLength int64 `json:"context_length,omitempty"`"
  evidence: internal/gateway/gateway.go:259 — "if m.ContextLength > 0 && m.ContextLength <= registry.MaxContextLength {"
  evidence: internal/gateway/gateway_test.go:979 — "func TestListModelsOmitsAnUnknownContextLength(t *testing.T) {"
  evidence: internal/registry/registry_test.go:1020 — "func TestRescanOmitsContextLengthWhenConfigDeclaresNone(t *testing.T) {"
- ac-3 — MET: positionalRange accepts only a JSON number that is integral, positive and at or below the 8,388,608 ceiling, checking the bounds on the float before conversion; a table test over negative, zero, fractional, string, null, object, one-above-ceiling and 1e300 asserts a zero figure and StateReady preserved, and the bound is reapplied when a stored figure is read back and again at the LAN edge
  evidence: internal/registry/registry.go:777 — "if !ok || n != math.Trunc(n) || n <= 0 || n > MaxContextLength {"
  evidence: internal/registry/registry.go:71 — "const MaxContextLength = 1 << 23"
  evidence: internal/registry/registry_test.go:1044 — "func TestRescanRejectsImplausibleContextLengths(t *testing.T) {"
  evidence: internal/registry/registry_test.go:1076 — "t.Errorf("state = %q, want ready — an implausible figure must not affect the model", m.State)"
  evidence: internal/registry/registry.go:163 — "if !plausibleContextLength(m.ContextLength) {"
- ac-4 — MET: Rescan's existing-entry branch assigns ContextLength alongside Path/Bytes/State/Err, and App.New runs that rescan over the models directory at startup, so a record written by a build that predates the field gains the figure from the directory already on disk; a registry test seeds a figure-less ready entry and asserts the rescan supplies 40960, and an app test asserts a restored ready record keeps its figure without re-downloading
  evidence: internal/registry/registry.go:548 — "existing.ContextLength = m.ContextLength"
  evidence: internal/app/app.go:119 — "if err := reg.Rescan(opts.Paths.Models); err != nil {"
  evidence: internal/registry/registry_test.go:1100 — "func TestRescanAddsContextLengthToAnEntryFromAnOlderBuild(t *testing.T) {"
  evidence: internal/app/app_test.go:637 — "func TestRestoredReadyModelKeepsItsContextLength(t *testing.T) {"
- ac-5 — MET_WITH_CONCERNS: the panel state snapshot serialises registry.Model directly so context_length reaches the browser, contextLabel renders it as "max context …", modelInfoLine places it on every ready card's info line, and a node-backed panel test asserts the rendered string — but the card shows an abbreviated, rounded-down figure rather than the number the models list publishes
  evidence: internal/gateway/control.go:141 — "Models []registry.Model `json:"models"`"
  evidence: internal/ui/static/app.js:50 — "return `max context ${n >= 1024 ? `${Math.floor(n / 1024)}K` : n}`;"
  evidence: internal/ui/static/app.js:225 — "const info = modelInfoLine(m);"
  evidence: internal/ui/panel_test.go:27 — "1.0 KB · max context 256K"
  evidence: internal/ui/panel_test.go:32 — "1.0 KB · max context 255K"
- ac-6 — MET: docs/models-list.md documents both spellings in the field table, states the figure is the architectural maximum read from max_position_embeddings, and says outright that what a given Mac can serve may be smaller; a gateway test holds the page's field table to the exact key set the listing emits and requires the phrases "architectural maximum", "may be smaller" and the literal ceiling
  evidence: docs/models-list.md:39 — "| `context_length` | The model's maximum context, in tokens. See below. |"
  evidence: docs/models-list.md:40 — "| `max_model_len` | The same figure again, under the name vLLM-derived clients read. |"
  evidence: docs/models-list.md:61 — "**What the number is not.** It is not what a given Mac can serve. The usable"
  evidence: internal/gateway/gateway_test.go:1076 — "page, err := os.ReadFile(filepath.Join("..", "..", "docs", "models-list.md"))"

Gap audit:
- honoured:
  - the models list carries each model's context length beside it, under the name the client's tooling already looks for
    evidence: internal/gateway/gateway.go:260 — "entry["context_length"] = m.ContextLength"
    evidence: internal/gateway/gateway.go:261 — "entry["max_model_len"] = m.ContextLength"
  - an architecture that declares no positional range is listed exactly as it is today, with no figure
    evidence: internal/registry/registry.go:765 — "return 0"
    evidence: internal/gateway/gateway_test.go:979 — "func TestListModelsOmitsAnUnknownContextLength(t *testing.T) {"
  - a hostile or corrupt configuration cannot hand a client an absurd figure; an implausible value is dropped without touching the model's state
    evidence: internal/registry/registry.go:777 — "if !ok || n != math.Trunc(n) || n <= 0 || n > MaxContextLength {"
    evidence: internal/registry/registry_test.go:1076 — "t.Errorf("state = %q, want ready — an implausible figure must not affect the model", m.State)"
  - a model already on disk gains the figure at the next start, with no re-download
    evidence: internal/registry/registry.go:548 — "existing.ContextLength = m.ContextLength"
    evidence: internal/app/app.go:119 — "if err := reg.Rescan(opts.Paths.Models); err != nil {"
  - Bob sees the same figure on each model card in the control panel, labelled as that model's maximum
    evidence: internal/ui/static/app.js:50 — "return `max context ${n >= 1024 ? `${Math.floor(n / 1024)}K` : n}`;"
    evidence: internal/ui/static/app.js:225 — "const info = modelInfoLine(m);"
  - the figure is presented as the architectural maximum, because what a given Mac can actually hold at once may be smaller
    evidence: docs/models-list.md:61 — "**What the number is not.** It is not what a given Mac can serve. The usable"
    evidence: internal/ui/static/app.js:40 — "// The label says "max context" so the figure is not read as the window this"
  - the figure is the positional range the model's own configuration declares, which the server does not enforce
    evidence: internal/registry/registry.go:776 — "n, ok := level["max_position_embeddings"].(float64)"
    evidence: docs/models-list.md:63 — "weights, and a very long one can take minutes to process. Nor is it enforced —"
- diverged:
  - the model card shows the figure — delivered as a rounded-DOWN abbreviation in whole units of 1,024 above 1,024 tokens (a model declaring 262,143 reads "max context 255K"), so the card is an approximation of the published number rather than the number; the exact figure is on the models list only. Deliberate and documented, but it is not the same number on both surfaces
    evidence: internal/ui/panel_test.go:32 — "1.0 KB · max context 255K"
    evidence: docs/models-list.md:85 — "card, labelled `max context`. From 1,024 tokens upwards the card abbreviates"
  - "the fields already served are unchanged" holds for the unkeyed listing, which is the shape the criterion's test pins; a keyed install additionally serves state/in_flight/last_used/pinned on the same entries, from the separate residency work, so the entry a keyed client sees is not the four-field OpenAI shape plus two
    evidence: internal/gateway/gateway.go:264 — "addResidency(entry, residency[config.FoldRepoID(m.RepoID)], pinned[config.FoldRepoID(m.RepoID)])"
    evidence: docs/models-list.md:41 — "| `state` | Whether the model is loaded, still loading, or not loaded. Only on an install with an API key. See below. |"
- missing: (none)

Scope-condition dispositions:
- cond-2609061822375202 — survived: the delivery reads the range only where a configuration declares one and yields 0 otherwise, and the listing then serves such a model exactly as before — no figure, still ready
  evidence: internal/registry/registry.go:765 — "return 0"
  evidence: internal/registry/registry_test.go:1020 — "func TestRescanOmitsContextLengthWhenConfigDeclaresNone(t *testing.T) {"
  evidence: internal/gateway/gateway_test.go:979 — "func TestListModelsOmitsAnUnknownContextLength(t *testing.T) {"
- cond-2609061822374039 — survived: no scaling arithmetic is applied and nothing enforces the figure; both the card label and the reference page say outright that it is the architectural maximum and that a given Mac may serve less
  evidence: internal/registry/registry.go:776 — "n, ok := level["max_position_embeddings"].(float64)"
  evidence: docs/models-list.md:61 — "**What the number is not.** It is not what a given Mac can serve. The usable"
  evidence: internal/ui/static/app.js:40 — "// The label says "max context" so the figure is not read as the window this"
- cond-2609061822370244 — survived: the figure ships as extra top-level keys on a raw JSON map rather than a schema change, and the reference page's Compatibility section states the assumption in the intent's own terms — a typed SDK object that drops unknown fields never sees them
  evidence: internal/gateway/gateway.go:260 — "entry["context_length"] = m.ContextLength"
  evidence: docs/models-list.md:235 — "is unaffected — but a typed SDK object that discards fields it does not know"
- cond-2609061822370207 — survived: mlx-community is still the org Gropius downloads from and the key rule was sampled against those configurations, so the assumption holds where it applies; it is not load-bearing, because Rescan adopts any org directory on disk and applies the same rule to it
  evidence: internal/gateway/control.go:594 — "author = "mlx-community""
  evidence: internal/app/app_test.go:28 — ""config.json": []byte(`{"model_type":"qwen3","max_position_embeddings":40960}`),"
  evidence: internal/registry/registry.go:529 — "ContextLength: contextLength,"
## Grounds

- pursued: we expect a machine-wide temperature default and a visible context length to remove the two commonest client misconfigurations on a shared Mac, wrong temperature and oversized prompts; we are wrong if clients keep sending their own values regardless, or if no client reads the context field within two releases of it shipping
