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

<!-- abcd-review: OWED receipt=rcp-20d902524b1c -->
Fidelity review OWED (receipt rcp-20d902524b1c).

## Grounds

- pursued: we expect GropiusChat to stop offering models that cannot hold a conversation, and other clients to pick by the Hub's own tags without Gropius inventing a taxonomy; shown wrong if people keep changing the default rule because it hides models they wanted, or if too many downloads arrive untagged for the picker to be useful.
