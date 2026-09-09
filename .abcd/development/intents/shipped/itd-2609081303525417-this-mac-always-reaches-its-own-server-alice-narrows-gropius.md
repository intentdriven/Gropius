---
id: itd-2609081303525417
slug: this-mac-always-reaches-its-own-server-alice-narrows-gropius
spec_id: spc-2609091240035356
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# This Mac always reaches its own server: Alice narrows Gropius to one address so the rest of the network cannot reach it, and the app on her own Mac keeps working — as does the copy she runs from a second user account for testing, and the control panel she narrowed it from. Gropius listens on loopback whatever else it listens on, so choosing who on the network may reach the server never costs Alice the ability to reach it herself, and the panel's list of addresses stops being a list that includes one the server does not answer on.

## Press Release

Alice runs Gropius on a Mac that other people share. She narrows it so that only
one address on the network reaches the server — and then finds she cannot reach
it herself. The control panel she narrowed it from is served on this Mac's own
loopback address, and the narrowing took that away; the second user account she
keeps for testing cannot reach it either; and the panel goes on listing loopback
as somewhere to point a client, which it is not.

Gropius now listens on loopback whatever else it listens on. Choosing who on the
network may reach the server never costs Alice the ability to reach it from the
Mac it runs on. The panel stays reachable, the second account keeps working, and
the address the panel lists for this machine is one the server actually answers
on.

Nothing widens. Loopback is this Mac and only this Mac: no other machine gains
anything, on any network, under any bind.

## Why This Matters

Narrowing the bind is the one honest way to reduce who can reach the server —
adr-2609081118587999 rule 3 says so, because a bind is enforcement Gropius owns
end to end rather than an inference about something else. But the narrowing is
currently a trap: iss-7 records that binding a specific address leaves the
control panel unreachable from anywhere, so the setting that most deserves to be
used is the one that locks the operator out of their own app. Nobody uses it,
and the recorded advice is to hand-edit a configuration file.

It is also what makes a claim elsewhere true. itd-2609081015545349 requires the
panel to list only addresses the server answers on; the panel lists loopback
under every bind, which is false today under a specific bind. Binding loopback
always makes that criterion true by construction rather than by exception.

And it is an invariant the maintainer stated once already, for the
private-network bind (iss-2609081221415955): no other machine may reach the
server, but this Mac must, including a different user account on it. Stated for
one mode, it belongs to the bind generally.

## Mechanism

We expect that the reason nobody narrows the bind is that narrowing costs them
their own access, not that they do not want it — so removing that cost is what
makes the safer setting usable. This is wrong if operators still leave the bind
wide after loopback is guaranteed, which would mean the obstacle was never
access but something else: not knowing the setting exists, or not believing the
exposure matters.

## Scope Conditions

- macOS on a Mac serving one or more user accounts, where a second account <!-- cond: cond-2609091240039473 -->
  reaching the server over loopback is a case that occurs. Loopback is the
  boundary the claim rests on: this is a statement about one machine, and it
  says nothing about any network.

## Acceptance Criteria

- Given any bind Gropius accepts, When it starts serving, Then it answers on
  this Mac's loopback address as well as on whatever else that bind names.
- Given a bind to one specific non-loopback address, When Alice opens the
  control panel from the Mac itself, Then it is reachable.
- Given that same bind, When a client in a second user account on this Mac
  connects over loopback, Then it is served, and the API key exemption for
  loopback behaves exactly as it does under a wildcard bind.
- Given any bind, When the panel lists endpoints, Then every address it lists is
  one the server answers on, loopback included.
- Given a bind to one specific non-loopback address, When any machine other than
  this one connects to any address other than the bound one, Then it is refused.
  Guaranteeing loopback widens nothing.
- Given the second listener cannot be acquired at startup, When Gropius starts,
  Then it fails closed and says so rather than serving on one address while
  reporting both.

## Open Questions

All three resolved on 2026-09-09 by adr-2609091123526871, taken at interview:
two listeners, loopback acquired first as the singleton's contention point and
the challenge probe's target; a second address that cannot be acquired serves
loopback only, loudly, without exiting; iss-7 is resolved at its cause, and the
unbracketed IPv6 fault is closed by building every address with JoinHostPort.


- Whether this is two listeners or one wildcard listener with a refusal rule.
  Two listeners is the honest reading of "the bind is the narrowing"; a wildcard
  plus a filter puts the narrowing in a code path rather than in the socket,
  which is weaker in exactly the way adr-2609081118587999 rule 3 cares about.
  The spec settles it.
- What the port-ownership challenge in `cmd/gropius/singleton.go` means for a
  pair of listeners: it proves this process owns the port, and there would be
  two.
- Whether this supersedes iss-7 or merely resolves its cause, given iss-7 also
  records unbracketed IPv6 literals failing at startup, which is a separate
  fault living in the same setting.

## Audit Notes

<!-- abcd-review: OWED receipt=rcp-adf339cc8e59 -->
Fidelity review OWED (receipt rcp-adf339cc8e59).

## Grounds

- pursued: we expect operators to narrow the bind once narrowing stops locking them out of their own panel and second account; shown wrong if binds stay wide after loopback is guaranteed, which would mean the obstacle was never access.
