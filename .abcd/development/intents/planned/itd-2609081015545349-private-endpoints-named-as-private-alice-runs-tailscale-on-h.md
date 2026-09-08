---
id: itd-2609081015545349
slug: private-endpoints-named-as-private-alice-runs-tailscale-on-h
spec_id: spc-2609081222104376
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Private endpoints marked as private: Alice sees which address on the list is the one that does not cross the café's Wi-Fi, and the list stops offering addresses her Mac never answers on

## Press Release

Alice serves models from her Mac and reaches them from her laptop. She runs a
mesh VPN across both, so a private, encrypted path between them already exists —
but Gropius has never said so. Its panel lists every address the server answers
on, and because the VPN's interface is simply another interface, its address is
quietly in that list, looking exactly like the address that carries her prompts
across whatever Wi-Fi she is sitting on. Alice picks between them by reading the
numbers.

Gropius now marks them. The address on the private network is labelled as such,
and the addresses that are the local network keep saying that they are the local
network. When there is no private network, the list looks exactly as it always
has.

The mark says what Gropius observed, not what it guarantees. It reads "private
network" and names no product, because Gropius cannot see whether that network
has since been shared, published to the internet, or logged out from — and a
label that says "encrypted" survives every one of those and then lies. What it
can honestly say is which address belongs to which network, which is the fact
Alice is missing.

The same change stops the list offering addresses the server never answers on.
Gropius lists every address the machine holds, but when Alice has told it to
listen on one specific address, the rest are dead — and a mark that promotes a
dead address to the recommended one would be worse than no mark at all.

Nothing about who may reach the server changes: the same bind, the same API key
rule, the same warnings.

## Why This Matters

The gateway serves plain HTTP with no TLS. Over the local network every prompt,
every completion and the API key itself cross the wire in the clear; over a mesh
VPN the same traffic is inside an encrypted tunnel and reachable off the local
network entirely. Those are very different propositions, and the panel presents
them as one undifferentiated list of addresses.

The cost of getting it wrong runs one way. An operator who assumes the private
path is in use, because nothing told her otherwise, hands out the café's address
and her key with it. Marking the list is the cheapest thing that removes the
guess.

The dead addresses matter for the same reason. A specific-address bind is the
one way an operator can genuinely narrow who reaches the server, and the panel
answers it by listing every address on the machine — most of which refuse the
connection. Marking one of those as the private one would turn a confusing list
into a confidently wrong one.

## Mechanism

We expect that when someone has a private path available, they will use it, and
that the only reason they do not today is that they cannot tell which address is
which. This is wrong if the marked list makes no difference — if people go on
pasting the local-network address once the mark exists, then the missing
information was not what was stopping them, and marking is not the fix.

## Scope Conditions

- A Mac running a mesh VPN installed the ordinary way, where the private <!-- cond: cond-2609081222119118 -->
  address has the shape those products normally give it. A self-hosted or
  unusually configured network may go unmarked, and unmarked is the designed
  failure: the claim is that Gropius never marks wrongly, not that it always
  marks.

## Acceptance Criteria

- Given a Mac on a mesh VPN, When Alice opens the control panel, Then the
  endpoint on the private network is marked and the local-network endpoints are
  not.
- Given that mark, When Alice reads it, Then it says the address is on a private
  network, names no vendor, and claims nothing about encryption, reachability or
  who else can connect.
- Given a Mac with no private network, When Alice opens the control panel, Then
  nothing about the endpoint list changes.
- Given Gropius is bound to one specific address, When Alice opens the control
  panel, Then only addresses the server actually answers on are listed, and the
  mark never appears on an address it does not answer on.
- Given any request arrives at the gateway, When it is handled, Then
  authentication, admission and every other enforcement path behave exactly as
  they do on a build with no private-network detection at all.
- Given the private network appears, disappears or changes address while Gropius
  is running, When Alice reloads the control panel, Then the list reflects the
  current state and Gropius keeps serving throughout — no fact about the private
  network is cached for the life of the process.

## Open Questions

- Which signals identify a private-network address, given the mark must never be
  wrong: the address range alone is a heuristic with real false positives, since
  the same range is handed out by some ISPs. The conjunction of that range and a
  tunnel interface is the candidate the spec should settle.
- Whether the dead-address fix belongs in the same commit as the mark or lands
  first as its own. It is the larger half and it is a fix, not an addition.
- Whether `iss-7` (a specific-address bind leaves the control panel unreachable)
  must be resolved before criterion 4 can be satisfied, or whether criterion 4
  can be met without touching what iss-7 defers.

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._

## Grounds

- pursued: the footgun is already live. The private-network address is in the endpoint list today with nothing said about it, so an operator running a mesh VPN is already choosing between addresses on no information, and the cheapest wrong choice hands out the cafe's address and the API key with it. This is closing a hole that shipped rather than adding a feature. Shown wrong if nobody is in fact serving Gropius across a mesh VPN, in which case the mark is decoration nobody reads; shown wrong a second way if people keep pasting the local-network address once the mark exists, which would mean the missing information was never what stopped them.
