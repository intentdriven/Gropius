---
id: spc-2609081222104376
slug: private-endpoints-named-as-private-alice-runs-tailscale-on-h
intent: itd-2609081015545349
origin: researcher-authored
production_mode: hand-written
---
# private-endpoints-named-as-private-alice-runs-tailscale-on-h

## Summary

Two changes to what the control panel says about addresses, and nothing at all
to what the gateway enforces.

1. **The endpoint list stops offering addresses the server does not answer on.**
   `Endpoints` currently lists every non-loopback IPv4 on the machine whenever
   `ExposedToLAN` is true, which is right for a wildcard bind and wrong for
   every other exposed bind.
2. **An endpoint on a private network is marked as such.** The mark states which
   network the address belongs to. It names no vendor and claims nothing about
   encryption, reachability or who else can connect, per adr-2609081118587999
   rule 1.

The order matters: (1) lands first. A mark on an address that refuses the
connection is worse than no mark, so the list must be true before anything is
added to it.

## Scope

In: `internal/gateway/control.go` (`Endpoints`, `lanIPs`, `State.Endpoints`), a
new classifier package, `internal/ui/static/app.js`, `cmd/gropius/menubar.go`,
and the tests for each.

Out: any change to `withAuth`, `ExposedToLAN`, the eviction-grace key rule, the
control plane's loopback-only rule, or the warning at `control.go:264`. Out
also: obtaining the network's own name for the machine, which needs the VPN
vendor's daemon and was cut before planning; and the bind mode that would
narrow exposure to the private network, captured as iss-2609081221415955.

## Approach

### Classification

A new package — `internal/netshape` — with no dependency on `internal/gateway`,
so `internal/archtest` can hold the rule that nothing in the enforcement path
imports it. It answers one question about an address: which kind of network is
it on.

The signal is the **conjunction** of two facts, because neither alone is safe:

- the address is inside `100.64.0.0/10`, and
- it is on a tunnel interface (`utun*` on macOS).

The range alone has real false positives — it is the shared CGNAT range and some
ISPs hand it out — but ISP CGNAT arrives on a physical interface, not a tunnel.
The interface alone is worse: `utun*` is taken by several VPN clients, by iCloud
Private Relay, and by macOS's own idle tunnels. Requiring both leaves one
collision, another mesh VPN on the same range, which the mark describes
correctly anyway since it names no vendor.

The cost of a miss is the scope condition the intent records: an unusual or
self-hosted network goes unmarked, and unmarked is the designed failure. The
cost of a false mark is a wrong statement, which the conjunction is chosen to
avoid.

This needs `net.Interfaces()` and each interface's own addresses, not the flat
`net.InterfaceAddrs()` that `lanIPs` uses today, because the interface name is
half the signal. The enumeration is injected behind a package-level variable, in
the `historySource` mould already used in `control.go`, so tests drive a fixed
interface list rather than the machine's.

### The list

`Endpoints` gains the bind test. With a wildcard `Host`, behaviour is exactly as
today. With a specific address, the list holds that address alone — plus the
`.local` name only when it resolves to the bound address, and loopback, which is
always listed and always answers.

`Endpoints` returns `[]Endpoint{URL string, Network string}` rather than
`[]string`, and `State.Endpoints` carries the pair. `Network` is empty for an
ordinary address and names the kind — not the vendor — otherwise.

### Surfaces

`app.js` renders the mark beside the URL and keeps copying only the URL; the
clipboard and the curl/Python examples take `.URL`, never the rendered line.
`menubar.go` keeps taking the first entry, and ordering is unchanged: the
private-network address is marked, not promoted. Promoting it would change which
address the menu bar hands out, which is behaviour rather than presentation, and
the intent does not ask for it.

### Freshness

Nothing is memoized. `snapshot` calls `Endpoints` per request today and the SSE
stream re-renders every two seconds; the classifier is pure interface inspection
with no network call and no subprocess, so it can stay on that path. This is
criterion 6 stated as a design constraint rather than a test of existing
behaviour.

## How this satisfies the Acceptance Criteria

1. **Marked on a mesh VPN, not on the LAN** — a table test over an injected
   interface list: a tunnel carrying a `100.64.0.0/10` address is marked, the
   Wi-Fi address beside it is not.
2. **Says private network, names no vendor, claims nothing more** — the
   rendered string is asserted against a fixed set, and a test greps the
   panel's strings for vendor names and for the words "encrypted", "secure" and
   "only". This is the criterion that keeps the record honest, so it is
   enforced rather than reviewed.
3. **No private network, no change** — the same injected list with the tunnel
   removed returns exactly what the current build returns for that list. The
   injection is what makes "exactly" checkable; against the real machine it
   never was.
4. **A specific bind lists only what answers** — table test over bind addresses
   (wildcard, a specific LAN address, a specific tunnel address, loopback),
   asserting membership and, for the marked case, that the mark never lands on
   an address outside the bind.
5. **Enforcement untouched** — archtest rules that no package in the
   enforcement path imports `internal/netshape`, and that nothing outside the
   endpoint list's own declarations names the classifier, the field its answer
   travels on or the type that carries it; plus the existing gateway tests
   unchanged. A behavioural assertion is not available here, and neither is a
   mechanical one: the rules catch an accidental coupling and nothing more,
   because any code in the process can call `net.Interfaces()` and re-derive
   the classification without naming anything a scan can see. The criterion is
   carried by review, with the rules narrowing what review has to find.
   `internal/archtest/enforcement_detection_test.go` states the limit in full,
   including the one route that cannot be closed even in principle: the
   endpoint list's own shape varies with the classification, because criterion
   4 requires it to.
6. **Reflects the current state, no caching** — a test that changes the
   injected list between two calls and asserts the second reflects it; and the
   absence of any package-level cache, which the same injection makes visible.

## Open

- Whether criterion 4 can be met without touching iss-7. Listing only what the
  bind covers does not require the control panel to be reachable at that bind,
  so the working assumption is yes — the two are separable, and iss-7 stays
  deferred. If that proves wrong the criterion, not the deferral, is what moves.
