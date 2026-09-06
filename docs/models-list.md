# Reference: the models list

`GET /v1/models` reports the models this Gropius can serve. It is the
OpenAI-shaped listing every OpenAI client already calls, with a small number
of Gropius extensions carried as extra top-level fields.

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
card, labelled `max context` and abbreviated to whole units of 1,024 rounded
down — a model declaring 262,143 reads `max context 255K`. The models list
carries the exact number.

## Compatibility

The extension fields are additions to the OpenAI shape, not changes to it.
Every field the OpenAI schema defines keeps its meaning, so an existing client
is unaffected — but a typed SDK object that discards fields it does not know
never sees the context figure. Read the raw JSON of the response to get at it.
