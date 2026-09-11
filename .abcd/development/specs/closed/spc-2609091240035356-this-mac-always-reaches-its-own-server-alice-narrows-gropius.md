---
id: spc-2609091240035356
slug: this-mac-always-reaches-its-own-server-alice-narrows-gropius
intent: itd-2609081303525417
origin: researcher-authored
production_mode: hand-written
---
# this-mac-always-reaches-its-own-server-alice-narrows-gropius

## Summary

A bind stops being an address and becomes a **set of listeners**. Loopback is
first in every set, whatever else the bind names, so narrowing the bind never
costs the operator their own access — the control panel, the menu bar, and a
client in a second user account on this Mac all reach the server over loopback.

The design is settled by `adr-2609091123526871`, taken at interview on
2026-09-09, which also resolves this intent's three open questions: two
listeners rather than a wildcard plus a filter; loopback acquired first as the
singleton's contention point and the port-ownership challenge's target; and a
second address that cannot be acquired serving loopback only, loudly, without
exiting.

## Scope

In: the bind plan as a value; the listener set and its acquisition; the
singleton re-reasoned for a pair; the fail-closed narrowing and what it says;
the endpoint list derived from what was acquired; `net.JoinHostPort` wherever a
listen address is built; tests.

Out: the private-network mode itself, which is `spc-2609081750378874` and
builds on this one; the posture page (`itd-2609081718534201`), which is where a
narrowing is meant to be read at length; Bonjour's scope beyond the two cases
the ADR settles.

## Approach

### The listener set

`internal/bind` turns a configuration into a `Plan`: the loopback address,
always, and at most one further address. It is a value rather than an address
built where the listener is taken, which is what makes "this bind answers on
exactly these addresses" checkable without a listener and without a second
machine. `Plan.Addrs` builds every listen address with `net.JoinHostPort`, which
closes iss-7's second fault — `fmt.Sprintf("%s:%d", …)` could not spell an IPv6
literal, so `"::1"` passed `Validate` and then killed the process at startup.

The second address is dropped only when it would duplicate the loopback
listener: the literal `127.0.0.1`, or a name `ExposedToLAN` reads as loopback.
`::1` and `127.0.0.53` are separate sockets and are acquired alongside it.

### The singleton

`EADDRINUSE` is the only signal `acquireListener` has, and it exists only where
every instance binds the same address. Measured on this hardware, and kept as a
test: two processes binding *different* addresses on one port both succeed,
while an exact duplicate bind is refused. So loopback — the one address every
mode can be made to take — is acquired **first**, by every mode, and is the
contention point. It is also the address `fetchChallengeAnswer` already
contacts, so the winner is guaranteed to be answering exactly where the next
instance asks.

`acquireBind` owns the whole acquisition. A second address held by something
else cannot be a peer of this build (that peer would have taken loopback first),
so loopback is released and the port is classified exactly as it was before
there were two listeners — which keeps an older single-listener build working
through the upgrade that can produce the case.

### The fail-closed narrowing

A second address that is not on this Mac narrows the bind to loopback, records
*which* address was dropped and why in `Plan.Refusal`, and **serves**. It does
not exit: exiting is what left the operator with no panel and a file to
hand-edit, which is iss-7's whole complaint. The refusal is the single source
for both surfaces — the startup log line and the panel's notice under the bind
control — so a narrowing cannot happen on one and not the other. The same holds
when the address resolves to loopback under the operator's feet, and when a key
cannot be persisted for a bind that reaches other machines, where the second
socket is *closed* rather than reported shut.

Nothing re-binds after launch (ADR rule 9). The served set can only narrow.

## How this satisfies the Acceptance Criteria

1. **Loopback as well as whatever else the bind names** —
   `internal/bind.TestEveryPlanAcquiresLoopbackFirst` over every mode, and
   `TestWhatEachBindAcquiresBesidesLoopback` for the whole table of spellings;
   `cmd/gropius.TestTheBindAcquiresLoopbackFirstAndThenTheSecondAddress`
   measures the acquisition itself, and
   `internal/bind.TestEveryAddressAPlanNamesIsOneAListenerTakes` binds every
   address a plan names rather than asserting it could be bound.
2. **The control panel is reachable from the Mac under a specific bind** — the
   panel is loopback-only (`internal/gateway.TestControlAPIAllowsLoopback`,
   `TestControlAPIRejectsNonLoopback`) and loopback is now in every bind, so the
   two together are the criterion.
   `cmd/gropius.TestALoopbackBindTakesOneListener` and the acquisition test
   above hold the second half in place.
3. **A second account over loopback is served, with the key exemption
   unchanged** — the exemption is `internal/gateway.TestLoopbackIsExemptFromAuth`
   and is untouched by this work; what changes is that a narrow bind now holds
   the loopback socket at all. `docs/bind-address.md` states the consequence in
   the operator's terms, and ADR rule 7 records it as a cost rather than a
   side-effect: any account on this Mac reaches `/v1` and the panel without the
   key, which was already true of the wildcard and is new for the narrow binds.
4. **Every address the panel lists is one the server answers on** —
   `internal/gateway.TestLoopbackIsListedUnderEveryBindBecauseEveryBindAnswersOnIt`
   (which replaces the test that recorded the opposite as a known divergence
   handed to iss-7), `TestABindThatNarrowedListsOnlyWhatItAnswersOn`, and
   `TestAnAcquiredAddressThatWentAwayIsNoLongerOffered`.
5. **Another machine is refused on any address but the bound one** —
   `cmd/gropius.TestAnAddressOutsideTheBindRefusesTheConnection` dials an address
   this Mac holds outside the bind and requires the refusal, with the wildcard
   case beside it so a dial that failed for its own reasons cannot read as a
   pass. It skips cleanly where the environment has nothing to dial.
6. **A second listener that cannot be acquired fails closed and says so** —
   `cmd/gropius.TestASecondAddressThisMacDoesNotHoldServesLoopbackAndSaysSo`,
   `TestAFailedSecondListenerNeverWidensTheBind`,
   `TestANameThatResolvesToLoopbackNarrowsAndSaysSo`, and the lockdown case of
   `TestTheKeyIsRequiredForWhatWasAcquiredAndNothingElse`; the panel half is
   `internal/ui.TestThePaneSaysWhenTheBindNarrowed`.

The singleton, which no criterion names but every one of them rests on, is
`internal/bind.TestLoopbackIsAContentionPointAndADifferingBindIsNot` (the
measurement the design is built from) with
`cmd/gropius.TestAPeerHoldingLoopbackIsStillClientMode`,
`TestAPeerHoldingOnlyTheSecondAddressReleasesLoopbackAndDefers` and
`TestAForeignHolderOfTheSecondAddressIsRefused`.

## Open

- Two real processes, or two accounts, are not stood up against the singleton:
  the peer in every test is an in-test listener with a stubbed `portHolder`.
- `stillHeld` drops a departed address from the endpoint list for IPv4 literals
  only, because `internal/netshape` enumerates IPv4; a bound name or IPv6
  literal stays listed. Recorded in the ADR's consequences and in
  `docs/bind-address.md` rather than left as a silent limit.
- A foreign process holding *only* the second address exits the app with no
  panel, which is iss-7's shape reached from a direction this design accepts:
  ADR rule 3 names it as the residual and names the narrower fix it declines.
