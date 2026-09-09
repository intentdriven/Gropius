# Changelog

All notable changes to Gropius are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

A release is a dated heading rolled out of the `[Unreleased]` section in a
reviewed pull request; on merge, the release workflow tags that commit and
publishes the build. Releases before this file existed are listed with their
GitHub release notes.

## [Unreleased]

### Fixed

- **A second account can serve a model the first account has already served.**
  Each model server writes a log named after the model, and while those logs
  sat in the shared folder the second account could not open one the first
  account had written — so that model would not start for it at all, and every
  model in a shared cache is one the other account has served. The same fault
  silently stopped it cleaning up model servers left behind by a crash. Logs
  and that record now live with each account's own settings.

- **Gropius starts again on a Mac with a shared model cache.** Two safeguards
  had come to refuse each other: the startup check that stops another account
  planting a link under one of Gropius' folders insisted every folder sit
  inside the shared one, and the decision that no account runs another
  account's programs had since moved the private Python runtime into each
  account's own folder. The check now covers the folders that are actually
  shared — the models and the download cache, which are still held to the
  permissions the installer sets — and lets each account's own folders be its
  own. A single-account install never saw this.

### Changed

- **Under a shared model cache, every account now keeps its own settings and
  its own model list.** They used to be one `config.json` and one
  `registry.json` beside the models, which worked for whichever account ran
  first and for no other: the second account could not read the first's
  settings, and the shared folder's sticky bit — the thing that stops one
  account deleting another's models — made its every attempt to save its model
  list fail. It could serve, and it could record nothing. Both files now live
  in the account's own folder, where it owns them; the models stay shared,
  which is what the shared cache is for. A HuggingFace token or an API key one
  account sets is now unreadable by the others, rather than shared by accident.
  On an account's first start after this change, its model list is rebuilt from
  the models already in the shared folder, so nothing is downloaded twice, and
  the settings it had kept in the shared folder are moved into its own — only
  ever its own: settings belonging to another account are left untouched and
  unread. Moved, not copied: a key left in the shared folder would still be
  readable there, and would come back into service under an older build.
  **A single-account install is unaffected.** In an existing shared install the
  second and later accounts genuinely behave differently, which is the point:
  they can now save what they change.

## [0.4.0] - 2026-09-08

### Added

- **An endpoint on a private network is marked as one.** The Connect tab lists
  every address this server answers on, and the one your mesh VPN gave this Mac
  looked exactly like the one that crosses the café's Wi-Fi. It now carries a
  mark beside it reading "private network". The mark says which network the
  address is on and nothing else: it names no product, and it does not say the
  connection is encrypted, or that only your own devices can reach it, because
  neither is something Gropius can see. Gropius serves plain HTTP everywhere.
  A Mac with no such network sees no marks and no change.

### Changed

- **The endpoint list no longer offers addresses the server does not answer
  on.** It used to list every address on the Mac whenever the bind was not
  loopback, which is right for the default wildcard bind and wrong for every
  other one. Under a bind to one specific address, that address is what is
  listed. This Mac's `.local` name is offered only when it resolves to
  something the server answers on. A bind that binds IPv6 loopback lists the
  IPv6 loopback URL rather than `127.0.0.1`, which such a server refuses.
  **A default install is unaffected.**

- **A release is proved before it is published.** The release workflow now runs
  the tagged tree's own `install.sh` against the artefacts it has just built —
  after they are packaged, before the build-provenance attestation and before
  the release is created — and fails the run if the installer does not install
  both apps. A tag whose installer is broken therefore publishes nothing at
  all. 0.3.0 shipped an installer that stopped on its first line of work
  because the chain built and published it without ever executing it, and the
  break was reachable only by someone installing from the tag. The gate also
  checks that the executable inside each installed bundle is byte-for-byte the
  one that run built, so an installer that quietly falls back to the previous
  release cannot pass it.
