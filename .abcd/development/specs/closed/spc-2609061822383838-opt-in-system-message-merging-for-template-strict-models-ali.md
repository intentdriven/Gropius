---
id: spc-2609061822383838
slug: opt-in-system-message-merging-for-template-strict-models-ali
intent: itd-2609061441310453
origin: researcher-authored
production_mode: hand-written
---
# opt-in-system-message-merging-for-template-strict-models-ali

## Summary

This spec lets the gateway fold every system-role message of a chat completion
request into one leading system message, for models the operator has switched it
on for in Settings, and for nothing else. It is the first feature that reads
prompt content, and it is bounded by adr-2609061610102325: merged content is
never logged, retained or counted, only opted-in models are read, and merging is
the only rewrite of prompt content Gropius performs. This record also fixes the
per-model settings structure that itd-2609061429508050 reuses.

## Scope

In scope:

- `config.Config.PerModel`, a map keyed by canonical repo id, and the settings
  handler change that lets a model be switched off again.
- The merge in `handleCompletions`, on the already-buffered request, before the
  upstream request is built, and the parse footprint it needs: role for every
  message, content only for system-role messages.
- Settings validation of the map's keys.

Out of scope:

- Automatic detection from a template failure: that means interpreting an
  unstructured error and sending the request twice.
- Overriding the model server's chat template at launch, which would change
  behaviour for every client of that model.
- A machine-wide switch. Per model, or not at all.
- Plain completions (`/v1/completions`), which carry no messages, and any other
  reading of prompt content.

## Approach

**The per-model structure, fixed here.** `config.Config` gains

    PerModel map[string]ModelSettings `json:"per_model,omitempty"`

with `ModelSettings` holding `MergeSystemMessages bool
\`json:"merge_system_messages,omitempty"\`` and room for the sampling defaults
itd-2609061429508050 will add. Keys are the registry's canonical repo id;
`App.SetConfig` canonicalises each key against the registry (case-folded) and
refuses a key that is not a well-formed repo id, leaving the stored settings
untouched.

**Switching a model off.** `handleSetSettings` decodes the posted body *into a
copy of the current config*, and `encoding/json` **merges** keys into an
existing map rather than replacing it — so a form that omits a model, or posts
`{}`, would leave its flag on. Every other field on that form is a scalar and
has never met this. The handler therefore first decodes the raw body into
`map[string]json.RawMessage`; if the key `per_model` is present, the decoded map
is **assigned wholesale** over the current one after the copy-decode, so
omitting a model turns its switch off. That rule is stated in a comment beside
the existing decode-into-current comment, because the two are easy to read as
contradicting each other.

**The merge.** After `resolveModel` yields the canonical id and before the
upstream body is built, `handleCompletions` checks
`cfg.PerModel[canonical].MergeSystemMessages`. When it is off, or the path is
`/v1/completions`, nothing else happens and the code path is today's.

When it is on, the gateway decodes `payload["messages"]` into
`[]json.RawMessage` and, for each element, decodes only
`struct{ Role string \`json:"role"\` }`. For an element whose role is `system`
it additionally decodes `struct{ Content string }`. That is the entire reading
footprint, and it is the footprint the ADR names. It then rebuilds the array as:
one system message whose content is the system texts joined in their original
order, followed by every non-system element **as its original `json.RawMessage`**
— untouched bytes, so their content is preserved exactly rather than
re-encoded field by field. The rebuilt array replaces `payload["messages"]` and
the request continues down today's single path.

**Fail open, never lossily.** If `messages` is absent, does not decode as an
array, has an element that is not an object, or has a system message whose
content is not a plain string (a list of content parts, say), the request is
relayed **unchanged** rather than rewritten. Nothing about that decision is
logged with content; at most a counter-free debug line naming the model.

**Equality is of decoded values, not bytes.** `handleCompletions` already
re-marshals `map[string]json.RawMessage` with `json.Marshal`, which sorts
top-level keys and compacts each raw value, so no implementation going through
`encoding/json` can promise byte-identity — and today's relay does not have it
either. Every criterion below is therefore written and tested as JSON-value
equality, which is what adr-2609061610102325 records.

**Never logged, retained or counted.** No log line, error message, statistics
record or control-panel view carries any fragment of message content. The merged
body exists only for the duration of the relay. The launch arguments of a model
server are unaffected — merging happens in the gateway, never at launch.

**Streaming.** The merge is done once, on the buffered body, so streaming and
non-streaming requests take exactly the same path and the buffered-body limit
(`maxRequestBody`) is untouched. `relayRewritingModel` still maps the backend's
model value back to the name the client asked for, in every chunk.

## How each acceptance criterion is satisfied

1. _Given a model with merging on and a request carrying one system message first
   and another mid-conversation, when the request reaches the model server, then
   it carries exactly one system message, leading, holding both texts in order,
   and every other message and field is unchanged in value._ The rebuild above.
   Test (`internal/gateway`): a recording upstream captures the relayed body;
   assert one system message at index 0 whose content is the two texts in order,
   assert the remaining messages decode equal to the input's, and assert every
   other top-level field decodes equal (with `model` rewritten, as today).
