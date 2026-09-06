# Local on-device telemetry for a local LLM server — state of the art, 2026-09-06

Research question: what do comparable local LLM servers record and show to
their own operator, on the machine or the LAN, and what should Gropius record
and show? Constraint fixed by the maintainer before the pass finished, and
recorded as `adr-2609061503319212`: no public telemetry ever; local only; a
strict opt-in that is off by default. Produced by an independent research
pass (evaluator outside the loop); the maintainer's decision is the gate.
Evidence tiers are marked: [CONSENSUS], [EVIDENCE — primary], [CONTESTED],
[ANECDOTE]. The companion note `2026-09-06-anonymous-telemetry-sota` covers
the public-telemetry question and why it was closed.

## Bottom line

Every comparable local server converges on the same small operator-facing
set: prompt and completion token counts, time to first token, generation
tokens per second, what is loaded and how much memory it holds, and a queue or
in-flight count. The heavyweight stacks (vLLM's thirty-odd Prometheus series,
OpenTelemetry tracing) exist because those servers batch hundreds of
concurrent requests across GPUs; none of that transfers to one Mac running one
mlx-lm subprocess per model.

For Gropius the state of the art is: per-request timing and token counts
measured by the gateway itself, which needs no backend cooperation beyond one
small request rewrite; kept in a bounded in-memory ring; shown in the existing
control panel over the event stream it already has; behind a Settings toggle
rather than a first-run prompt. mlx-lm gives almost nothing for free and, at
DEBUG log level, writes full request bodies and responses to the per-model
log, so a debug-logging switch must stay separate from the statistics switch.

## Ranked recommendations

1. **Measure TTFT, duration, token counts and derived tokens per second in
   the gateway, using the streaming usage option.** The gateway already
   parses every server-sent-events data line to rewrite the model field
   (`internal/gateway/gateway.go`, the streaming relay), so reading the
   `usage` object from the final chunk is nearly free. mlx-lm only emits
   streaming usage when the request carries
   `stream_options.include_usage: true`; the gateway can inject that into the
   upstream request, which it already re-marshals, and drop the extra chunk
   if the client did not ask for it. Non-streaming responses always carry
   `usage`, including cached prompt tokens. This is what the community
   metrics proxy for Ollama does: forward every request and instrument the
   response stream. [EVIDENCE — mlx-lm server source; OpenAI streaming
   semantics] [CONSENSUS across that proxy, LM Studio's stats, llama.cpp's
   timings]
2. **Record queue wait, load time and eviction events in the pool, not the
   gateway.** The pool's acquire path already spans semaphore wait and
   launch, and there is a single eviction site (`internal/runtime/pool.go`).
   Timestamping those gives queue-wait and load-time histograms plus an
   eviction counter with zero backend involvement. vLLM treats queue time
   and TTFT as first-class; Ollama's load duration is the only load-time
   figure any local server exposes. [EVIDENCE — vLLM metrics design doc;
   Ollama API doc]
3. **Store in a bounded in-memory ring plus per-minute rollups; do not add
   SQLite.** A ring of the last thousand request records at about 150 bytes
   each is about 150 KB; 24 hours of per-minute rollups is 1,440 rows.
   `go.mod` has three direct dependencies; SQLite would be a fourth needing
   sign-off, with cgo or a large pure-Go port. Syncthing moved to SQLite for
   a file index of millions of rows and had to drop platforms over
   cross-compilation, a different scale problem. [EVIDENCE — Prometheus
   storage docs; Netdata retention docs; Syncthing 2.0 pull request]
4. **Present it in the control panel, not the menu bar.** Apple's Human
   Interface Guidelines: use a symbol for a menu bar extra, display a menu
   rather than a popover on click, and let people decide whether it is in
   the bar. Nothing in the HIG addresses live-updating text in the bar.
   Extend the existing state snapshot (`internal/gateway/control.go`) so the
   event stream carries the rollup and the last N requests; the UI already
   subscribes. [EVIDENCE — HIG, the menu bar]
