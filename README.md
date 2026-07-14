# Gropius

Download MLX models on your Mac and serve them to the rest of your network
through an OpenAI-compatible API. A menu-bar app with a web control panel.

Everything any OpenAI client can do against ChatGPT, it can do against your Mac —
`base_url=http://your-mac.local:11535/v1`.

## What it does

- **Browse and download** MLX models from HuggingFace (the `mlx-community` org),
  with live progress and resumable transfers.
- **Serve** them over an OpenAI-compatible API (`/v1/chat/completions`,
  `/v1/completions`, `/v1/models`), including streaming.
- **Share** the endpoint with other machines on your LAN — discoverable over
  Bonjour — and with other user accounts on the same Mac.
- **Manage the runtime for you**: it installs its own private Python + MLX under
  `~/Library/Application Support/Gropius`. Nothing touches your system Python.

## Requirements

- Apple Silicon Mac (MLX is Metal-only), macOS 13+.
- An internet connection for the first run (to install MLX and download models).

## Build & run

```sh
make app        # build Gropius.app
make install    # copy to /Applications and launch it
```

Or for development:

```sh
make run        # headless, in the foreground
```

The first launch installs the MLX runtime (a few minutes); the control panel
shows the progress. Then open the panel from the menu-bar icon, find a model,
and download it.

## Using it from another machine

Open the **Connect** tab in the control panel for ready-to-paste snippets. In
short:

```python
from openai import OpenAI
client = OpenAI(base_url="http://your-mac.local:11535/v1", api_key="not-needed")
print(client.chat.completions.create(
    model="mlx-community/Qwen3-8B-4bit",
    messages=[{"role": "user", "content": "Hello!"}],
).choices[0].message.content)
```

## Security

By default the server is **reachable by anyone on your network with no API key**.
The control panel warns you while this is the case. To restrict access, set an
API key in **Settings** — clients then send `Authorization: Bearer <key>`.
Requests from the same Mac (loopback, including other user accounts) never need a
key.

The control panel itself is bound to loopback only.

## Sharing models across user accounts

By default each account keeps its own models. To share one copy across every
account on the Mac:

```sh
make install-shared     # creates /Users/Shared/Gropius (needs sudo)
```

After that, whichever account runs Gropius first serves the models; others become
menu-bar clients of it. One copy on the GPU, one copy on disk.

## Architecture

| Package | Responsibility |
|---|---|
| `internal/config` | On-disk layout and settings |
| `internal/hub` | Pure-Go HuggingFace client + resumable downloader |
| `internal/registry` | File-backed index of local models |
| `internal/runtime` | Installs Python/MLX; runs one `mlx_lm.server` per model with LRU eviction |
| `internal/gateway` | OpenAI API, control API, auth |
| `internal/discovery` | Bonjour/mDNS advertising |
| `internal/app` | Composition root |
| `cmd/gropius` | Menu-bar app + singleton election |

Key design notes are in [DECISIONS.md](DECISIONS.md).

## Development

```sh
make test       # go test -race ./...
make lint       # fmt + vet + test
```