- **`install.sh` reads `GROPIUS_ASSET_DIR`, and stops unless
  `GITHUB_ACTIONS=true`.** The seam lets the release gate hand the script the
  artefacts of a release that does not exist yet. The reason for the refusal is
  the checksum: while the seam is honoured, the bundle and the `SHA256SUMS.txt`
  it is verified against both come from the named directory, so the
  verification shows only that the directory is self-consistent and says
  nothing about where its contents came from. Every step of the script still
  executes — the destination choice, the unpacking, the staged swap — but the
  integrity check is the CI one, not the one a user gets.

  What that refusal is worth, stated plainly: `GITHUB_ACTIONS` is an ordinary
  environment variable, and a caller who sets one can set two. Setting both
  still installs whatever the named directory holds, end to end — quarantine
  cleared, firewall rule added, app launched. This is a loudness control, not
  an integrity control. It keeps the seam from being reached by accident, by a
  stray export, or by a tutorial that tells someone to set it, and it makes the
  substitution announce itself on stderr before `Checksum OK.` is printed. What
  proves where a build came from is the published release and
  `gh attestation verify`.

### Fixed

- **A bind address written the way IPv6 requires is no longer treated as a LAN
  bind.** A server bound to `[::1]` is reachable from this Mac and nowhere
  else, and Gropius told the operator it bound a LAN address, generated an API
  key for it, and advertised it over Bonjour where nothing could reach it.
- **`localhost` is recognised however it is spelled.** `LOCALHOST`,
  `LocalHost` and `localhost.` each bind this Mac's loopback address and
  nothing else, and each was read as a LAN bind — so an API key was generated
  and saved, the log printed it under a security warning, Bonjour advertised a
  service nothing on the network could reach, and the panel said the server
  was reachable by anyone on your network. None of that was true.
- **A hand-edited bind address that is refused no longer discards the rest of
  your settings.** A `host` Gropius cannot bind used to send the whole of
  `config.json` back to the shipping defaults — your API key, port, pinned
  models, memory budget and statistics retention with it — under a message
  saying the file could not be read. The bind is narrowed to loopback and
  everything else is kept, and the message says what actually happened.
- **A `host` that is a number rather than a name is refused, in decimal, octal
  and hexadecimal alike.** `"host": "0"` bound every interface on the Mac while
  the Connect tab offered a single address nothing could use. So did `"0x0"`
  and `"0.0.0.0x0"`: an address in hexadecimal has letters in it, and the first
  version of this check asked only whether the value contained a letter, so the
  hexadecimal spellings walked straight through it. Those are the three ways
  macOS reads a number as an address, and a value written in any of them, in
  any position, is now refused.
- **A `host` written as an IPv6 address without its brackets is refused rather
  than left to fail at start-up.** `"host": "::1"` is not a bind Gropius can
  make — the address it builds is `::1:11535`, which is not an address — so the
  server logged "cannot listen" and quit. A hand-edited typo now narrows the
  bind to loopback and says so, and the app starts. Write `"[::1]"`, which
  binds.

- **A standard account can install the server.** The installer asked for
  administrator rights with `sudo`, at the very end of its work. `sudo` can
  only ever accept the invoking user's own password, and a standard
  (non-administrator) account is not in the sudoers set at all, so no password
  that person could type would do — and they found out only after the download
  and after the app had been placed. The request now comes through the standard
  macOS authentication panel, which takes an administrator's name **and**
  password, so someone else can enter theirs. It is raised before anything is
  downloaded or written, so declining costs nothing and leaves nothing behind.
  The firewall grant is keyed to the bundle's code identity, and the bundle is
  ad-hoc signed, so the panel appears again on every update rather than once.
- **A failed rename no longer leaves the Mac with no application.** The staged
  swap deleted the installed bundle before moving the new one into place and,
  if that move failed, deleted the staged copy too — the exact outcome staging
  exists to prevent, reachable by one ordinary rename failure with no attacker
  involved. The old bundle is now renamed aside, the new one moved in, and the
  set-aside copy deleted last; a failure puts the old bundle back. The staging
  directory also takes an unguessable name instead of one built from the
  process id.
- **A failed checksum says what went wrong.** The verification's output went to
  `/dev/null`, so a corrupt download, a truncated checksums file and an HTML
  error page were reported identically. The output is now printed.

