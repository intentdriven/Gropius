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

<!-- abcd-review: INGESTED receipt=rcp-9706a6e500a3 -->
Fidelity review — receipt rcp-9706a6e500a3 (verifier abcd:intent-auditor claude-opus-5[1m]).

Provenance: abcd:intent-auditor@claude-opus-5[1m] · rubric_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5 · prompt_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5
Input attestations: diff:worktree at HEAD 18f1a4e286fd85f6e94bb2ae1d2ecba40e13cc77 (clean); history was rewritten during the rename, so there is no reliable per-spec commit range and the tree was audited as shipped@-; file:internal/gateway/systemmerge.go@sha256:92220ece67baf88aa45e20804ea07fb5cf2f67dc9cd69a6b9a08b06ed2f24dbc;

Acceptance rollup: MET 6 · MET_WITH_CONCERNS 1 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: the merge runs on the buffered payload behind the per-model switch and rebuilds the array as one leading system message holding both texts joined by a blank line, with every non-system element carried over as its original bytes; a passing test asserts exactly that against a recording upstream, including a float, a nested object, an array and a 23-digit integer relayed unchanged
  evidence: internal/gateway/gateway.go:460 — "if r.URL.Path == chatCompletionsPath && cfg.PerModel[model].MergeSystemMessages {"
  evidence: internal/gateway/systemmerge.go:197 — "merged[contentField] = content"
  evidence: internal/gateway/systemmerge.go:204 — "rebuilt, err := json.Marshal(append([]json.RawMessage{mergedRaw}, others...))"
  evidence: internal/gateway/systemmerge_test.go:131 — "func TestMergingFoldsSystemMessagesIntoOneLeadingMessage(t *testing.T)"
  evidence: internal/gateway/systemmerge_test.go:157 — "map[string]any{"role": "system", "content": "You are Alice's assistant.\n\nAnswer briefly."}"
- ac-2 — MET: the merge is inside the flag's branch, so with no per_model entry the request takes today's path; the test posts the same two-system-message shape against config.Default and asserts the relayed messages decode equal to the input's
  evidence: internal/gateway/gateway.go:460 — "cfg.PerModel[model].MergeSystemMessages"
  evidence: internal/gateway/systemmerge_test.go:338 — "func TestWithoutTheSwitchTheMessagesReachTheModelUnchanged(t *testing.T)"
  evidence: internal/gateway/systemmerge_test.go:339 — "srv, _, fake, _ := newMergeGateway(t, config.Default)"
- ac-3 — MET: system content that is not a plain JSON string fails instructionText, which returns mergeRefused, and mergeSystemMessagesInto only assigns back to payload on mergeDone, so the request is relayed as it came; the test sends a list of content parts and asserts value-equal messages upstream and no canary fragment in the log
  evidence: internal/gateway/systemmerge.go:219 — "func instructionText(fields map[string]json.RawMessage) (string, bool)"
  evidence: internal/gateway/systemmerge.go:80 — "if outcome == mergeDone {"
  evidence: internal/gateway/systemmerge_test.go:356 — "func TestMergingLeavesContentPartsAlone(t *testing.T)"
  evidence: internal/gateway/systemmerge_test.go:374 — "want them unchanged"
- ac-4 — MET: merging happens once on the already-buffered body before the upstream request is built, so a streamed request takes the same path; the test holds the upstream open on a release channel, reads the first SSE line while it is still blocked, and asserts every chunk's model field is the name the client asked for and that the backend path never appears
  evidence: internal/gateway/gateway.go:459 — "It happens here, on the body already in hand, so a streamed request takes exactly this path too"
  evidence: internal/gateway/systemmerge_test.go:678 — "func TestMergedStreamingRequestStillStreams(t *testing.T)"
  evidence: internal/gateway/systemmerge_test.go:709 — "t.Fatal("the first chunk did not reach the client while the upstream was still generating")"
  evidence: internal/gateway/systemmerge_test.go:751 — "t.Errorf("chunk names model %q, want the requested %q", chunk.Model, mergeModel)"
- ac-5 — MET_WITH_CONCERNS: the log half is directly demonstrated — a canary in the prompt, a slog.Handler whose Enabled returns true at every level, and an assertion that the canary appears in no record — and the launch-argument half holds by construction, since the gateway hands the pool only a repo id and runtime.Spec has no field that could carry message text; the concern is that the launch-argument half is asserted through a proxy rather than against a recorded launch Spec, which the spec said it would cover
  evidence: internal/gateway/systemmerge_test.go:489 — "func TestMergedRequestLeavesNoPromptContentBehind(t *testing.T)"
  evidence: internal/gateway/systemmerge_test.go:76 — "func (h *logRecorder) Enabled(context.Context, slog.Level) bool { return true }"
  evidence: internal/gateway/systemmerge_test.go:511 — "t.Errorf("prompt content reached the log:\n%s", rec.text())"
  evidence: internal/gateway/gateway.go:29 — "Acquire(ctx context.Context, repoID string) (*runtime.Upstream, func(), error)"
  evidence: internal/runtime/launcher.go:20 — "type Spec struct {"
  evidence: internal/gateway/systemmerge_test.go:484 — "which is a proxy and is named as one"
  evidence: internal/archtest/prompt_content_test.go:50 — "func TestOnlyTheMergeReadsPromptContent(t *testing.T)"
