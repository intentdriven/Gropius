---
id: itd-2609061441310453
slug: opt-in-system-message-merging-for-template-strict-models-ali
spec_id: spc-2609061822383838
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Opt-in system-message merging for template-strict models: Alice switches it on per model in Settings, and a client that re-sends its system prompt mid-conversation gets a completion instead of a template error

## Press Release

Alice's coding agent re-sends its system prompt on every turn. Her favourite
model refuses any conversation whose instructions are not all at the top, so
every agentic turn fails instantly. She switches on system-message merging for
that model in Settings, and Settings tells her plainly what she is agreeing to:
for that model, Gropius reads the instruction messages in each request, gathers
them into one at the front, keeps none of it and writes none of it down. From
then on her agent works. Bob's model, which tolerates the pattern, is untouched,
because he never switched it on, and nothing of his prompts is ever read.

## Why This Matters

The 2026-09-05 model-bench lab hit this with the Qwen3.8 template and solved it
with a proxy that rewrites every request. The gateway today parses only the
model field of a request and relays the rest, so this would be the first feature
that reads and rewrites prompt content, which is why it is opt-in and per model.
The alternative is documenting the client-side workaround and leaving the
gateway blind to prompts, which does not help Alice with an agent she does not
control. The standing rule the switch is granted under — merged content is never
logged, retained or counted, only named models are read, and merging is the only
rewrite of prompt content Gropius performs — is recorded as a decision record
alongside adr-2609061503319212, which it refines.

Evidence: research note 2026-09-06-model-bench-evidence (the dense 27B's chat template rejected a non-leading system message, so every local benchmark run of it went through the lab's merging proxy; the four sanitised runs scored 200, 197, 200 and 160).

## Mechanism

We expect an opted-in template-strict model to answer a client that re-sends
its system prompt mid-conversation because such templates reject where system
messages sit rather than what they say, so folding them into one leading
message satisfies the template; we are wrong if a template also constrains the
system content itself.

## Scope Conditions

- Models whose chat template rejects a system message that is not first but <!-- cond: cond-2609061822388946 -->
  accepts one leading system message of any length; a template that also
  rejects long system content, or that reads mid-conversation system text as a
  signal of its own, is not covered.
- Clients that send system content as plain text; content sent as a list of <!-- cond: cond-2609061822388522 -->
  parts is passed through untouched.
- Chat completion requests only, and only for the models the operator has <!-- cond: cond-2609061822387739 -->
  named.
- Requests within the body size the gateway already buffers; nothing here <!-- cond: cond-2609061822389490 -->
  raises that limit.

## Acceptance Criteria

- Given a model with merging on and a request carrying one system message first
  and another mid-conversation, when the request reaches the model server, then
  it carries exactly one system message, leading, holding both texts in order,
  and every other message and field is unchanged in value.
- Given a model with merging off, when a request of the same shape reaches the
  model server, then its messages are unchanged in value.
- Given a model with merging on and a system message whose content is a list of
  parts rather than plain text, when the request reaches the model server, then
  its messages are unchanged in value.
- Given a model with merging on and a streaming request, when the completion
  streams, then the first chunk reaches the client before the upstream response
  completes, and every chunk still names the model the client asked for.
- Given a model with merging on and any log level, when a merged request has
  been served, then no fragment of any message's content appears in the Gropius
  log or in the model server's launch arguments.
- Given a model with merging on, when Alice switches it off in Settings, then
  the next request to that model reaches the model server with its messages
  unchanged in value.
- Given a settings save naming a model id that is not a valid identifier, when
  it is submitted, then the save is refused and the stored settings are
  unchanged.

## Open Questions

- Resolved: Gropius may read and rewrite prompt content, opt-in per model,
  under a decision record stating the invariant that merged content is never
  logged, retained or counted, that only opted-in models are read, and that
  merging is the only rewrite of prompt content Gropius performs.
- Resolved: the switch is per model in Settings; there is no automatic
  detection from a template failure, which would mean interpreting an
  unstructured error and sending the request a second time.
- Resolved: the merge is done by Gropius rather than by overriding the model
  server's chat template, because an override would change behaviour for every
  client of that model rather than only the ones that re-send.
- Resolved: what "unchanged" means is equality of the decoded request, not of
  its bytes, because the gateway already re-serialises every request it relays.
- Resolved: merging happens once on the request Gropius has already buffered,
  so streaming and non-streaming requests take the same path and the buffered
  body limit is untouched.
- Resolved: the setting can be switched off again from Settings; the stored
  per-model settings are replaced by what the form submits rather than merged
  into, so leaving a model out turns its switch off.
- Resolved: per-model settings are keyed by the registry's canonical model id,
  so an id written with different letter case still matches the model it names.
- Resolved: a request whose messages cannot be read, or whose system content is
  not plain text, is relayed unchanged rather than rewritten lossily; plain
  completions carry no messages and are never touched.
- Resolved: the per-model settings structure is shared with
  itd-2609061429508050; whichever record is planned first fixes its shape and
  the other reuses it.

## Audit Notes

<!-- abcd-review: OWED receipt=rcp-9706a6e500a3 -->
Fidelity review OWED (receipt rcp-9706a6e500a3).

## Grounds

- pursued: we expect a shared Mac to serve several agents without their models evicting each other once the operator can pin, budget and grace, and once keyed clients can see what is warm; we are wrong if model swaps stay as frequent with those controls set as they were without them, measured by the statistics store
