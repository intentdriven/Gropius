---
id: itd-2609091129451578
slug: every-model-carries-a-category-and-a-client-picks-by-it-when
spec_id: spc-2609091236506400
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609061431463108]
severity: minor
impact: additive
promoted_from: iss-2609081743175520
origin: researcher-authored
production_mode: dictated-and-formatted
---

# Every model carries a category, and a client picks by it. When Alice browses models to download, each result says what kind of model it is: chat, coding, or other. Gropius records the category when the model is downloaded and publishes it in the models list, so a client can offer only the models that fit its job. GropiusChat offers only chat models; a coding harness Bob writes asks for coding models for implementation and for chat and coding models for reviews. Every model stays callable by name over the API whatever its category.

## Press Release

Alice opens the search tab and every result now says what kind of model it is,
in HuggingFace's own words: text generation, image and text to text, speech
recognition, and so on, or no tag when the Hub has none. She downloads a coder
model and a chat model. In GropiusChat only the models that can hold a
conversation appear in the picker, so she never sends a conversation to a model
that cannot answer one. Bob's coding harness asks the models list for the tags
it needs when it implements and for a wider set when it reviews. Any model,
whatever its tag, still answers a request that names it.

Gropius does not invent a taxonomy. The category is the Hub's pipeline tag and
tags, recorded when the model is downloaded and published as they are. What
counts as a chat model is a rule with a default (text generation or image and
text to text, tagged conversational): the server carries it as a setting the
operator can change, and GropiusChat ships the same default the user can change
in its own Settings.

## Why This Matters

Today every downloaded model is offered for chat, including models that cannot
chat, and the server does not know a model's kind after the download: the
browse step sees the Hub's tags and the registry forgets them. A picker that
offers an OCR model beside a chat model wastes the first message; a harness
that wants coding models has nothing to ask for. Publishing the Hub's own tags
gives every client the same fact without Gropius guessing.

## Mechanism

We expect HuggingFace's pipeline tag and tags, captured at download time and
stored in the registry, to be enough for a client to pick sensibly, because the
Hub is the one source every model here comes from and 87% of mlx-community
repositories carry a usable tag (sample of 500 on 2026-09-09); the chat rule
(text generation or image and text to text, plus conversational) is a server
setting with a default, and GropiusChat ships the same default the user can
change. Shown wrong if a meaningful share of downloaded models arrives with no
tag, or if the default rule hides models people want to chat with.

## Scope Conditions

- Models downloaded through Gropius from HuggingFace, where the Hub's metadata is reachable at download time. <!-- cond: cond-2609091236504319 -->
- The category is advisory to clients and never an API-side filter, so every model stays callable by name. <!-- cond: cond-2609091236502214 -->
- The chat rule's default is the server's and GropiusChat's, each changeable on its own surface. <!-- cond: cond-2609091236508587 -->
- A client that discards unknown fields sees nothing new. <!-- cond: cond-2609091236506898 -->
- The tag vocabulary is HuggingFace's, so it may change without a Gropius release. <!-- cond: cond-2609091236504129 -->

## Acceptance Criteria

- Given Alice searches for models to download, when results are listed, then each shows HuggingFace's pipeline tag, or "no tag" when the Hub reports none.
- Given a download completes, when the registry records the model, then the pipeline tag and tags are stored with it; a download the Hub did not tag stores none.
- Given a client requests the models list, when an entry is returned, then it carries the pipeline tag and tags as top-level fields under common names, omitted when absent, plus a `chat` flag derived from the server's chat rule.
- Given the server's chat rule is at its default, when a model is text-generation or image-text-to-text and tagged conversational, then its flag is true, otherwise false; the rule is a setting in config.json and the control panel, and a save never refuses a field the operator did not touch.
- Given GropiusChat's picker, when it lists models, then it offers those matching its own rule, shipped with the same default and changeable in its Settings; a model with no tags is not offered.
- Given an API caller, when it names a model outside the chat rule, then the request is served.
- Given the fields ship, when a reader opens the models-list reference page, then it documents them.

