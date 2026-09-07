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

Clear the box and save to switch it off again. That stops recording and
empties the Statistics tab, so nothing worked out from a request is shown any
more. The records already written to this Mac stay where they are until you
remove them; [What removes the records](#what-removes-the-records) says how.

## What is recorded

One record per request: which of your models served it, when it arrived, how
it ended, whether it streamed, the model server's own count of the tokens in
and out, how long the first token took, how long the whole request took, and
how long it waited for a free slot or for the model to load. That is the whole
of it, field by field, in
[Reference: the request statistics store](statistics-store-reference.md).

Alongside those, Gropius counts per model how many requests each outcome
accounted for, how many tokens went in and came out altogether, how many
times the model loaded and how many times it failed to, how long the last
load took, how many times it was evicted to make room for another, and the
last request's own timings. It also keeps a per-minute total of requests and
tokens for the last day.

The Statistics tab shows the most recent thousand requests one by one, and the
totals for the last hour and the last day, which reach back further than a
thousand rows do on a busy Mac. Those live figures are held in memory and a
restart empties them.

Under them the same tab reads the records back over a range you choose — four
tables: tokens per day by model with each model's share, how long requests
took, the spread of those times, and when models were evicted and reloaded.
[Understanding the historical views](statistics-explained.md) says what each of
those tables means and what none of them can show.

The records themselves are written to a folder of plain text files that
outlives the process — one line of JSON each, which any tool can read. Where
they are, what every line holds and what removes them is the
[reference page](statistics-store-reference.md); the rest of this page is what
you do about them.

## How much is kept, and where

Records go to a `stats` folder inside your own Gropius data folder, normally
`~/Library/Application Support/Gropius/stats`. On a Mac with the shared model
cache, the models live in the shared folder and the records do not: they stay
in the serving account's own folder, because a shared folder is writable by
every account on the Mac.

Two limits, both under the switch in **Settings → Request statistics**:

1. **Keep records for (months)** — six by default.
2. **Never use more than (MB)** — 200 by default. This is the limit that always
   wins.

Set either and **Save settings**; a limit you lower takes effect when you save
it. A record measures about 230 bytes, so 200 MB is roughly three months of
ten thousand requests a day — but how far back your own store reaches depends
on how much you use it, which is why Settings shows the date beside the two
limits rather than promising a span.

Beside them Settings also shows how much room the records use, and says when
something went wrong: how many records were dropped rather than made to hold up
an answer, if the disk could not keep up with a burst; and, if Gropius could not
open the store at all, that the figures are being kept in memory and nothing is
on disk.

The exact rules — when the limits are applied, and what is removed — are on the
[reference page](statistics-store-reference.md#retention).

## What removes the records

- **Clear records**, the button under the two limits, removes every record
  file and empties the Statistics tab. It leaves recording as it found it, and
  works whether recording is on or off — so you can stop recording first and
  then decide the history should go too. While recording is off the panel shows
  nothing about the store, here as everywhere else, so this is the one control
  you press without a figure beside it.
- **Deleting the Gropius data folder** removes them with everything else, as
  [Uninstalling](getting-started.md#uninstalling) describes.

Switching recording off does neither: it stops new records and leaves the ones
already written where they are.

## On a Mac several people share

With the shared model cache, whoever launches Gropius first runs the server
and everyone else's menu-bar app points at it, so one process serves every
account. The records are that account's: they are kept in the serving
account's own folder — not the shared one the models are in — under that
account's opt-in, and they cover every request the server handled — including
requests from other accounts on this Mac. The records still say nothing about
who sent a request, because no client address is recorded.

## Who can see it

The control panel answers anyone who can reach it on this Mac, and asks for no
password. On a Mac only you log into, that is you. If other people have
accounts on this Mac — which is the point of the shared model cache — then any
of them can open the control panel, turn this switch on, and read what it
records. What they would see is which of your models served each request, when
it arrived, how many tokens it cost and how long it took: an activity
timeline, and the shape of your prompts by size. They would not see a prompt,
an answer, a key or an address, because none of those is ever recorded.

That reaches as far back as the records do, not merely as far as the live view.
The records themselves are files only your account can open, but the Statistics
tab reads them back and shows the totals to whoever has the panel open, so what
another account can see is months of which models served what, when in the day,
and how fast — not the last thousand requests alone.
[Understanding the historical views](statistics-explained.md) is what those
tables are.

Any of them can also press **Clear records** and remove what is kept, which
Gropius notes in its own log without being able to say who did it — the
control panel has no idea who is asking.

So on a shared Mac this switch is a decision for everyone who uses it, not
just for you. While it is on, the control panel says **Recording** beside the
server status, so anyone who opens it can see that it is.

## What is never recorded

- **No prompt.** Not the messages, not the text of them, not a hash of them.
  The model server's own count of the tokens they came to is recorded, and is
  named on the [reference page](statistics-store-reference.md); nothing else
  about them is.
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
   and nothing else, kept on this Mac — in memory for the live view, and in
   the files the [reference page](statistics-store-reference.md) describes.
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
