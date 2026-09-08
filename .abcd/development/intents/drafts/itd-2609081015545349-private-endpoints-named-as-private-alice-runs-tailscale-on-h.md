---
id: itd-2609081015545349
slug: private-endpoints-named-as-private-alice-runs-tailscale-on-h
spec_id: null
kind: null
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Private endpoints named as private: Alice runs Tailscale on her Mac and on her laptop. Gropius' control panel already lists every address the server answers on, and one of them is her tailnet address — sitting in the list looking exactly like the flat's Wi-Fi address, with nothing to say that one of them is encrypted and reaches her laptop from anywhere while the others are the coffee shop's network in cleartext. Gropius now names them: the tailnet endpoint is labelled as private and encrypted and is offered under the stable name Bob's laptop can actually use rather than a bare address, and the LAN endpoints keep saying plainly what they are. Nothing about who may reach the server changes — the same bind, the same API key, the same warnings — only what Alice is told about the addresses she is being handed.

## Press Release

Alice serves models from her Mac and reaches them from her laptop. She has
Tailscale on both, so there is already a private, encrypted path between them —
but Gropius has never said so. Its control panel lists every address the server
answers on, and because the tailnet interface is simply another non-loopback
interface, the tailnet address is quietly in that list, indistinguishable from
the addresses that carry her prompts across whatever Wi-Fi she is sitting on.
Alice has to know which is which by reading the numbers.

Gropius now names them. The endpoint list marks the tailnet address as a
private, encrypted one, and offers it under the stable name the tailnet resolves
rather than the bare address, so Alice can paste something into Bob's client
that keeps working after a reboot. The addresses that are the local network keep
saying that they are the local network. When there is no tailnet, the list looks
exactly as it always has.

This is naming only. The bind address is unchanged, the API key rule is
unchanged, and the warning shown to an exposed server with no key still fires
on exactly the same condition. Gropius does not decide anything differently
because it noticed a tailnet; it only stops handing Alice four addresses and
letting her guess which one is the safe one.

## Why This Matters

The gateway serves plain HTTP with no TLS, guarded by one optional shared bearer
token. Over the local network that token, every prompt and every completion
cross the wire in the clear. Over a tailnet the same traffic is inside WireGuard
and reachable off the local network entirely. Those are very different
propositions, and today the control panel presents them as one undifferentiated
list of addresses.

The stable name matters for a second reason: Gropius' Bonjour advertisement is
link-local multicast and does not traverse a tailnet, so the `.local` name that
makes a LAN endpoint memorable simply does not exist for a remote peer. Without
a name the tailnet endpoint is a bare address the operator is expected to
remember, which is exactly the failure the `.local` name was introduced to fix
for the LAN.

The cost of getting this wrong in the other direction is a security cost: an
operator who assumes the encrypted path is in use, because nothing told her
otherwise, sends her key over the coffee shop's network.

## Mechanism

> _Prompted (the claim-recording gradient): why the authors expect this to work, as a falsifiable "we expect X because Y" — not the outcome restated. Replace this line with the claim, or with the exact token `None stated.` alone on its line to record the claim as considered and declined._

## Scope Conditions

> _Required (the claim-recording gradient): the population, platform, scale, or assumptions this claim holds under, one per top-level bullet — `abcd intent plan` stamps each with a persistent identity. Replace this line with those bullets, or with the exact token `None stated.` alone on its line._

## Acceptance Criteria

- Given a Mac with a joined tailnet and the gateway bound to all interfaces,
  When Alice opens the control panel, Then the endpoint drawn from the tailnet
  address is marked as private and encrypted, and the endpoints drawn from the
  local network are not.
- Given the same Mac, When Alice opens the control panel, Then the tailnet
  endpoint is offered under the name the tailnet resolves for this machine, and
  falls back to the address when no such name resolves.
- Given a Mac with no tailnet, When Alice opens the control panel, Then the
  endpoint list is byte-identical to what the current build produces.
- Given a tailnet-joined Mac with the gateway exposed and no API key set, When
  Alice opens the control panel, Then the "reachable by anyone on your network"
  warning is shown unchanged: the labelling relaxes no warning and gates no
  behaviour.
- Given a tailnet-joined Mac, When a request arrives at the gateway from any
  address, Then authentication, rate limiting and every other enforcement path
  behave exactly as they do on a build with no tailnet detection at all.
- Given a machine where the tailnet interface appears, disappears or changes
  address while Gropius is running, When Alice reloads the control panel, Then
  the list reflects the current interfaces and Gropius keeps serving throughout.

## Open Questions

- **Provenance of the criteria above: agent-seeded, not yet confirmed by the
  maintainer.** They are proposals for the planning interview to walk bullet by
  bullet, not approvals.
- What identifies a tailnet endpoint? An address inside the range Tailscale
  allocates from (100.64.0.0/10) is one signal; the interface name is another;
  querying the local Tailscale daemon is a third and the only one that could
  name the tailnet reliably. The first is a heuristic with false positives (the
  range is also used by ISP carrier-grade NAT), the third adds a dependency on a
  socket that may not exist. Resolve before planning: it decides whether this is
  a pure-inspection change or one that talks to another process.
- Should the label name Tailscale, or say "private, encrypted network"? Naming
  the vendor is clearer for the operator who installed it and wrong the moment
  another mesh VPN presents the same shape.
- Is there a menu-bar surface as well as the control panel? The menu bar shows
  the first endpoint as *the* endpoint, so ordering may be part of this change.
- Does the how-to that follows this work belong to this intent or to the
  later "tailnet only" bind intent?

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._
