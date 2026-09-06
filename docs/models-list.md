# Reference: the models list

`GET /v1/models` reports the models this Gropius can serve. It is the
OpenAI-shaped listing every OpenAI client already calls, with a small number
of Gropius extensions carried as extra top-level fields. Some of those fields
appear only on an install with an API key configured.

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
| `state` | Whether the model is loaded, still loading, or not loaded. Only on an install with an API key. See below. |
| `in_flight` | How many requests that model is already handling. Only on an install with an API key. |
| `last_used` | Unix time at which Gropius last handled a request for that model. Only on an install with an API key, and only while the model is in memory — `loaded` or `loading`. |

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

`state`, `in_flight` and `last_used` say what each model is doing right now, so
a client can send its work to a model that is already warm instead of forcing a
load it did not know about. Loading a model takes seconds to a minute, longer
for the largest; picking the warm one costs nothing.

**They appear only when an API key is configured.** Set a key in **Settings**
and the three fields are on every entry, for every client the key admits and
for same-machine clients that need no key. Leave the key unset — the shipping
default, where anyone on the network may use the server — and the listing is
exactly the four OpenAI fields and the context figure.

What that withholds is the *listing*, and only the listing. On a server left
open, a client that never presents a key can still work out which models are
warm by timing a one-token completion — a loaded model answers straight away, a
cold one takes seconds to a minute — and that probe loads the model it asks
about, which reading the field never does. A model already at its request
ceiling, or one that does not fit in the memory budget, is refused with a
message that names how many requests are already in flight for it, or the
budget figure. So an unkeyed
server keeps activity off the listing; it does not keep it secret. The key is
what protects the server.

The key is also one key, shared by every client that has it. A client holding
it sees the whole machine's activity — every model's in-flight count and
last-used time, not only its own.

`state` carries one of three values:

| Value | Meaning |
| --- | --- |
| `loaded` | The model's server has answered its readiness probe. A request is served straight away. |
| `loading` | The model's server is running but has not answered its readiness probe yet. A request is served, after the wait. |
| `not_loaded` | Gropius is holding no server for this model. A request loads it first, evicting another model if the memory budget is full. |

On an install with a key, an entry reads:

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
  "last_used": 1757231998
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
  memory budget allows. The budget is 60% of this Mac's physical RAM, which
  leaves the rest for everything else on a machine whose GPU and CPU share one
  pool of memory. There is no setting for it.
- Each loaded model is charged 1.2 times its size on disk, the weights plus
  headroom for the cache and activations a running model needs.
- A request for a model that does not fit in what is left unloads the
  least-recently-used idle model, at once, to make room. A model with a request
  in flight is never the one chosen, and a model still loading is not either. If
  nothing can be freed, the request is refused with an error naming the memory
  pressure rather than waiting.
- A model larger than the whole budget is refused outright: no eviction helps.
- **Idle timeout** (**Settings**, off by default) unloads a model that has gone
  that long without a request, whether or not anything needs the room.
- **Preload** (**Settings**) loads the models it names at startup so their first
  request is fast. It does not pin them: a preloaded model is evicted under
  memory pressure and reaped by the idle timeout like any other.

## Compatibility

The extension fields are additions to the OpenAI shape, not changes to it.
Every field the OpenAI schema defines keeps its meaning, so an existing client
is unaffected — but a typed SDK object that discards fields it does not know
never sees them. Read the raw JSON of the response to get at them.
