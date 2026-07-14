# Getting started with Gropius

This walks you from a fresh checkout to answering a prompt from another machine.
About 15 minutes, most of it downloads.

## 1. Requirements

- An **Apple Silicon** Mac (M1 or later). MLX runs on Metal, so Intel Macs are
  not supported.
- macOS 13 or later.
- Go 1.25+ and the Xcode command-line tools (`xcode-select --install`) to build.
- An internet connection for the first run.

You do **not** need Python installed — Gropius installs its own.

## 2. Build and launch

```sh
git clone <this repo> && cd Gropius
make install        # builds Gropius.app, copies it to /Applications, launches it
```

A small icon appears in the menu bar. The first launch downloads a private Python
and the MLX runtime (a few minutes). You can watch progress in the control panel:

**Menu-bar icon → Open Control Panel** (or visit <http://localhost:11535>).

While setup runs, the panel shows a "Setting up the MLX runtime…" banner. When it
clears, you are ready.

## 3. Download a model

1. In the control panel, open the **Find Models** tab.
2. Search for something small to start — try `SmolLM` or `Qwen3-0.6B`.
3. Click **Download**. Progress appears under **My Models**.

Good first models (small, fast, download in under a minute):

| Model | Size | Notes |
|---|---|---|
| `mlx-community/Qwen3-0.6B-4bit` | ~340 MB | Tiny, capable, a thinking model |
| `mlx-community/SmolLM-135M-Instruct-4bit` | ~75 MB | Smallest useful |
| `mlx-community/Qwen3-8B-4bit` | ~4.5 GB | A solid everyday model |

## 4. Talk to it — from this Mac

Once a model shows **ready**, from a terminal on the same Mac:

```sh
curl http://localhost:11535/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mlx-community/Qwen3-0.6B-4bit",
    "messages": [{"role": "user", "content": "Say hello in one word."}]
  }'
```

The first request for a model loads it (a few seconds to a minute for large
ones); later requests are fast. You can pre-load from **My Models → Load**.

## 5. Talk to it — from another machine

Open the **Connect** tab. It lists the exact base URLs to use, for example
`http://your-mac.local:11535/v1`. From any other machine on the same network:

```python
from openai import OpenAI

client = OpenAI(base_url="http://your-mac.local:11535/v1", api_key="not-needed")
resp = client.chat.completions.create(
    model="mlx-community/Qwen3-0.6B-4bit",
    messages=[{"role": "user", "content": "Hello from across the network!"}],
)
print(resp.choices[0].message.content)
```

If `your-mac.local` does not resolve, use the IP address shown in the Connect tab
instead.

## 6. Lock it down (optional but recommended)

By default anyone on your network can use the server. To require a key:

1. **Settings → API key → Generate a key** (or type your own), then **Save**.
2. Clients now send it:

   ```sh
   curl http://your-mac.local:11535/v1/chat/completions \
     -H "Authorization: Bearer YOUR_KEY" \
     -H "Content-Type: application/json" \
     -d '{"model":"mlx-community/Qwen3-0.6B-4bit","messages":[{"role":"user","content":"hi"}]}'
   ```

Requests from the Mac itself (including other user accounts, over `localhost`)
never need the key.

## 7. Sharing across user accounts (optional)

If several people log into this Mac, let them share one copy of each model:

```sh
make install-shared     # creates /Users/Shared/Gropius, needs your password
```

After that, whoever launches Gropius first runs the server; everyone else's
menu-bar app just points at it. One copy on disk, one on the GPU.

## Troubleshooting

- **The menu-bar icon never appears.** Run it in the foreground to see errors:
  `./dist/Gropius.app/Contents/MacOS/gropius`.
- **A model stays "downloading" forever / fails.** Check the panel for the error.
  Gated models need a HuggingFace token in **Settings**.
- **`your-mac.local` won't resolve from another machine.** Use the IP from the
  Connect tab. Make sure both machines are on the same network and that macOS is
  not blocking incoming connections for the app (System Settings → Network →
  Firewall).
- **First request is slow.** That is the model loading into memory. Pre-load it
  from **My Models → Load**, and raise the idle timeout in **Settings** so it
  stays resident.

## Uninstalling

Quit from the menu, drag `Gropius.app` to the Trash, and remove its data:

```sh
rm -rf ~/Library/Application\ Support/Gropius
```

That directory holds the private Python runtime and your downloaded models —
deleting it removes every trace.
