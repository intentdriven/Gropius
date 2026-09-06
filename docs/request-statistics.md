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
  start, never ready, no answer, the model server's own refusal, or the client
  leaving before the answer was done.
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
accounted for, how many times the model loaded and how long the last load
took, and how many times it was evicted to make room for another.

Everything held is in memory, and a restart empties it. The Statistics tab
shows the most recent thousand requests and a day of per-minute totals.

## What is never recorded

- **No prompt.** Not the messages, not the text of them, not their length in
  characters, not a hash of them.
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

## What the process still logs, whether or not you switch this on

Gropius writes a line to its own log for each API request it serves. That line
records the method, path, status and duration of the request, and nothing
else — in particular, never the address of the client that made it.

Each model server also writes its own log, at its normal level: what it is
loading, how far a prompt has been processed, and its own errors. It writes no
prompt and no answer at that level. Raising a model server's log level is a
separate, deliberate action per model, and it says so where you do it: at the
higher level that model server writes every request and every response it
produces to its log. Switching request statistics on never changes any model
server's log level.
