---
id: adr-2609061610107154
slug: statistics-store-format-json-lines-size-rotated-per-account
status: superseded by adr-2609090716413337
date: 2026-09-06
supersedes: null
superseded_by: adr-2609090716413337
related_intents: [itd-2609061521102742, itd-2609061602043757, itd-2609061521159233]
related_rfcs: []
related_adrs: [adr-2609061503319212]
---

# ADR-2609061610107154: Statistics store format: JSON Lines, size-rotated, per account, with months and size caps in Settings

## Context

The local-statistics intents keep per-request records on the operator's Mac
so that months of local model use can be analysed later, under the opt-in
that adr-2609061503319212 requires. Files on users' machines are hard to
change once they exist, so the format, the retention rule and the location
were decided before the store's spec, in the planning interview of
2026-09-06.

The 2026-09-06 research note on local telemetry compared the options: a
bounded in-memory ring needs no file at all; append-only JSON Lines needs no
dependency and any tool can read it; SQLite makes months of queries cheap but
adds either a C toolchain through cgo or a very large pure-Go dependency
tree to a module that has three direct dependencies today, each new one
needing explicit sign-off. The adversarial review of the store draft added
that in the shared-cache install mode one process serves every account on
the Mac, so whatever is written is written under the serving account.

## Decision

We will store statistics as append-only JSON Lines: one JSON object per
line, one file at a time, rotated by size. Files carry a schema version so a
later Gropius can read older files. Record kinds are a request line (model,
status class, token counts, timings, queue wait, a coarse UTC timestamp), a
load or eviction event with its reason, and a startup record of the
effective settings.

We will bound retention two ways, both set in Settings: a months figure and
a size cap, with the size cap the hard bound that always wins. The panel
shows the date of the oldest record so the operator knows how far back the
store reaches. Before rotation drops a file, a coarse per-model per-day
summary of it is written (itd-2609061602043757).

We will keep the store per account under the per-user data root, in files
opened owner-only with no-follow, as the launcher already does for logs. In
the shared-cache install mode the store belongs to the account that runs the
serving process and holds the requests of every local account that used it,
under that account's opt-in; the docs say so in one sentence.

We will not add SQLite. The condition for reconsidering is a measured one:
the dashboard failing to render a month of records at the default size cap
within a few seconds on the slowest supported Mac.

## Alternatives Considered

1. **JSON Lines, size-rotated (chosen).** No dependency, any tool reads it,
   growth is bounded by construction. Queries are a linear scan, acceptable
   because the dashboard pre-aggregates and the summary intent keeps a
   coarse record beyond the cap.
2. **SQLite through cgo.** Cheap queries in one file. Rejected: a fourth
   direct dependency, a C toolchain in the build, and cross-compilation cost
   for kilobytes to megabytes of data.
3. **SQLite through a pure-Go port.** No cgo. Rejected: a very large
   dependency tree for the same small data.
4. **JSON Lines plus writer-maintained daily rollup files.** Bounded query
   cost with no dependency. Rejected as the primary format because it keeps
   two formats consistent forever; its rollup idea survives as the summary
   written before deletion, which is produced once rather than maintained.
5. **Time cap only or size cap only.** Rejected: months alone leaves disk
   unbounded under heavy use; size alone hides how far back the store
   reaches. Both, with size as the hard bound, gives the operator a horizon
   and the machine a limit.

## Consequences

- The store's spec inherits the format, the record kinds and the location;
  it decides field names and the rotation thresholds.
- Settings gains three fields: the months figure, the size cap, and the
  existing statistics switch that gates all of it.
- The dashboard reads files, not a database, and must pre-aggregate; the
  summary intent is what preserves history beyond the cap.
- The shared-mode condition is a scope condition on the store intent and a
  documented fact, not a hidden behaviour.
- Reconsidering SQLite needs the measurement named above and a new ADR
  that supersedes this one.