## Open Questions

- Resolved 2026-09-09 at interview: the category is the Hub's tag, not a Gropius vocabulary; "coding" was dropped because a design review of 500 mlx-community repositories found it not derivable (13 of 30 name-identified coder models carry a code tag). The earlier decision of the same day to publish a boolean chat capability from the chat template is superseded by the tag-derived rule.

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-20d902524b1c -->
Fidelity review — receipt rcp-20d902524b1c (verifier intent-auditor claude-opus-5[1m]).

Provenance: intent-auditor@claude-opus-5[1m] · rubric_hash sha256:895d36c8e2beb3b0f7fc9041d796619f49332f5d919f0301e63d1c45d6908a05 · prompt_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5
Input attestations: diff:f13ccc2^1..f13ccc2 (merge of feat/model-category-from-the-hub into integrate/issue-sweep-2026-09-09; verified at HEAD 0edbf01)@sha256:4e1e0d8fb9d467d2c8cdd940e714b79a7c08f8b17c8c4d58204604b3d5ceb662;

Acceptance rollup: MET 6 · MET_WITH_CONCERNS 1 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: the search tab renders a pipelineLabel for every result, returning the Hub's tag or the literal "no tag", and renderSearch is the caller; both the value and the wiring are pinned by tests
  evidence: internal/ui/static/app.js:330 — "function pipelineLabel(m) {"
  evidence: internal/ui/static/app.js:332 — "return tag ? escapeHtml(tag) : 'no tag';"
  evidence: internal/ui/static/app.js:371 — "const kind = pipelineLabel(m);"
  evidence: internal/ui/chatrule_test.go:15 — "func TestSearchResultShowsThePipelineTag(t *testing.T) {"
  evidence: internal/ui/chatrule_test.go:38 — "func TestSearchResultIsBuiltFromThePipelineLabel(t *testing.T) {"
- ac-2 — MET: the download path reads the Hub's repo info once the transfer completes and writes both words onto the ready registry record; a failed or untagged lookup records neither and the model still lands ready
  evidence: internal/app/app.go:1181 — "pipelineTag, tags := a.repoCategory(ctx, repoID)"
  evidence: internal/app/app.go:1189 — "PipelineTag: pipelineTag,"
  evidence: internal/app/app.go:1276 — "return "", nil"
  evidence: internal/registry/registry.go:73 — "PipelineTag string `json:"pipeline_tag,omitempty"`"
  evidence: internal/app/app_test.go:1430 — "func TestDownloadRecordsTheHubsCategory(t *testing.T) {"
  evidence: internal/app/app_test.go:1456 — "func TestADownloadWithoutACategoryIsStillReady(t *testing.T) {"
  evidence: internal/registry/category_test.go:52 — "func TestAnUntaggedModelCarriesNoCategory(t *testing.T) {"
  evidence: internal/hub/hub.go:278 — "func (c *Client) RepoInfo(ctx context.Context, repoID string) (Model, error) {"
- ac-3 — MET: handleListModels writes pipeline_tag and tags as top-level fields only when present and chat on every entry from the rule in force; the omission and the always-present flag are both asserted
  evidence: internal/gateway/gateway.go:425 — "entry["pipeline_tag"] = m.PipelineTag"
  evidence: internal/gateway/gateway.go:428 — "entry["tags"] = m.Tags"
  evidence: internal/gateway/gateway.go:430 — "entry["chat"] = chatRule.Matches(m.PipelineTag, m.Tags)"
  evidence: internal/gateway/category_test.go:18 — "func TestListModelsPublishesTheHubsCategory(t *testing.T) {"
  evidence: internal/gateway/category_test.go:50 — "func TestListModelsOmitsAnUnknownCategory(t *testing.T) {"
  evidence: internal/gateway/gateway_test.go:971 — ""context_length": true, "max_model_len": true, "chat": true}"
