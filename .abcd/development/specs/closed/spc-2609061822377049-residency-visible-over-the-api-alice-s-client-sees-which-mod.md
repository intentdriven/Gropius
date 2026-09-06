---
id: spc-2609061822377049
slug: residency-visible-over-the-api-alice-s-client-sees-which-mod
intent: itd-2609061441228998
origin: researcher-authored
production_mode: hand-written
---
# residency-visible-over-the-api-alice-s-client-sees-which-mod

## Summary

This spec extends each entry of `GET /v1/models` with four top-level fields —
`state`, `in_flight`, `last_used` and `pinned` — on installs that have an API
key configured, and leaves the listing byte-for-byte as it is today on installs
that do not. It owns the shape of every Gropius extension to the models list:
top-level fields under common names, no namespaced object, and an explicit
allow-list projection so a later field added to `runtime.Resident` cannot leak
by default.

## Scope

In scope:

- `runtime.Resident` gains a `State` field distinguishing a loaded model from
  one whose server is up but has not yet answered its readiness probe.
- `internal/gateway`'s `handleListModels` projects residency onto each entry
  when, and only when, an API key is configured.
- The extension shape, recorded here as this repository's rule for every future
  models-list field.
- A reference page for the models list under `docs/`, which also settles the
  budget-and-eviction half of iss-2609061443332414.

Out of scope:

- Any new endpoint. There is no status route; residency rides the listing.
- Any change to `withAuth`, to the loopback exemption, or to who may call the
  listing at all.
- The pin list itself (itd-2609061441241254) and the context length
  (itd-2609061431463108), both of which follow the shape this record fixes.
- Holding a model warm on a client's behalf. The values are a snapshot taken
  when the list is built, never a reservation.

## Approach

**The pool's side.** `runtime.Resident` today carries `RepoID`, `Port`,
`Bytes`, `LoadedAt`, `LastUsed` and `InFlight`. It gains
`State ResidencyState` with values `loaded` and `loading`, computed inside
`Pool.Resident()` from the existing non-blocking `isReady(e)` helper under
`p.mu`. `Pool.Resident()` already walks every entry in `p.entries`, loading
ones included, so no new bookkeeping is needed: an entry is present with its
`ready` channel open exactly when the model is loading. A model with no entry
is not loaded, which is the third value and is supplied by the gateway, not by
the pool. `Pinned` is added to `Resident` by itd-2609061441241254 and is
projected by the allow-list this record defines.

**The gateway's side.** `handleListModels` builds each entry from the registry
as it does today. When `g.cfg().APIKey != ""` it additionally indexes
`g.pool.Resident()` by repo id and adds, on each entry:

- `state`: `"loaded"`, `"loading"` or `"not_loaded"`.
- `in_flight`: the entry's `InFlight`, or `0`.
- `last_used`: Unix seconds, omitted when the model has never been used in this
  process.
- `pinned`: boolean, once the pin list exists.

The condition is the *install's* — a configured key — not the request's. A
loopback client that is exempt from the bearer check on a keyed install sees the
same picture the control panel shows it, which is the point. On an install with
no key the listing is constructed exactly as today, by the same code path, so
"unchanged" is a property of the code and not only of the tests.

**The projection is an allow-list.** The four fields above are written by name
from the `runtime.Resident` value; the struct is never marshalled wholesale.
`Port` and the model's on-disk path therefore cannot reach the listing, and a
field added to `Resident` later reaches it only when someone adds a line here.

**The extension shape, fixed by this record.** Gropius's additions to a models
entry are top-level fields with the common names for what they hold. There is no
namespaced object and no version field: an OpenAI client ignores fields it does
not know, and a namespaced object would only move the problem. The context
length follows under both `context_length` and `max_model_len`; the statistics
records follow the same rule. A dated line in `.abcd/work/DECISIONS.md` already
carries this decision.

## How each acceptance criterion is satisfied

1. _Given an API key is configured and a downloaded model is not loaded, when a
   client lists the models with the key, then that model's entry reports it as
   not loaded and the entry's pre-existing fields are unchanged._ The gateway
   emits `"state": "not_loaded"` for a registry model with no pool entry and
   leaves `id`, `object`, `created` and `owned_by` untouched.
   `internal/gateway`'s existing `stubPool` already implements `Resident()`; the
   test lists with a key and an empty resident set and compares the four
   pre-existing fields against today's golden entry.
2. _Given an API key is configured and a model's server has answered its
   readiness probe, when a client lists the models with the key, then that
   model's entry reports it as loaded._ A closed `ready` channel yields
   `State: loaded`. Test: `stubPool` returns a `Resident` with `State` loaded;
   assert `"state": "loaded"`.
