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

A file holds one JSON object per line — the format every tool already reads:

```sh
cat ~/Library/Application\ Support/Gropius/stats/*.jsonl | jq -r 'select(.kind == "request") | [.model, .class, .completion_tokens] | @tsv'
```

Every line carries two fields before anything else:

| Field | Meaning |
| --- | --- |
| `v` | The version of this format the line was written under. A later Gropius can still read an older file. |
| `kind` | Which of the four kinds below the line is: `request`, `load`, `removed` or `settings`. A load and a removal are two spellings of the one event kind the [decision record](../.abcd/development/decisions/adrs/2609061610107154-statistics-store-format-json-lines-size-rotated-per-account.md) names, which is why it counts three kinds and this page counts four. |

A reader should ignore a field it does not know, and skip a line it cannot
parse. A line whose `v` is newer than the reader understands is one to skip:
the version changes only when the shape changes.

Records are written a couple of seconds behind the requests they describe, and
Gropius never asks the disk to make sure they are really on it — forcing a
write on every request would put a slow disk in the path of every answer, to
protect figures worth less than the answer is. So a crash or a power cut costs
the last few seconds of records, and leaves the line being written at that
moment half-finished. Everything before it is there, and the half-finished line
costs itself alone: the next start closes it off before appending to it.

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

A record measures about 230 bytes, so 200 MB is roughly three months of ten
thousand requests a day. How far back the store actually reaches is shown on
the Settings page beside the two limits, rather than promised here.

## What removes the files

- **Clear records** in Settings removes every file the store wrote, and nothing
  else in the folder. It works whether recording is on or off, and leaves the
  switch as it found it.
- **Deleting the Gropius data folder** removes them with everything else, as
  [Uninstalling](getting-started.md#uninstalling) describes.

Switching recording off does neither: it stops new records and leaves the ones
already written where they are.
