# Reference: response headers

Gropius adds two headers to the answers it gives a completion request, on
`POST /v1/chat/completions` and `POST /v1/completions`. They say what the
request spent getting to a model server, which the OpenAI response body has no
field for.

Both appear on a successful answer, streamed or not, and on the 503 that a
request gets when this Mac has no memory for its model. They carry no model
name and no count of anything: what they say is that this machine was busy and
for how long, which is what a client with a stopwatch already knows.

They are absent from every other answer: a request refused before a model was
chosen at all (400, 401, 404), the 503 for a model that is already serving as
many requests as it will take, and the 503 for a model server that could not be
started. None of those spent time getting to a model server, so there is
nothing for the headers to say — read their absence as "this did not wait",
not as "this waited an unknown amount".

```
HTTP/1.1 200 OK
Content-Type: text/event-stream
X-Gropius-State: waited
X-Gropius-Queue-Time: 47320
```

## The headers

| Header | Value |
| --- | --- |
| `X-Gropius-State` | `warm` or `waited`. `warm` means the request went straight to a model server. `waited` means it did not. |
| `X-Gropius-Queue-Time` | Whole milliseconds spent waiting, as a decimal integer. `0` on a warm request. |

The two agree by construction: the state is `waited` exactly when the queue
time is not `0`. A client can read either.

## What the queue time counts

Three waits, added together, all of them before the model server is asked
anything:

- **Waiting for room.** With
  [eviction grace](eviction-grace.md) switched on, a request for a model that
  does not fit waits for another model to fall idle rather than evicting one
  that has just finished work. This is the largest of the three, and the reason
  the headers exist.
- **Waiting for a model to load.** A model that is not in memory takes seconds
  to minutes to load, and every request waiting on that load pays it, not only
  the one that triggered it.
- **Waiting for a slot.** A model already serving its full batch queues the
  next requests rather than letting them into the model server, where an
  unbounded burst would exhaust GPU memory.

Time spent generating is not counted: the queue time ends where the answer
begins.

Under a millisecond reads as `warm`. Contention on the pool's own bookkeeping
is measurable and is not what anybody means by a wait.

## Using them

A client that sees `waited` learnt something about this Mac, not about its own
request: another client's model was in memory, or its own model was not. The
answer itself is unaffected.

To avoid the wait rather than be told about it, read residency from
[the models list](models-list.md) before choosing a model — `state` says which
models are loaded — and ask for one that is already warm. That listing needs an
API key configured on the install.

A request refused for want of memory carries the same two headers on its 503,
so a client that held a connection open for minutes and got nothing can tell
that apart from an instant refusal.
