# Give a busy model a moment before it is evicted

Eviction grace protects a model for a short while after it finishes work. A
request that needs that model's memory waits for it rather than taking it, and
the answer says that it waited. It is what stops two people's models evicting
each other in the pauses between turns;
[Why a request waits instead of taking the memory](eviction-grace-explained.md)
says why that happens and what the wait costs.

Off unless you turn it on. Leave it off and a request for a model that does not
fit unloads a model at once, exactly as it always has.

## Switch it on

1. Open the control panel and go to **Settings**.
2. Tick **Wait for a model to fall idle instead of evicting one that just
   finished**.
3. Set **Protect a model that just finished for** — how long a model is safe
   after its last request ends. The default is 120 seconds, about the length of
   a pause while somebody reads an answer.
4. Set **Wait at most** — the longest a request will wait for room before it is
   refused. The default is 300 seconds. Keep it under the timeout your clients
   use, or they give up first and see their own error instead of Gropius's, and
   at or above the protection above: a maximum shorter than the protection is
   refused, because it would refuse a waiting request before its own wait could
   override that protection, which is the starvation this feature exists to
   prevent.
5. **Save settings**.

It applies at once — no restart. A model already in memory is protected from
the next eviction, and switching it off again releases anything already
waiting.

## What a waiting request waits for

Once anything is waiting, every request for a model that is not already in
memory joins the queue behind it — including one there is room for. Free memory
belongs to the request that has waited longest, or a stream of small requests
would take it as it appears while a request needing more than any one unload
frees never fits.

A waiting request is served as soon as either happens:

- a resident, unpinned model has been idle for the protection interval, or
- the request has itself waited that long and a resident model is between
  requests.

The second rule is what keeps a busy model from blocking everyone: a client
sending a short request to one model every few seconds would otherwise keep it
protected forever. Waiting requests are served oldest first.

Three things end a wait early:

- **The memory budget goes up** so that the model now fits. Every waiting
  request the raise fits is served on it, oldest first, without any model
  unloading or any request ending. Removing a pin wakes them too, though a
  model that has just been unpinned is still inside its own protection until
  that runs out.
- **A pin is added** that makes the request impossible. It is refused at once
  rather than waiting out the maximum.
- **The maximum wait runs out.** The request gets the same refusal it would
  have had immediately, now saying how long it waited.

## What never waits

- A request that could never fit — the pinned models plus what it needs are
  more than the whole budget — is refused straight away.
- A request that arrives when the queue is already full and needs a model
  unloaded is refused straight away too. The queue is deliberately short: it
  drains only when a model is unloaded, and every waiting request holds its
  whole body in memory meanwhile. One that arrives at that point and needs
  nothing unloaded — its model is in memory already, or it fits in memory
  nobody is using — is served rather than refused, since the queue it cannot
  join is not waiting for what it needs. Below the cap it queues like anything
  else.
- Models loaded at start-up by **Preload**, which load one after another and
  would otherwise wait for each other. They are not held behind the queue
  either.
- A model you load yourself from the control panel does wait. You asked for it,
  and you can unload something to force the swap.

## The protection and the idle timeout

The protection may not be longer than **Unload models after idle** when that is
set. The idle timeout would otherwise unload the very model a request is
waiting on, and the wait would be for nothing. Three things enforce it.

- **A save that breaks it is refused**, naming both figures.
- **A settings file edited by hand is not** — that would take the whole install
  down to its defaults over one number. The protection is shortened to the idle
  timeout instead, and the start-up log says so.
- **A raised idle timeout does not take effect until you restart**, but a
  protection does. So a single save that raises the timeout to 900 seconds and
  sets a 600 second protection is accepted and stored, and until you restart the
  protection runs at the timeout still in force. The start-up log and the save's
  own log line say so. Restart and you get the pair you asked for.

## What a client sees

On an install with an API key set, an answer that came from a model server
carries two headers saying whether the request waited and for how long:
`X-Gropius-State` and `X-Gropius-Queue-Time`. The 503 a request refused for
want of memory gets carries them too. An open server sends them to nobody, the
same rule [the models list](models-list.md) applies to residency. See
[the response header reference](response-headers.md).

A client that would rather not wait at all can read residency from
[the models list](models-list.md) and ask for a model that is already loaded.

## What this costs you

Switching this on means a request that would otherwise be served immediately, by
evicting somebody else's model, may wait instead. The control panel's
**My Models** tab says how many requests are waiting for memory, so a queue
that is not draining is visible rather than inferred. See
[Why a request waits instead of taking the memory](eviction-grace-explained.md)
for the trade in full, including the case that reaches the maximum wait most
easily.
