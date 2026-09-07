# Context windows of the local models, measured — 2026-09-06

What each of the four local models' context windows really is on this machine:
the cap its configuration declares, the largest prompt the server verifiably
accepts through the gateway, the prompt size a client can rely on for recall,
the memory a long prompt costs, and where the gateway's timeout rather than
the model sets the limit. The data is in `../evidence/2026-09-06-context-
windows/`, sanitised (no paths, hostnames or addresses; the results carry no
prompt text and not the needle's value, only whether each answer contained
it); the script there holds the filler and regenerates every prompt from a
seed. This note extends the context probe in `2026-09-06-model-bench-
evidence.md`, which stopped at a 128K cap, and corrects two of its readings
(see "What changed in the record").

Machine: one Apple Silicon Mac with 128 GB unified memory, macOS 26, Gropius
serving on its default port, mlx-lm 0.31.3, decode concurrency at the default
of 4. Every request went through the gateway, so every number is what a client
sees, including the gateway's fixed ten-minute upstream response-header
timeout. Another session ran the Go test suite on the same machine for most
of the evening; the one-minute load average is recorded per probe (between 2
and 87), and the probes that decide a window were repeated in quiet windows
the other session granted. Models were loaded one at a time and unloaded
before the next.

## Files in the evidence folder

| File | What it is |
| --- | --- |
| `nominal-caps.json` | Per model: declared cap, rope entries, attention layout and the KV bytes per token the configuration implies |
| `results/<model>.json` | Every probe: target and verified prompt tokens, wall time, load average, memory samples, the needle runs and (Nemotron) the concurrency and streaming-control runs; a `note` field marks the three contaminated Coder probes |
| `context_windows_probe.py` | The probe: `load`, `sweep` (fixed sizes, the cap, a bisection), `needle`, `concurrency`, `stream-control`, `unload`; run against a live Gropius on the same machine |
| `tabulate.py` | Renders the result JSONs as the tables in this note (`--fit` adds the memory fit) |

## Method

- **Prompts** are synthetic filler generated from a seed. Every prompt
  starts with a unique nonce and uses its own marker order, so no two
  requests share a prefix and the model server's prompt cache cannot shorten
  a later prefill. The earlier probe's prompts shared a prefix, and its GLM
  timings fell as prompts grew; that was the cache at work, and it is why
  that probe's GLM figure does not survive here.
- **Prompt size** is the `usage.prompt_tokens` the gateway returned, chat
  template included; the target sizes in the text are what the generator
  aimed at.
- **Prefill time** is the wall time of a non-streaming request with one
  output token at temperature 0, so it includes one decode step and the
  gateway hop. A request that died at exactly 600 s died at the gateway's
  timeout; the model server keeps prefilling such a request after the
  gateway has answered 502. Once that was seen (after the Coder's first two
  timeouts, whose two following probes are marked contaminated in the JSON),
  every long probe was preceded by an unload and reload, which the JSON
  records as `reloaded_before`.
- **Memory** is sampled every three to four seconds around each probe, two ways,
  because the model server runs under the service account and only `top`
  can read its footprint from the measuring account: `top`'s MEM column for
  the mlx-lm process (integer gigabytes above 10 GB, so plus or minus
  0.5 GB) and `vm_stat`'s free pages system-wide (page precision, but noisy
  under the concurrent test runs). The growth per thousand tokens is a
  least-squares line through the clean probes of each model, so the rounding
  averages out. A guard in the script projected each probe's footprint from
  the previous ones and skipped any that would come within 24 GB of the
  machine's available memory; the skips are in the JSON.
- **Recall** is a needle-in-a-haystack check: one distinctive fact placed at
  10%, 50% and 90% of the filler, a question at the end, temperature 0, up
  to 64 output tokens, scored as exact presence of the fact's value in the
  answer. The chat template's thinking mode is off (`enable_thinking: false`
  through `chat_template_kwargs`, which the gateway relays), because a model
  with a thinking mode otherwise spends the whole 64-token budget thinking
  and answers nothing (in a preliminary 2K-token run before the script took
  its final form, Nemotron produced 218 characters of reasoning and no
  answer; that run is not in the results); the result then measures
  retrieval, not reasoning budget.
- **Nominal cap** is `max_position_embeddings` in each model's `config.json`,
  read from the HuggingFace hub at the main revision, because the local model
  directory belongs to the service account and is not readable from the
  measuring account. None of the four declares a `rope_scaling` entry.

## 1. Nominal cap

