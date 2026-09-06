# Changelog

All notable changes to Gropius are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

A release is a dated heading rolled out of the `[Unreleased]` section in a
reviewed pull request; on merge, the release workflow tags that commit and
publishes the build. Releases before this file existed are listed with their
GitHub release notes.

## [Unreleased]

### Added

- **Merge system messages**, a per-model setting in Settings that is off until
  it is switched on. Some models refuse a conversation whose instructions are
  not all at the top, which breaks any assistant that repeats its instructions
  as the conversation goes on. For a model it is switched on for, Gropius
  gathers each request's system messages into the first one, in the order they
  were sent and separated by a blank line, and passes every other message and
  field on untouched. The merged message is the conversation's own first system
  message with its text extended, so anything else that message carries stays
  with it. It is the one thing Gropius reads of a prompt: it reads the system
  messages of requests to that model and nothing else, and keeps none of what
  it reads — nothing of a request's contents reaches the log, the model
  server's command line or any file. A request merging cannot rebuild exactly
  is passed on unrewritten instead. See
  [Merge system messages for a template-strict model](docs/system-message-merging.md);
  the rule this runs under is
  [an architecture decision](.abcd/development/decisions/adrs/2609061610102325-the-gateway-may-rewrite-prompt-content-only-to-merge-system.md),
  and an architecture test holds the reading to the single file that does it.
- Every ready model on `GET /v1/models` carries its maximum context in tokens,
  under both `context_length` and `max_model_len`, so a client can size its
  prompts instead of discovering the limit by failure. The figure is the
  architectural maximum read from the model's own configuration, not the
  window a given Mac can serve, and nothing is enforced by it; a model whose
  configuration declares no range is listed as before. The control panel shows
  the same figure on each model card, and
  [docs/models-list.md](docs/models-list.md) is a new reference page for every
  field the models list serves.
- A public page at <https://intentdriven.sh/Gropius>, carrying the download of
  the current release, the one-line installer and a link to the repository. Its
  title, tagline and pitch render from the project's canonical identity block
  and the rest of its copy from README.md and the getting-started guide, so the
  page says what the repository says. The release chain renders and deploys it;
  `make site` renders it locally.
- The page names the current release: its version, the day it was published,
  every file it carries with that file's size, and a link to the checksums to
  verify a download against. The release run reads the release GitHub flags as
  latest and renders the page from it, so the facts follow the release without
  anyone editing the page; a release that cannot be read costs the page those
  facts and nothing else. The page still carries no version of its own, and its
  download button still resolves the latest release. It also ships a
  content-security policy that admits nothing but its own files and the web font
  service.
- Settings holds the sampling defaults the whole machine serves with —
  temperature, top-p, top-k, min-p and a completion-token budget — with an
  optional override per model, so a fleet of clients that never set a
  temperature no longer has to be reconfigured one by one. A request that
  carries its own value still wins, and no request body is touched: the
  values are given to each model server as it starts, so a change reaches a
  model the next time it loads and the panel names the loaded models that
  must load again. Sampling is documented in three pages —
  [docs/sampling-defaults.md](docs/sampling-defaults.md) for setting one,
  [docs/sampling-reference.md](docs/sampling-reference.md) for the parameters
  and their ranges, and
  [docs/sampling-explained.md](docs/sampling-explained.md) for why there is no
  seed and why reproducibility comes from a fixed temperature.
- On an install with an API key configured, every entry on `GET /v1/models`
  also reports whether that model is `loaded`, `loading` or `not_loaded`, how
  many requests are in flight for it, and when it was last used — so a client
  can send its work to a model that is already warm instead of triggering a
  load it did not know about. The values are a snapshot taken as the list is
  built and reserve nothing. Where no key is set the listing is unchanged, and
  [docs/models-list.md](docs/models-list.md) states the values, the
  keyed-install condition, and the memory budget and eviction rules they move
  under.

### Fixed

- A model named with different capitalisation than the registry records — a
  hand-edited `preload` entry, or a call to the load endpoint — no longer
  starts a second model server for the same weights alongside the first, each
  charged against the memory budget. Repo ids now fold through one rule
  everywhere they are used as a key.

### Changed

- The app icon is redrawn as the four-form mark — a yellow triangle, a grey
  square, a blue square and a red circle on a dark tile — and `build/icon.svg`
  is now its source. The shipped icon used different shapes and colours, so the
  Dock and menu-bar icon changes with this release. The landing page draws the
  same mark, so the icon on the page is the one that arrives in the Dock.
- The server's request log records the method, path, status and duration of a
  request, and never the client's network address. A handler panic is reported
  the same way, so no line the server writes identifies a caller.

### Removed

- **Breaking:** support for every macOS below 26. Gropius and the GropiusChat
  client both require macOS 26; the one-line installer refuses an older Mac
  before it downloads anything, and the bundles declare the same minimum, so
  macOS refuses to launch them there.

## [0.1.2] - 2026-09-06

### Security

Shared-cache mode (`make install-shared`) treats every other local account as
untrusted. Every state file in the shared root is now read only when it is a
regular file, without following symlinks, and with a size cap, so a planted
FIFO or link can no longer hang startup or a model launch. The process-group
ledger is trusted only when this account owns it. Removing a model deletes the
directory derived from its id, never a path read from the registry, and never
through a symlinked parent. The layout directories, download destinations and
per-model log files are created without following planted symlinks. The
shared root is adopted only when an administrator created it. The Python
runtime is refused unless it is a regular file, not writable by other
accounts, and owned by the account running it or by the administrator. With an
API key set, a loopback client must address `localhost` or `127.0.0.1` or send
the key, closing a DNS-rebinding read of the model list.

### Fixed

- A model id that differs from a known one only by letter case reuses that
  model instead of creating a second entry over the same directory, so
  removing either no longer deletes the other's weights.
- A repository whose files differ only by letter case is refused up front,
  instead of two downloads racing over one file.
- A download whose weight index names a shard the repository does not contain
  is marked failed instead of advertised as ready and failing on every load.
- A registry entry with an invalid id is skipped on load rather than served as
  a model that cannot be removed.

### Changed

- The installer verifies a download against the checksums file published on
  the same release, and every release asset carries a GitHub build-provenance
  attestation; minisign is no longer required.
- A symlinked `config.json` is refused and the server starts locked to this
  Mac; a hand-made shared cache directory is ignored. Both are explained in
  the getting-started guide.
- Releases are cut from this file: the newest dated heading is tagged and
  published on merge.

## [0.1.1] - 2026-07-29

See the [v0.1.1 release](https://github.com/intentdriven/Gropius/releases/tag/v0.1.1).

## [0.1.0] - 2026-07-29

See the [v0.1.0 release](https://github.com/intentdriven/Gropius/releases/tag/v0.1.0).

[Unreleased]: https://github.com/intentdriven/Gropius/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/intentdriven/Gropius/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/intentdriven/Gropius/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/intentdriven/Gropius/releases/tag/v0.1.0
