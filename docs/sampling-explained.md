# How sampling defaults work

Why the sampling defaults behave as they do: where the value is applied, why a
change waits for a model to load again, and why some things that look like
they should work do not. The parameters themselves are in
[Reference: sampling parameters](sampling-reference.md); setting them is in
[Set default sampling parameters](sampling-defaults.md).

## The default lives in the model server, not in the request

Gropius runs one model server process per loaded model, and that server takes
its sampling defaults as options when it starts. Gropius passes the values you
saved as those options.

Everything else follows from that one choice:

- A request that omits a parameter is served with the value the process
  started with, because that is what the server falls back to.
- A request that carries its own value is served with it, because the server
  prefers what the request says. Gropius never has to touch a request body,
  which is why nothing it does can alter one.
- A change reaches a model only when that model loads again, because the
  values were fixed when its process started.

The alternative — adding the missing parameters to each request as it passes
through — would apply instantly, but it would mean the server rewriting the
bodies of requests it relays. The value here is worth less than that
guarantee.

## Why null is not the same as omitted

To be served with a default, a request must leave the parameter *out*. An
explicit `null` is not the same thing: the model server sees the key as
present, refuses the value it holds, and closes the connection without
answering. That reaches the client as `502 the model server did not respond`.

Some OpenAI client libraries send `null` for an option that was never set. If
requests fail that way, configure the client to omit the field instead.

## Why a value out of range is refused rather than passed on

A saved value becomes the model server's own default, and the server checks
the values it is actually using on every request. If it will not accept one,
it does not refuse politely and it does not fail to start: the model loads,
looks healthy, and then every request that omits that parameter closes without
an answer, while requests carrying their own value carry on working. The
symptom is a 502 for some clients and not others, on a server that reports
itself as running.

That is a hard fault to diagnose from the outside, and one saved setting
causes it on every model at once. So the ranges Gropius accepts are read from
the model server's own, never guessed, and a value outside them is refused
where a person is there to read the reason.

Two of the limits are stricter than the server's own, for the same reason.
Top-k is capped because the server refuses a top-k as large as the model's
vocabulary — and it refuses it from inside generation, one layer deeper still.
The completion-token budget is capped because a default beyond any real
context window means every request that omits it runs until the model stops of
its own accord.

## Why there is no seed

A `seed` is the usual way to ask for a repeatable answer, and the model server
ignores the one a request carries — measured across four models, where two
different seeds at the same temperature produced identical output every time.
There is therefore nothing for Gropius to default: a seed field would be a
control that does nothing.

What does make generation repeatable is a fixed temperature. At temperature 0
the model takes the most likely token every time rather than sampling, so the
same prompt gives the same answer. That is the whole mechanism, and it needs
no setting beyond the temperature itself.
