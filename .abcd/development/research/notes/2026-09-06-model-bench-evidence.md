# Model-bench evidence for Gropius — 2026-09-06

The measurements from the model benchmark lab of 2026-09-05 and its follow-up
probes of 2026-09-06 that concern Gropius: the local models it served, how
they compared with the same models via a hosted API, and what the server did
under the probes. The lab folder itself is not kept; the data this note rests
on is in `../evidence/2026-09-06-model-bench/`, sanitised (log paths and
output tails removed, no hostnames or addresses). The reading of these
numbers is in `2026-09-06-model-bench-findings.md`; this note is the numbers.

Machine: one Apple Silicon Mac with 128 GB unified memory, macOS 26, Gropius
serving on its default port, mlx-lm 0.31.3, decode concurrency at the
default of 4. The hosted comparison ran through an aggregator API against
the same open-weight models at the provider's quantisation.

## Files in the evidence folder

| File | What it is |
| --- | --- |
| `runs.json` | 80 benchmark runs, 16 model variants times 5 tasks: model, task, duration, exit code, auto score and per-check detail |
| `sampling-probe.json` | Five probes per model on four local models: temperature 0 twice, temperature 0.7 with two seeds, temperature 1.5; outputs, first-token time, completion tokens |
| `context-probe.json` | Bisection between 1K and 128K target tokens on the same four models: verified prompt tokens, prefill seconds, failure point and error |
| `sampling_probe.py`, `context_probe.py` | The probe scripts; run against a live Gropius on the same machine |
| `writer_reviewer.py` | The writer-and-reviewer loop script (one model writes, another reviews, one revise round) |

## Benchmark: five auto-scored agentic coding tasks, 40 points each

