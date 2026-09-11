# Ideate verdict — session-management

**Verdict: killed.** Recorded on 2026-09-09 by abcd's idea-admission protocol —
primary-source research, a grill against the existing record, and an
independent adversarial review. This record exists so the idea is not
re-litigated: it stands whether the idea lived or died.

## The idea

full session management to customise individual sessions and to keep state

## Leg 1 — Primary-source research

Every load-bearing claim checked against its primary source, never a
secondary citation.

| Claim | Primary source | Finding |
|---|---|---|
| Stock OpenAI SDKs tolerate additive response fields and pass extra request fields and default headers through, so a session header is a zero-friction carrier | https://raw.githubusercontent.com/openai/openai-python/main/src/openai/_models.py | verified |
| The OpenAI API has a server-side conversation primitive (previous_response_id, store, Conversations CRUD) | https://raw.githubusercontent.com/openai/openai-python/main/src/openai/types/responses/response_create_params.py | verified |
| OpenAI's retention and per-key scoping of stored responses is documented | https://platform.openai.com/docs/api-reference/responses | unverifiable |
| Anthropic has no server-side session at all | https://platform.claude.com/docs/en/api/beta/sessions/create | falsified |
| mlx-lm's server keeps one prompt cache per model and a later request can name it | https://raw.githubusercontent.com/ml-explore/mlx-lm/v0.31.3/mlx_lm/server.py | falsified |
| mlx-lm 0.31.3 keeps many concurrent prompt caches keyed by model and token prefix, LRU-evicted under a count and a byte cap, retrieved by nearest prefix, with no way for a request to name one | https://raw.githubusercontent.com/ml-explore/mlx-lm/v0.31.3/mlx_lm/models/cache.py | verified |
| Keeping a KV cache per session multiplies resident memory by a published factor | https://raw.githubusercontent.com/ml-explore/mlx-lm/v0.31.3/mlx_lm/models/cache.py | unverifiable |
| vLLM exposes no client session id; prefix caching is automatic and keyed on block hash | https://raw.githubusercontent.com/vllm-project/vllm/main/vllm/config/cache.py | verified |
| SGLang exposes no client session id | https://raw.githubusercontent.com/sgl-project/sglang/main/python/sglang/srt/entrypoints/http_server.py | falsified |
| MCP defines a server-assigned session id the client must echo on every request | https://modelcontextprotocol.io/specification/2025-06-18/basic/transports | verified |
| A stateless HTTP server cannot know a session without a client-carried token | https://www.rfc-editor.org/rfc/rfc9110.html | verified |

## Leg 2 — Record grill

Does the brief, an intent, an ADR, or a principle already cover,
contradict, or supersede this idea? Every hit is cited by record id, and
every id resolved in this repository when the verdict was recorded.

| Record | Relation | Note |
|---|---|---|
| itd-2609091707499248 | contradicted | Held 2026-09-09: no candidate session identifier works for a stateless OpenAI-compatible server; an already-seen memory is unbounded state keyed on client strings inside a trust boundary. |
| itd-2609061521134968 | superseded | The one prior session-identity intent was retired with 'What is a session?' unanswered, because the API has no session and Gropius records no client address. |
| adr-2609061610102325 | contradicted | 'We will never log, retain, count, index or otherwise keep any prompt content'; server-kept history needs a superseding record. |
| adr-2609061503319212 | contradicted | Local telemetry never records prompt text or completions, and is for the operator of that Mac; a client-readable session store crosses both. |
| itd-2609061429508050 | contradicted | Sampling precedence is settled per request; the model server takes defaults once at launch, so per-session sampling cannot be a launch flag. |
| itd-2609061431481936 | contradicted | The served window is per model and static and the memory charge is computed from it; a per-session window breaks admission. |
| iss-2609062213413447 | covered | Per-model settings are one map with one ceiling; a third keying dimension is barred. |
| itd-2609061441241254 | contradicted | The model pin is the operator's; a client-set pin lets a network client hold this Mac's memory. |
| spc-2609061822385499 | contradicted | The statistics endpoint returns aggregates only; no record, header or address reaches the browser. |
| spc-2609061822383782 | contradicted | No prompt, completion, key or client address is ever recorded; a session token is a client-supplied identifier of that kind. |
| adr-2609090716413337 | covered | The store's record kinds are fixed; a session record needs a new ADR. |
| iss-3 | covered | Cache retention and stacking are measured as an over-admission hazard (108 GB with swap); keeping caches per session multiplies an unfixed bug. |

## Leg 3 — Adversarial review

Conducted fresh-context and off-policy by an evaluator that did not carry
out the research and received the idea as an artefact of unknown
authorship — the evaluator-outside-the-loop principle applied to ideas.

- **fatal** — A server-minted token is carried, never authenticated: one shared key, keyless loopback clients, proxies; any LAN client rides another's session
- **partial** — A session table is unbounded state keyed on client strings inside a trust boundary; count and TTL bounds answer it
- **fatal** — Kept conversation history is retained prompt content, forbidden by two ratified records with no mitigation
- **fatal** — A stored system prompt is retained prompt content; per request it costs the client one SDK default
- **fatal** — Per-session sampling is a third precedence layer and a third keying dimension, and the SDK's default parameters already give per-conversation sampling with no server code
- **fatal** — A client-chosen served window breaks the admission arithmetic computed from the static per-model window
- **fatal** — A client-set model pin lets a network client hold the machine's memory
- **fatal** — mlx-lm cannot name a cache; retrieval is by nearest token prefix, so a session id has nothing to point at
- **fatal** — Cross-request cache reuse for N concurrent conversations already happens, bounded and LRU-evicted, with no identity; the proposal adds nothing observable
- **fatal** — Exempting a session's cache from eviction is a client holding KV memory against the byte cap, and the KV cache is prompt content in another encoding
- **partial** — Per-cache memory cost is real but the byte cap already bounds it without pinning
- **partial** — Stock SDKs send developer-set headers but do not echo a header the server returned, so a stock client must be modified the moment identity exists
- **fatal** — A token is a cross-request linkable identifier the server deliberately does not record today; any stored state timestamped by token is a per-client trace
- **fatal** — On a shared Mac loopback and LAN share one key, so sessions leak between accounts and a per-session window or pin lets one user starve the rest
- **fatal** — Everything the client wants is per request via the SDK or already done by mlx-lm; the content-free affinity token has no consumer

## Rejected alternatives

- **An opaque, content-free affinity token carried in a header, MCP-style** — Nothing downstream accepts it: mlx-lm has no nameable cache to route to, and its automatic prefix cache already serves N concurrent conversations; revisit only if a model server exposes a cache handle.
- **Per-conversation customisation through the client's own SDK defaults and per-request fields** — Not rejected: this is what exists today and what kills the proposal on necessity; sampling, system prompt and max_tokens are per request by the shipped precedence rule.
- **Server-kept conversation history behind a superseding decision record** — It would reverse two ratified never-retain records and, on a shared Mac with one key, expose one account's conversation to another; the recording mode that would need the same reversal is held on the same ground.

## What follows

The idea is closed. Before proposing it again, read this record: the
findings above are why it did not survive, and re-proposing it costs
nothing only if something above has changed.
