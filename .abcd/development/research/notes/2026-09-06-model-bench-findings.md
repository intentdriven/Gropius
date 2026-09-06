# Model-bench lab findings relevant to Gropius — 2026-09-06

Source: a model benchmark lab run on 2026-09-05 against a Gropius install on
a 128 GB Apple Silicon Mac, kept outside this repository (a local `model-bench`
folder with a report, per-run verdicts, raw transcripts, and two proxy
scripts). The numbers themselves, with the sanitised run data and probe scripts, are in `2026-09-06-model-bench-evidence.md` and `../evidence/2026-09-06-model-bench/`. These are measurements and observations, not decisions; each item
names the Gropius record it feeds. Model names are as the lab wrote them.

## What was measured

- **Same-model concurrency is cheap.** With one model resident, time to first
  token went from about 0.4 s solo to 0.74 s with two concurrent agents,
  0.64 s with four, and 1.7 s with six; six requests took 16.6 s in total
  against 6.0 s for one, about 3.6x throughput. Consistent with the decode
  concurrency default of 4 and the per-model semaphore. Feeds the docs-gap
  issue and the residency intents.
- **Model swaps are the expensive event.** A request for a different model
  evicts the least-recently-used idle model and reloads from disk. The lab's
  agentic clients are idle between turns, so they evicted each other's model.
  The lab called this "preemption" and wrote a FIFO broker to avoid it. Feeds
  the pinned-models and eviction-grace intents.
- **Local quality matches hosted for small-active-parameter MoE models.**
  Qwen3-Coder-Next 4bit scored identically locally and hosted at the same
  speed; Nemotron-3.5-Lightning 4bit was faster locally. Dense 27B at 8bit
  kept quality but ran 3.8x slower; at 4bit it twice failed to finish the
  longest task. Not a Gropius finding; recorded because it shapes which
  models the docs might recommend.
- **KV-cache cost differs sharply by architecture.** The lab estimates about
  0.25 GB per thousand tokens at f16 for a dense 27B and far less for
  hybrid-attention MoE models. Evidence attached to iss-3.
- **Reported quirks.** Per-request `temperature` reaches the model; per-request
  `seed` is reported as not reliably honoured. Unverified here; captured as
  iss-2609061429558510.
- **Template-strict models reject a non-leading system message.** The Qwen3.8
  chat template failed instantly when a client re-sent its system prompt
  mid-conversation; the lab's fix was a proxy that merges all system content
  into one leading message. Feeds the opt-in merging intent.

## Co-residency under the current budget

The lab reserved about 18 GB for macOS and reckoned 110 GB usable. Gropius's
default budget is 60% of physical RAM (about 77 GB on this machine) and
charges each model 1.2x its disk size, so the lab's table reads differently
under the actual rule:

| Pair | Disk | Charged at 1.2x | Fits 77 GB budget |
| --- | --- | --- | --- |
| Coder-Next 4bit + Lightning 4bit | 63 GB | 75.6 GB | yes, barely |
| Coder-Next 4bit + Qwen3.8-27B 8bit | 74 GB | 88.8 GB | no |
| Coder-Next 4bit + Qwen3.8-27B 4bit | 61 GB | 73.2 GB | yes |
| Kimi-Dev-72B 6bit alone | 60 GB | 72 GB | yes, nothing else |

The lab's preferred author-plus-reviewer pair cannot co-reside under the
default although it fits physically. Feeds the configurable-budget intent.

## Not relevant to this repository

The model lineup recommendations, the client-side plugin configuration, and
the benchmark task checkers. The two proxy scripts are reference behaviour
for the intents above, not code to absorb.

## Addendum: probes run on 2026-09-06 against the live server

Three further probes, recorded in the lab's findings document of the same
date (outside the repository, with scripts and raw JSON):

- **Seed is ignored, temperature works.** Five probes per model on four
  models: identical outputs for two different seeds at temperature 0.7 on
  every model; temperature 0 deterministic; temperature 1.5 diverges. The
  issue verifying seed passthrough is resolved on this evidence.