- ac-4 — MET: DefaultChatRule is exactly text-generation/image-text-to-text plus conversational and Matches decides the flag; the rule is a config.json key and a control-panel field, and the save decodes into a deep clone of the settings in force so an unnamed field survives, including a deliberately cleared rule
  evidence: internal/config/chatrule.go:125 — "func DefaultChatRule() ChatRule {"
  evidence: internal/config/chatrule.go:147 — "func (r ChatRule) Matches(pipelineTag string, tags []string) bool {"
  evidence: internal/config/config.go:614 — "ChatRule ChatRule `json:"chat_rule,omitzero"`"
  evidence: internal/ui/static/index.html:265 — "< legend>Which models can chat< /legend>"
  evidence: internal/gateway/control.go:1330 — "incoming := current.Clone()"
  evidence: internal/config/chatrule.go:72 — "func (r ChatRule) Clone() ChatRule {"
  evidence: internal/gateway/control_test.go:1024 — "func TestSavingSettingsWithoutNamingTheChatRuleKeepsIt(t *testing.T) {"
  evidence: internal/gateway/control_test.go:992 — "func TestSavingTheChatRuleTouchesNothingElse(t *testing.T) {"
  evidence: internal/config/chatrule_test.go:31 — "func TestChatRuleMatches(t *testing.T) {"
  evidence: internal/ui/chatrule_test.go:112 — "func TestThePanelNamesTheShippedRule(t *testing.T) {"
- ac-5 — MET_WITH_CONCERNS: the picker is derived by filtering the served list through the client's own rule, whose default is pinned to the server's and is editable in two Settings fields; the concern is the second half of the criterion, which is delivered indirectly and untested behaviourally
  evidence: client/GropiusChat/GropiusChat.swift:718 — "chatModels = list.data.filter { rule.offers($0) }.map(\.id).sorted()"
  evidence: client/GropiusChat/GropiusChat.swift:178 — "fileprivate func offers(_ m: ModelsResponse.Model) -> Bool {"
  evidence: client/GropiusChat/GropiusChat.swift:179 — "guard m.pipeline_tag != nil || m.tags != nil else { return m.chattable }"
  evidence: client/GropiusChat/GropiusChat.swift:500 — "@AppStorage("chatPipelineTags") var chatPipelineTags: String = "text-generation, image-text-to-text""
  evidence: client/GropiusChat/GropiusChat.swift:1703 — "TextField("text-generation, image-text-to-text", text: $model.chatPipelineTags)"
  evidence: internal/archtest/chat_client_load_state_test.go:232 — "func TestChatClientShipsTheServersOwnChatRule(t *testing.T) {"
  evidence: internal/gateway/category_test.go:72 — "if entry["chat"] != false {"
- ac-6 — MET: nothing on the completions path reads the category, and a model published with chat:false is posted a completion by name and relayed to the backend in the same test that asserts the flag
  evidence: internal/gateway/category_test.go:125 — "func TestAModelOutsideTheChatRuleIsStillServed(t *testing.T) {"
  evidence: internal/gateway/category_test.go:151 — "resp := post(t, srv, "/v1/chat/completions", map[string]any{"
  evidence: internal/gateway/gateway.go:422 — "The flag decides nothing about what is served."
- ac-7 — MET: the models-list reference page carries all three fields in its field table, an example payload, and a section on the category and the chat flag naming the rule, its default and where to change it
  evidence: docs/models-list.md:43 — "| `pipeline_tag` | What HuggingFace says the model does"
  evidence: docs/models-list.md:45 — "| `chat` | Whether the model counts as able to hold a conversation, under the rule this server runs. Always present."
  evidence: docs/models-list.md:73 — "## The chat flag"
  evidence: docs/models-list.md:26 — ""tags": ["mlx", "conversational"],"
  evidence: docs/chat-models.md:1 — "# Choose which models are offered for chat"

