# Getting started with Gropius

This walks you from a fresh checkout to answering a prompt from another machine.
About 15 minutes, most of it downloads.

## 1. Requirements

- An **Apple Silicon** Mac (M1 or later). MLX runs on Metal, so Intel Macs are
  not supported.
- **Requires macOS 26.** The app bundle declares that minimum, so macOS refuses
  to launch it on anything older. (The one-line `install.sh` in the README
  checks the version up front; `make install` below builds and installs first.)
- Go 1.25+ and the Xcode command-line tools (`xcode-select --install`) to build.
- An internet connection for the first run.

You do **not** need Python installed — Gropius installs its own.

## 2. Build and launch

```sh
git clone <this repo> && cd Gropius
make install        # builds Gropius.app, copies it to /Applications, launches it — needs your password, for the firewall
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

`GET /v1/models` lists what this Mac can serve, and gives each model's maximum
context so a client can size its prompts before sending. With an API key set it
also says which models are loaded. The [models list reference](models-list.md)
describes every field, including what those values do and do not promise.

### Set how much memory models may use

**Settings → Memory for loaded models** is how much of this Mac Gropius fills
with loaded models: 60% of its memory until you type a figure of your own, in
gigabytes, with the share of the machine shown beside it. Raise it on a Mac that
serves models and does nothing else, and two large models sit in memory together
rather than taking turns. A change applies to the next load, with no restart,
and nothing is unloaded to fit a lowered figure. See
[Set how much memory models may use](memory-budget.md), and
[Why there is a memory budget](memory-budget-explained.md) for what the figure
does and does not account for.

### Keep a model in memory

**Settings → Pinned models** protects the models you rely on. A pinned model is
never unloaded to make room for another and the idle timeout does not touch it;
a request that would need its memory is refused instead, and that refusal never
says which models are protected. Pinning is not preloading: preloading loads a
model at start-up and leaves it as evictable as any other, pinning protects a
model but loads nothing. See
[Pin a model so it stays in memory](pinning-models.md).

### Stop two people's models evicting each other

**Settings → Eviction grace** is off by default, and while it is off a request
for a model that does not fit unloads the least recently used idle model at
once — even one that answered a moment ago. Turn it on and that request waits
instead: for a model to have been idle for the interval you set, or for its own
wait to have passed that long, up to a maximum you also set. If nothing frees
up in time it gets the same refusal it would have had immediately, now saying
how long it waited. With an API key set, every answer that reached a model
server also carries two headers saying whether it waited and for how long; an
open server sends them to nobody, the same rule the models list applies to
residency. The interval may not be longer than the idle timeout above when one
is set. See [Give a busy model a moment before it is evicted](eviction-grace.md)
and [the response header reference](response-headers.md).

## 5. Talk to it — from another machine

Open the **Connect** tab. It lists the exact base URLs to use, for example
`http://your-mac.local:11535/v1`. An address in that list that sits on a private
network carries a mark saying so: the mark names the network the address belongs
to, and says nothing about how safe it is or who else can reach it. From any
other machine on the same network:

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

Requests from the Mac itself (including other user accounts) never need the
key, as long as they are addressed to `localhost` or `127.0.0.1`. A local proxy
or tunnel that forwards another host name must send the key like any other
client.

Setting a key also turns on the models list's residency fields, so a client
holding it can see which models are loaded and pick a warm one instead of
triggering a load — see the [models list reference](models-list.md).

### What Gropius reads of a request

Gropius passes a request on to the model without reading what is in it. There
is one exception, it is per model, and it is off until you switch it on:
**Settings → Merge system messages**, for a model whose template refuses a
conversation whose instructions are not all at the top. For a model you switch
it on for, Gropius reads that request's instruction messages and nothing else,
gathers them into the first one, and keeps none of what it reads. See
[Merge system messages for a template-strict model](system-message-merging.md).

## 7. Choose what an omitted parameter means (optional)

Clients that never send a `temperature` are served with the model server's
own default, which is 0 — greedy decoding. **Settings → Sampling defaults**
sets that for the whole machine, and **Per-model sampling** gives one model
its own figures. A request that sets its own value always wins, and a change
reaches a model the next time it loads. See
[Set default sampling parameters](sampling-defaults.md), with the parameters
themselves in [Reference: sampling parameters](sampling-reference.md).

## 8. See how your models are performing (optional)

Gropius keeps no record of the requests it serves unless you ask it to.
**Settings → Request statistics** turns on a content-free record of each
request — the model, the token counts and the timings — shown per model on the
**Statistics** tab, so two quantisations of the same model can be compared by
their numbers. It never records a prompt, an answer, an API key or the address
of the client, and nothing recorded leaves this Mac. The records are kept in a
`stats` folder inside the Gropius data folder, one line of JSON each, for as
many months and as many megabytes as you say in Settings. When those limits
drop a day's records, a coarse per-model summary of that day is kept in their
place, so the shape of last year's use survives the detail. See
[Record request statistics on this Mac](request-statistics.md), and
[Understanding the historical views](statistics-explained.md) for what the
tab's tables over days and months mean.

