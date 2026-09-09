---
schema_version: 1
id: "iss-2609080852262710"
slug: "the-statistics-store-diverges-from-adr-2609061610107154-on-t"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
resolution: "adr-2609090716413337 supersedes adr-2609061610107154 on the record kinds only and ratifies the shipped removed kind with its seven reasons; the superseded record is marked in its frontmatter and in the ADR index."
impact: internal
---

The statistics store diverges from adr-2609061610107154 on the record kinds. The ADR reserves the record kinds to itself and names an eviction event; the shipped kind is removed, covering seven reasons (evicted, idle, unloaded, abandoned, load-failed, crashed, shutdown) at internal/stats/stats.go:101. Found by an independent fidelity review (receipt rcp-799061841f58) while correcting a separate misquote in itd-2609061521102742's Audit Notes, where the earlier note attributed a different divergence to the same ADR. This one is real and is a divergence from a ratified decision record, so it needs the maintainer to adopt it or reject it; the shipped reference page already documents the seven-reason removed kind.

## Grounds

- pursued: the ratified record and the shipped store now agree on the record kinds, with the seven reasons kept because only one of them is an eviction; shown wrong if the removed kind or its reason list has to change on disk anyway, or if a reader still cannot tell which record is in force.
