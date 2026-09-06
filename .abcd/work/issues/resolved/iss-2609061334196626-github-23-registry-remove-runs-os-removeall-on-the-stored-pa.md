---
schema_version: 1
id: "iss-2609061334196626"
slug: "github-23-registry-remove-runs-os-removeall-on-the-stored-pa"
severity: "major"
category: "security"
source: "user-observation"
found_during: "github issue triage 2026-09-06 (adversarially verified)"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/registry/registry.go"
resolution: "Fixed: Registry.Remove(repoID, dir) deletes the directory the app recomputes from the validated id; the stored path never reaches os.RemoveAll. Test plants a registry.json pointing at a victim directory and asserts it survives Delete."
impact: fix
---

GitHub #23: Registry.Remove runs os.RemoveAll on the stored path field, so a registry.json planted in the shared root (0644, permanent) makes one Remove click delete an arbitrary victim-owned tree under the victim's euid. Every write path derives Path from ModelDir(RepoID), so nothing legitimate diverges. Fix: recompute the deletion directory from the validated repo id and never consult the stored path. Unfiled sibling: Bytes is trusted verbatim and feeds the memory budget.

## Grounds

- pursued: no legitimate entry's Path differs from ModelDir(RepoID), so nothing is lost; a model whose files are not removed on Delete would show it wrong