| Model | Declared cap | Attention | KV bytes per token at f16 |
| --- | --- | --- | --- |
| Qwen3-Coder-Next 4bit | 262,144 | 12 full-attention layers of 48, the rest gated DeltaNet; MoE, 10 of 512 experts per token | 24 KB |
| Nemotron-3.5-Lightning 30B-A3B 4bit | 262,144 | 6 full-attention layers of 52, 23 Mamba-2, 23 MoE | 6 KB |
| GLM-4.7-Flash 8bit | 202,752 | 47 multi-head latent attention layers; MoE, 4 of 64 experts per token | 53 KB as the latent cache, 940 KB if the server caches decompressed K and V |
| Qwen3.8-27B 8bit | 262,144 | 16 full-attention layers of 64, the rest gated DeltaNet; no MoE, every parameter active | 64 KB |

The KV figure is full-attention layers times KV heads times head dimension
times two (K and V) times two bytes; the linear-attention and Mamba layers
hold a fixed-size state per sequence instead of a per-token cache. The
27B's `config.json` also carries multimodal rope parameters
(`mrope_section`, partial rotary factor 0.25) but no scaling entry; the
model ships as text-only in this quantisation.

## 2. Verified window

| Model | Largest verified prompt | Prefill s | What stopped the probe |
| --- | --- | --- | --- |
| Qwen3-Coder-Next 4bit | 221,743 | 591.4 | the gateway timeout: the 256K target died at 600.0 s twice, at load averages of 57 and 7; a 244K target also died at 600.0 s but with a needle request overlapping it, so it is marked contaminated |
| Nemotron-3.5-Lightning 4bit | 253,106 | 468.7 | nothing: the cap target (261,120, the declared cap less a margin) completed; the declared 262,144 itself was not sent |
| GLM-4.7-Flash 8bit | 81,100 | 431.9 | the gateway timeout: the 96K target died at 600.0 s at a load average of 4; the 202K cap was never reachable inside 600 s |
| Qwen3.8-27B 8bit | 91,673 | 497.4 | nothing failed: the campaign sent this model no target expected to run past 540 s, and this one finished 103 s under the timeout |

Nemotron is the only model the gateway can serve to within 9K of its declared
cap: the 261,120 target took 469 s, and the three 256K needle prompts later in
the evening took 436, 495 and 546 s under test load, so even it sits within a
minute of the timeout at its cap. Qwen3-Coder-Next's window is bounded by the
timeout, not the model: 221,743 tokens completed nine seconds under the limit,
and the 256K target died at exactly 600.0 s with the gateway's 502 twice, once
under heavy test load and once at a load average of 7. Its rate at that size
(375 tokens per second) puts the 256K cap at about 700 s, so the cap is
unmeasurable until the timeout issue lands. GLM-4.7-Flash is bounded the same
way and much lower: its prefill rate falls steeply with length, a 96K prompt
cannot finish in 600 s, and 81,100 tokens is the largest verified; the true
window between 81K and 96K was not bisected further.

The 27B ran up to the largest prompt that completed under 540 s, as
planned; its window above that is unmeasurable until the timeout issue
lands, and that is the record's position on it. At 184 tokens per second
the next target up (about 105K) would take about 570 s, over the budget, and
the timeout itself falls near 110K; the range from 92K to 110K is unprobed,
and everything above it waits on the timeout issue.

## 3. Prefill rate

Tokens per second is prompt tokens over wall time. Load is the one-minute load
average at the end of the probe; the Coder's first four points ran under heavy
test load, and its 96K figure matches the earlier quiet probe (645 against 650
tokens per second), so CPU load from the tests moved these GPU-bound timings
little. Two of the three Coder probes marked contaminated in the JSON (105,638
tokens at 224 tokens per second and 124,234 at 258) ran while the server was
still prefilling an abandoned 256K request; they and the overlapped 244K
failure are left out here.

| Model | 8K | 32K | 64K | 96K | Largest verified |
| --- | --- | --- | --- | --- | --- |
| Qwen3-Coder-Next 4bit | 1,292 (8,010 in 6.2 s) | 1,055 (31,952 in 30.3 s) | 812 (63,855 in 78.6 s) | 645 (95,758 in 148.5 s) | 375 (221,743 in 591.4 s); 426 at 189,260 |
| Nemotron-3.5-Lightning 4bit | 1,501 (7,957 in 5.3 s) | 1,325 (31,787 in 24.0 s) | 1,116 (63,517 in 56.9 s) | 929 (95,306 in 102.6 s) | 540 (253,106 in 468.7 s) |
| GLM-4.7-Flash 8bit | 863 (8,113 in 9.4 s) | 409 (32,462 in 79.3 s) | 231 (64,887 in 280.6 s) | timed out at 600 s | 188 (81,100 in 431.9 s) |
| Qwen3.8-27B 8bit | 247 (7,703 in 31.2 s) | 228 (30,599 in 134.0 s) | 205 (61,165 in 299.1 s) | 184 (91,673 in 497.4 s) | 184 (the 92K probe is its largest) |

