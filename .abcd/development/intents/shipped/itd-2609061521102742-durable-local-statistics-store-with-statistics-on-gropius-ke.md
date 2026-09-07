---
id: itd-2609061521102742
slug: durable-local-statistics-store-with-statistics-on-gropius-ke
spec_id: spc-2609061822381335
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609061521082551]
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Durable local statistics store: with statistics on, Gropius keeps the per-request records on disk with a stated retention and a documented format, so months of local model use can be analysed later on that Mac

## Press Release

Alice has had statistics on for three months and the records are still there:
one line of JSON per record, in plain text files under her own account's data
folder, so she can point any tool she likes at them. In Settings she says how
many months to keep and sets a size limit, and the size limit always wins;
beside them the panel shows the date of the oldest record she still has, so
she can see how far back her own history reaches. Turning statistics off stops
new records; Clear removes what is there. Nothing in the store is a prompt, a
completion, a key, or a client's network address.

## Why This Matters

A live view forgets on restart, and the point of turning statistics on is to
learn how local models are actually used over time: which models, how many
tokens, what latencies, and how that changes. That needs records that outlive
the process, kept on the Mac that produced them, under the same opt-in the
live view has (adr-2609061503319212). Files on other people's machines are
hard to change once they exist, which is why the format, the retention rule
and the location were decided before the spec, in adr-2609061610107154.

## Mechanism

We expect a size-capped JSON Lines store to hold months of local use on one
Mac without a database because a record is about 150 bytes, so ten thousand
requests a day is about 1.5 MB a day, and a single sequential pass over a
store at the default cap answers the dashboard's aggregates within two seconds
on Apple Silicon.

## Scope Conditions

- One serving process writes one store, under the account that runs it, on an <!-- cond: cond-2609061822385818 -->
  Apple Silicon Mac.
- In the shared-cache install mode one process serves every account on the <!-- cond: cond-2609061822386584 -->
  Mac, so the store holds every local account's requests under the serving
  account's opt-in.
- The store holds only what was recorded while the switch was on; the size cap <!-- cond: cond-2609061822382727 -->
  is the hard bound and the months figure prunes within it.
- A crash loses at most the few seconds of records not yet written out. <!-- cond: cond-2609061822382003 -->
- Record volume is bounded by the pool's per-model concurrency, so hundreds of <!-- cond: cond-2609061822385461 -->
  records a minute at most rather than thousands a second.

## Acceptance Criteria

- Given the switch is on, when ten requests complete and Gropius is restarted,
  then the ten records are readable from the store's files, each carrying the
  documented fields and a schema version.
- Given the switch is off, when requests complete, then no store file is
  created and no existing store file changes.
- Given a store at its size limit, when further records are written, then the
  total stays at or under the limit and the panel shows the date of the oldest
  record still held.
- Given records on disk, when Alice presses Clear, then the store holds no
  record files and the per-model logs are untouched.
- Given a request carrying a sentinel string in its prompt, a bearer token in
  its headers and a non-loopback client address, when it is recorded, then a
  byte scan of every file in the store finds none of the three.
- Given a store location that resolves inside a group-writable directory, when
  Gropius starts, then it refuses to create the store there.
- Given the reference page, when it is compared with a record the current
  build writes, then every field in the record is described on the page and no
  described field is missing from the record.

## Open Questions

- Resolved: format — append-only JSON Lines, one object per line, rotated by
  size, every file carrying a schema version so a later Gropius reads older
  files. No database and no new dependency.
- Resolved: retention — a months figure and a size cap, both in Settings, with
  the size cap as the hard bound; the panel shows the date of the oldest
  record beside them.
- Resolved: location and permissions — per account under the per-user data
  root, files owner-only and opened without following symlinks, in every
  install mode.
- Resolved: record schema — request lines; load and eviction events each
  carrying their reason; a startup record of the effective settings; UTC
  timestamps to the second.
- Resolved: shared-cache mode — the store belongs to the account running the
  serving process and holds every local account's requests under that
  account's opt-in; the documentation says so in one sentence.
- Resolved: the session label reserved for the telemetry pack is not written;
  that intent (itd-2609061521134968) is retired as contradicting
  adr-2609061503319212.
- Deferred: rotation thresholds, file naming and exact field names are the
  spec's, within the format the decision record fixes.
- Deferred: the measured scan time at the cap is taken during the spec rather
  than before the format was fixed. The reviewer asked for that measurement
  first; the maintainer fixed the format now and the decision record names the
  measurement as the condition for reconsidering a database instead.
- Depends on: adr-2609061610107154 (statistics store format, retention and
  location), minted this session.
- Depends on: adr-2609061503319212 (no public telemetry; local only; strict
  opt-in, off by default).
- Depends on: itd-2609061521082551, whose records this store keeps.

## Audit Notes

<!-- abcd-review: OWED receipt=rcp-799061841f58 -->
Fidelity review OWED (receipt rcp-799061841f58).

## Grounds

- pursued: we want to learn how local models are actually used on this Mac, which models, how many tokens, what latencies, and we expect a month of records to change which models we keep and how we set the memory budget; we are wrong if, after a month with the dashboard, no such decision has changed
