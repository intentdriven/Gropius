# Why a request waits instead of taking the memory

Gropius holds a fixed amount of memory for loaded models, and when a request
arrives for a model that does not fit, something has to go. The rule that picks
what goes is least-recently-used: the idle model nobody has touched for longest.
The [models list reference](models-list.md) states the rules;
[Give a busy model a moment before it is evicted](eviction-grace.md) says how to
switch this on. This page is about why it exists and what it costs.

## What is wrong with least-recently-used on a shared Mac

Least-recently-used is the right rule for one person. It treats a model idle for
one second exactly the same as one idle for an hour, and when one person is
working that is fine — the model they have stopped using is the one they have
stopped using.

Two people break it, and so does one person with two agents. An agent is idle
between turns: it answers, and then nothing happens while a human reads. Those
pauses are seconds to minutes, and they are exactly when somebody else's request
arrives. The model that answered a moment ago is, by the only measure the rule
has, the least recently used one — so it goes, and the next turn pays a reload
of seconds to minutes for memory that was free for a few seconds.

The two clients then take turns doing this to each other. Neither is doing
anything wrong, and neither can tell it is happening.

## Why a wait rather than a reservation

A wait is the whole mechanism. There is no reservation interface, no broker in
front of the pool and no second process: a request that does not fit joins a
queue inside the pool that owns the memory, and is served when the memory is
genuinely free rather than when it can be taken.

That is a deliberate choice about where fairness lives. A broker in front of the
pool would have to model the pool's memory to decide anything, which is the same
decision made twice, in two places, from two sets of facts. The pool already
knows what is loaded, what is busy and what the budget is.

## The two clocks, and why the second one matters

A model is protected for the grace interval after it finishes work. On its own,
that rule can be gamed without anyone meaning to: a client sending a one-token
request to one model every few seconds keeps renewing its protection, and
another client's request for another model never gets in. That is starvation —
a failure least-recently-used does not have, and one this feature must not
introduce in exchange for what it fixes.

So a waiting request carries its own clock. Once it has waited longer than the
protection interval, it may take a model that is merely between requests, and
the trickle stops mattering. Whichever clock runs out first decides.

## Why the queue is short and strictly ordered

Waiting requests are served oldest first. The alternative — serve whichever load
is quickest — starves large models: under a stream of requests for small ones, a
request for a large model never sees enough free memory at once. Order is what
makes the wait bounded for everybody rather than bounded on average.

The queue is deliberately short. It drains when a model is unloaded, which is
seconds to tens of seconds, rather than at the speed a model generates tokens,
and every request waiting in it is holding its whole body in memory meanwhile —
memory the budget does not count, because the budget counts model weights. A few
of those is fine; a hundred is a way to fill the machine without ever being
admitted to anything.

## What it costs

Switching this on means a request that used to be served immediately, by
evicting somebody else's model, may now wait instead. That is the trade: the
model in memory keeps it, and the request that wanted the room pays. On a Mac
with one user and models that all fit the budget, nothing ever waits and nothing
changes.

A waiting request holds its connection open, so the maximum wait is also how
long a client can occupy one. The queue's own length is the second bound.

The case that reaches the maximum most easily is not a protected model but a
busy one. A model with a request still running is never an eviction candidate at
all, so one long generation turns every request for another model into a wait
rather than an instant refusal, for as long as it runs — and because the queue
is one queue for the whole machine, a request stuck at its head holds up every
load behind it, from every client, until its own maximum runs out. Requests for
models already in memory are unaffected throughout, and so is a request that
arrives at a full queue needing nothing unloaded. On a Mac whose endpoint is
open to the network, that is worth weighing against a shorter maximum wait.

## Why the protection is bound to the idle timeout

The idle reaper unloads a model that has gone untouched for the idle timeout,
whether or not anything needs the room. A protection longer than that timeout is
a promise the reaper breaks: the model a request is waiting for is unloaded by
the very rule the wait was supposed to outlast, and the request waits for
nothing. The two figures are held to each other everywhere they can be, which
the how-to lists.
