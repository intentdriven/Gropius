# Reference: sampling parameters

The sampling parameters Gropius can hold a default for, machine-wide or per
model. To set one, see
[Set default sampling parameters](sampling-defaults.md).

## The parameters

| Field | Applies to | Blank means |
| --- | --- | --- |
| Temperature | `temperature` | 0 — greedy decoding |
| Top-p | `top_p` | 1 |
| Top-k | `top_k` | 0, which switches top-k off |
| Min-p | `min_p` | 0, which switches min-p off |
| Maximum completion tokens | `max_tokens`, and `max_completion_tokens` | 512 |

"Blank means" is the model server's own default, which applies when Gropius
holds no value. Each field's placeholder in the panel shows the same figure.

These are the parameters the model server accepts when it starts. Anything else
a request can carry — repetition and presence penalties, `logit_bias`,
`logprobs`, `seed` — is a per-request field only, and cannot be given a
default.

## Accepted ranges

| Field | Accepted |
| --- | --- |
| Temperature | at least 0, no upper limit |
| Top-p | between 0 and 1 |
| Top-k | a whole number, 0 to 1024 |
| Min-p | between 0 and 1 |
| Maximum completion tokens | a whole number, 0 to 1048576 |

A value outside its range is refused when it is saved, with the field named
and nothing else changed.

Every range but two is the model server's own: temperature is at least 0,
top-p and min-p are between 0 and 1, and top-k and the token budget are whole
numbers of at least 0. Two ceilings are Gropius' own. Top-k has an upper limit
of 1024, because the model server refuses a top-k as large as the model's
vocabulary and Gropius cannot tell what that is at the moment you save; a
top-k above a few hundred keeps every plausible token anyway. The maximum
completion tokens has an upper limit of 1048576, because a default larger than
any real context window does not mean a generous budget, it means every
request that omits the parameter runs until the model stops of its own accord.

A maximum of 0 is accepted and means every request that omits the parameter
gets an empty answer.

## Precedence

| The request | What is served |
| --- | --- |
| omits the parameter | the per-model override, if the model has one; otherwise the machine-wide default; otherwise the model server's own |
| carries its own value | the request's value |
| carries `null` | nothing — the request fails, see [How sampling defaults work](sampling-explained.md#why-null-is-not-the-same-as-omitted) |

No sampling parameter is ever added to or changed in a request body on its way
to the model, and nothing here caps or overrides what a client asks for.

Within an override, a blank field means "use the machine-wide value". There is
no way to say "use the model server's own default for this one parameter while
the machine sets one" — clear the machine-wide field instead, and give the
models that want a figure their own.

## Where the values are kept

In `config.json`, under `sampling` for the machine-wide set and
`model_sampling` for the per-model overrides. At most 256 overrides are held.

A value of the right kind but the wrong size, hand-edited into that file, is
ignored rather than fatal: Gropius starts normally, logs which fields it
dropped, and serves as though they had never been set.

A value of the wrong *kind* is a different matter. `"temperature": "0.7"` with
quotes round it, `"top_k": 40.0` with a decimal point, or a number too large
for the field, is a malformed file rather than a setting out of range — the
same as a misspelled port — and Gropius cannot tell a damaged file from a
deliberate one. It starts locked to this Mac, with the shipping defaults, so a
file it cannot read never opens the server to the network by accident. The log
says `config.json could not be read`. Fix the file and restart, or change the
setting from the panel, which never writes a malformed one.