- ac-6 — MET: the save path replaces rather than merges the per-model map — handleSetSettings clears incoming.PerModel whenever the posted body names per_model however it is spelled — the Settings form posts the whole map with an unticked model simply left out, and the gateway reads settings live, so the next request is relayed unchanged; three passing tests cover the save, the form's map construction and the following request
  evidence: internal/gateway/control.go:828 — "if namesPerModel(raw) {"
  evidence: internal/gateway/control.go:829 — "incoming.PerModel = nil"
  evidence: internal/gateway/permodel_test.go:45 — "func TestSavingSettingsWithoutAModelTurnsItsSwitchOff(t *testing.T)"
  evidence: internal/gateway/systemmerge_test.go:380 — "func TestSwitchingMergingOffRelaysTheNextRequestUnchanged(t *testing.T)"
  evidence: internal/ui/static/app.js:766 — "function perModelSettings(current, listed, checked) {"
  evidence: internal/ui/static/index.html:351 — "< legend>Merge system messages< /legend>"
- ac-7 — MET: App.SetConfig validates every per-model key as a well-formed < org>/< name> repo id before config.Save runs, so a malformed key returns an error the control handler turns into a 400; one test asserts the settings file is byte-identical after the refusal and the live config untouched, another asserts the stored switch survives the refused save
  evidence: internal/app/app.go:285 — "perModel, err := a.canonicalPerModel(c.PerModel)"
  evidence: internal/app/app.go:876 — "if err := config.ValidatePerModelKeys(in); err != nil {"
  evidence: internal/config/config.go:492 — "func ValidatePerModelKeys(m map[string]ModelSettings) error {"
  evidence: internal/app/app_test.go:742 — "func TestSetConfigRejectsInvalidPerModelKeyAndLeavesTheFileAlone(t *testing.T)"
  evidence: internal/gateway/permodel_test.go:81 — "func TestSettingsRejectsAPerModelKeyThatIsNotAModelID(t *testing.T)"

Gap audit:
- honoured:
  - Alice switches merging on for one model in Settings and Bob's model is untouched: the switch is per model, stored under the registry's canonical repo id, and off unless ticked
    evidence: internal/config/config.go:457 — "PerModel map[string]ModelSettings `json:"per_model,omitempty"`"
    evidence: internal/config/config.go:482 — "MergeSystemMessages bool `json:"merge_system_messages,omitempty"`"
    evidence: internal/ui/static/app.js:716 — "function renderMergeSwitches() {"
  - Gropius reads the instruction messages in each request and gathers them into one at the front, so the client that re-sends its system prompt mid-conversation gets a conversation the template accepts
    evidence: internal/gateway/systemmerge.go:129 — "func mergeSystemMessages(raw json.RawMessage) (json.RawMessage, mergeOutcome)"
    evidence: internal/gateway/systemmerge_test.go:187 — "func TestMergingMovesALoneSystemMessageToTheFront(t *testing.T)"
  - keeps none of it and writes none of it down: no message content reaches the log at any level, the statistics store, or a model server's command line
    evidence: internal/gateway/gateway.go:468 — "g.log.Debug("relayed a request unmerged: its messages carry something merging cannot rebuild faithfully", "model", model)"
    evidence: internal/gateway/systemmerge_test.go:489 — "func TestMergedRequestLeavesNoPromptContentBehind(t *testing.T)"
  - merging is the only rewrite of prompt content Gropius performs, and only opted-in models are read — the ADR's boundary is enforced rather than promised
    evidence: internal/archtest/prompt_content_test.go:26 — "var promptContentReaders = map[string]string{"
    evidence: internal/gateway/systemmerge_test.go:762 — "func TestPlainCompletionsAreNeverMerged(t *testing.T)"
  - Settings tells Alice plainly what she is agreeing to, and the docs say the same in the two places the spec named plus a page of its own
    evidence: internal/ui/static/index.html:353 — "Gropius reads the instruction messages of requests to that model and nothing else, keeps none of what it reads, and writes none of it down."
    evidence: docs/getting-started.md:151 — "**Settings → Merge system messages**, for a model whose template refuses a"
    evidence: README.md:166 — "exception is **Merge system messages**, a per-model setting that is off unless"
    evidence: docs/system-message-merging.md:1 — "# Merge system messages for a template-strict model"
  - nothing here raises the buffered-body limit: merging runs on the body the gateway had already read under the existing 32 MiB cap
    evidence: internal/gateway/gateway.go:318 — "const maxRequestBody = 32 << 20"
    evidence: internal/gateway/gateway.go:349 — "raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))"
