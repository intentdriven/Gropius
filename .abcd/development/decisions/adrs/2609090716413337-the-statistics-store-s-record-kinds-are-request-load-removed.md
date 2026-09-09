---
id: adr-2609090716413337
slug: the-statistics-store-s-record-kinds-are-request-load-removed
status: accepted
date: 2026-09-09
supersedes: adr-2609061610107154
superseded_by: null
related_intents: [itd-2609061521102742, itd-2609061602043757]
related_rfcs: []
related_adrs: [adr-2609061610107154, adr-2609061503319212]
---

# ADR-2609090716413337: The statistics store's record kinds are a request line, a load, a removal carrying one of seven reasons, and a settings record

**Supersedes:** [adr-2609061610107154](2609061610107154-statistics-store-format-json-lines-size-rotated-per-account.md),
on the record kinds only.

## Context

adr-2609061610107154 settled the statistics store's format, its retention rule
and its location, and in the same paragraph reserved the record kinds to
itself: "a request line (model, status class, token counts, timings, queue
wait, a coarse UTC timestamp), a load or eviction event with its reason, and a
startup record of the effective settings".

What shipped names the middle one differently, and says more. `internal/stats`
declares an `EventKind` with two values, `load` and `removed`, and
`RemovalReasons()` enumerates seven reasons a model server leaves memory:
`evicted`, `idle`, `unloaded`, `abandoned`, `load_failed`, `crashed`,
`shutdown`. Only the first of those is an eviction. `docs/` already documents
the shipped shape: the store's reference page carries a `kind: "removed"`
section listing all seven reasons and stating that only `evicted` is a model
taken out to make room for another, and `internal/archtest` holds that page to
`stats.RemovalReasons()`, so a reason added in Go without a word about it on
the page fails the build.

iss-2609080852262710 recorded the gap: shipped behaviour against a ratified
record, found by a fidelity review rather than by anything going wrong. A
ratified record is not edited to match what shipped — a decision record whose
text moves is no longer evidence of what was decided, and the index of records
would then be a record of the present rather than of the reasoning. So the
divergence is settled the only way it can be: by superseding the sentence that
is wrong, and leaving the rest of that record standing.

## Decision

We supersede adr-2609061610107154 on the record kinds only, and adopt what
shipped as the ratified set.

The store's record kinds are:

1. **A request line** — the model's repo id, the outcome class, token counts,
   timings, the queue and load waits, and a coarse UTC timestamp.
2. **A load** — a model server that became ready, with how long it took, and
   whether it started but never answered.
3. **A removal**, spelled `removed`, carrying exactly one of seven reasons:
   `evicted`, `idle`, `unloaded`, `abandoned`, `load_failed`, `crashed`,
   `shutdown`. Only `evicted` is an eviction.
4. **A settings record** — what Gropius was serving under, written when
   recording starts and whenever one of those settings changes.

We keep the richer kind rather than folding the seven reasons back into a
single eviction event, for three reasons.

- **The reasons are what an operator needs.** A model that left because
  another needed room is the memory budget working as designed; one that
  crashed, or failed to load at all, is a fault to look into; an idle reap or
  an operator's own unload is neither. Recording all seven as "evicted" would
  make the load and eviction figures disagree with what actually happened, and
  would answer the one question the store exists to answer — why was that
  request slow — with a figure that is wrong six times out of seven.
- **The shipped shape is already documented and already gated.** The store's
  reference page under `docs/` carries the seven reasons, and the architecture
  test holds that page to the Go list. Renaming the kind would mean changing a
  page, a test and an on-disk format that files on operators' machines are
  already written in — and the record being superseded exists precisely
  because "files on users' machines are hard to change once they exist".
- **`evicted` as a kind name would be untrue.** The name of a record kind is
  read by whoever writes the `jq` line, and six of the seven paths out of the
  pool are not evictions.

The count of kinds is unchanged at three collection paths — a request, an
event, a settings record — which is what the recorder's `Store` seam carries.
The two further kinds a file on disk holds, the per-model day summary and its
index, belong to the summary intent (itd-2609061602043757) and are not
reopened here.

## Alternatives Considered

1. **Supersede on the record kinds and adopt the shipped `removed` kind
   (chosen).** The ratified record and the running code agree, the earlier
   reasoning stays legible as what was thought on 2026-09-06, and exactly one
   sentence is marked as no longer in force.
2. **Edit adr-2609061610107154 in place.** One file, no new record. Rejected:
   a ratified decision record is never rewritten. The only edit it takes is to
   its status, and a record that quietly acquires the answer it did not have
   destroys the evidence that anyone ever decided otherwise.
3. **Change the code to match the record** — rename `removed` to `evicted` and
   drop the reasons. Rejected: it throws away information the operator needs,
   rewrites an on-disk format already present on machines, and would take the
   reference page and its architecture test with it, all to preserve a
   sentence written before the pool's removal paths were enumerated.
4. **Leave the divergence in the issue ledger, unratified.** Rejected: it
   leaves the ratified record saying one thing and the shipped store another,
   so a reader who was not in the room cannot tell which is in force — which
   is the whole job of the record.
5. **Supersede the whole of adr-2609061610107154 and restate it.** Rejected:
   the rest of that record — JSON Lines, size rotation, the schema version,
   the months figure and the size cap, the per-account location, the
   shared-mode consequence, and not adding SQLite — is in force and unchanged.
   Restating it would put the same decision in two documents and hide which
   sentence actually moved.

## Consequences

- adr-2609061610107154 is marked superseded by this record. Its status changes
  and its content does not; everything it decided other than the record kinds
  remains in force and is read from there, not from here.
- `stats.RemovalReasons()` and the store's reference page are together the
  ratified list, and the architecture test that holds them to each other is
  what keeps them one list rather than two.
- An eighth reason changes this record's ratified list: it is a code change, a
  page edit the architecture test already forces, and a dated line in
  `.abcd/work/DECISIONS.md`. It needs a new ADR only if the shape of the kind
  changes, not when the enumeration grows.
- The condition for reconsidering SQLite is the one adr-2609061610107154
  names, unchanged by this record.