Gap audit:
- honoured:
  - Alice opens the search tab and every result now says what kind of model it is, in HuggingFace's own words, or no tag when the Hub has none
    evidence: internal/ui/static/app.js:332 — "return tag ? escapeHtml(tag) : 'no tag';"
  - the category is the Hub's pipeline tag and tags, recorded when the model is downloaded
    evidence: internal/app/app.go:1181 — "pipelineTag, tags := a.repoCategory(ctx, repoID)"
  - Bob's coding harness asks the models list for the tags it needs — the words are published to every client as top-level fields
    evidence: internal/gateway/gateway.go:425 — "entry["pipeline_tag"] = m.PipelineTag"
  - any model, whatever its tag, still answers a request that names it
    evidence: internal/gateway/category_test.go:125 — "func TestAModelOutsideTheChatRuleIsStillServed(t *testing.T) {"
  - what counts as a chat model is a rule with a default that the server carries as an operator setting, and GropiusChat ships the same default changeable in its own Settings
    evidence: internal/config/chatrule.go:125 — "func DefaultChatRule() ChatRule {"
    evidence: internal/archtest/chat_client_load_state_test.go:232 — "func TestChatClientShipsTheServersOwnChatRule(t *testing.T) {"
  - in GropiusChat only the models that can hold a conversation appear in the picker
    evidence: client/GropiusChat/GropiusChat.swift:718 — "chatModels = list.data.filter { rule.offers($0) }.map(\.id).sorted()"
- diverged:
  - the Hub's words are published as they are — delivered as "published as they are, if they are made of the characters a Hub tag is known to use": usableTag is an allow-list, so a legitimate but unusual Hub tag is dropped rather than republished, and the model is listed with one word fewer
    evidence: internal/registry/registry.go:145 — "func usableTag(tag string) string {"
    evidence: internal/registry/registry.go:164 — "if r >= utf8.RuneSelf || !hubTagRune(byte(r)) {"
    evidence: internal/registry/category_test.go:193 — "func TestOnlyHubShapedTagsAreKept(t *testing.T) {"
  - the intent's headline says each result names a category of "chat, coding, or other" — no such enum shipped; the delivery publishes the Hub's raw tags instead, on the reason recorded in the intent's own resolved Open Question
    evidence: .abcd/development/intents/shipped/itd-2609091129451578-every-model-carries-a-category-and-a-client-picks-by-it-when.md:60 — ""coding" was dropped because a design review of 500 mlx-community repositories found it not derivable"
    evidence: internal/gateway/gateway.go:428 — "entry["tags"] = m.Tags"
  - the client picks by the category — delivered as: it picks by the category when the server publishes one, and falls back to the server's chat flag (default true) when the entry carries neither field, so a pre-feature server still offers every model
    evidence: client/GropiusChat/GropiusChat.swift:179 — "guard m.pipeline_tag != nil || m.tags != nil else { return m.chattable }"
    evidence: client/GropiusChat/GropiusChat.swift:140 — "var chattable: Bool { chat ?? true }"
- missing:
  - "every model carries a category" is true only of models downloaded after this build: the category is captured at download time and never backfilled, and Rescan re-derives nothing from disk, so an existing install's models are all marked unable to chat and GropiusChat's picker is empty until each is downloaded again. Deliberate (pre-1.0, no migration) and disclosed in the panel, but the promise is not delivered for a model already on disk
    evidence: internal/registry/category_test.go:141 — "func TestRescanKeepsAStoredCategory(t *testing.T) {"
    evidence: internal/ui/static/index.html:273 — "A model downloaded before Gropius recorded these words, or while HuggingFace could not be reached, carries none and is marked as unable — download it again to give it the words."
  - no behavioural test covers the client half of the promise: the Swift picker rule and its Settings bindings are held only by regular-expression pins over the source in internal/archtest, so the rule's verdict itself is never executed anywhere in CI
    evidence: internal/archtest/chat_client_load_state_test.go:174 — "func TestChatClientOffersEveryModelAServerDoesNotRuleOut(t *testing.T) {"
    evidence: internal/archtest/chat_client_load_state_test.go:176 — "source := readRepoFile(t, root, filepath.Join("client", "GropiusChat", "GropiusChat.swift"))"