2. _Given a model with merging off, when a request of the same shape reaches the
   model server, then its messages are unchanged in value._ The merge is inside
   the flag's branch. Test: the same request with no `per_model` entry; assert
   the relayed `messages` decode equal to the input's — value equality, because
   the gateway has always re-serialised.
3. _Given a model with merging on and a system message whose content is a list of
   parts rather than plain text, when the request reaches the model server, then
   its messages are unchanged in value._ The content decode fails and the
   fail-open rule relays unchanged. Test: a system message whose content is an
   array of parts; assert value equality of `messages`, and assert with a
   captured log handler that no fragment of it was recorded.
4. _Given a model with merging on and a streaming request, when the completion
   streams, then the first chunk reaches the client before the upstream response
   completes, and every chunk still names the model the client asked for._ Merging
   happens before the request is made, so `streamCopy`'s per-chunk flush is
   unchanged. Test: a stub upstream that emits a chunk, blocks, then emits more;
   assert the first chunk is readable by the client while the upstream is still
   blocked, and that every chunk's `model` is the requested name.
5. _Given a model with merging on and any log level, when a merged request has
   been served, then no fragment of any message's content appears in the Gropius
   log or in the model server's launch arguments._ Test mechanism, stated so it is
   written as a test and not as a code-review promise: `gateway.Options.Log` is
   injectable, so the test installs a `slog.Handler` recording every message and
   attribute, puts a canary string in the prompt, serves the request at debug
   level, and asserts the canary appears nowhere in the captured records. A
   second assertion covers the fake launcher's recorded `Spec`.
6. _Given a model with merging on, when Alice switches it off in Settings, then
   the next request to that model reaches the model server with its messages
   unchanged in value._ The replace-not-merge rule. Two tests: a `control_test`
   posting settings that omit the model and asserting `App.Config().PerModel` no
   longer carries it; and a gateway test that the following request is relayed
   with value-equal messages.
7. _Given a settings save naming a model id that is not a valid identifier, when
   it is submitted, then the save is refused and the stored settings are
   unchanged._ Key validation in `App.SetConfig`, before `config.Save`. Test: post
   a malformed key, assert the 400, and assert the file on disk is byte-identical
   to before. A companion test asserts a case-variant but valid key is
   canonicalised to the registry's spelling and matches at request time.

## Trust-boundary review notes

`internal/gateway` and `internal/config` are both touched, and this is the
change adr-2609061610102325 exists to bound. It ships with the adversarial
security review the conventions require, and that review checks the never-retain
rule against every log and store path, not only the ones this record adds.

- **What actually changes.** Gropius already buffers the whole prompt in memory
  and hands it to a child process it launched; the gateway's reason for not
  parsing `messages` today is CPU cost, stated at the decode site. The honest
  framing is that Gropius goes from *holding* the prompt without interpreting it
  to *interpreting one role's text* for models the operator names. That is
  narrower than "no longer a blind relay" and wider than "nothing changes", and
  it is what the Settings copy and the docs say.
- **Bounded reading footprint.** Role for every element, content for system
  elements only, and only for opted-in models on the chat-completions path. The
  parse shape is written into the code and cited from the ADR, so widening it is
  a visible edit rather than a drift.
- **Never retained.** No content reaches a log, an error body, the statistics
  store or the control plane. Criterion 5's canary test runs at debug level, so
  it holds at every log level.
- **`internal/config` (file parsing).** A nested map is a larger parse surface
  than a scalar. It is bounded by the existing `MaxConfigBytes` read through
  `ReadRegular`; keys are validated as repo ids so a key cannot become a path;
  and unknown fields inside `ModelSettings` fall back to their zero values, so a
  file from a newer build still loads.
- **No new memory bound.** The merge allocates the joined system text and the
  rebuilt array; both sit under the already-enforced 32 MiB body limit, which
  this record does not raise. adr-2609061503319212 is unaffected: nothing here
  leaves the machine or is written anywhere.

## Docs to change

- `docs/getting-started.md`: a subsection under section 6 naming the setting,
  saying which models need it (templates that reject a system message that is not
  first), exactly what it does to a request, that Gropius reads the system
  messages of requests to that model and nothing else, and that nothing read is
  kept.
- `README.md`: one present-tense sentence, naming the setting as per-model and
  off by default, and pointing at the ADR for the rule it is granted under.

## Dependencies and sequencing

- adr-2609061610102325 is the decision this record implements; the spec adds
  exactly what that ADR permits and nothing more.
- This record fixes the `per_model` structure. itd-2609061429508050 (default
  sampling parameters) reuses it rather than deciding again; whichever is planned
  first owns the shape, and this record is it.
- Shares `App.SetConfig`'s validation block and canonicalisation helper with
  itd-2609061441241254 (pinned models). Whichever lands first builds the helper.
- No dependency on the pool records. The merge is entirely in the gateway.

## Open design points

- The separator between merged system texts. A blank line is recommended; it is
  documented either way, because a client can observe it.
- Whether a request with no system message skips the rebuild. Recommended as a
  fast path; the criteria hold either way.
- Whether the fail-open cases emit a debug line naming only the model. Permitted
  by the ADR, as it carries no content.
