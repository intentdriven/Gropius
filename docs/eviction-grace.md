# Give a busy model a moment before it is evicted

Gropius unloads the least recently used idle model to make room for a new one.
That rule treats a model idle for one second the same as one idle for an hour,
and an agent is idle between turns — so on a Mac two people share, their models
take turns evicting each other in the pauses, and each pays a reload of seconds
to minutes on its next turn.

Eviction grace protects a model for a short while after it finishes work. A
request that needs that model's memory waits for it rather than taking it, and
the answer says that it waited.

Off unless you turn it on. Leave it off and requests are served exactly as they
always have been.

## Switch it on

1. Open the control panel and go to **Settings**.
2. Tick **Wait for a model to fall idle instead of evicting one that just
   finished**.
3. Set **Protect a model that just finished for** — how long a model is safe
   after its last request ends. The default is 120 seconds, about the length of
   a pause while somebody reads an answer.
4. Set **Wait at most** — the longest a request will wait for room before it is
   refused. The default is 300 seconds. Keep it under the timeout your clients
   use, or they give up first and see their own error instead of Gropius's.
5. **Save settings**.

It applies at once — no restart. A model already in memory is protected from
the next eviction, and switching it off again releases anything already
waiting.

## What a waiting request waits for

A request for a model that does not fit is served as soon as either happens:

- a resident, unpinned model has been idle for the protection interval, or
- the request has itself waited that long and a resident model is between
  requests.

The second rule is what keeps a busy model from blocking everyone: a client
sending a short request to one model every few seconds would otherwise keep it
protected forever. Waiting requests are served oldest first.

Three things end a wait early:

- **The memory budget goes up**, or a pin is removed, and the model now fits.
  The request proceeds without waiting for anything to finish.
- **A pin is added** that makes the request impossible. It is refused at once
  rather than waiting out the maximum.
- **The maximum wait runs out.** The request gets the same refusal it would
  have had immediately, now saying how long it waited.

## What never waits

- A request that could never fit — the pinned models plus what it needs are
  more than the whole budget — is refused straight away.
- A request that arrives when the queue is already full is refused straight
  away too. The queue is deliberately short: it drains only when a model is
  unloaded, and every waiting request holds its whole body in memory meanwhile.
- Models loaded at start-up by **Preload**, which load one after another and
  would otherwise wait for each other.
- A model you load yourself from the control panel does wait. You asked for it,
  and you can unload something to force the swap.

## The protection and the idle timeout

The protection may not be longer than **Unload models after idle** when that is
set. The idle timeout would otherwise unload the very model a request is
waiting on, and the wait would be for nothing. A save that breaks the rule is
refused, naming both figures.

A settings file edited by hand that breaks it is not refused — that would take
the whole install down to its defaults over one number. The protection is
shortened to the idle timeout instead, and the start-up log says so.

## What a client sees

Every answer carries two headers saying whether the request waited and for how
long: `X-Gropius-State` and `X-Gropius-Queue-Time`. The 503 a refused request
gets carries them too. See
[the response header reference](response-headers.md).

A client that would rather not wait at all can read residency from
[the models list](models-list.md) and ask for a model that is already loaded.

## What it costs

Switching this on means a request that used to be served immediately, by
evicting somebody else's model, may now wait instead. That is the trade: the
model in memory keeps it, and the request that wanted the room pays. On a Mac
with one user and models that all fit the budget, nothing ever waits and
nothing changes.

The control panel's **My Models** tab says how many requests are waiting for
memory, so a queue that is not draining is visible rather than inferred.