Scope-condition dispositions:
- cond-2609091236504319 — survived: the category is read from the Hub at download time and only then; the delivery stays inside the assumed range and degrades to no category when the metadata is unreachable rather than failing the download
  evidence: internal/app/app.go:1181 — "pipelineTag, tags := a.repoCategory(ctx, repoID)"
  evidence: internal/app/app.go:1274 — "a.Log.Info("the hub did not say what kind of model this is", "model", repoID, "err", err)"
  evidence: internal/app/app_test.go:1456 — "func TestADownloadWithoutACategoryIsStillReady(t *testing.T) {"
- cond-2609091236502214 — survived: the flag is written onto the listing and read nowhere on the completions path; a model the rule excludes is relayed and answered by name, asserted in the same test as its flag
  evidence: internal/gateway/category_test.go:125 — "func TestAModelOutsideTheChatRuleIsStillServed(t *testing.T) {"
  evidence: internal/gateway/gateway.go:430 — "entry["chat"] = chatRule.Matches(m.PipelineTag, m.Tags)"
- cond-2609091236508587 — survived: one default is declared in Go, resolved into the listing and into the panel's answer, and pinned to the client's two stored strings; each surface changes its own copy, the panel through Settings and the client through its Settings sheet
  evidence: internal/config/chatrule.go:125 — "func DefaultChatRule() ChatRule {"
  evidence: internal/gateway/control.go:714 — "c.ChatRule = c.EffectiveChatRule()"
  evidence: client/GropiusChat/GropiusChat.swift:1703 — "TextField("text-generation, image-text-to-text", text: $model.chatPipelineTags)"
  evidence: internal/archtest/chat_client_load_state_test.go:232 — "func TestChatClientShipsTheServersOwnChatRule(t *testing.T) {"
- cond-2609091236506898 — survived: the three fields are added to the models-list entry and nothing existing is renamed, removed or re-typed; the exact-key-set assertion shows the previous field set intact beside them
  evidence: internal/gateway/gateway.go:425 — "entry["pipeline_tag"] = m.PipelineTag"
  evidence: internal/gateway/gateway_test.go:971 — ""context_length": true, "max_model_len": true, "chat": true}"
- cond-2609091236504129 — narrowed: the vocabulary is still HuggingFace's and no Gropius enum was introduced, but the registry now accepts only words drawn from a fixed ASCII character set read off the tags the Hub is known to use, so a future Hub tag outside that set is silently dropped and no rule can name it
  narrowing: the vocabulary may change freely only within [A-Za-z0-9] plus the five separators - _ . : / and at most 128 bytes and 64 tags per model; a legitimate Hub word outside that shape does not reach the registry, the listing, the panel or the client, and would need a Gropius release to be admitted
  evidence: internal/registry/registry.go:158 — "hubTagRune(b byte) bool"
  evidence: internal/registry/registry.go:145 — "func usableTag(tag string) string {"
  evidence: internal/registry/registry.go:87 — "MaxTags = 64"
  evidence: internal/registry/category_test.go:193 — "func TestOnlyHubShapedTagsAreKept(t *testing.T) {"
## Grounds

- pursued: we expect GropiusChat to stop offering models that cannot hold a conversation, and other clients to pick by the Hub's own tags without Gropius inventing a taxonomy; shown wrong if people keep changing the default rule because it hides models they wanted, or if too many downloads arrive untagged for the picker to be useful.
