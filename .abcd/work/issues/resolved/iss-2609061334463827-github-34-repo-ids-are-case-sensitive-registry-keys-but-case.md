---
schema_version: 1
id: "iss-2609061334463827"
slug: "github-34-repo-ids-are-case-sensitive-registry-keys-but-case"
severity: "major"
category: "bug"
source: "user-observation"
found_during: "github issue triage 2026-09-06 (adversarially verified)"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/registry/registry.go"
resolution: "Fixed: registry keys case-fold (Get/Put/SetState/SetSize/UpdateProgress/Remove/Rescan), Put keeps the first-seen spelling, Open collapses case-variant rows preferring a ready one; app.Download/Delete canonicalise the id and the in-flight map folds. Tests pin folding, collapse, remove-by-variant, re-cased download reuse, and in-flight variant refusal."
impact: fix
---

GitHub #34: repo ids are case-sensitive registry keys but case-insensitive APFS paths, and the Hub itself redirects case variants to one canonical repo. A re-cased download (any loopback API caller; the shipped UI only emits canonical ids) mints a second entry over the same directory; a Retry on the variant is the already-complete fast path, so two ready rows appear, and Remove on either os.RemoveAll's the shared directory while the other row stays 'ready' until the next Rescan. The in-flight downloads map is keyed the same way, so Delete's cancel-and-wait misses a case-variant download. Affects per-user mode identically. Fix: fold the registry key and the downloads key, keep the first-seen spelling, canonicalise ids at the app entry points, and collapse case-variant rows deterministically on Open.

## Grounds

- pursued: a re-cased download can no longer mint a second row over the same directory or delete the original's weights; a sideloaded case-sensitive volume holding two genuinely distinct case-variant directories collapses to one entry, which is the accepted trade
