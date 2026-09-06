# Set default sampling parameters

Most tools that speak the OpenAI API never send a temperature. Whatever the
model server picks then applies to every one of their requests. This page is
how to decide that yourself, once, for the whole machine.

## Set a machine-wide default

1. Open the control panel and go to **Settings → Sampling defaults**.
2. Fill in the parameters you want, and leave the rest blank.
3. **Save settings**.

The message under the button names any model that is already loaded and needs
loading again — see [When a change takes effect](#when-a-change-takes-effect).

## The parameters

| Field | Applies to | Blank means |
| --- | --- | --- |
| Temperature | `temperature` | 0 — greedy decoding |
| Top-p | `top_p` | 1 |
| Top-k | `top_k` | 0, which switches top-k off |
| Min-p | `min_p` | 0, which switches min-p off |
| Maximum completion tokens | `max_tokens`, and `max_completion_tokens` | 512 |

A maximum of 0 is accepted and means every request that omits the parameter
gets an empty answer. The largest maximum is 1048576 tokens — beyond the
longest context any of these models serves, and high enough that the limit is
never what stops a real answer.

These are the parameters the model server accepts when it starts. Anything else
a request can carry — repetition and presence penalties, `logit_bias`,
`logprobs`, `seed` — is a per-request field only, and cannot be given a
default here.

Each field's placeholder shows the model server's own value, so a blank field
is not a mystery: it is the figure in the table above.

## A request's own value always wins

A default fills a gap and nothing more. A request that sets `temperature`
itself is served with its own value, and no sampling parameter is ever added
to or changed in a request body on its way to the model. Nothing here caps or
overrides what a client asks for.

To be served with a default, leave the parameter out of the request. An
explicit `null` is not the same thing: the model server reads the key as
present, refuses the value, and closes the connection without answering, which
reaches the client as `502 the model server did not respond`. Some OpenAI
client libraries send `null` for an option that was never set — if requests
fail that way, configure the client to omit the field instead.

## One model, its own figures

**Settings → Per-model sampling** gives a single model its own values. Choose
the model — the fields fill in with whatever it already has — set the ones
that should differ from the machine-wide defaults, and **Save settings**. A
field left blank keeps the machine-wide value, so an override can be a
temperature alone. Clearing every field removes the override, as does
**Remove** beside it in the list.

The fields are part of the settings form, so **Save settings** takes whatever
is in them; **Set override** is for building up several models' overrides
before saving.

The override applies to that model the next time it loads. A model with no
override is served with the machine-wide set.

A blank field in an override means "use the machine-wide value", so there is
no way to say "use the model server's own default for this one parameter while
the machine sets one". Clear the machine-wide field instead, and give the
models that want a figure their own.

## When a change takes effect

The defaults are given to each model server as it starts, so a change reaches
a model the next time it loads. A model that is already loaded goes on serving
with the values it started with until it is unloaded — from **My Models →
Unload**, or by an idle timeout — and loaded again. The saved-settings message
names the loaded models this applies to.

## Reproducibility

A `seed` does nothing here: the model server ignores the one a request
carries, so there is no seed to set. What makes generation repeatable is a
fixed temperature. At temperature 0 the same prompt gives the same answer
every time, and there is no setting to add to that.

## Values the model server will not take

Every field has a range, and the panel refuses a value outside it, naming the
field and changing nothing. The ranges are the model server's own: temperature
is at least 0, top-p and min-p are between 0 and 1, and top-k and the token
budget are whole numbers of at least 0. Two ceilings are Gropius' own rather than the
model server's. Top-k has an upper limit of 1024, because the model server
refuses a top-k as large as the model's vocabulary and Gropius cannot tell what
that is at the moment you save; a top-k above a few hundred keeps every
plausible token anyway. The maximum completion tokens has an upper limit of
1048576, because a default larger than any real context window does not mean a
generous budget, it means every request that omits the parameter runs until the
model stops of its own accord.

The check matters because these values are given to the model server at
start-up. A figure it will not accept does not stop the model loading: the
server refuses the value on each request instead, and it does so by closing
the connection rather than answering, which reaches the client as `502 the
model server did not respond`. Every request that omits that parameter would
fail that way — exactly the requests a default exists to serve — so Gropius
never accepts such a figure.

The same value hand-edited into `config.json` is ignored rather than fatal:
Gropius starts normally, logs which fields it dropped, and serves as though
they had never been set.