The rate is not a constant per model: it roughly halves between 8K and 96K on
the hybrid models and falls fourfold on GLM, whose every layer attends over
the whole prompt. A timeout sized from the small-prompt rate is therefore
wrong by a factor of two to four at the sizes where it matters. For a prefill-
aware bound (iss-2609061541313832) the floor across these four models at their
largest verified sizes is about 185 tokens per second (the 27B at 92K, GLM at
81K); a bound of prompt tokens over 150 tokens per second plus a minute, or
today's ten minutes if that is larger, covers every probe here (256K would get
about 30 minutes; anything under about 80K keeps the ten).

## 4. Memory per thousand tokens

Growth is the process's peak during the probe minus its footprint just
before, from `top`. The line is a least-squares fit through the clean
probes; the intercept is the working set prefill needs regardless of size.
The per-probe table that `tabulate.py` prints has an "after" column, which
shows that the server does not return to its idle footprint once the
response is out: it keeps the prompt's cache.

| Model | Weights on disk | Idle footprint | Intercept | Growth per 1K tokens | Bytes per token, measured | Bytes per token, configuration | Peak at largest verified |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Qwen3-Coder-Next 4bit | 44.9 GB | 43 GB | 1.0 GB | 0.110 GB | 115 KB | 24 KB | 68 GB at 221,743 |
| Nemotron-3.5-Lightning 4bit | 17.8 GB | 18 GB | 3.4 GB | 0.011 GB | 12 KB | 6 KB | 28 GB at 253,106 |
| GLM-4.7-Flash 8bit | 31.8 GB | 30 GB | 1.5 GB | 0.337 GB | 353 KB | 53 KB latent, 940 KB decompressed | 59 GB at 81,100 |
| Qwen3.8-27B 8bit | 29.5 GB | 28 GB | 3.4 GB | 0.191 GB | 201 KB | 64 KB | 49 GB at 91,673 |

Per-probe rows are in the evidence (`tabulate.py`). Three things for iss-3:

- **The cost per token is an architecture property and the spread is
  thirtyfold**: 12 KB on Nemotron, 115 KB on Qwen3-Coder-Next, 201 KB on the
  27B, 353 KB on GLM. Every measured figure is above what the configuration's
  f16 KV arithmetic gives, by two to seven times, so the server holds more per
  token than the raw cache (the linear-attention states, activations kept for
  the batch, allocator granularity); the configuration figure is a floor, not
  an estimate.
- **The flat 1.2 times disk size is wrong in both directions.** It gives
  Nemotron 21 GB against a measured 28 GB peak at its cap, the Coder 54 GB
  against 68 GB at 222K, GLM 38 GB against 59 GB at 81K and about 100 GB at
  its declared 202K, and the 27B 35 GB against 49 GB at 92K (about 80 GB at
  its declared cap, extrapolated). A budget that fits these numbers is weights
  plus the intercept plus the per-token slope times the window it intends to
  serve, per model.
- **The footprint does not fall back after a request, and it stacks.** The
  "after" reading sits above the "before" by up to 17 GB (the 27B at 92K),
  more for the larger prompts; the server keeps the prompt cache. After the
  Coder's abandoned 256K request the process stayed at 74 to 76 GB, a
  following 105K prompt took it to 103 GB, and when a second long request
  overlapped it the process reached 108 GB and the machine swapped 2.5 GB. A
  model's admission cost is not weights plus one prompt; on this server it is
  weights plus the caches it has been allowed to keep.

## 5. Usable window

Recall of the fact at every depth and size tested, thinking mode off, up to
64 output tokens. The 27B's 64K prompt ran at 50% depth only, to keep its
check inside the campaign's 40-minute budget for one measurement.

| Model | 16K | 64K | Largest verified |
| --- | --- | --- | --- |
| Qwen3-Coder-Next 4bit | 1 of 1 at 90% (16,009 tokens); the 10% and 50% runs hit but overlapped a sweep probe and were dropped | 3 of 3 (63,892) | 3 of 3 at about 189,295 |
| Nemotron-3.5-Lightning 4bit | 3 of 3 (15,957) | 3 of 3 (63,612) | 3 of 3 at about 253,142 |
| GLM-4.7-Flash 8bit | 3 of 3 (16,283) | 3 of 3 (64,922) | 3 of 3 at 81,135 |
| Qwen3.8-27B 8bit | 3 of 3 (15,316) | 1 of 1 at 50% (61,165) | 3 of 3 at 91,674 |

