# Gropius — architecture decisions

Empirically verified on macOS 26.5.2 / M-series / Go 1.25.6, 2026-07-14;
items 7-9 read from the pinned mlx-lm 0.31.3 source on 2026-09-06 (see
`../research/notes/2026-09-06-mlx-lm-sampling-launch-flags.md`).

## Verified by spike (not assumed)

1. **`mlx_lm.server --model <plain directory>` works.** A directory of files fetched
   over plain HTTP loads fine. Gropius therefore does **not** reproduce
   huggingface_hub's blobs/snapshots/symlinks cache format. Models live at
   `<root>/models/<org>/<name>/` as ordinary files.
2. **`HF_HUB_CACHE` must point at an existing directory.** `mlx_lm.server` calls
   `scan_cache_dir()` when serving `/v1/models`; if the directory is missing it
   raises `CacheNotFound` and the client sees an empty 200. We always create it
   and always pass the env var.
3. **The request's `model` field is a load instruction, not a label.** Send a name
   that isn't the exact `--model` value and mlx-lm tries to *download that repo
   from HuggingFace*. The gateway therefore rewrites the client's friendly model
   name into the backend's exact `--model` path on every proxied request. This is
   the single most important routing rule in the system.
4. **`HF_HUB_OFFLINE=1`** on the child process guarantees inference never reaches
   the network.
5. Streaming (SSE), `/health`, and `--decode-concurrency` (real batching) all work.
6. Thinking-model control is `chat_template_kwargs` in the request body
   (`--chat-template-args` is the CLI spelling of the same thing).
7. **The sampling parameters that can be defaulted are exactly five.** mlx-lm
   0.31.3 takes `--temp`, `--top-p`, `--top-k`, `--min-p` and `--max-tokens`
   when it starts, and applies each to any request that omits the field.
   Everything else a request may carry — the repetition, presence and
   frequency penalties, `xtc_*`, `logit_bias`, `logprobs`, `seed` — is
   per-request only. There is no `--seed`.
8. **An out-of-range value does not fail the launch; it kills every request
   that omits the parameter.** `argparse` range-checks nothing, so the process
   starts and looks healthy. The server then validates the *effective* value
   of each request — the body's where there is one, the flag's otherwise — and
   `validate_model_parameters` raises **uncaught** out of `do_POST` (only the
   `Content-Length` parse sits in a `try`). The socket closes with no HTTP
   response, and the gateway turns that into `502`. Requests carrying their
   own value keep working, so the symptom is a 502 for some clients and not
   others on a server that reports itself as running. Gropius therefore
   validates every sampling default against the server's own ranges before it
   can be saved.
9. **`top_k` must be below the model's vocabulary size.** `sample_utils.
   apply_top_k` refuses anything else, and it raises from *inside* compiled
   generation, one layer deeper than the request check. A vocabulary size is
   not knowable when a setting is saved, so Gropius caps `top_k` well below
   the smallest an MLX model ships.

## Decisions

- **Python is ours.** Homebrew's Python is 3.14 and has no MLX wheels. `uv` installs
  a private CPython 3.12 + `mlx-lm` under `<root>`. Uninstall is `rm -rf <root>`.
- **One `mlx_lm.server` child per model**, each pinned with `--model` on its own
  loopback port. Go owns routing, the RAM budget, and LRU eviction. mlx-lm's
  per-request model switching is a *hot-swap that evicts the resident model* — using
  it behind a multi-client gateway would thrash weights in and out of RAM.
- **Menu bar via `fyne.io/systray`; control panel is an embedded web UI.** Wails v3 is
  still alpha; this keeps the shell boring and the UI reachable from a browser.
- **mDNS via `github.com/brutella/dnssd`** — the maintained Go responder.
  `grandcat/zeroconf` is abandoned. `NSBonjourServices` in Info.plist is mandatory:
  omit it and advertising fails *silently*.
- **Cross-account sharing by singleton election.** The daemon tries to bind the port;
  on `EADDRINUSE` it becomes a client of the already-running instance. One server,
  one GPU, N user accounts. The shared model cache lives in `/Users/Shared/Gropius`
  (setgid, group-writable) so a second account does not re-download gigabytes.

## Open security note

The default is **LAN-exposed with no authentication**, at the user's explicit request.
An API-key path (bearer token, constant-time compare) is fully implemented and is one
toggle away; the control panel warns while auth is off. The safer default would be to
require a key whenever the bind address is non-loopback.
