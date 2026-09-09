# Reference: the models list

`GET /v1/models` reports the models this Gropius can serve. It is the
OpenAI-shaped listing every OpenAI client already calls, with a small number
of Gropius extensions carried as extra top-level fields. Some of those fields
appear only for a client connecting over loopback, or on an install with an
API key configured.

Only models in the **ready** state appear. A model that is downloading, or
whose last download failed, is not listed.

```sh
curl http://localhost:11535/v1/models
```

```json
{
  "object": "list",
  "data": [
    {
      "id": "mlx-community/Qwen3-8B-4bit",
      "object": "model",
      "created": 1757145600,
      "owned_by": "gropius",
      "context_length": 40960,
      "max_model_len": 40960
    }
  ]
}
```

## Fields

| Field | Meaning |
| --- | --- |
| `id` | The model's name: its HuggingFace repository id. This is the exact string to put in a request's `model` field. |
| `object` | Always `model`, as the OpenAI schema requires. |
| `created` | Unix time at which this Mac first recorded the model. A re-download or a retry does not move it. |
| `owned_by` | Always `gropius`. |
| `context_length` | The model's maximum context, in tokens. See below. |
| `max_model_len` | The same figure again, under the name vLLM-derived clients read. |
| `state` | Whether the model is loaded, still loading, or not loaded. Only for a client connecting over loopback, or on an install with an API key. See below. |
| `in_flight` | How many requests that model is already handling. Only for a client connecting over loopback, or on an install with an API key. |
| `last_used` | Unix time at which Gropius last handled a request for that model. Only for a client connecting over loopback, or on an install with an API key, and only while the model is in memory — `loaded` or `loading`. |
| `pinned` | Whether the operator has protected the model from eviction. Only for a client connecting over loopback, or on an install with an API key. See below. |

## The context figure

`context_length` and `max_model_len` always carry the same number, as a JSON
integer. Both names are published so the figure lands under whichever one a
client's tooling already looks for: `context_length` is the spelling
OpenRouter- and Ollama-style listings use, `max_model_len` the spelling vLLM
uses.

**What the number is.** The architectural maximum: the positional range the
model's own configuration declares, which is what it was trained or scaled
for. Gropius reads it from the model's `config.json` — `max_position_embeddings`
at the top level, or `text_config.max_position_embeddings` for the multimodal
and composite architectures that nest the text model's settings — and applies
no scaling arithmetic of its own.

**What the number is not.** It is not what a given Mac can serve. The usable
window may be smaller: a long prompt has to fit in memory alongside the
weights, and a very long one can take minutes to process. Nor is it enforced —
Gropius refuses no request and trims no prompt because of it. A prompt beyond
the figure is accepted, and the model's answers degrade outside the range it
was scaled for.

**When the fields are absent.** Both are omitted, rather than sent as zero, in
these cases:

- The configuration declares no positional range under either key.
- The declared range is negative, zero, fractional, or not a number.
- The declared range exceeds 8,388,608 tokens, the ceiling above which a
  figure is treated as corrupt.
- The top-level key is present but holds one of the values above. A bad
  top-level key is never rescued by a good `text_config` one: the top level is
  the authoritative key, and a configuration that contradicts itself is not
  believed.

The model is listed and served exactly as it would be with a figure; only the
context fields are missing. Treat an absent figure as "unknown", never as "no
context".

**On the model card.** The control panel shows the figure on each model's
card, labelled `max context`. From 1,024 tokens upwards the card abbreviates
it to whole units of 1,024, rounded down, so a model declaring 262,143 reads
`max context 255K`; below 1,024 the card prints the number itself. The models
list always carries the exact number.

## Residency

`state`, `in_flight`, `last_used` and `pinned` say what each model is doing right
now and which models are protected, so a client can send its work to a model that
is already warm instead of forcing a load it did not know about. Loading a model takes seconds to a minute, longer
for the largest; picking the warm one costs nothing.

**They appear for a client connecting over loopback, and for every client an
API key admits.** A chat application, a script, or any other program on the
same Mac that reaches the server at `localhost` or `127.0.0.1` is served all
four, whether or not a key is set: it is the same person at the same machine
the control panel already shows this to. Set a key in **Settings** and the four
fields are on every entry for every client the key admits, wherever it is.

It is the connection that decides, not the computer. A program on this Mac that
reaches the server by this Mac's **network** address rather than by loopback is
a network client here, and is served no residency on a keyless install — point
it at `localhost` or set a key. In the other direction, a tunnel or proxy
running on this Mac (`ssh -L`, for instance) makes the clients behind it
loopback clients: forwarding the port forwards this too.

What is withheld is the listing served to the network on an install with no
key. That is the shipping default, where anyone who can reach the server may
use it, and a client on the network is then served exactly the four OpenAI
fields and the context figure: nobody off this Mac learns from the listing what
it is running or when. A page in a browser cannot borrow the loopback rule
either — the request has to name loopback in its `Host`, and carry either no
`Origin` or a loopback one, so a site that points its own hostname at
`127.0.0.1` is refused the fields exactly as the network is.

What that withholds is the *listing*, and only the listing. On a server left
open, a client that never presents a key can still work out which models are
warm by timing a one-token completion — a loaded model answers straight away, a
cold one takes seconds to a minute — and that probe loads the model it asks
about, which reading the field never does. A model already at its request
ceiling, or one that does not fit in the memory budget, is refused with a
message that names how many requests are already in flight for it, or the
budget figure. So an unkeyed
server keeps activity off the listing it serves the network; it does not keep
it secret. The key is what protects the server.