### Security

- **The installer no longer trusts `PATH` for the commands it depends on.**
  `curl | bash` runs with the invoking user's `PATH`, and an ordinary
  developer `PATH` puts user-writable directories ahead of `/usr/bin`. Every
  command the installer resolved by name was therefore substitutable by
  unprivileged code already running as that user. `shasum` is the sharp one:
  that call is the only integrity control in the whole install path, so a
  planted shim defeated the verification silently. `osascript` is the other,
  because a shim there receives the elevation and can draw its own
  authentication panel to harvest an administrator password — a risk the move
  to a system panel creates rather than inherits, since it teaches the reader
  that a panel is the legitimate way to install. `shasum`, `osascript`,
  `ditto`, `xattr`, `mktemp` and `pgrep` are now invoked by absolute path, and
  a test refuses a bare invocation of any of them.

## [0.3.1] - 2026-09-08

### Fixed

- **The one-line install runs again.** `install.sh` read `$DEST…` as the
  variable name plus the first byte of the ellipsis that follows it, so under
  `set -u` it stopped with `DEST?: unbound variable` after reporting where it
  would install and before copying anything. The line runs on every path, so
  this affected every user rather than only the non-administrator case it was
  written for. Shipped in 0.3.0 and fixed immediately afterwards; the install
  command on the website fetches the script from the default branch, so it was
  serving the fixed script from the moment it merged.

- **The website no longer offers a download that macOS refuses.** Following the
  download button produced "Apple could not verify Gropius is free of malware",
  whose only offered action is Move to Bin: a browser download is quarantined,
  and the bundles are ad-hoc signed rather than notarized. The page now leads
  with the install command, which verifies the archive against the published
  checksums and clears the quarantine, and keeps the link to the repository
  beside it. Release assets are still named with their sizes; none is linked.


- **The landing page no longer scrolls sideways.** A grid track held at the
  intrinsic width of the install one-liner — 894 pixels of unbreakable
  command — and pushed the column beside it off the page, so the release list
  was clipped on a desktop and the page scrolled sideways at every width
  measured between 375 and 1440 pixels. It also meant the rule that exists to
  scroll that command inside its own box had never once engaged.

- **The right-hand column gives way on a narrow screen.** The mark steps aside
  below 820 pixels instead of reordering above the headline, where the first
  thing on a phone was an ornament rather than the sentence saying what
  Gropius is; the fact list stacks below 560 pixels rather than wrapping its
  values beside a label column.

### Added

- **One click selects a whole install command.** Clicking a command on the
  landing page takes the entire line, ready to copy, rather than the word
  under the pointer.

## [0.3.0] - 2026-09-08

### Changed

- **A server that binds a LAN address now generates an API key rather than
  running open.** The default bind reaches every machine on your network, and
  the only thing standing between a fresh install and an open endpoint was a
  warning nobody running headless ever reads. A key is now generated, saved and
  printed before the server answers anything. Existing clients need that key:
  it is in Settings, and you can change it or clear it there. If the key cannot
  be generated or saved, the server binds loopback only rather than continuing
  open. **A loopback-only install is unaffected.**

- **Eviction grace needs an API key on a LAN-exposed server.** The queue of
  requests waiting for memory is now shared out per key, so one client filling
  it costs that client its own share and nobody else's. Without a key there is
  no way to tell callers apart, so grace cannot be switched on. **A
  loopback-only install is unaffected** and keeps the feature with no key.

- **Each account gets its own copy of the MLX runtime.** Models are still
  shared between accounts on one Mac, which is what the shared cache is for.
  The interpreter and its packages are not: whoever installed a shared runtime
  owned those files and could change them, and every other account ran the
  result. The first launch on each account provisions its own runtime.

### Fixed

- **Installing without administrator rights works.** `/Applications` is
  writable only by administrators, so a standard account could not install at
  all — which is exactly the account that most often wants the chat client on a
  shared Mac. Gropius now installs to your own Applications folder when the
  system one is not writable, and says which it used.