- diverged:
  - "From then on her agent works" — delivered merging refuses more shapes than the press release implies: a system message after the first carrying any field beyond role and content, content that is null or absent, and content whose bytes are not valid UTF-8 are all relayed unmerged, so an agent producing one of those still meets the template error the feature exists to remove. The narrowing is deliberate and documented, not silent
    evidence: internal/gateway/systemmerge.go:177 — "return nil, mergeRefused"
    evidence: internal/gateway/systemmerge.go:224 — "if !utf8.Valid(raw) {"
    evidence: docs/system-message-merging.md:48 — "## What it passes on unrewritten"
  - the launch-argument half of the never-retain rule was to be asserted against the fake launcher's recorded Spec; delivered it is asserted through what the pool is handed, a proxy the test names as one, so no test would catch a future Spec field that carried request text
    evidence: internal/gateway/systemmerge_test.go:484 — "is a proxy and is named as one"
    evidence: internal/gateway/systemmerge_test.go:516 — "a launch is built from what the pool is given, and it must be the model id alone"
  - the spec described merging as building one system message from the joined texts; delivered it reuses the conversation's own first system message and replaces only its content, so that message's other fields (a name, a cache directive) survive the merge — a wider promise than the record made, and the reason a later system message carrying extra fields is refused instead
    evidence: internal/gateway/systemmerge.go:196 — "merged := owner"
    evidence: internal/gateway/systemmerge_test.go:232 — "func TestMergingKeepsTheLeadingSystemMessagesOwnFields(t *testing.T)"
- missing: (none)

Scope-condition dispositions:
- cond-2609061822388946 — untested: the assumption is about how a template-strict model's chat template behaves, and nothing in the delivered tree exercises a real template — every test relays to internal/mlxtest's fake server, which accepts whatever it is sent, so no delivered artefact confirms or contradicts that one leading system message of any length is accepted
- cond-2609061822388522 — survived: content sent as a list of parts is passed through untouched exactly as assumed: the content decode refuses anything that is not a plain JSON string, the refusal relays the request as it arrived, and a passing test asserts value-equal messages upstream
  evidence: internal/gateway/systemmerge.go:219 — "func instructionText(fields map[string]json.RawMessage) (string, bool)"
  evidence: internal/gateway/systemmerge_test.go:356 — "func TestMergingLeavesContentPartsAlone(t *testing.T)"
- cond-2609061822387739 — narrowed: the route half holds exactly — only /v1/chat/completions is touched and a plain completion carrying a messages array is relayed untouched — but "the models the operator has named" is bounded in the delivery in ways the condition did not assume: a key must be a well-formed < org>/< name> repo id or the save is refused, at most 256 models may be named, and keys beyond that ceiling or duplicating another spelling are dropped when the settings file is read
  narrowing: holds for the chat-completions route only, and for at most MaxPerModel (256) well-formed < org>/< name> keys folded onto the registry's canonical spelling; a malformed key is refused at save and a key beyond the ceiling or duplicating another spelling is dropped at load rather than honoured
  evidence: internal/gateway/gateway.go:460 — "if r.URL.Path == chatCompletionsPath && cfg.PerModel[model].MergeSystemMessages {"
  evidence: internal/config/config.go:500 — "if len(m) > MaxPerModel {"
  evidence: internal/config/config.go:512 — "const MaxPerModel = MaxModelSampling"
  evidence: internal/config/config.go:551 — "if len(kept) >= MaxPerModel {"
  evidence: internal/gateway/systemmerge_test.go:762 — "func TestPlainCompletionsAreNeverMerged(t *testing.T)"
- cond-2609061822389490 — survived: the buffered-body limit is untouched: the merge runs on the payload already read under the existing 32 MiB MaxBytesReader, and the delivered tree carries no change to that constant or a second read of the body
  evidence: internal/gateway/gateway.go:318 — "const maxRequestBody = 32 << 20"
  evidence: internal/gateway/gateway.go:349 — "raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))"
  evidence: internal/gateway/systemmerge.go:78 — "func mergeSystemMessagesInto(payload map[string]json.RawMessage) mergeOutcome {"
## Grounds

- pursued: we expect a shared Mac to serve several agents without their models evicting each other once the operator can pin, budget and grace, and once keyed clients can see what is warm; we are wrong if model swaps stay as frequent with those controls set as they were without them, measured by the statistics store