The key is also one key, shared by every client that has it. A client holding
it sees the whole machine's activity — every model's in-flight count and
last-used time, not only its own. The same is true of a loopback client.

`state` carries one of three values:

| Value | Meaning |
| --- | --- |
| `loaded` | The model's server has answered its readiness probe. A request is served straight away. |
| `loading` | The model's server is running but has not answered its readiness probe yet. A request is served, after the wait. |
| `not_loaded` | Gropius is holding no server for this model. A request loads it first, evicting another model if the memory budget is full. |

Where the fields are served, an entry reads:

```json
{
  "id": "mlx-community/Qwen3-8B-4bit",
  "object": "model",
  "created": 1757145600,
  "owned_by": "gropius",
  "context_length": 40960,
  "max_model_len": 40960,
  "state": "loaded",
  "in_flight": 2,
  "last_used": 1757231998,
  "pinned": true
}
```

`in_flight` counts the requests that model is already handling, queued ones
included, and is `0` for a model that is not loaded. Use it to spread work
across two warm models rather than queueing behind one.

`last_used` is Unix time in seconds. It is the record kept alongside the model
in memory, so it is there exactly while Gropius is holding the model — `loaded`
or `loading` — and goes when the model does: a model that was busy a minute ago
and has since been evicted reports `not_loaded` and no `last_used` at all. The
field is absent rather than zero, which a client would read as 1970 rather than
as "unknown". Read it as "this model was last touched then", never as "this
model has not been used since"; `state` is what says whether it is warm.

A model still loading already carries one, stamped when its load was asked for.
The time moves when a request starts and again when it finishes, so a model in
the middle of a long generation carries the time that generation began, not the
time it will end — do not compute an idle-timeout deadline from it.

`pinned` is `true` for a model the operator pinned in **Settings**, and `false`
for every other model. A pinned model is never evicted to make room and is
never unloaded by the idle timeout, so it is the model that will still be warm
on the next turn — the one field here that is a promise about the future rather
than a description of the moment. It is a fact about the model, not about
whether it is in memory: a pinned model that nothing has loaded yet reads
`"state": "not_loaded", "pinned": true`, and it is protected from the moment a
request loads it. See
[Pin a model so it stays in memory](pinning-models.md).

**A snapshot, not a reservation.** The values describe the moment the list is
built. Reading `loaded` holds nothing warm on your behalf: another client's
request, the idle timeout, an eviction, or the model server crashing can take
that model away before your own request arrives. Treat the values as a hint
worth acting on, never as a promise: a request is still the thing that decides.
A client that lists before each request, or on a short schedule, keeps a
picture worth acting on; one that lists once at start-up does not.

## The memory budget and eviction

Residency is worth reading because it changes, and it changes under these
rules.

- Gropius runs one model server per model and keeps as many resident as its
  memory budget allows. The budget defaults to 60% of this Mac's physical RAM,
  which leaves the rest for everything else on a machine whose GPU and CPU share
  one pool of memory, and **Settings → Memory for loaded models** sets it to any
  figure up to the whole machine. A change applies to the next load, with no
  restart: nothing is unloaded to fit a lowered budget, so the machine can sit
  above it until the models resident at the time go, and a figure larger than
  this Mac's memory is held down to it. See
  [Set how much memory models may use](memory-budget.md) and
  [Why there is a memory budget](memory-budget-explained.md).
- Each loaded model is charged 1.2 times its size on disk, the weights plus
  headroom for the cache and activations a running model needs. The cache a
  request builds as it works through a long prompt is not counted, so a machine
  loaded to its budget can still run out of memory under long prompts served
  concurrently.
- A request for a model that does not fit in what is left unloads the
  least-recently-used idle model, at once, to make room. The memory of the model
  being unloaded is credited back as the replacement starts, so the two overlap
  for the seconds it takes the first to exit and the budget is not a hard
  ceiling in that moment. A model with a request
  in flight is never the one chosen, a model still loading is not either, and a
  pinned model is not either. If nothing can be freed, the request is refused
  with an error naming the memory pressure. That refusal names no model: which
  models this Mac is protecting stays off the network.
- **Eviction grace** (**Settings**, off by default) changes when that unload
  happens. With it on, a model is protected for a set interval after it
  finishes a request, and a request that needs its memory waits for another
  model to fall idle rather than taking it — bounded by a maximum wait, past
  which it gets the same refusal. On an install with an API key set, an answer
  that reached a model server also says whether it waited, in the headers
  [the response header reference](response-headers.md) describes; an open
  server sends those to nobody, for the reason it withholds `state` here.
  Reading `state` from this listing before choosing a model is how a client
  avoids the wait rather than only being told about it. See
  [Give a busy model a moment before it is evicted](eviction-grace.md) and
  [Why a request waits instead of taking the memory](eviction-grace-explained.md).
- A model larger than the whole budget is refused outright: no eviction helps.
- **Idle timeout** (**Settings**, off by default) unloads a model that has gone
  that long without a request, whether or not anything needs the room.
- **Pinned models** (**Settings**) are never evicted to make room and never
  reaped by the idle timeout. A request that would need a pinned model's memory
  is refused instead.
- **Preload** (**Settings**) loads the models it names at start-up so their first
  request is fast. Preloading and pinning are separate settings and do different
  things: preloading loads a model and leaves it as evictable as any other,
  pinning protects a model but loads nothing. Name a model in both to have it
  loaded at start-up and protected from then on.

## Compatibility

The extension fields are additions to the OpenAI shape, not changes to it.
Every field the OpenAI schema defines keeps its meaning, so an existing client
is unaffected — but a typed SDK object that discards fields it does not know
never sees them. Read the raw JSON of the response to get at them.