- **Context windows.** The three hybrid-attention MoE models reached a 128K
  probe cap with no failure. The dense 27B at 8-bit verified 93K; its
  failures above that were the gateway's ten-minute upstream timeout, which
  bounds usable prefill near 105K tokens at 180 tokens per second. Captured
  as a gateway issue; evidence appended to the measurement issue.
- **Writer and reviewer loop.** A 4-bit MoE coder wrote a task in 45 s and a
  dense 27B reviewer took 551 s to return a genuine verdict; swaps between
  the two cost about 8 s. The reviewer's generation, not the swap, is the
  bottleneck. That reviewer exhausted a 4,096-token completion budget on
  reasoning alone and needed 8,192; it streams reasoning in a `reasoning`
  delta field rather than the `reasoning_content` name some clients expect.
  Relevant to the sampling-defaults intent (a machine-wide completion-token
  default) and to any docs on using reasoning models.

## Context length in the model configuration

Sampled on 2026-09-06 while implementing itd-2609061431463108, which defers
the key rule to build time. Two sources: the `config.json` each of the four
benchmarked models publishes on the Hub, and every model configuration on the
lab Mac's HuggingFace cache. The question is which key states the model's
architectural context length, per architecture.

| Model | `model_type` | Where the range is declared | Value |
| --- | --- | --- | --- |
| Qwen3-Coder-Next-4bit | `qwen3_next` | top level | 262,144 |
| NVIDIA-Nemotron-3.5-Lightning-30B-A3B-4bit | `nemotron_h` | top level | 262,144 |
| GLM-4.7-Flash-8bit | `glm4_moe_lite` | top level | 202,752 |
| Qwen3.8-27B-8bit | `qwen3_5` | `text_config` only | 262,144 |
| Qwen3-Coder-30B-A3B-Instruct-4bit | `qwen3_moe` | top level | 262,144 |
| Qwen3-1.7B-4bit | `qwen3` | top level | 40,960 |
| Qwen3-Embedding-0.6B-4bit-DWQ | `qwen3` | top level | 32,768 |
| Qwen2.5-Coder-1.5B-Instruct-8bit | `qwen2` | top level | 32,768 |
| Ornith-1.0-35B-4bit | `qwen3_5_moe` | `text_config` only | 262,144 |
| diffusiongemma-26B-A4B-it-4bit | `diffusion_gemma` | `text_config` only | 262,144 |
| MinerU2.5-Pro-2605-1.2B | `qwen2_vl` | both, disagreeing | 32,768 top level, 8,192 nested |

The rule this supports, and the one Gropius implements:

- `max_position_embeddings` at the top level is authoritative. Every plain
  causal-LM configuration in the sample declares it there.
- Failing that, `text_config.max_position_embeddings`. Multimodal and
  composite configurations — including the dense 27B the lab benchmarked —
  declare no top-level range and nest the text model's settings.
- The nested key is a fallback, never an override: one sampled configuration
  declares both and they disagree, and a top-level key that is present but
  implausible yields nothing rather than falling through to a figure its own
  authoritative key contradicts.
- No scaling arithmetic. No configuration in the sample declares a
  `rope_scaling` factor without a pre-scaling figure; the ones that carry
  `rope_scaling` at all carry it as `null` or as an `mrope` section with no
  factor. Where the case does arise, the declared range is published as it
  stands: under-reporting is the safe direction for a client that trims its
  history to fit, whereas over-reporting hands it a number the model was never
  scaled to.
- Nothing in the sample declares a range Gropius would refuse. The ceiling it
  applies is 8,388,608 tokens, about 32x the widest window sampled and 68x the
  largest prompt the context probe verified.

Consistency check against the probe in `../evidence/2026-09-06-model-bench/`:
the three hybrid-attention MoE models reached the probe's 128K cap with no
failure, and each declares at least 202,752 — so the declared range is not
contradicted by measurement anywhere in the sample. The declared range is a
larger number than any of them was measured at; that is expected, and is why
the figure is published as the architectural maximum rather than as the window
a given Mac can serve.