5. **Make the opt-in a Settings toggle that names exactly what is recorded
   and where; keep a separate, session-scoped debug-logging action for the
   mlx-lm DEBUG level.** mlx-lm at DEBUG logs the full incoming request body
   and the full outgoing response, meaning prompts and completions land in
   the per-model log. Gropius launches with INFO (`internal/runtime/launcher.go`),
   which is correct today. [EVIDENCE — mlx-lm server source]
6. **Borrow OpenTelemetry GenAI metric names and bucket boundaries for what
   is emitted, without adopting the SDK.** Time to first token, time per
   output token and request duration are histograms in seconds with
   published buckets (TTFT 0.001 to 10 s; per-token 0.01 to 2.5 s);
   attributes carry model name and error type only, never prompt content.
   Stability is "Development", so borrow the vocabulary, not the dependency.
   [EVIDENCE — semantic-conventions repository]
7. **Optional, opt-in, loopback-only Prometheus text endpoint, hand-written.**
   The exposition format is a few formatted lines for a dozen series; no
   client library needed. llama.cpp (off by default), Syncthing (behind its
   GUI auth), Tailscale (web exposure opt-in since v1.78) and Caddy (HTTP
   metrics off because they "reduce performance on really busy servers") all
   do this. [CONSENSUS]

## Comparison: what each server exposes to its operator

| Server | Metrics | Surface | Per-request timings | Retention | Prompt logging default |
| --- | --- | --- | --- | --- | --- |
| llama.cpp server | Prompt and predicted token totals and seconds, requests processing and deferred, KV-cache usage ratio, decode counters, speculative-decode counters | `/metrics` (off by default); `/slots` (on by default); `/props` | A `timings` object in every response with prompt and predicted counts, milliseconds and per-second rates, plus cached count; per-token timings when streaming | Counters since start; no history | Not logged unless a log-prompts directory is set; but `/slots` returns prompt text, and a March 2025 study found 117 of about 400 internet-exposed servers leaking prompts through it |
| Ollama | None built in; a metrics-endpoint request has been open since 2024; the OpenTelemetry tracing issue closed without a merged change found | `ollama ps` (name, size, processor, context, until); the ps API (size, VRAM size, expiry); a server log file | In every generate and chat response: total, load, prompt-eval and eval durations and counts in nanoseconds; the OpenAI shim honours the streaming usage option | None | Only with the debug env var, which also dumps every prompt; an open issue calls that coupling an information-security hazard |
| vLLM | About thirty series: TTFT, inter-token latency, end-to-end latency, queue time, prefill and decode time, running and waiting requests, KV-cache usage, prefix-cache hits, token histograms | `/metrics` on by default; traces via an OTLP endpoint flag; a shipped Grafana dashboard; a stats log line every few seconds | Only via histograms | Whatever Prometheus keeps | Request logging off by default (flag inverted in v0.10.1); prompt text only at DEBUG |
| LM Studio | Per-message tokens per second, TTFT, token count; context-fill percentage under the input box | Chat UI; a REST stats object; CLI log stream and server status | Yes, per response | Dated log files in the user cache directory | Two separate toggles, verbose logging and log prompts and responses |
| Jan | None beyond logs | Server logs panel; an app log file | Not documented | Log file | Verbose server logs on by default and include requests and responses: an anti-pattern for Gropius |
| text-generation-webui | One console line per generation: seconds, tokens per second, tokens, context, seed | stdout | Yes | None | Not by default |
| LocalAI | OpenTelemetry SDK with a Prometheus exporter; an API-call counter by method and path; a community Grafana dashboard | `/metrics` | No | Prometheus | Unverified |
| Open WebUI (client) | Per-message popover with response and prompt tokens per second and durations | Chat UI | Derived from Ollama's fields; wrong by roughly ten times for non-Ollama backends, open since December 2024 | Database | n/a |
| mlx-lm server (main branch) | Nothing: no timings, no rate, no memory in responses; only `usage` with cached tokens | `/health`; `/v1/models` | No | Per-process log | INFO: prompt-processing progress, prompt-cache size, Python's default access-log lines. DEBUG: full request body, full response, per-token text |

