# Ideate verdict — brave-web-search

**Verdict: reframed.** Recorded on 2026-09-10 by abcd's idea-admission protocol —
primary-source research, a grill against the existing record, and an
independent adversarial review. This record exists so the idea is not
re-litigated: it stands whether the idea lived or died.

## The idea

Configure a Brave API key at the server level to include (optional) web searches via GropiusLLM served models.

## Leg 1 — Primary-source research

Every load-bearing claim checked against its primary source, never a
secondary citation.

| Claim | Primary source | Finding |
|---|---|---|
| Brave Search API takes a query with one subscription-token header and returns results on a per-key plan; end-user IP is not forwarded | https://api-dashboard.search.brave.com/api-reference/web/search/get | verified |
| Brave retains submitted queries for up to 90 days; zero data retention is enterprise-only | https://api-dashboard.search.brave.com/documentation/resources/privacy-notice | verified |
| The key holder is Brave's Customer and must bind every end user by written agreement, may not share the key, may not store results beyond transient use, and must display Brave attribution conspicuously | https://api-dashboard.search.brave.com/documentation/resources/terms-of-service | verified |
| Brave's terms forbid using results to evaluate, benchmark or otherwise improve AI models, while Brave markets an LLM-grounding product; the clause's reach over inference-time grounding is ambiguous | https://api-dashboard.search.brave.com/documentation/services/llm-context | unverifiable |
| mlx-lm 0.31.3 implements OpenAI tool calling: it renders tools into the chat template and returns tool_calls with finish_reason tool_calls, in streaming and non-streaming answers | https://raw.githubusercontent.com/ml-explore/mlx-lm/v0.31.3/mlx_lm/server.py | verified |
| mlx-lm 0.31.3 implements tool_choice and executes tools server-side | https://raw.githubusercontent.com/ml-explore/mlx-lm/v0.31.3/mlx_lm/server.py | falsified |
| mlx-lm detects tool calls by string-matching the chat template; Nemotron is unsupported and a Mistral-family model returns an empty message on the pinned version | https://github.com/ml-explore/mlx-lm/issues/1307 | verified |
| OpenAI's built-in web search is a Responses API tool with citation items that do not exist in Chat Completions; function tools are executed by the client | https://developers.openai.com/api/docs/guides/tools-web-search | verified |
| No open-weight OpenAI-compatible server executes tools in-process | https://raw.githubusercontent.com/ggml-org/llama.cpp/master/tools/server/README.md | falsified |
| Anthropic runs web search server-side at a per-search price and returns citations | https://platform.claude.com/docs/en/docs/agents-and-tools/tool-use/web-search-tool | verified |
| Ollama and LM Studio leave tool execution to the client on their OpenAI-compatible endpoints | https://docs.ollama.com/capabilities/tool-calling | verified |

## Leg 2 — Record grill

Does the brief, an intent, an ADR, or a principle already cover,
contradict, or supersede this idea? Every hit is cited by record id, and
every id resolved in this repository when the verdict was recorded.

| Record | Relation | Note |
|---|---|---|
| adr-2609061503319212 | contradicted | The outbound connections are exactly those that fetch models and provision the runtime; a release check was refused on 2026-09-08 as a third outbound host whatever it carries. |
| adr-2609061610102325 | contradicted | The gateway may read and rewrite prompt content for exactly one purpose; any further reading is a superseding decision. A search query is derived from prompt content and leaves the machine. |
| itd-2609091707499248 | contradicted | Held 2026-09-09: writing generated content is a right the boundary record does not grant; a tool loop injects results into the prompt and composes the answer. |
| iss-2609062308320686 | covered | The prompt-content scan arms both sides; a search path is a new reader on both lists. |
| itd-2609061521134968 | superseded | The one prior proposal to send derived data off-machine was dropped rather than amend the no-telemetry record. |
| itd-2609061441228998 | covered | Entitlement keys on the install's API key, never on client-supplied state; the shipping default is keyless, so an unkeyed LAN client would spend the operator's quota. |
| adr-2609081118587999 | covered | Third-party state may inform what the app reports and never what it enforces. |
| iss-2609061546490357 | covered | The operational log keeps method, path, status and duration; query text is strictly more than an address. |
| adr-2609061610107154 | covered | The one sanctioned local record store never holds prompt text; search queries cannot land there. |
| iss-2609062026370824 | covered | A second stored secret inherits the bounding rule and the anti-wedge rule. |
| iss-10 | covered | No token crosses accounts under the shared cache; a Brave key is a second third-party secret under the same constraint. |
| spc-2609061822370424 | covered | The identity pitch that anything talking to ChatGPT can talk to your Mac is a gated surface. |
| itd-2609081015545349 | covered | Honest marking: the app says the minimum it can honestly say. |

## Leg 3 — Adversarial review

Conducted fresh-context and off-policy by an evaluator that did not carry
out the research and received the idea as an artefact of unknown
authorship — the evaluator-outside-the-loop principle applied to ideas.

- **partial** — Key custody: Brave forbids sharing the key and the project forbids a secret crossing accounts under the shared cache, so a server-level key is per-operator or a breach
- **fatal** — Brave's terms land on a home operator: bind every LAN user by written agreement, display attribution a headless API cannot render, never cache results
- **fatal** — Brave retains every query 90 days by default; the server would cause third-party retention of prompt-derived text it promises never to keep
- **partial** — The outbound-host rule refused a release check this week as a third host whatever it carries; search carries the most sensitive payload the app handles
- **fatal** — A server-side tool loop parses message content, holds a multi-turn loop and writes generated content: a superseding decision on two axes
- **partial** — No tool_choice on the pinned server, Mistral-family returns empty messages, Nemotron unsupported: the capability degrades silently per model
- **partial** — Fetched pages enter a server-held loop with no provenance vehicle in Chat Completions: prompt injection on behalf of LAN clients
- **partial** — Once keyed, every LAN client can trigger metered spend with no per-client gate the record permits
- **survived** — The identity claim survives but search parity with OpenAI cannot be marketed: OpenAI's search is Responses-only
- **fatal** — Every peer runtime has the client execute tools; clients that want search already own the loop and the key; an MCP sidecar is the smallest diff and touches no record

## Rejected alternatives

- **A server-side agent loop in the gateway with a server-held Brave key (the only shape that works with stock SDKs)** — Dies on Brave's terms landing on a home operator, on 90-day third-party retention of prompt-derived text, and on the relay invariant: it needs a superseding decision on reading and writing prompt content and a new outbound host.
- **A server-declared web_search tool the client executes** — The executing client needs the key, which Brave forbids sharing and the project forbids crossing accounts; proxying the search collapses it back into the agent loop.
- **The reframing that survives: a separate, opt-in MCP search sidecar with the operator's own Brave key, outside the gateway, for MCP-capable clients only** — Not rejected; it keeps the relay, touches no ratified record, and answers the operator who wants search from a capable client, at the cost of doing nothing for a plain SDK client.

## What follows

The idea as posed does not survive, but the reframing recorded above
does. Any graduation to a draft intent carries the reframing, not the
original wording.