No model's recall degraded anywhere inside the window the gateway can
serve: every answer contained the code, ended with finish reason `stop`,
and was 15 characters long, the code's own length.
The Coder's largest needle ran at 189K rather than its verified 222K
because the latter sits nine seconds under the timeout and a needle needs
64 output tokens on top; the usable window is therefore at least 189K there
and, on this evidence, the verified window everywhere else. A client that
sets a small `max_tokens` on a model with a thinking mode should know that
the budget goes to reasoning first (see Method).

## 6. Two concurrent 64K prompts (Nemotron, the fastest prefill)

Streaming requests, 16 output tokens, first-token time measured at the
first delta the gateway relayed.

| | First token | Wall | Process footprint |
| --- | --- | --- | --- |
| One request, streaming, first run | 126.3 s | 126.5 s | 25 to 29 GB |
| One request, streaming, two control runs | 64.3 and 67.5 s | 64.5 and 67.6 s | not sampled |
| One request, non-streaming, control | 60.2 s | 60.2 s | not sampled |
| Two concurrent requests | 117.5 s and 117.5 s | 117.8 s | 25 to 31 GB (24 GB after) |

Two 64K prompts each waited for the pair's prefill: first-token time
1.8 times the single figure, throughput 1.1 times, so long-prompt
concurrency on the same model is close to serial, unlike the cheap
same-model decode concurrency the earlier note measured at 6K prompts. The
memory cost of the second request was about 2 GB more than one request's
growth; no swap. The first single run is an outlier at twice the control
figure; it was the first request after the 256K sweep, and the two controls
that followed agree with the non-streaming figure, so the controls are the
single-request baseline.

## What each model's windows are

| Model | Nominal | Verified through the gateway | Usable (recall) | Timeout-bounded? | Memory at the verified window |
| --- | --- | --- | --- | --- | --- |
| Qwen3-Coder-Next 4bit | 262,144 | 221,743 | at least about 189,295, no degradation seen | yes: 256K needs about 700 s | 68 GB process |
| Nemotron-3.5-Lightning 4bit | 262,144 | 253,106 (9K under the cap) | about 253,142, no degradation seen | no, but within a minute of it at the cap | 28 GB process |
| GLM-4.7-Flash 8bit | 202,752 | 81,100 | about 81,135, no degradation seen | yes: 96K dies at 600 s; the cap would need about 20 minutes | 59 GB process, about 100 GB at the cap |
| Qwen3.8-27B 8bit | 262,144 | 91,673 | 91,674, no degradation seen | by this campaign's 540 s budget; the timeout itself falls near 110K, and above that is unmeasurable until the timeout issue lands | 49 GB process |

## What changed in the record

- The 27B that the earlier notes call "dense" is dense only in the sense
  that every parameter is active. Its attention is hybrid, three
  linear-attention layers to every full-attention one, the same family as
  Qwen3-Coder-Next. The record's estimate of 0.25 GB per thousand tokens
  for it assumed full attention on every layer; the configuration gives
  0.06 GB and the measurement 0.19 GB. The record's number was nearer the
  truth than the configuration's, but its reason was wrong.
- GLM-4.7-Flash's earlier verified figure of 122,347 tokens was reached
  with prompts that shared a prefix, so the server's prompt cache did most
  of the prefill; with unique prompts its window through the gateway is
  81K to 96K, bounded by the timeout. The earlier figure holds only for a
  client that resends a cached prefix.
- Qwen3-Coder-Next's earlier verified figure of 121,366 was a cap, not a
  limit; its gateway-bounded window is 221,743.

## What remains unmeasurable, and why

- The 27B's window above 91,673 and Qwen3-Coder-Next's above 221,743, and
  GLM's above 81,100: the gateway's 600 s upstream timeout
  (iss-2609061541313832). Nothing about the models failed.
- GLM's memory at its declared cap: never reachable inside the timeout, so
  the 100 GB figure is the fit extrapolated, not a reading.
- Process memory finer than 1 GB: `top` is the only reader of another
  account's footprint without root, and it rounds.
- Whether the retained prompt caches have a ceiling: the server was
  reloaded before each long probe once the stacking was seen, to keep the
  machine off swap, so the campaign did not find where mlx-lm stops
  keeping them.
