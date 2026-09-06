---
id: spc-2609061822377137
slug: context-window-visible-per-model-alice-s-client-reads-each-m
intent: itd-2609061431463108
origin: researcher-authored
production_mode: hand-written
---
# context-window-visible-per-model-alice-s-client-reads-each-m

## Summary

Each ready model's architectural context length is read from the model's own
configuration file at rescan, kept on the registry entry, and published on the
models list under both `context_length` and `max_model_len` as top-level
fields. The control panel shows the same figure on each model card, labelled
as that model's maximum. A model whose configuration declares no positional
range is listed exactly as it is today.

## Scope

In scope:

- A `ContextLength` field on `registry.Model`, set at `Rescan` and at the
  app layer's download-validation path, persisted in the registry file.
- Publication on `GET /v1/models` under both spellings, top-level, leaving
  every field already served unchanged.
- A validation rule that omits anything that is not a plausible positive
  integer, without affecting the model's state.
- The figure on the model card in the control panel.
- A reference page for the models list.

Out of scope:

- The effective window a given Mac can hold, which is itd-2609061431481936
  and iss-2609061431537735.
- Any enforcement: nothing refuses or trims a prompt because of the figure.
- A nested extension object. The shape is settled by the 2026-09-06 decision
  line: extensions are top-level fields with names already common elsewhere.
- Residency, in-flight and last-used fields, which are itd-2609061441228998.

## Approach

The registry already reads each model directory's configuration file at rescan
through `readManifest`, which goes via `config.ReadRegular` with a size cap,
and `plausibleModelConfig` already decodes it into a map. This spec adds
`readContextLength(dir string) int64` beside them, reusing the same read, so
no new file access is introduced on the scan path.

The key rule, sampled at spec time against the architectures actually in use
and recorded in the dated research note the tests cite:

- `max_position_embeddings` at the top level is authoritative.
- If it is absent, `text_config.max_position_embeddings` is read, which is
  where multimodal configurations nest the text model's settings.
- Scaling arithmetic is never applied. When `rope_scaling` carries
  `original_max_position_embeddings`, the top-level figure is already the
  scaled window and is published as it stands. When `rope_scaling` declares a
  factor and no pre-scaling figure, the declared `max_position_embeddings` is
  published without multiplying it, and the family is recorded in the note.
  Under-reporting is the safe direction for a client that trims its history;
  over-reporting would hand a client a number the model was never scaled to.

Validation: the value is accepted only when it decodes as a JSON number that
is integral, greater than zero and at or below a fixed ceiling of 8,388,608
tokens — far above any window in use (the lab verified prompts of about
122,000 tokens) and low enough that a hostile or corrupt configuration cannot
hand a client an absurd figure to size buffers from. Anything else omits the
field; the rescan completes and the model's state is untouched.

`registry.Model` gains `ContextLength int64` with `json:"context_length,omitempty"`,
so an unknown figure is absent rather than zero. `Rescan`'s existing-entry
branch assigns field by field — `Path`, `Bytes`, `State`, `Err` — and must
assign `ContextLength` too, or a model already recorded by an older build
would never gain the figure; this is the mechanism behind the "already on
disk" criterion, and the three `Registry.Put` call sites in `internal/app`
set it on the download paths.

`handleListModels` in `internal/gateway/gateway.go` builds each entry as a
map; when the figure is non-zero it adds `context_length` and `max_model_len`
with the same value, leaving `id`, `object`, `created` and `owned_by` as they
are. Two spellings are deliberate duplication: the first is what
OpenRouter-style and Ollama-style listings use, the second is what
vLLM-derived clients read.

The panel's `renderModels` prints the figure on the info line beside
`bytes(m.bytes)`, as "max context 128K", so it is not read as the effective
window.

## How each acceptance criterion is satisfied

- "Given a ready model whose configuration declares a positional range N, when
  a client requests the models list, then that model's entry carries N under
  both `context_length` and `max_model_len` and the fields already served are
  unchanged." A gateway test with a registry stub asserts both keys carry N
  and that the entry's other four keys are exactly what they are today.
- "Given a model whose configuration declares no positional range, when the
  models list is requested, then its entry carries no context figure and the
  model is still listed as ready." A registry test writes a configuration with
  neither key and asserts a zero field; a gateway test asserts neither key is
  present in the JSON and the model is listed.
- "Given a configuration whose declared range is negative, zero, non-integer
  or above the documented ceiling, when the registry rescans, then the figure
  is omitted and the model's state is unaffected." A table test over the four
  cases (and a string value, and a value one above the ceiling) asserts a zero
  field and `StateReady` preserved.
- "Given a model already on disk from a build that predates the figure, when
  Gropius next starts, then that model's entry carries the figure without a
  re-download." A registry test seeds a registry file whose entry has no
  figure, runs `Rescan` over the directory, and asserts the entry gains it —
  the test that fails until the existing-entry branch assigns the new field.
- "Given the control panel, when Alice opens the Models tab, then each card
  shows the figure labelled as that model's maximum context." A panel test
  asserts the label and the figure render on a card with a figure, and that a
  card without one shows no label.
- "Given the documentation, when a reader looks up the models list, then it
  states the figure, both names it is published under, and that the window a
  given Mac can serve may be smaller." A documentation test compares the
  field table on the reference page with the JSON keys `handleListModels`
  emits.

## Trust-boundary review notes

- `internal/config` as the reader: the configuration file lives in a model
  directory that, in the shared-cache install mode, another local account can
  write. The read stays inside `readManifest`, so the existing regular-file
  check and size cap apply unchanged; nothing new opens a file.
- `internal/gateway`: the figure is served to the LAN. Only an integer is ever
  published, never a string taken from the file, so no attacker-chosen text
  reaches a client through this field. The ceiling bounds the value a hostile
  configuration can advertise.
- `internal/registry`: the new field is persisted, so a corrupt registry file
  can carry an implausible figure; the same bounds are applied when an entry
  is read back, not only when it is scanned.

## Docs to change

- `README.md`: one line under Features saying the models list publishes each
  model's maximum context.
- `docs/getting-started.md`: a sentence where the models list is first shown,
  pointing at the reference page.
- A new reference page under `docs/` for the models list: every field served,
  the two spellings of the context figure, that it is the architectural
  maximum, and that what a given Mac can hold at once may be smaller.

## Dependencies and sequencing

- Binding: the 2026-09-06 decision line on the models-list extension shape —
  top-level fields, context length under both `context_length` and
  `max_model_len`.
- Nothing must ship first.
- itd-2609061431481936 builds on this spec and reuses the field shape for the
  effective window; itd-2609061441228998 adds its residency fields under the
  same convention.
- itd-2609061521082551's context-fill column waits on this figure.
- Related, not blocking: iss-2609061443332414 shares the documentation page.

## Open design points

- The exact set of configuration keys per architecture, from the sampling
  recorded in the research note; a family that declares its range under a
  third key is added to the rule there rather than guessed here.
- The ceiling's value, if the sampling finds a shipped model above it.
- Whether the card abbreviates the figure (128K) or prints it in full.
