# Gropius — architecture decisions

Empirically verified on macOS 26.5.2 / M-series / Go 1.25.6, 2026-07-14.

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
