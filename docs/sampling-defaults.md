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

These are the parameters the model server accepts when it starts. Anything else
a request can carry — repetition and presence penalties, `logit_bias`,
`logprobs`, `seed` — is a per-request field only, and cannot be given a
default here.

Each field's placeholder shows the model server's own value, so a blank field
is not a mystery: it is the figure in the table above.

## A request's own value always wins

A default fills a gap and nothing more. A request that sets `temperature`
itself is served with its own value, and no request body is ever altered on
its way to the model. Nothing here caps or overrides what a client asks for.

An omitted parameter and an explicit `null` mean the same thing — use the
default — which is what OpenAI clients expect.

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
budget are whole numbers of at least 0. Top-k has an upper limit of 1,024 as
well, because the model server refuses a top-k as large as the model's
vocabulary and Gropius cannot tell what that is at the moment you save. A
top-k above a few hundred keeps every plausible token anyway.

The check matters because these values are given to the model server at
start-up. A figure it will not accept would leave every request that omits
that parameter failing — exactly the requests a default exists to serve — so
Gropius never accepts one.

The same value hand-edited into `config.json` is ignored rather than fatal:
Gropius starts normally, logs which fields it dropped, and serves as though
they had never been set.