- **An interrupted upgrade no longer leaves you with no app.** The installer
  removed the installed copy before writing the new one, so a copy that failed
  part way — a full disk, a locked file — left nothing behind. The new copy is
  staged and swapped into place.

- **Large prompts are no longer cut off at ten minutes.** A model reading a
  long prompt was mistaken for a stalled one, and the request failed with a
  message blaming the model server. The wait now scales with the prompt: short
  prompts are unchanged, a very long one gets around half an hour, and the
  message says the work may still be running and that retrying makes it slower.
  `upstream_header_timeout_sec` in Settings overrides it.

- **A second copy of Gropius can no longer be impersonated.** Deciding whether
  the process already on the port was your own Gropius relied on a token stored
  on disk and served to any local caller. Another account on the same Mac could
  read it, wait for your server to quit, take the port, and have your next
  launch quietly route your prompts through theirs. Ownership is now proved by
  a single-use challenge that stores nothing and repeats nothing.

## [0.2.1] - 2026-09-08

### Fixed

- **The one-line install from the README runs again.** `install.sh` read
  `$APP…` as the variable name plus the first byte of the ellipsis that
  follows it, so under `set -u` the script stopped with `APP?: unbound
  variable` before it downloaded anything. Every shell tested was affected:
  the macOS stock `/bin/bash` 3.2, `/bin/sh`, and Homebrew bash 5.3.9.

## [0.2.0] - 2026-09-07

### Added

- **The application is renamed.** The server is Gropius and the chat client is
  GropiusChat. The bundle identifiers, the Bonjour service, the environment
  variable, the module path and the released asset names all follow.


- **Eviction grace**, in Settings, off until you turn it on. Gropius unloads the
  least recently used idle model to make room for a new one, and that rule
  treats a model idle for one second the same as one idle for an hour — so on a
  Mac two people share, their models take turns evicting each other in the
  pauses between turns, each paying a reload of seconds to minutes afterwards.
  Switch grace on and a model is protected for a set interval after it finishes
  a request: a request that needs its memory waits for a model to fall idle
  rather than taking one that has only just finished. It is served as soon as a
  resident model has been idle for that interval, or as soon as its own wait has
  passed it and a model is between requests — which is what stops a trickle of
  short requests to one model denying another client indefinitely. Waiting
  requests are served oldest first, and once anything is waiting, every request
  for a model that is not already in memory joins the queue behind it —
  including one there is room for, since free memory belongs to whoever has
  waited longest. Raising the memory budget, or removing a pin, wakes them:
  every request the raise fits is served on it, oldest first, without any model
  unloading or any request ending. A request that could never fit, one that
  arrives when the short queue is already full and needs a model unloaded, and
  the models loaded at start-up by Preload never wait at all; one that arrives
  at a full queue and needs nothing unloaded is served rather than refused,
  since the queue it cannot join is not waiting for what it needs. The maximum
  wait may not be shorter than the protection, which would refuse a waiting
  request before its own wait could override that protection. If nothing frees
  up within the maximum wait, the request gets the same refusal it would have
  had immediately, now saying how long it waited. The protection may not be
  longer than the idle timeout when one is set, since the idle timeout would
  otherwise unload the very model a request is waiting on. A save that breaks
  that is refused naming both figures; a settings file edited by hand is
  shortened to the timeout rather than refused; and because a raised idle
  timeout only takes effect at a restart while a protection takes effect at
  once, a protection saved beside a raised timeout runs at the timeout still in
  force until you restart. Defaults are
  120 seconds of protection and a 300 second maximum, and both apply the moment
  they are saved. My Models says how many requests are waiting for memory. See
  [Give a busy model a moment before it is evicted](docs/eviction-grace.md) and
  [Why a request waits instead of taking the memory](docs/eviction-grace-explained.md).
