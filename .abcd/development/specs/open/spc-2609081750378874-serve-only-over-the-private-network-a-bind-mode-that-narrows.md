---
id: spc-2609081750378874
slug: serve-only-over-the-private-network-a-bind-mode-that-narrows
intent: itd-2609081718469419
origin: researcher-authored
production_mode: hand-written
---
# serve-only-over-the-private-network-a-bind-mode-that-narrows

## Summary

A third bind mode. With it chosen, the gateway answers on the private-network
address and on this Mac's loopback address, and on nothing else.

The address is resolved by `internal/netshape`, which is enforcement reading a
detection — closed by adr-2609081118587999 rule 2 as originally written, and
opened by that ADR's 2026-09-08 amendment under two conditions this spec must
implement: **ambiguity is refused, never resolved**, and **the selection is
always shown**.

## Depends on

`itd-2609081303525417` (this Mac is always in the bind) ships first and carries
the whole listener and singleton design. This spec adds only address selection,
the refusal, the display, and the setting. Building this one first would mean
implementing that design here and then moving it.

## Scope

In: a new configuration field for the mode; address selection in a package that
may legally hold it; the ambiguity refusal; the selection's display in Settings
and on the posture page; the Settings control; tests.

Out: the listener acquisition and singleton design (the intent above); the
posture page itself (`itd-2609081718534201` — this spec supplies it a fact);
Bonjour's scope, which is an open question below and may become its own record;
`iss-2609081742422989`, the Settings form's existing refusal to save any host it
does not offer, which blocks the Settings half and is fixed separately.

## Approach

### Where the selection may live

`cmd/gropius` acquires the listener and is refused the detection outright by
`internal/archtest/enforcement_detection_test.go`, on the recorded ground that it
"decides whether an exposed bind may run at all". `internal/config` is on the
same enforcement path.

The mode therefore resolves to a **bind plan** — a value naming the addresses to
acquire — produced by a package that may read the classifier, and consumed by
`cmd/gropius`, which acquires what the plan names and knows nothing about why.
The archtest rule gains an explicit exception for the plan's producer, worded to
name the amendment rather than to widen quietly: a guard that grows a silent
exception is the failure this repository spent 2026-09-08 correcting elsewhere.

Making the plan a value rather than an address built inline in `main` is also
what makes criterion 1 testable without a second machine.

### The two conditions

**Ambiguity is refused.** `netshape.Addrs` can return several addresses carrying
the private-network shape; its own comment concedes other VPN clients take
`utun` and some hand out addresses from the same range. With more than one
candidate the plan is not built: the mode refuses, names the candidates, and the
server binds loopback only. Refusing is the whole value of the condition — an
arbitrary pick between a mesh VPN and a corporate VPN binds the operator to
their employer's network while they believe they narrowed to their own.

**The selection is shown.** The plan carries the address it chose and the
candidates it saw, and both surfaces render it. Under rule 1 this is an
observation and is permitted; it is also the only way a wrong selection is
visible rather than inferred.

### Where the mode lives in the configuration

Its own field, never a sentinel in `Host`. A word like `"private"` passes
`ValidBindHost` as a hostname, then fails to listen, and the app exits with no
panel and no recovery but editing the file — the precise failure this intent
exists to remove.

### Zero candidates

Serves loopback only, and says so on both surfaces. Never a wider set: the mode
can only ever narrow, and a mode that falls back to the wildcard when it cannot
find its address is the fail-open the amendment's conditions exist to prevent.

## How this satisfies the Acceptance Criteria

1. **Exactly the selected address and loopback** — a table test over the bind
   plan for injected interface lists: one candidate, several, none, and one
   whose address changes between calls. The plan is a value, so this needs no
   listener and no second machine.
2. **Any other address refused** — an integration test that dials every other
   IPv4 this Mac holds and expects `ECONNREFUSED`. Environment-dependent by
   nature: it must skip cleanly on a machine with no non-loopback IPv4, and this
   repository already carries flaky-test issues from tests that bind real
   addresses, so it is written to skip rather than to fail when the environment
   is not there.
3. **More than one candidate refuses and names them** — table test; asserts the
   plan is absent, the candidates are reported, and the served set is loopback.
4. **No candidate serves this Mac only** — same table.
5. **The selection is named on both surfaces** — asserted against the control
   plane's state payload and the Settings render.
6. **The singleton holds across differing binds** — inherited from
   `itd-2609081303525417`; this spec asserts only that the mode goes through the
   same acquisition path rather than around it.
7. **A changing address never widens the served set** — nothing re-binds, so the
   risk runs the other way: the listener holds an address the Mac no longer has
   and the panel would go on offering it, which breaks the already-shipped
   `itd-2609081015545349` criterion 4. The mode reports the address as
   unreachable rather than dropping it, and the posture page is where that is
   read.
8. **Bonjour** — see Open.

## Open

- **Bonjour.** `ExposedToLAN()` is true for a private-network address, so the
  advert goes out on every interface, carrying hostname, port, model count and
  whether a key is required — to the network this mode exists to exclude. The
  bind narrows connections; it does not narrow discovery. Scoping the advert to
  the bound interface, disabling it in this mode, or stating the limit are the
  three answers; the third is honest and the weakest. This may deserve its own
  record, because it is a property of advertising rather than of this mode.
- Whether the archtest exception is best expressed as an allowed package or an
  allowed declaration. The declaration is narrower and this repository's guards
  have been defeated by breadth four times in one day.