Caveat: the mlx-lm analysis is of the main branch, not the 0.31.3 tag Gropius
pins. The usage behaviour and DEBUG body logging are long-standing, but
verify against 0.31.3 before relying on cached-token counts or the batching
model described on main.

## Metric definitions and who can measure them

Definitions in practice, on which vLLM, the OpenTelemetry GenAI conventions
and LM Studio agree:

- **Time to first token:** request receipt to first content token; includes
  queue wait and prefill.
- **Time per output token, generation tokens per second:** duration minus
  TTFT, divided by completion tokens minus one. Reporting tokens per second
  as eval count over eval duration is Ollama's formula.
- **Prompt tokens per second:** prompt tokens over prefill time. Only the
  backend knows prefill time precisely; a proxy approximates it as TTFT minus
  queue wait, and must subtract cached tokens or the figure inflates on
  cache hits.
- **Context fill:** prompt plus completion tokens over the model's context
  length. This needs the architectural context length that
  `itd-2609061431463108` proposes to read from the model's config.
- **KV-cache occupancy:** llama.cpp and vLLM expose a ratio; mlx-lm exposes
  nothing comparable and only logs a prompt-cache size at INFO.

| The gateway or pool can measure alone | Needs the backend |
| --- | --- |
| Request count by model and status class; queue wait; load time; TTFT; total duration; prompt and completion tokens via `usage`; derived generation rate; approximate prompt rate; cached tokens; evictions; in-flight per model; client-cancel count; upstream-failure count; bytes streamed | Exact prefill time; KV-cache or prompt-cache occupancy; per-token latency distribution; batch size; process RSS or peak Metal memory; sampling settings in effect |

## Proposed Gropius metric set

**Gateway and pool can measure alone (recommend):**

- Requests total by model and status class, where the class is one of ok,
  client error, busy, launch failed, upstream failed, cancelled. This
  mirrors vLLM's success counter by finish reason and the gateway's existing
  error branches.
- Queue wait seconds (histogram, from acquire entry to upstream ready,
  excluding load) and model load seconds by model.
- TTFT seconds and request duration seconds by model, on the OpenTelemetry
  buckets.
- Prompt, completion and cached-prompt token totals by model, from `usage`.
- Derived per request: generation tokens per second, shown rather than
  stored separately.
- Evictions total by model; gauges for resident models, resident bytes
  (already computed as disk size times 1.2) and in-flight per model (already
  in the resident snapshot).
- Context fill ratio by model, once the architectural context length is
  read. That is a new file read in a trust-boundary package.

**Needs the mlx-lm backend (defer; main gives none of it):**

- Prefill time, per-token latency, prompt-cache occupancy, batch size, peak
  Metal memory. The only route is parsing mlx-lm's INFO log lines, which is
  brittle. Process RSS could instead be sampled from the OS per process id,
  which is how Ollama's memory figures work in spirit; a nice-to-have.

**Not worth it:**

- Go runtime metrics beyond a single RSS figure; the interesting memory is
  in the Python child.
- OpenTelemetry traces or spans: single hop, single process.
- Per-token inter-token-latency histograms: the gateway sees event lines,
  not tokens, and mlx-lm chunks are not one token each.
- Speculative-decoding, LoRA and prefix-cache counters: not applicable to
  Gropius's launch flags.

## Storage and presentation

**Recommended:** an in-memory ring of the last thousand requests, content
free, plus 24 hours of per-minute rollups, both in the state snapshot and
pushed over the event stream. Optional persistence when the operator opts
in: append-only JSON Lines under the existing logs directory, opened with the
same create, write-only, no-follow and owner-only discipline the launcher
already uses, self-rotated at a fixed byte cap (for example two files of
5 MB) rather than pulling in a rotation library. Retention is size-capped,
not time-capped, so growth is bounded regardless of traffic; this is the
Prometheus size-retention and Netdata per-tier-cap pattern scaled down.
[CONSENSUS on size caps for desktop daemons; ANECDOTE on the specific
numbers]

