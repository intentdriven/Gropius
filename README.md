<div align="center">

  <h1>Gropius</h1>

  <p>Download MLX models on your Mac and serve them to the rest of your network — an OpenAI-compatible endpoint, in a menu-bar app.</p>

  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-green" alt="License: MIT"></a>
  <img src="https://img.shields.io/badge/status-experimental-orange" alt="Status: experimental">
  <img src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white" alt="Go 1.25">
  <a href="https://claude.ai/claude-code"><img src="https://img.shields.io/badge/Built_with-Claude_Code-3B5CE7?logo=anthropic&logoColor=white" alt="Built with Claude Code"></a>
  <br />
  <img src="https://img.shields.io/badge/Apple_Silicon-000000?logo=apple&logoColor=white" alt="Apple Silicon">
  <img src="https://img.shields.io/badge/MLX-Metal-informational" alt="MLX / Metal">

</div>

---

**Gropius** turns one Apple Silicon Mac into a shared local-inference server.
Browse and download [MLX](https://github.com/ml-explore/mlx) models from
HuggingFace, and serve them over an OpenAI-compatible API to every other machine
and user account on your network. Anything that talks to ChatGPT can talk to your
Mac — just change the base URL.

It manages its own runtime: on first launch it installs a private Python and MLX
under `~/Library/Application Support/Gropius` and never touches your system
Python. Uninstalling is deleting one folder.

## Status

Experimental. Runs and is tested end-to-end on macOS 26 / Apple Silicon.
Cross-machine LAN use works; TLS and notarized distribution are not yet included.

## Features

- **Model browser** — search the `mlx-community` org, download with live progress,
  resume interrupted transfers.
- **OpenAI-compatible server** — `/v1/chat/completions`, `/v1/completions`,
  `/v1/models`, streaming included. Drop-in for any OpenAI SDK.
- **Context window published** — the models list gives each model's maximum
  context, so a client can size its prompts instead of discovering the limit
  by failure ([reference](docs/models-list.md)).
- **Residency published** — with an API key set, the models list also says which
  models are loaded, how busy each one is and when it was last used, so a client
  picks the warm model instead of triggering a load
  ([reference](docs/models-list.md)).
- **Runs many models** — one process per model, with an LRU memory budget so a
  request for a second model evicts an idle one instead of OOMing the machine.
  The budget is yours to set: give a dedicated Mac most of itself, or a shared
  laptop less ([how to](docs/memory-budget.md)).
- **Pinned models** — the models you rely on stay in memory: never evicted to
  make room, never reaped by the idle timeout
  ([how to](docs/pinning-models.md)).
- **Eviction grace, opt-in** — on a Mac two people share, a model that answered
  a moment ago is not torn out for the next request: that request waits a
  bounded time for something to fall idle, and is told that it waited
  ([how to](docs/eviction-grace.md),
  [why](docs/eviction-grace-explained.md)).
- **Sampling defaults** — one place to say what an omitted `temperature` means
  for the whole machine, with an optional override per model; a request that
  sets its own value still wins.
- **Request statistics, opt-in** — off until you turn it on: a content-free
  record of each request's model, token counts and timings, shown per model in
  the control panel and kept on this Mac. The records are plain text, one line
  of JSON each, kept for as many months and as many megabytes as you say
  ([how to switch it on](docs/request-statistics.md)). The Statistics tab also
  reads those records back as history: tokens per day by model with each
  model's share, how long requests took, the spread of those times, and when
  models were evicted and reloaded
  ([what the views mean](docs/statistics-explained.md)).
- **Network-shared** — bind the LAN, discoverable over Bonjour (the chat client
  lists the servers it finds, so nobody has to guess an address), optional API
  key. A mesh VPN reaches it from further away, with the same steps and a
  different address ([how to](docs/mesh-vpn.md)).
- **Multi-account** — other user accounts on the same Mac share one copy of each
  model on disk and on the GPU.

## Install

One line — installs `Gropius.app` (the menu-bar server) to `/Applications`, allows
it through the firewall, and launches it:

```sh
curl -fsSL https://raw.githubusercontent.com/intentdriven/Gropius/main/install.sh | bash
```

The server needs administrator rights once, to allow itself through the macOS
firewall so other machines can reach it. The installer asks for them at the
start, through the standard macOS authentication panel: **this account does not
have to be an administrator** — the panel takes an administrator's name and
password, so someone else can enter theirs. Decline it and nothing is downloaded
or installed. The bundle is ad-hoc signed, so its identity changes with every
build and the firewall grant has to be made again on each update. The chat
client needs no administrator rights at all.

That command and a direct download of the current release are also on the
project's page at <https://intentdriven.sh/Gropius>, which is where someone who
is not building from source starts. The page names the release GitHub flags as
latest, lists every file it carries with its size, and links the checksums to
verify a download against; the release run renders it from the release itself,
so no one edits the page to keep it current.

**Requires macOS 26.** Both apps declare that minimum and the installer checks
it, so an older Mac is turned away before anything is installed. The **server
needs Apple Silicon** (MLX runs on Metal). For the native chat client
(`GropiusChat.app`, universal — it runs on any Mac that runs macOS 26, Intel
included, and talks to a server over the network):

```sh
curl -fsSL https://raw.githubusercontent.com/intentdriven/Gropius/main/install.sh | bash -s -- client
```

The installer **verifies the download before installing it**: it fetches the
checksums file published on the same GitHub release and refuses anything that
does not match. Every release asset also carries a GitHub build-provenance
attestation binding it to the release workflow run; check it yourself with
[`gh attestation verify`](https://cli.github.com/manual/gh_attestation_verify):

```sh
gh attestation verify Gropius.app.zip --repo intentdriven/Gropius
```

There is no offline signing key; building from source is the escape hatch. The
binaries are ad-hoc signed, not notarized; because the installer has verified
the download, it clears the Gatekeeper quarantine so it launches without a
prompt. The first launch installs
the MLX runtime (a few minutes, shown in the control panel), then you can download
and serve models. New here? See **[docs/getting-started.md](docs/getting-started.md)**.

## Build from source

```sh
make app         # build Gropius.app (menu-bar app bundle)
make install     # copy to /Applications and launch it
make run         # or: run headless in the foreground, for development
```

## Using it

Click the menu-bar icon → **Open Control Panel**, or from any OpenAI client:

```python
from openai import OpenAI
client = OpenAI(base_url="http://your-mac.local:11535/v1", api_key="not-needed")
print(client.chat.completions.create(
    model="mlx-community/Qwen3-8B-4bit",
    messages=[{"role": "user", "content": "Hello!"}],
).choices[0].message.content)
```

## Security

By default the server is **reachable by anyone on your network with no API key** —
the control panel warns you while this is so. Set a key in **Settings** to require
`Authorization: Bearer <key>`. Same-machine clients (loopback, including other
user accounts) never need a key. The control panel and its `/api/*` endpoints are
bound to loopback only and are never reachable from the LAN.

Setting a key also turns on the models list's residency fields, which say which
models are loaded and how busy they are. With no key set, the list still names
every downloaded model and says nothing about what this Mac is doing with them —
though a client on an open server can still time a request to find out. Keeping
activity private means setting the key, not leaving the fields off.

The server's request log records the method, path, status and duration of a
request, and never the client's network address.

Gropius collects no telemetry: nothing about usage, models, hardware or errors
leaves your Mac, to the project or to anyone else. **Request statistics** is a
local record, off unless you turn it on, holding token counts and timings —
never a prompt, an answer, an API key or a client's address. The boundary is
the Mac rather than your account: the control panel answers anyone who can
reach it on this Mac, so on a Mac several people log into, any of them can
turn the switch on and read what it records
([what is recorded, and who can see it](docs/request-statistics.md)).

Gropius passes a request's prompt on to the model without reading it. The one
exception is **Merge system messages**, a per-model setting that is off unless
you switch it on: for a model you switch it on for, Gropius gathers that
request's system messages into the first one, reads nothing else of the
request, and keeps none of what it reads
([how to switch it on](docs/system-message-merging.md); the rule it runs under
is written down as
[an architecture decision](.abcd/development/decisions/adrs/2609061610102325-the-gateway-may-rewrite-prompt-content-only-to-merge-system.md)).

## Layout

- [`cmd/gropius/`](cmd/gropius/) — menu-bar app + singleton election.
- [`internal/`](internal/) — the engine: `hub` (HuggingFace client + downloader),
  `runtime` (Python/MLX provisioning + process pool), `gateway` (OpenAI + control
  API), `registry`, `discovery`, `config`, `capability`, `ui`, `app`.
- [`docs/`](docs/) — getting-started guide, how-to pages and reference.

Design decisions and the empirical facts behind them: [`DECISIONS.md`](.abcd/development/decisions/DECISIONS.md).

## Development

```sh
make test        # go test -race ./...
make lint        # fmt + vet + test
```

## Licence

MIT. See [`LICENSE`](LICENSE).