Tasks: a single-file landing page; four seeded bugs in a Python module gated
by a unit-test suite; a CSV-to-JSON CLI with eight acceptance scenarios;
four algorithms from docstrings with 19 tests; a stdlib HTTP task API with 12
live requests. The runner drove each model through an agent harness in an
isolated workspace; local models went through Gropius (the four marked
"sanitiser" through a system-message-merging proxy the lab wrote, because
the dense model's chat template rejects a non-leading system message).

| Variant | Route | Score /200 | Mean s per task | Task that lost points |
| --- | --- | --- | --- | --- |
| Qwen3-Coder-Next 4bit | local | 200 | 32 | none |
| Qwen3-Coder-Next | hosted | 200 | 30 | none |
| Nemotron-3.5-Lightning 30B-A3B 4bit | local | 197 | 65 | HTTP API 37/40 |
| Nemotron-3.5-Lightning | hosted | 200 | 80 | none |
| Qwen3.8-27B 8bit | local | 200 | 511 | none |
| Qwen3.8-27B 4bit | local | 160 | 777 | HTTP API 0/40, timed out twice |
| Qwen3.8-27B | hosted | 200 | 136 | none |
| GLM-4.7-Flash 8bit | local | 172 | 119 | CSV 35, HTTP API 17 |
| Kimi-Dev-72B 6bit | local | 47 | 1221 | landing page 0, CSV 5, algorithms 2, HTTP API 0 |
| Qwen3.6-35B-A3B | hosted | 200 | 21 | none |
| Ling-3.0-Flash | hosted | 200 | 25 | none |
| Qwen3.5-35B-A3B | hosted | 200 | 48 | none |
| Laguna-S 2.1 | hosted | 200 | 91 | none |
| Muse-Glimmer 30B | hosted | 200 | 137 | none |
| Nemotron-3-Super 120B-A12B | hosted | 193 | 213 | HTTP API 33 |
| Qwen3.5-122B-A10B | hosted | 180 | 29 | HTTP API 20 |

Local against hosted, same model:

- The 4-bit MoE coder matches its hosted run in score and speed.
- The 4-bit MoE Lightning is faster locally than hosted and dropped three
  points on one task.
- The dense 27B at 8 bits keeps its score and runs 3.8 times slower than
  hosted; at 4 bits it fails to finish the longest-horizon task at all,
  twice, which is a capability loss rather than slowness.

## Concurrency and swaps, measured on the server

Single-model concurrency with GLM-4.7-Flash 8bit resident: time to first
token about 0.4 s solo, 0.74 s with two concurrent agents, 0.64 s with four,
1.7 s with six; six requests completed in 16.6 s against 6.0 s for one, about
3.6 times the throughput. Same-model concurrency is cheap; a model swap
evicts the resident model and reloads from disk, and is the expensive event.
Between the two workhorse models of the writer-reviewer loop a cold swap
took 7.7 s in total with a 7.2 s first-token time.

Load on first call in the probes (cold): 0.8 to 0.9 s for the two 4-bit MoE
models when already resident from a prior run, 8.5 to 12.9 s for a load from
disk, 18.4 s for the dense 27B at 8 bits.

## Sampling probe, 2026-09-06

The probe JSON's `seed_honoured` field means the two runs with the same seed
matched; `seed_differentiates` means runs with different seeds differed. On
all four models the first is true and the second is false: the output at
temperature 0.7 was byte-identical for seed 42 and seed 999. Temperature 0
was deterministic on all four, and temperature 1.5 diverged on all four.

| Model | Temperature 0 deterministic | Different seeds differ | Temperature effective |
| --- | --- | --- | --- |
| Qwen3-Coder-Next 4bit | yes | no | yes |
| Nemotron-3.5-Lightning 4bit | yes | no | yes |
| GLM-4.7-Flash 8bit | yes | no | yes |
| Qwen3.8-27B 8bit | yes | no | yes |

Reading: the pinned model server ignores the seed parameter; sampling is
deterministic per prompt and temperature. Gropius sets no sampler flags at
launch and relays the field unchanged, so this is upstream behaviour.

## Context probe, 2026-09-06

Bisection at temperature 0 with one output token, prompt size verified by the
usage object's prompt-token count, chat template included, capped at a 128K
target.

| Model | Largest verified prompt | Prefill tokens per second | Outcome |
| --- | --- | --- | --- |
| Qwen3-Coder-Next 4bit | 121,366 | 567 to 819 | cap reached, no failure |
| Nemotron-3.5-Lightning 4bit | 120,719 | 856 to 1,107 | cap reached, no failure |
| GLM-4.7-Flash 8bit | 122,347 | 224 to 963 | cap reached, no failure |
| Qwen3.8-27B 8bit | 93,222 | 180 to 202 | failed at the 106,688 target with the gateway's 502 |

The dense model's two failures came at exactly 600.0 s, the gateway's fixed
upstream response-header timeout, not from the model: at about 180 tokens
per second, 600 s bounds prefill near 105K tokens. Its true window above 93K
is unproven. The hybrid-attention MoE models prefill four to five times
faster than the dense 27B.

## Writer-and-reviewer loop, 2026-09-06

One run of the algorithms task: the 4-bit MoE coder wrote in 45.0 s and
scored 40/40 first pass; a cold swap to the dense 27B at 8 bits took 7.7 s;
the reviewer's one-shot review took 551.5 s and returned a genuine verdict
(it re-derived a tie-break rule and checked it against the code); the revise
round swapped back in 38.0 s with nothing to fix. Total 642.3 s, of which
about 9.2 minutes was the reviewer generating. The reviewer exhausted a
4,096-token completion budget on reasoning alone and needed 8,192 to reach
a verdict; it streams reasoning in a `reasoning` delta field rather than the
`reasoning_content` name some clients expect.

## What each record takes from this

- `itd-2609061429508050` (sampling defaults): seed is inert, temperature
  works, temperature 0 is deterministic; a completion-token default matters
  for reasoning models.
- `itd-2609061431463108` (context window visible) and
  `itd-2609061431481936` (effective context, held): verified windows per
  model and the timeout confound.
- `itd-2609061441241254`, `itd-2609061441285238`,
  `itd-2609061441261073` (pinned, grace, budget): swap cost against
  same-model concurrency; the co-residency arithmetic in the findings note.
- `itd-2609061441310453` (system-message merging): the dense model's
  template rejection is why the lab's local runs needed a merging proxy.
- `itd-2609061521082551` and `itd-2609061521159233` (statistics and
  dashboard): the figures worth recording are the ones this lab had to
  measure by hand: first-token time, prefill and generation rates, swap
  time.
- `iss-3`: KV-cache cost per architecture; `iss-2609061541313832`: the
  timeout; `iss-2609061431537735`: the measured windows;
  `iss-2609061429558510` (resolved): the seed result.