**Control panel:** per-model cards (resident, budgeted bytes, in-flight,
last used, last request's rate and TTFT, evictions today); one request-log
table with time, model, status class, prompt and completion tokens, TTFT,
rate and duration, with no message text and no client address; two
sparklines (requests per minute, generation rate) from the rollups. LM Studio
and Open WebUI show per-message stats inline; Gropius has no chat surface,
so the request table is the equivalent.

**Menu bar:** symbol only; menu items for "N models loaded" and a last-request
line with rate and TTFT; "Open control panel". No live-updating text in the
bar.

**Do-least option:** three fields added to the existing state snapshot:
requests total, last tokens per second, last TTFT. No ring, no persistence,
and arguably no toggle, because nothing is stored beyond process memory and
the figures contain no content. This alone matches what text-generation-webui
and LM Studio show, which is what operators actually look at.

## The local opt-in: presentation, persistence, and what "off" means

How the comparables separate always-on operational logging from opt-in
collection:

- llama.cpp: three independent switches: metrics (off), slots (on, and it
  leaks prompt text), log-prompts directory (off, "only used for
  debugging"). [EVIDENCE]
- vLLM: metrics and the periodic stats line are on; request logging is off,
  with the flag inverted in v0.10.1 for clearer semantics; prompt text only
  at DEBUG even then. [EVIDENCE]
- Ollama: a single debug knob couples "tell me about truncation" with "log
  every prompt"; an issue open since February 2025 argues this is a security
  hazard. This is the negative example. [EVIDENCE]
- LM Studio: two distinct toggles, verbose logging and log prompts and
  responses. [CONSENSUS from docs and bug tracker; the default state of the
  second is unverified]
- Grafana Alloy: live debugging is disabled and must be explicitly enabled in
  config, with a TLS warning. [EVIDENCE]
- Apple unified logging: dynamic strings are private unless marked public;
  private by default at the API level. [EVIDENCE — practitioner write-ups]

Recommendation for Gropius under the ADR:

- **Presentation:** a Settings toggle, "Record request statistics on this
  Mac", persisted through the existing settings path into config.json,
  default off. Under it, a fixed list of what is recorded (model name,
  status, token counts, timings) and the file path pattern, plus a Clear
  button. Not a first-run prompt: the HIG's advice for the analogous
  menu-bar decision is to let people decide, typically in the settings
  window, with an optional mention during setup. A first-run modal is the
  pattern for remote-telemetry consent, which this is not. [CONSENSUS by
  analogy; no direct evidence for local-only opt-in UX was found]
- **Three states, not two:** off, meaning nothing beyond today's operational
  logs and the live gauges already in the state snapshot; statistics on,
  meaning the content-free ring and rollups in memory, optionally persisted
  as JSON Lines; and a separate "debug logging for this model until restart"
  action that relaunches mlx-lm at DEBUG, labelled in plain words that it
  writes prompts and completions to the per-model log, with the existing
  truncate-on-restart behaviour clearing it. Never fold the third into the
  second.
- **What "off" still records, verified against the code:** the gateway logs
  two error lines, both with model name and error and no content; mlx-lm at
  INFO writes startup lines, prompt-processing progress, prompt-cache sizes,
  and Python's default access-log lines. Because the gateway proxies, the
  address in those lines is always loopback, so no LAN client address
  reaches the per-model log. Files are owner-only and truncated on restart,
  so they are bounded and private under the shared-cache path too.
- **Whether the do-least counters count as telemetry:** they contain no
  content, leave no trace on disk, and match what the ps command and the
  state snapshot already show. Treat them as operational state, not
  telemetry, and say so in the docs. Flagged for the maintainer's decision.

## Privacy of local logs

Never written by default, even locally: prompt text, completions, bearer
tokens (the gateway already strips the client token before proxying), and
absolute paths in anything the UI displays (the launch-error redaction is the
pattern). Acceptable behind the explicit debug action only: mlx-lm DEBUG
output. File permissions: owner-only plus no-follow for any new statistics
file; in shared-cache mode each account's statistics belong to that account,
so never in a group-writable directory.

## The counter-position

The evidence for exposing very little is strong. Ollama, the most widely used
local server, has shipped for over two years with no metrics endpoint and the
request still open. Tailscale only added client metrics in late 2024. Caddy
documents that metrics reduce performance on busy servers. Open WebUI's
per-response stats have been wrong by an order of magnitude for non-Ollama
backends since December 2024 and remain open, and LM Studio's log toggles have
had multiple "does not work" bugs. Wrong or half-maintained statistics cost
trust; for a solo maintainer, every series is a test to keep green. Against
that, every UI operators actually use shows tokens per second and TTFT per
response, because that is the number people compare models and quantisations
by. The honest middle is the do-least option plus recommendation 1, with the
ring and persistence behind the toggle. [CONTESTED: "observability for solo
developers" pieces argue for full stacks; they assume remote backends the
ADR excludes]

## Not worth adopting

- The OpenTelemetry SDK or OTLP: designed for shipping to a collector; the
  Go SDK's dependency tree dwarfs Gropius's module; GenAI conventions are
  still at Development stability. Borrow the names only.
- The Prometheus Go client for a dozen series: hand-written exposition is
  under fifty lines and adds no dependency.
- SQLite for metrics: dependency and cgo cost for kilobytes of data.
- Unified logging as the sink: needs cgo; macOS redacts dynamic strings by
  default, which would hide model names from the operator; retention is
  system-controlled. MetricKit delivers daily app-level power and CPU
  payloads, not per-request server metrics.
- A slots-style live endpoint returning in-progress requests: llama.cpp's is
  the one that leaks prompts.
- Jan-style verbose request and response logging on by default.
- Third-party exporter sidecars: they solve a problem Gropius does not have,
  since the gateway is already the proxy.

## Could not verify

- The closure reason for Ollama's OpenTelemetry tracing issue and whether any
  server-side code landed since.
- LocalAI's exact configuration for its metrics endpoint; its docs page
  returned a 404.
- The default state of LM Studio's prompts-and-responses logging toggle.
- The exact llama.cpp README sentence advising against slots in production;
  the study quotes it, the README fetch did not surface it.
- Any HIG guidance on frequently updating text in menu bar extras: none
  exists in the current HIG.
- mlx-lm 0.31.3 specifically; main was analysed.

## Sources

llama.cpp server README and discussion 11040 · the UpGuard llama.cpp
prompt-leak study · Ollama API docs, FAQ, OpenAI-compatibility page, issues
3144, 9254, 9208 and 10950 · the community ollama-metrics proxy · vLLM
metrics design doc, v0.10.1 release notes, issue 21422 · LM Studio 0.3.3
release post, REST endpoints and log-stream docs, bug-tracker issue 81 · Jan
API-server docs · text-generation-webui issue 3955 · LocalAI issue 1445 and
Grafana dashboard 25179 · Open WebUI issue 8127 · mlx-lm server source and
SERVER.md · OpenAI cookbook on streaming · OpenTelemetry GenAI metrics
conventions · Caddy metrics and Caddyfile options · Syncthing metrics docs and
pull request 9965 · Tailscale client-metrics reference and blog post · Grafana
Alloy live-debugging and controller-metrics docs · Prometheus client_golang
collectors package and storage docs · Netdata disk and retention docs · the
`ul` unified-logging Go binding · a 2025 practitioner post on unified-logging
privacy · Apple MetricKit docs · Apple HIG, the menu bar · Bjango on
designing menu bar extras.