## 9. Sharing across user accounts (optional)

If several people log into this Mac, let them share one copy of each model:

```sh
make install-shared     # creates /Users/Shared/Gropius, needs your password
```

After that, whoever launches Gropius first runs the server; everyone else's
menu-bar app just points at it. One copy on disk, one on the GPU.

The shared folder holds the model files, the download cache they arrive
through, and the server's own log files. Everything belonging to one account
stays in that account's own `~/Library/Application Support/Gropius`: its
settings (`config.json`, which holds the API key and the HuggingFace token),
its list of models (`registry.json`), its request statistics, and the private
Python runtime it starts model servers with. So an API key or a token one
account sets is never readable by another.

The first time an account runs with the shared cache, its list of models starts
empty and is rebuilt from the models already in the shared folder — nothing is
downloaded again. If that account had used the shared cache before this became
the rule, the settings it kept in the shared folder are moved into its own on
that first start, and are no longer readable by anyone else on the Mac.

Request statistics stay with the account that runs the server: if that account
has recording on, its records cover every request the server handled, from any
account on this Mac, and they are kept in that account's own folder rather
than the shared one.

Gropius only uses `/Users/Shared/Gropius` when the installer created it: the
directory must be owned by the administrator account (`root`), which is what
`make install-shared` produces. A folder someone made by hand there is ignored
and each account falls back to its own data directory.

## Troubleshooting

- **Another machine gets `ERR_EMPTY_RESPONSE` / "didn't send any data", but
  `localhost` works on the Mac itself.** The macOS Application Firewall is
  blocking incoming connections to Gropius. A locally-built app is not signed by
  a Developer-ID certificate, so the firewall accepts the connection and then
  drops it — loopback is exempt, which is why same-machine access still works.
  Allow it through once:

  ```sh
  make allow-firewall     # or, for the installed app:
  sudo /usr/libexec/ApplicationFirewall/socketfilterfw \
    --add "/Applications/Gropius.app/Contents/MacOS/gropius"
  sudo /usr/libexec/ApplicationFirewall/socketfilterfw \
    --unblockapp "/Applications/Gropius.app/Contents/MacOS/gropius"
  ```

  `make install` does this for you. You can also do it in **System Settings →
  Network → Firewall → Options** by setting Gropius to "Allow incoming
  connections". Only the Gropius app needs this; its Python helper only ever
  listens on loopback.

- **A model answers the first message, then fails with a template error once the
  assistant repeats its instructions.** That model's chat template refuses a
  system message that is not the first one. Switch on **Merge system messages**
  for that model — see
  [Merge system messages for a template-strict model](system-message-merging.md).
- **The menu-bar icon never appears.** Run it in the foreground to see errors:
  `./dist/Gropius.app/Contents/MacOS/gropius`.
- **A model stays "downloading" forever / fails.** Check the panel for the error.
  Gated models need a HuggingFace token in **Settings**.
- **`your-mac.local` won't resolve from another machine.** Use the IP from the
  Connect tab. Make sure both machines are on the same network and that macOS is
  not blocking incoming connections for the app (System Settings → Network →
  Firewall).
- **The server worked, then later looks down from other machines.** The serving
  Mac has gone to sleep — a sleeping Mac does not wake for network traffic, so
  remote clients see it as down even though it answers on `localhost` the
  moment you wake it. Keep the serving Mac awake while it serves: prevent
  automatic sleeping in **System Settings → Energy** (on a laptop, **Battery →
  Options**), or run `caffeinate` in a terminal for a headless session.
- **The log says `config.json could not be read` and other machines cannot
  connect.** Gropius refuses a `config.json` that is not an ordinary file (a
  symlink from a dotfiles manager, say) or that does not parse — a hand-edited
  value of the wrong kind, such as a number in quotes — and starts locked to
  this Mac only, so a file it cannot trust never opens the server to the
  network. Replace the link with a real copy of the file, or correct the value,
  and restart.
- **First request is slow.** That is the model loading into memory. Pre-load it
  from **My Models → Load**. With the default settings a loaded model stays
  resident forever; an idle timeout in **Settings** unloads it after that many
  seconds without requests, so keep the timeout at 0 (= never unload) if you
  want it to stay loaded. The `preload` list in `config.json` loads models at
  startup, so the first request after a restart is fast too.

## Uninstalling

Quit from the menu, drag `Gropius.app` to the Trash, and remove its data:

```sh
rm -rf ~/Library/Application\ Support/Gropius
```

That directory holds the private Python runtime, your downloaded models and
any request statistics you recorded — deleting it removes every trace. If you
set up the shared cache (step 9), the models live in `/Users/Shared/Gropius`
instead, while your settings and model list stay in the folder above; remove
the shared one too, once every account has finished with it:

```sh
sudo rm -rf /Users/Shared/Gropius
```
