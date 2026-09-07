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

- **A memory budget you set**, in Settings, beside the idle timeout and decode
  concurrency. How much of this Mac Gropius fills with loaded models was fixed
  at 60% of its memory with no field to change it, so a Mac that does nothing
  but serve models and a laptop someone also works on got the same answer — and
  a 45 GB coder and a 29 GB reviewer, charged about 89 GB together, would not
  sit in memory together on a 128 GB machine that had the room. Type a figure in
  gigabytes and it applies at once, with no restart: the next model to load is
  measured against it, and the Search tab immediately hides the models that no
  longer fit and shows the ones that now do. The panel shows what share of this
  Mac the budget is and what the models in memory are using of it. Leave the
  field blank and the default stands, worked out on the Mac that is running, so
  a settings file copied to a smaller Mac gets that Mac's default rather than
  the first one's figure. Lowering the budget unloads nothing — no model is
  taken away at the moment you press Save — and the panel says the machine is
  over its budget until those models go. What a save refuses is a change that
  makes matters worse: one that raises the budget past what this Mac has, naming
  what it has, and one that lowers it under what the pinned models need, naming
  their sum. A figure that arrives already over the machine — a settings file
  carried from a larger Mac — is applied and reported instead, so it never
  stands between you and saving an unrelated setting, and models are held to the
  memory that exists whatever it says. A budget claiming most of the machine is
  saved with a warning rather than refused: a model is charged the weights it
  loads and not the cache a long prompt adds, so a Mac committed in full on
  paper can still run out under load. One too small to hold any model on this
  Mac is reported too. See
  [Set how much memory models may use](docs/memory-budget.md) and
  [Why there is a memory budget](docs/memory-budget-explained.md).
- **Request statistics**, a switch in Settings that is off until it is turned
  on. While it is on, Gropius records one content-free row per request it
  serves — the model, when the request arrived, how it ended, whether it
  streamed, the model server's own token counts, the time to the first
  streamed chunk, the total duration, and how long it waited for a free slot
  or for the model to load — and a new **Statistics** tab shows those rows and
  each model's totals, loads and evictions, so two quantisations of the same
  model can be compared by their numbers. It never records a prompt, an
  answer, an API key or the address of the client: the part of Gropius that
  keeps the figures is never handed a request, its headers or its connection,
  and the only text it can be given is the id of a model already on this Mac.
  The live figures are held in memory, a restart empties them, and turning the
  switch off empties them at once; the records themselves are kept on this Mac
  in files that outlive the process (see the entry below). The boundary is the Mac rather than one account: the
  control panel asks for no password and answers every account on this Mac by
  design, so where several people log in, any of them can turn the switch on
  and read what it holds — and the panel says **Recording** beside the server
  status for as long as it is on. Nothing leaves the Mac, under
  [the standing decision that no telemetry ever will](.abcd/development/decisions/adrs/2609061503319212-no-public-telemetry-local-telemetry-only-as-a-strict-opt-in.md).
  A streamed answer carries no token counts unless the request asks for them,
  so while recording is on Gropius asks the model server on the client's
  behalf and removes the extra chunk before relaying the answer when the
  client did not ask — what a client receives is the stream it would have
  received. Switching this on never changes any model server's own log level,
  and two architecture tests hold that apart. See
  [Record request statistics on this Mac](docs/request-statistics.md).
- **Pinned models**, a list in Settings of the models that stay in memory.
  Gropius keeps as many models loaded as its memory budget allows and unloads
  the one used longest ago when a request needs the room, which on a shared Mac
  takes the models that matter most — an agent is idle between its turns. A
  pinned model is never chosen to be unloaded and the idle timeout does not
  touch it; a request for a different model that would need its memory is
  refused instead, in a message that says only that there is not enough memory
  and never which models are protected. Pins apply the moment they are saved,
  with no restart, and a save that adds a pin the memory budget cannot hold is
  refused with both figures. A set that arrives from another Mac and no longer
  fits is reported rather than refused, so it never stands between you and
  saving an unrelated setting. Pinning is separate from preloading and does a
  different thing: preloading loads a model at start-up and leaves it as
  evictable as any other, pinning protects a model but loads nothing. Your own
  Unload still works on a pinned model, and the pin stays. On an install with
  an API key, the models list carries a `pinned` field beside the residency
  ones. See [Pin a model so it stays in memory](docs/pinning-models.md).
- **A durable store for those records.** With recording on, each record is
  also written to a `stats` folder inside your own account's Gropius data
  folder — the one place that does not move to the shared folder when the
  model cache is shared, because a shared folder is writable by every account
  on the Mac: append-only
  JSON Lines, one object per line, rotated into dated files, every line
  stamped with the version of the format it was written under so a later
  Gropius still reads an older file. Any tool reads it — `jq`, a spreadsheet,
  a script. There are four kinds of line: a request, a model server becoming
  ready, a model server leaving memory with the reason it left, and a record
  of the settings in force so a change in the figures can be told from a
  change in the settings that produced them. Settings gains two limits — how
  many months to keep and how many megabytes the records may use, the size
  limit always winning — and shows beside them the date the records reach back
  to and the room they use. **Clear records** removes them; switching
  recording off does not. The folder and its files are the account's own, and
  Gropius refuses to write records into a folder any other account on this Mac
  could write to. On a Mac with the shared model cache, one process serves
  every account, so the records belong to the account running the server and
  cover every request that server handled, under that account's opt-in. A
  request never waits on the disk: records are queued and written by one
  goroutine, and a burst the disk cannot keep up with is counted and shown
  rather than allowed to slow an answer. A crash or a power cut costs the last few
  seconds of records and leaves the line being written half-finished; nothing
  forces a write to the disk, because a disk in the path of every answer costs
  more than the figures are worth, and the next start closes the half-finished
  line off before appending to it. The format, the
  retention rule and the location are
  [an architecture decision](.abcd/development/decisions/adrs/2609061610107154-statistics-store-format-json-lines-size-rotated-per-account.md),
  and every field is described in
  [Reference: the request statistics store](docs/statistics-store-reference.md).
- **The Statistics tab reads those records back**, over a range you choose —
  seven, thirty or ninety days, or everything still kept — as four tables under
  the live view: tokens per day for each model with that model's share of every
  token in the range, how long each model's answers took as the median,
  ninetieth and ninety-ninth percentile of both the time to the first token and
  the rate afterwards, the same times to first token counted into buckets so a
  model that is quick most of the time and slow the rest can be told from one
  that is evenly slow, and the local day hour by hour with the models evicted to
  make room for another and the model servers started. Tables, not charts: the
  figures are exact and the panel carries no charting library. The reading and
  the arithmetic happen in Gropius, on the control plane that answers this Mac
  and nothing else, so the browser is handed sums and counts and never a
  request's own row. Days and hours are this Mac's own, and only a removal that
  was an eviction is counted as one — an idle reap, an unload, a crash and a
  shutdown are removals. The line above the tables says what the figures cover
  and what bounded them: the range, whether it was narrowed to the widest one
  view covers, whether the reading stopped at its record bound, how many lines
  could not be read, and that nothing recorded while the switch was off appears.
  With recording off the tab says so and shows no figure, as it already does.
  See [Understanding the historical views](docs/statistics-explained.md).
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
