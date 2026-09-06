# Record request statistics on this Mac

Gropius can keep a record of the requests it serves, so you can see how each
model actually performs: how fast the first token arrives, how many tokens a
second it generates, how often a model has to be loaded, and how often it is
pushed out to make room for another. That is how two quantisations of the same
model are compared by numbers instead of by feel.

Recording is off until you turn it on. Nothing recorded leaves this Mac —
not to the project, not to a vendor, not to anyone — because Gropius collects
no telemetry of any kind and never will
([the decision that settles it](../.abcd/development/decisions/adrs/2609061503319212-no-public-telemetry-local-telemetry-only-as-a-strict-opt-in.md)).

The boundary is the Mac, not your account. Read [Who can see it](#who-can-see-it)
before turning it on if other people log into this Mac.

## Switch it on

1. Open the control panel and go to **Settings → Request statistics**.
2. Tick **Record request statistics on this Mac**.
3. **Save settings**.

It applies to the next request; there is nothing to restart. Open the
**Statistics** tab to see the figures.

Clear the box and save to switch it off again. That stops recording *and*
empties what is held, so the Statistics tab goes back to showing nothing and
the Mac is in the state it was in before you ever turned it on.

## What is recorded

One record per request, made of these and nothing else:

- `model` — which of your models served it, as its repo id. A request that was
  refused before it named a model you have is recorded with no model at all;
  the name the client asked for is never kept.
- `at` — when the request arrived, to the second.
- `class` — how it ended: answered, rejected, too busy, no room, could not
  start, never ready, no answer, the model server's own refusal, the client
  leaving before the answer was done, or Gropius's own failure.
- `streamed` — whether the client asked for the answer a chunk at a time.
- `prompt_tokens` and `completion_tokens` — the model server's own count of
  what went in and what came out. Only an answered request carries them.
- `first_token_ms` — how long the client waited for the first chunk of a
  streamed answer.
- `duration_ms` — how long the whole request took, from the moment it arrived.
- `queue_wait_ms` — how long it waited for a free slot on a model that was
  already loaded.
- `load_wait_ms` — how long it waited for the model to load.

Alongside those, Gropius counts per model how many requests each outcome
accounted for, how many tokens went in and came out altogether, how many
times the model loaded and how many times it failed to, how long the last
load took, how many times it was evicted to make room for another, and the
last request's own timings. It also keeps a per-minute total of requests and
tokens for the last day.

Everything held is in memory, and a restart empties it. The Statistics tab
shows the most recent thousand requests one by one, and the totals for the
last hour and the last day, which reach back further than a thousand rows do
on a busy Mac.

## Who can see it

The control panel answers anyone who can reach it on this Mac, and asks for no
password. On a Mac only you log into, that is you. If other people have
accounts on this Mac — which is the point of the shared model cache — then any
of them can open the control panel, turn this switch on, and read what it
records. What they would see is which of your models served each request, when
it arrived, how many tokens it cost and how long it took: an activity
timeline, and the shape of your prompts by size. They would not see a prompt,
an answer, a key or an address, because none of those is ever recorded.

So on a shared Mac this switch is a decision for everyone who uses it, not
just for you. While it is on, the control panel says **Recording** beside the
server status, so anyone who opens it can see that it is.

## What is never recorded

- **No prompt.** Not the messages, not the text of them, not a hash of them.
  The model server's own count of the tokens they came to is recorded, and is
  listed above; nothing else about them is.
- **No answer.** The tokens are counted, by the model server; the words are
  not read.
- **No API key**, yours or a client's.
- **No client address.** Gropius does not record which machine on your network
  sent a request, so it cannot show you and cannot tell anyone else.

This is a property of how the recording is built rather than a filter applied
afterwards: the part of Gropius that keeps the figures is never handed a
request, its headers or its connection. The only text it can be given is the
id of a model already on this Mac.

## What Gropius asks the model server for

A model server only reports token counts on a streamed answer if the request
asks it to. While recording is on, Gropius sets `include_usage` inside the
request's `stream_options` on the client's behalf, and removes the extra chunk
from the answer before relaying it when the client did not ask for it. A
client that did ask keeps it. Either way, what the client receives is the
stream it would have received anyway.

## The three states, and why they are three

Recording is one of three separate states, and no two of them share a switch:

1. **Off.** Nothing worked out from a request is recorded or shown. This is
   how Gropius starts and how it stays until you say otherwise.
2. **Recording on.** The switch on this page: the counts and timings above,
   and nothing else, held in memory on this Mac.
3. **A model server's own log level.** Every model server Gropius starts runs
   at a level that writes what it is loading and its own errors, and writes no
   prompt and no answer. Above that level it would write every request and
   every response it produces to its log — prompts and completions both — so
   raising it is a different decision about a different thing, made per model,
   and this switch can never make it. A test fails the build if the statistics
   switch ever reaches the code that starts a model server.

Gropius offers no way to raise a model server's log level today. Keeping the
three apart is what makes it safe to add one later: turning on statistics
will never be the thing that turns on a transcript.

## What the process still logs, whether or not you switch this on

Gropius writes a line to its own log for each API request it serves. That line
records the method, path, status and duration of the request, and nothing
else — in particular, never the address of the client that made it.

Each model server also writes its own log, at the level described above: what
it is loading, how far a prompt has been processed, its own errors, and no
prompt and no answer.
