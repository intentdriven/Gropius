---
schema_version: 1
id: "iss-2609081221415955"
slug: "serve-only-over-the-private-network-a-bind-mode-that-narrows"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
promoted_to: itd-2609081718469419
resolution: "Shipped as the third bind choice in Settings: it binds the one address this Mac holds on a private network and this Mac, refuses to choose where more than one matches, and falls back to this Mac alone rather than widening (spc-2609081750378874, adr-2609091123526871)."
impact: additive
---

Serve only over the private network: a bind mode that narrows exposure to the mesh-VPN address so the local network cannot reach the gateway at all. Admissible under adr-2609081118587999 rule 3 precisely because it is a bind rather than an inference, and it must fail closed when the interface is absent at launch. Inherits iss-7 as a prerequisite: a specific-address bind currently leaves the control panel unreachable and needs a hand-edited configuration file. Deferred from the itd-2609081015545349 decomposition and captured so it is not left living only as a sentence in an ADR consequence.

## Grounds

- pursued: narrowing who reaches the server is the only honest way to reduce exposure, and today it is a trap — a specific-address bind leaves the operator locked out of their own control panel, so the setting that most deserves to be used is the one nobody can use. We expect the reason operators leave the bind wide is that narrowing costs them their own access, not that they do not want it. Shown wrong if the bind stays wide after that cost is removed, which would mean the obstacle was never access but something else: not knowing the setting exists, or not believing the exposure matters.
- pursued: narrowing exposure is worth choosing now that it costs nothing — the bookkeeping is gone (Gropius selects and shows the address) and so is the old cost of narrowing, since loopback is always in the bind. Shown wrong if operators with the mode available leave the bind wide anyway, which would mean the obstacle was never the bookkeeping; or if the ambiguity refusal fires often enough in the field that the mode refuses more than it serves.

## Interview outcome (2026-09-08)

Two decisions from the maintainer, in their terms.

**What "fails closed" means here.** With the mode on and the private network
absent, no other Mac may reach the server — not even one on the same local
network — but this Mac must, including *a different user account on it*, which
is the case the maintainer named and uses for testing. That is loopback: a
second account connects over the loopback address and is indistinguishable from
the first, which `Gateway.withAuth` already relies on and says so in its
exemption comment. So the fallback is loopback-only, never a wider bind.

**How it is switched on.** A third choice in Settings beside the two that exist,
not a hand-edited configuration file and not automatic. Automatic was refused
on the ground that it changes who can reach an existing install the day someone
installs a VPN — a default change wearing a feature's clothes.

**Two consequences that follow, for whoever specs this.**

- The mode binds *two* addresses, the private one and loopback. Gropius has a
  single listener today, so this is a structural change rather than a
  configuration value.
- It dissolves the iss-7 prerequisite recorded in adr-2609081118587999 rather
  than inheriting it. iss-7's fault is that a specific-address bind leaves the
  loopback-only control panel unreachable; if loopback is always in this bind,
  the panel is always reachable. iss-7 still stands on its own for a plain
  specific-address bind.
