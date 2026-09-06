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

### The sampler's own limits, which the request check does not cover

`validate_model_parameters` is not the whole story. `mlx_lm/sample_utils.py`
builds the sampler from the effective values and carries limits of its own:

- `apply_top_k` raises `ValueError` unless `0 < top_k < vocab_size`. It is
  reached whenever the effective temperature is non-zero and top-k is above
  zero. So a top-k the request check waves through — 200,000, say — leaves the
  process healthy and raises on every request that omits `top_k`.
- `apply_min_p` raises unless `0 <= min_p <= 1`, which is the request check's
  range exactly.
- `apply_top_p` is applied only when `0 < top_p < 1`, so `0` and `1` are inert
  rather than refused.
- `categorical_sampling` divides by the temperature, and `make_sampler` short-
  circuits to `argmax` at temperature 0, so no temperature raises.

Gropius accepts these ranges and no wider, with one deliberate narrowing:
top-k is capped at `config.MaxTopK` (1024), far below the smallest vocabulary
an MLX model ships, because a vocabulary size is not knowable when the value
is saved. Pinned by `TestGoRangesAreNeverWiderThanThePinnedServers` and
`TestGoRangesAreExactlyThese`.

## The failure mode this pins

`argparse` performs no range checking, so an out-of-range launch flag does
not stop the process starting. It surfaces later, and worse than a refusal:
the server takes the flag as the default for the omitted field and
`validate_model_parameters` raises a `ValueError` on the effective value —
uncaught. `do_POST` wraps only the `Content-Length` parse in a `try`, so the
exception escapes into `ThreadingHTTPServer`, which logs a traceback to the
per-model log and closes the socket **without writing a response at all**.
There is no `400`. The gateway sees a connection that answered nothing and
returns `502 the model server did not respond`. Requests that carry their own
value still succeed.

The same is true of an explicit `null` in a request body: `body.get(key,
default)` finds the key present, `None` fails the type check, and the
connection drops. A parameter must be *absent* for the launch-flag default to
apply.

A value the sampler rather than the request check refuses — an oversized
`top_k` — fails the same way, one layer further in, from inside compiled
generation. One saved
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
