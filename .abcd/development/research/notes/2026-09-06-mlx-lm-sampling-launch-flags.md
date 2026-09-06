# Sampling launch flags and ranges in mlx-lm 0.31.3 — 2026-09-06

The pinned model server is `mlx-lm==0.31.3`
(`internal/runtime/mlx-requirements.txt`). This note records what that
server's own source says about sampling: which sampling parameters it takes
as launch flags, what it defaults them to, and what ranges it accepts. It is
the fixture the sampling-defaults tests cite
(spc-2609061822378193 / itd-2609061429508050).

Method: read `mlx_lm/server.py` from a provisioned 0.31.3 install — its
`main()` argument parser for the flags, and `HTTPHandler.validate_model_
parameters` / `_validate` for the ranges. Nothing here is inferred from
documentation or from a different version.

## Sampling parameters exposed as launch flags

| Flag | Type | Server default | Request field it defaults |
| --- | --- | --- | --- |
| `--temp` | float | `0.0` | `temperature` |
| `--top-p` | float | `1.0` | `top_p` |
| `--top-k` | int | `0` (disables top-k) | `top_k` |
| `--min-p` | float | `0.0` (disables min-p) | `min_p` |
| `--max-tokens` | int | `512` | `max_completion_tokens`, else `max_tokens` |

That is the whole set. No other sampling or logits parameter has a launch
flag: `repetition_penalty`, `repetition_context_size`, `presence_penalty`,
`frequency_penalty`, `xtc_probability`, `xtc_threshold`, `logit_bias`,
`logprobs`, `top_logprobs` and `seed` are read from the request body only,
and default to constants baked into the handler.

## Ranges the server itself accepts

`validate_model_parameters` runs on every request against the *effective*
value — the body's value where the request carries one, otherwise the launch
flag's. So a launch flag out of range is rejected once per request, not once
at start-up.

| Parameter | Accepted |
| --- | --- |
| `temperature` | number, at least `0`; no upper bound |
| `top_p` | number, `0` to `1` inclusive |
| `top_k` | integer, at least `0` |
| `min_p` | number, `0` to `1` inclusive |
| `max_tokens` | integer, at least `0` |

Gropius accepts exactly these ranges and no wider
(`internal/config/sampling.go`, pinned by
`TestGoRangesMatchThePinnedServerRanges`).

## The failure mode this pins

`argparse` performs no range checking, so an out-of-range launch flag does
not stop the process starting. It surfaces later: the server takes it as the
default for the omitted field, `validate_model_parameters` rejects the
effective value, and *every request that omits that parameter* is answered
`400` while requests that carry their own value still succeed. One saved
setting would therefore break the machine for exactly the clients this
feature exists to serve. The Go range check is what holds this, which is why
it is pinned to this table rather than chosen.

## Seed

`--seed` is not a launch flag. The parser has no such option, so a seed is
not a thing this feature could default even if it worked: the server reads
`seed` from the request body alone.

What it does with it there is settled empirically rather than from the
source. The lab's sampling probe against the live server found the seed
inert — seed 42 and seed 999 at temperature 0.7 produced identical outputs
on all four models probed — while temperature reached the model on every one
and temperature 0 was deterministic across runs
(`2026-09-06-model-bench-evidence.md`, iss-2609061429558510). This note does
not offer a mechanism for that; the observation is what the documentation
states. Reproducibility comes from a fixed temperature.
