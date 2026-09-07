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
- `first_token_ms` — how long the model took to produce the first chunk of a
  streamed answer, measured from the request arriving to that chunk reaching
  Gropius. It is `-1` for a request that produced no streamed chunk at all,
  and the panel shows a dash.
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

The Statistics tab shows the most recent thousand requests one by one, and the
totals for the last hour and the last day, which reach back further than a
thousand rows do on a busy Mac. Those live figures are held in memory and a
restart empties them; the records themselves are kept on disk and outlive the
process, which is what the rest of this page is about.

## Where the records are kept

In a `stats` folder inside your own Gropius data folder — the same folder the
models and the settings live in, `~/Library/Application Support/Gropius`,
unless you moved it. Settings shows the exact location beside the two limits.
The folder is yours alone (mode `0700`) and so is every file in it (`0600`),
and Gropius refuses to write records into a folder any other account on this
Mac could write to. If it has to refuse, it says so in its log and keeps the
figures in memory instead.

Files are named `stats-YYYYMMDD-NNN.jsonl`, dated in UTC, and hold one JSON
object per line — the format every tool already reads:

```sh
cat ~/Library/Application\ Support/Gropius/stats/*.jsonl | jq -r 'select(.kind == "request") | [.model, .class, .completion_tokens] | @tsv'
```

Each line carries `v`, the version of the format it was written under, so a
later Gropius can still read an older file. A reader should ignore a field it
does not know and skip a line that does not parse: if the Mac loses power
mid-write, the line being written at that moment is left half-finished, and
that line is the whole of what is lost.

There are four kinds of line, told apart by `kind`:

- `request` — one request, carrying the fields listed above.
- `load` — a model server became ready, or failed to. `duration_ms` is how
  long it took and `failed` says which of the two happened.
- `removed` — a model server left memory, with `reason`: `evicted` to make
  room for another, `idle` after a spell with no requests, `unloaded` because
  you asked, `crashed`, `shutdown`, `abandoned` or `load_failed`. Only
  `evicted` is an eviction; counting the others as one would make the figures
  disagree with what happened.
- `settings` — written each time recording starts and whenever one of these
  changes, so you can tell a change in the figures from a change in the
  settings that produced them: `budget_bytes`, `decode_concurrency`,
  `idle_timeout_sec`, `stats_months` and `stats_max_bytes`. These are the
  values in force, not the ones saved: the first three take a restart.

## How much is kept

Two limits, both in **Settings → Request statistics**:

- **Keep records for (months)** — six by default. A file whose newest record
  is older than this is removed even when there is room for it.
- **Never use more than (MB)** — 200 by default. This is the limit that always
  wins: while the records take more room than this, the oldest file goes,
  whatever the months figure says.

Records are removed a whole file at a time, oldest first, so the store loses
its past rather than its present; a file that was being written across the
horizon is kept until its own newest record falls beyond it. A record is about
150 bytes, so 200 MB is over four months of ten thousand requests a day.

Beside the two limits, Settings shows the date the records reach back to, how
much room they use, and — if the disk could not keep up with a burst — how
many records were dropped rather than made to hold up an answer.

## What removes the records

- **Clear records**, the button under the two limits, removes every record
  file and empties the Statistics tab. It leaves recording on.
- **Deleting the Gropius data folder** removes them with everything else, as
  [Uninstalling](getting-started.md#uninstalling) describes.

Switching recording off does neither: it stops new records and leaves the ones
already written where they are.

## On a Mac several people share

With the shared model cache, whoever launches Gropius first runs the server
and everyone else's menu-bar app points at it, so one process serves every
account. The records are that account's: they are kept in the serving
account's own folder, under that account's opt-in, and they cover every
request the server handled — including requests from other accounts on this
Mac. The records still say nothing about who sent a request, because no client
address is recorded.

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
