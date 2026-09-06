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

`context_length` and `max_model_len` always carry the same number. Both names
are published so the figure lands under whichever one a client's tooling
already looks for: `context_length` is the spelling OpenRouter- and
Ollama-style listings use, `max_model_len` the spelling vLLM uses.

The number is the **architectural maximum**: the positional range the model's
own configuration declares, which is what it was trained or scaled for.
Gropius reads it from the model's `config.json` — `max_position_embeddings`,
or `text_config.max_position_embeddings` for the multimodal and composite
architectures that nest the text model's settings — and applies no scaling
arithmetic of its own.

Two things follow from that.

- **It is a ceiling, not a promise.** The window a given Mac can serve at once
  may be smaller: a long prompt has to fit in memory alongside the weights,
  and a very long one can take minutes to process. Treat the figure as the
  limit above which a prompt is outside the range the model was built for, and
  measure what your own Mac sustains.
- **Nothing enforces it.** Gropius does not refuse or trim a prompt because of
  this number. A client is free to send more, and the model's answers simply
  degrade beyond the range it was scaled for.

Both fields are absent, rather than zero, when the model's configuration
declares no positional range, or declares one Gropius does not believe — a
figure that is negative, fractional, or implausibly large. The model is listed
and served exactly as it would be with a figure; only the context fields are
missing. A client should therefore treat an absent figure as "unknown", never
as "no context".

The control panel shows the same number on each model's card, labelled
`max context`.

## Compatibility

The extension fields are additions to the OpenAI shape, not changes to it.
Every field the OpenAI schema defines keeps its meaning, so an existing client
is unaffected — but a typed SDK object that discards fields it does not know
never sees the context figure. Read the raw JSON of the response to get at it.
