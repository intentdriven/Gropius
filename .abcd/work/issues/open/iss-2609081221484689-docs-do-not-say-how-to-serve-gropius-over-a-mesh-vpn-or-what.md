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