- **Two response headers on completions**, `X-Gropius-State` (`warm` or
  `waited`) and `X-Gropius-Queue-Time` (whole milliseconds), on the answer and
  on the 503 a request gets when this Mac has no memory for its model. They
  count the wait for room, the wait for a cold model to load and the wait for a
  slot on a busy one, and stop where generation begins. Served only on an
  install with an API key set, the same rule the models list applies to
  residency: an open server tells a network client neither what is warm nor who
  is busy. See [the response header reference](docs/response-headers.md).
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
  a script. There are four kinds of record line: a request, a model server
  becoming
  ready, a model server leaving memory with the reason it left, and a record
  of the settings in force so a change in the figures can be told from a
  change in the settings that produced them; two further kinds belong to the
  summary described below. Settings gains two limits — how
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
  seconds of records and leaves the line being written half-finished; no record
  is forced to the disk, because a disk in the path of every answer costs
  more than the figures are worth, and the next start closes the half-finished
  line off before appending to it. (The summary described below is forced to
  the disk, once per drop rather than once per request.) The format, the
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
  request's own row. The boundary stays the Mac rather than one account, and
  these tables widen what that means: the record files are the serving
  account's own, but the tab reads them back to whoever has the panel open, so
  on a Mac several people share, any of them sees months of which models served
  what rather than the last thousand requests alone. Days and hours are this
  Mac's own, and only a removal that was an eviction is counted as one — an
  idle reap, an unload, a crash and a shutdown are removals. The line above the tables says what the figures cover
  and what bounded them: the range, whether it was narrowed to the widest one
  view covers, whether the reading stopped at its record bound, how many lines
  could not be read, and that nothing recorded while the switch was off appears.
  With recording off the tab says so and shows no figure, as it already does. A
  reading covers at most a year and at most a million records, at most two run
  at once — a third is refused and the panel says how long to wait — and a
  reader who closes the panel part-way through one stops being paid for. The
  oldest row of a range is marked **part of the day**, since a range picked at
  four in the afternoon begins at four rather than at a midnight, and the line
  above the tables says when the records do not reach back as far as the range
  does.
  See [Understanding the historical views](docs/statistics-explained.md).
- **A summary of what the store drops.** Before either retention limit removes
  a file of records, Gropius folds that file into a coarse summary and keeps
  it: one line per model per day, holding how many requests that model served,
  how many tokens went in and out, the day's totals for each of the four
  timings, how the requests ended, and how often the model was loaded or left
  memory. It carries no figure for any single request and nothing the detailed
  records did not, it lives beside them in the same folder under the same
  switch and the same owner-only permissions, and it takes about a thousandth
  of the room the detail did — so the long view the records were turned on for
  survives the records themselves. A day already summarised is extended rather
  than written again. The summary is the one thing Gropius forces to the disk,
  and it does so before the records it counts are removed, so a power cut in
  the middle of a drop can leave the records and their summary both and never
  neither. The months limit does not remove summaries; what bounds them is a
  twentieth of the size limit, with the oldest days going first and the most
  recent day always kept, which at the 200 MB default is three years of them
  for ten models. The **Statistics** tab shows those days under **Earlier
  days** and says which of them have no detailed records left at all, so a
  month whose records have been dropped no longer looks like a month with no
  traffic in it. **Clear records** removes them with everything else. If
  Gropius cannot summarise what it is about to drop — an unreadable file, a
  summary it cannot write — it keeps the records rather than deleting them
  uncounted, says so on the Settings page and explains why in its own log;
  and if it is over its size limit and still cannot, the oldest file goes
  without a summary and Settings counts what that cost, because a full disk is
  exactly what stops a summary being written and a store that could not free
  its own room would make a full disk permanent. See [Retention](docs/statistics-store-reference.md#retention).
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
- A public page at https://intentdriven.sh/Gropius, carrying the download of
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

- **Breaking:** an existing install does not find its own data after the
  rename. Before first launch, rename the support directory under
  `~/Library/Application Support` to `Gropius`, and rename the install marker
  inside its `venv` directory to match, so the private Python and MLX runtime
  is not rebuilt. Without both, the app starts empty and downloads its models
  again.

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

See the v0.1.1 release.

## [0.1.0] - 2026-07-29

See the v0.1.0 release.

