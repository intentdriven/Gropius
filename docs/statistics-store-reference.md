# Reference: the request statistics store

What Gropius writes down while [request statistics](request-statistics.md) are
switched on, where it writes it, and what removes it. Nothing here is written
while the switch is off.

## Where the files are

In a `stats` folder inside your own Gropius data folder — normally
`~/Library/Application Support/Gropius/stats`.

On a Mac with the [shared model cache](getting-started.md#9-sharing-across-user-accounts-optional)
the store is the one thing that does not follow the models: the models,
`config.json` and `registry.json` move to the shared folder, and the records
stay in the serving account's own `~/Library/Application Support/Gropius/stats`.
A shared folder is writable by every account on the Mac, and one account's
record of what it served has no business there.

The folder is yours alone (mode `0700`), and so is every file in it (`0600`).
Gropius refuses to write records into a folder any other account on this Mac
could write to, or one that belongs to another account; if it has to refuse, it
says so in its own log, keeps the figures in memory, and says on the Settings
page that nothing is being written.

## File names and format

Files are named `stats-YYYYMMDD-NNN.jsonl`: the date the file was started, in
UTC, and a counter within that day. One file is written at a time, and the next
is started when the current one passes 5 MB.

Beside them is one file that is not a file of records: `summary.jsonl`, which
holds the coarse per-model per-day summary of records that have been dropped.
It is described under [Retention](#retention).

A file holds one JSON object per line — the format every tool already reads:

```sh
cat ~/Library/Application\ Support/Gropius/stats/*.jsonl | jq -r 'select(.kind == "request") | [.model, .class, .completion_tokens] | @tsv'
```

Every line carries two fields before anything else:

| Field | Meaning |
| --- | --- |
| `v` | The version of this format the line was written under. A later Gropius can still read an older file. |
| `kind` | Which of the six kinds below the line is: `request`, `load`, `removed`, `settings`, `summary` or `summary_index`. A load and a removal are two spellings of the one event kind the [decision record](../.abcd/development/decisions/adrs/2609061610107154-statistics-store-format-json-lines-size-rotated-per-account.md) names, and the last two belong to the summary that is kept when detail is dropped, which is why that record counts three kinds and this page counts six. |

A reader should ignore a field it does not know, and skip a line it cannot
parse. A line whose `v` is newer than the reader understands is one to skip:
the version changes only when the shape changes. Gropius holds itself to the
same rule when it rewrites the summary: a line it cannot read is written back
exactly as it was found. Those lines are inside the summary's size bound like
everything else, so a great many of them are dropped oldest first rather than
allowed to crowd out the records.

Records are written a couple of seconds behind the requests they describe, and
Gropius never asks the disk to make sure they are really on it — forcing a
write on every request would put a slow disk in the path of every answer, to
protect figures worth less than the answer is. So a crash or a power cut costs
the last few seconds of records, and leaves the line being written at that
moment half-finished. Everything before it is there, and the half-finished line
costs itself alone: the next start closes it off before appending to it.

The summary is the one exception. It is forced to the disk, and the detail it
counts is removed only after that has succeeded, so a power cut in the middle
of a drop can leave the records and their summary both — never neither.

## `kind: "request"` — one request

The whole of what a request is recorded as. There is nothing else: no prompt,
no answer, no key, no client address (see
[What is never recorded](request-statistics.md#what-is-never-recorded)).

| Field | Meaning |
| --- | --- |
| `model` | Which of your models served it, as its repo id. A request refused before it named a model you have is recorded with no model at all; the name the client asked for is never kept. |
| `at` | When the request arrived, in whole UTC seconds. |
| `class` | How it ended: `ok`, `client_error`, `upstream_status`, `busy`, `refused`, `launch_failed`, `not_ready`, `unreachable`, `cancelled` or `gateway_error`. |
| `streamed` | Whether the client asked for the answer a chunk at a time. |
| `prompt_tokens`, `completion_tokens` | The model server's own count of what went in and what came out. Only an answered request carries them. |
| `first_token_ms` | How long the model took to produce the first chunk of a streamed answer, measured from the request arriving to that chunk reaching Gropius. `-1` when there was no streamed chunk at all. |
| `duration_ms` | How long the whole request took, from the moment it arrived. |
| `queue_wait_ms` | How long it waited for a free slot on a model that was already loaded. |
| `load_wait_ms` | How long it waited for the model to load. |

## `kind: "load"` — a model server became ready

| Field | Meaning |
| --- | --- |
| `at` | When the load finished, in whole UTC seconds. |
| `model` | The model's repo id. |
| `duration_ms` | How long the load took. |
| `failed` | Present and `true` when the model server started but never became ready. |

A load carries no `reason`: nothing in Gropius knows why a model was loaded
beyond the fact that something asked for it.

## `kind: "removed"` — a model server left memory

| Field | Meaning |
| --- | --- |
| `at` | When it left, in whole UTC seconds. |
| `model` | The model's repo id. |
| `reason` | One of `evicted`, `idle`, `unloaded`, `abandoned`, `load_failed`, `crashed`, `shutdown`. |

Only `evicted` is an eviction — a model taken out to make room for another.
Counting an idle reap, an operator's unload or a crash as one would make the
figures disagree with what happened.

## `kind: "settings"` — what Gropius was serving under

Written each time recording starts and whenever one of these changes, so a
change in the figures can be told from a change in the settings that produced
them.

| Field | Meaning |
| --- | --- |
| `at` | When it was written, in whole UTC seconds. |
| `budget_bytes` | The memory budget model servers are held to. |
| `decode_concurrency` | How many requests a model server batches while generating. |
| `idle_timeout_sec` | How long a model may sit idle before it is unloaded. Zero means never. |
| `stats_months`, `stats_max_bytes` | The two retention limits below. |

These are the values in force, not the ones saved: the first three take a
restart, so a saved value that is not yet running is not recorded as though it
were.

## `kind: "summary"` — one model's day, after its detail has gone

Written into `summary.jsonl` from the records of a file that is about to be
dropped, and kept long after them. There is one line per model per day, and it
holds counts and totals only: nothing here describes a single request.

| Field | Meaning |
| --- | --- |
| `at` | The start of the day this line covers, in whole UTC seconds. |
| `day` | The day it covers, as `YYYY-MM-DD`, in this Mac's own time rather than UTC — a day is the day the person using the Mac had. |
| `tz_offset_min` | How far that day's local time stood from UTC, in minutes, so a reader elsewhere can still place it. |
| `model` | Which model served the requests, as its repo id. |
| `requests` | How many requests were folded into this line. |
| `by_class` | How those requests ended, counted by the classes above. It adds up to `requests`. |
| `prompt_tokens`, `completion_tokens` | The day's totals for that model. |
| `duration_ms_total`, `queue_wait_ms_total`, `load_wait_ms_total` | The day's sums of the record fields of the same name. A sum and a count is what a mean is made of; a slowest request or a median would need the records themselves, which are exactly what is gone. |
| `first_token_ms_total` | The same sum for the time to the first streamed chunk, over the requests that produced one. |
| `first_token_requests` | How many requests those were, which is what `first_token_ms_total` is a mean over. |
| `loads`, `failed_loads` | How many times that model's server became ready that day, and how many times it started and never did. |
| `removals` | How many times it left memory, counted by the reasons above. |

## `kind: "summary_index"` — the first line of the summary

Bookkeeping rather than a figure: the detail files already counted in the
summary whose removal has not been seen through. It exists so that a crash
between writing the summary and removing the detail cannot make the same
records count twice — the next start removes what this line names, and empties
it.

| Field | Meaning |
| --- | --- |
| `folded` | The files already counted whose removal has not been seen through, each as the three fields below. Normally empty. |
| `name` | The file's name. |
| `bytes` | How large it was when it was counted. |
| `newest` | Its newest record, in whole UTC seconds. Together with `bytes` this tells the file that was counted from a later file that happens to have taken its name. |

## Retention

Two limits, both set in **Settings → Request statistics**:

- **`stats_months`** — six by default. A file whose newest record is older than
  this is removed, even when there is room for it.
- **`stats_max_bytes`** — 200 MB by default. The limit that always wins: the
  oldest file goes while the store, plus room for the next file, would be over
  it, whatever the months figure says.

Records are removed a whole file at a time, oldest first, so the store loses
its past rather than its present. A file written across the horizon is kept
until its own newest record falls beyond it, and then it goes too — including
the file being written, on a Mac quiet enough that it never fills.

Both limits are applied when a file is rotated, at least once an hour while
Gropius is running, when recording starts, and the moment either figure is
changed.

Before a file is removed, whichever limit removes it, its records are folded
into `summary.jsonl`: one line per model per day, holding the counts and totals
described above and nothing else. The summary is written and forced to the disk
before the detail goes, so records are never lost without being counted. A day
already summarised is extended rather than written a second time, so the drop
that takes the rest of a day adds to the line the first drop left.

Summaries are not removed by the months limit — a summary of last spring is
what makes the shape of last spring's use visible once its detail is gone.
What bounds them is room: the summary counts toward `stats_max_bytes` like
everything else, and it may use a twentieth of it. Over that, its oldest days
go first, and the most recent day is always kept, because a summary that
emptied itself would take room while saying nothing. A summary line is at most
800 bytes, so at the 200 MB default the summary's twentieth holds 3 years of
daily lines for ten models, and far more for fewer.

A file the store is about to drop that it cannot read, or a summary it cannot
write, stops retention rather than the records: nothing is removed that has not
been counted, the Settings page says so, and Gropius's own log says why. The
size limit is still the limit, though. Once the store is over it by more than
one file's growth and still cannot summarise what it would drop, the oldest
file goes without a summary — a full disk is exactly what stops a summary being
written, and a store that could not then free its own room would make a full
disk permanent. Settings counts those records separately, so a loss is never
silent.

Anything under one of these names that is not a plain file — a named pipe, a
device, a folder — is refused rather than read. Gropius never waits on
something in this folder to answer it.

A record measures about 230 bytes, so 200 MB is roughly three months of ten
thousand requests a day. How far back the store actually reaches is shown on
the Settings page beside the two limits, rather than promised here.

## What removes the files

- **Clear records** in Settings removes every file the store wrote, the summary
  among them, and nothing else in the folder. It works whether recording is on or off, and leaves the
  switch as it found it.
- **Deleting the Gropius data folder** removes them with everything else, as
  [Uninstalling](getting-started.md#uninstalling) describes.

Switching recording off does neither: it stops new records and leaves the ones
already written where they are.
