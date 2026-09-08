---
schema_version: 1
id: "iss-2609081221484689"
slug: "docs-do-not-say-how-to-serve-gropius-over-a-mesh-vpn-or-what"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
---

Docs do not say how to serve Gropius over a mesh VPN, or what marking an endpoint means. Once itd-2609081015545349 ships, the panel shows a mark whose meaning is deliberately narrow: it says which network an address is on and claims nothing about who can reach it. That distinction has nowhere to live in docs/ today, and the how-to for reaching a Mac from another machine describes only the local network. Also needs the standing warning that publishing the address to the public internet through the VPN vendor's own tunnelling feature defeats the point. Precedent for filing a docs gap as an issue an intent depends on: iss-2609061443332414.

## Interview outcome (2026-09-08)

**Naming.** The page may name Tailscale, though the app does not. The
constraint differs by surface: the app cannot verify what it is looking at, so
it marks only the kind of network; a page is written by a person who can say
what was actually reasoned about and tested against.

**What it must cover.** All four hazards were adopted: publishing the address
to the public internet through the VPN's own tunnelling feature defeats the
mark; a sharing rule widens who reaches it; the mark is not a statement about
encryption; and Gropius serves plain HTTP either way, so the protection is the
VPN's and stops where the VPN stops.

**Re-route candidate, and the reason this issue may not stay a docs issue.**
The maintainer's steer was to keep it user-friendly, not to over-explain or
scare, and not to interrupt the flow — and they proposed a **security audit
page**: one place that shows what is on and what is not, rather than four
warnings distributed through prose. That is a product capability rather than a
documentation gap, and it would carry the same four facts without any of them
landing in someone's way. If adopted it belongs in an intent, with this issue
reduced to the how-to that remains. Not decided; recorded so the routing is a
visible choice rather than a default.