3. _Given an API key is configured and a model's server has been started but has
   not yet answered its readiness probe, when a client lists the models with the
   key, then that model's entry reports it as loading._ `loading` is defined
   precisely as *entry present in `p.entries`, `ready` channel open*. A request
   refused before `Launch` returns — budget refusal, `Precheck` failure — never
   appears in any state, because `startLocked` inserts the entry only after
   `Launch` succeeds. Two tests: a `runtime` test with a fake launcher whose
   readiness probe is held open, asserting `Pool.Resident()` reports `loading`;
   and a gateway test asserting the projection.
4. _Given an API key is configured, when a client lists the models with the key,
   then each entry also reports how many requests are in flight for that model
   and when that model was last used._ `in_flight` and `last_used` are projected
   from the same `Resident` value. Test: a stub resident with `InFlight: 3` and
   a fixed `LastUsed`, asserting both fields and that `last_used` is Unix
   seconds.
5. _Given no API key is configured, when a client lists the models, then no entry
   carries residency, in-flight, last-used or pinned information._ The whole
   projection is inside the `APIKey != ""` branch. Test: list with no key
   configured and assert the decoded entry has exactly the four pre-existing
   keys — an exact key-set assertion, not an absence check on four names, so a
   fifth field added later fails this test rather than shipping.
6. _Given any models listing, when the response is inspected, then it carries no
   loopback port and no filesystem path._ Guaranteed structurally by the
   allow-list projection. Test: with a stub resident carrying a port and a
   model path, marshal the response and assert the bytes contain neither
   `127.0.0.1` nor a path separator followed by a path segment from the fixture.
   This test is the regression guard on the projection staying an allow-list.
7. _Given the reference page for the models list, when a reader looks it up, then
   the residency values, the keyed-install condition and the fact that the
   listing is a snapshot rather than a reservation are all stated._ A new
   `docs/models-list.md` (Diátaxis reference, present tense) documents every
   field, the three residency values, that the fields appear only on a keyed
   install, that the values are a snapshot and hold nothing warm, and — settling
   iss-2609061443332414 — the resident memory budget and the least-recently-used
   eviction rule the values are read against. A docs test in `internal/sitetest`
   is not warranted; the bar is held by review and by `abcd docs lint` in CI.

## Trust-boundary review notes

`internal/gateway` is a trust boundary; `internal/runtime` is touched only by an
additive read.

- **Disclosure baseline.** An unauthenticated LAN client can already learn
  residency by posting a one-token completion and timing the first byte — and in
  doing so it *changes* residency, which reporting never does. The three-state
  value is therefore not new information on an open server. The in-flight count
  and the last-used time are new, and the pin list is a stronger signal still:
  all of them are gated on a configured key, so an open server discloses nothing
  it did not already disclose more expensively.
- **No authentication change.** `withAuth` is untouched. The keyed-install gate
  is a read of `g.cfg().APIKey` inside the handler, not a second authorisation
  path, so there is no new way to reach the listing and no new way to be refused.
- **No path or port on the network.** The allow-list projection is the control.
  In a per-user install the model path contains the serving account's home
  directory, which is exactly the class `relayRewritingModel` already exists to
  keep off the wire; criterion 6's test is the standing guard.
- **No new lock ordering.** `Pool.Resident()` takes `p.mu` and returns a copied
  slice, as today. The handler holds no lock while marshalling.
- The change ships with the trust-boundary security review the conventions
  require for `internal/gateway`.

## Docs to change

- New `docs/models-list.md`: the reference page for `GET /v1/models`, covering
  every field, the residency values, the keyed-install condition, the
  snapshot-not-reservation rule, the resident memory budget and the eviction
  rule. Present tense; one Diátaxis type.
- `docs/getting-started.md`: section 5 gains a sentence pointing a network
  client at the reference page, and section 6 notes that setting a key also
  turns on the residency fields.
- `README.md`: the API paragraph names the residency fields as key-gated.

## Dependencies and sequencing

- Settles iss-2609061443332414 (the docs gap on the memory budget and the
  eviction rule) as part of the reference page. The intent records it as a
  dependency; this spec discharges it here rather than waiting.
- Fixes the extension shape for itd-2609061431463108 (context length) and for
  the statistics records; both reuse it rather than deciding again.
- itd-2609061441241254 (pinned models) adds `Pinned` to `runtime.Resident`; if
  it ships first, the `pinned` field is projected in the same change, and if it
  ships second it adds one line to the projection and one to the reference page.
  Either order works; the projection's allow-list is what makes that safe.
- itd-2609061441285238 (eviction grace) depends on this record so clients can
  avoid a wait rather than only be told about it.
- No dependency on the statistics store. The grounds conjecture ("swap counts
  fall") is measurable only once itd-2609061521082551 ships; until then the
  lab's external measurement stands as the evidence.

## Open design points

- Whether `last_used` is omitted or emitted as `null` for a model never used in
  this process. The criterion is silent; omission is recommended, and whichever
  the implementer picks must be stated on the reference page.
- Whether `state` is also surfaced on the control panel's model cards. The
  panel already renders `Resident()` on loopback, so this is presentation only
  and needs no new plumbing.
