---
schema_version: 1
id: "iss-2609081743175520"
slug: "gropiuschat-offers-every-downloaded-model-for-chat-including"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "maintainer walkthrough of the chat client on a standard account"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/registry/registry.go"
resolution: "Every model now carries HuggingFace's own pipeline tag and tags, recorded at download time and published on the models list beside a chat flag from an operator-settable rule, and GropiusChat's picker runs its own copy of that rule."
impact: additive
resolved_by:
  intent: "itd-2609091129451578"
---

GropiusChat offers every downloaded model for chat, including models that cannot usefully chat — an OCR model or a base model has no business in the picker, though it must stay reachable over the API. The server does not currently know a downloaded model's kind: internal/hub fetches HuggingFace's pipeline_tag and tags when browsing (hub.go:63) but internal/registry.Model does not persist them, so nothing survives the download. Two signals are available. The functional one is the chat template: a model whose tokenizer_config.json declares no chat_template cannot serve /v1/chat/completions meaningfully, it is read from files already on disk, and it is the same shape as ContextLength, which registry.Model already reads from a model's own config. The metadata one is pipeline_tag, which would name an OCR or speech model outright but is remote, absent for some repos, and would have to be persisted at download time. Neither is a filter on the API: the wanted behaviour is a capability published in the models list and honoured by the client's picker, leaving every model callable by an API client that asks for it by name. Note the limit of the functional signal — a coder model with a chat template is chattable and would not be filtered, which is correct: it separates cannot-chat from would-not-choose.

## Grounds

- pursued: we expect a chat picker to stop offering models that cannot hold a conversation while every model stays callable by name over the API; shown wrong if people keep clearing the rule because it hides models they wanted, or if too many downloads arrive untagged for the picker to be useful.
