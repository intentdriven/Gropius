# Context — orientation for a fresh session

Gropius is a Go menu-bar app that turns one Apple Silicon Mac into a shared
local-inference server: it downloads MLX models from HuggingFace and serves
them over an OpenAI-compatible API to the local network. `cmd/gropius` is the
entry point; the packages under `internal/` are described in `AGENTS.md`,
which also carries the verified build/test commands.

Live status is not recorded here: open work lives in the issue ledger under
`.abcd/work/issues/` (folder membership is the status signal). Durable,
empirically verified design constraints live in
`.abcd/development/decisions/DECISIONS.md`.

## Sharp edges

- **The request's `model` field is a load instruction, not a label.** The
  gateway rewrites the client's friendly model name into the backend's exact
  `--model` path on every proxied request; a name that misses sends mlx-lm to
  HuggingFace to download it. This is the single most important routing rule.
- **Child processes get `HF_HUB_OFFLINE=1` and an existing `HF_HUB_CACHE`.**
  A missing cache directory makes `mlx_lm.server` raise `CacheNotFound` and
  return an empty model list.
- **Shared-cache mode** (`make install-shared`) uses `/Users/Shared/Gropius`
  with directory mode `3775` (setgid + sticky) and file modes left to the app
  (secrets and logs written `0600`). Several deferred security findings only
  bite in this mode — check the ledger before changing anything here.
- **Firewall:** a locally built, ad-hoc-signed binary run from a new path is
  silently blocked for LAN traffic (loopback still works) — re-run
  `make allow-firewall`. The Makefile pins a stable codesign identifier for
  exactly this reason.
- **The serving Mac must never sleep** — a sleeping Mac does not wake for
  network traffic, and looks "down" to remote clients.
- **`abcd identity` cannot see the landing page until it is rendered.** The
  `landing-hero` surface names `site/Gropius/index.html`, which is a build
  artefact and untracked, so on a plain checkout the surface reports *absent*
  and the check stays green whatever the page says. Run `make site` first to
  make it a real check. What holds the page to the identity block on every run
  is `internal/sitetest`, which renders the page and applies that surface's own
  patterns to the output.
- `make test` always runs with the race detector; treat `-race` findings as
  failures, not noise.
