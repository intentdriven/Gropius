---
schema_version: 1
id: "iss-2609061334201471"
slug: "github-24-the-download-destination-is-reached-through-symlin"
severity: "major"
category: "security"
source: "user-observation"
found_during: "github issue triage 2026-09-06 (adversarially verified)"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/hub/download.go"
resolution: "Fixed: hub.DownloadRequest.ModelsDir (the app passes Paths.Models); openDest creates org/name relative to an os.Root at the models root with Lstat is-a-directory checks and setgid-inheriting modes, then opens the model root from there; mkdirAllInherit removed. Test pins a symlinked org refusal with nothing written outside."
impact: fix
---

GitHub #24: the download destination is reached through symlink-followable ancestors — mkdirAllInherit and os.OpenRoot(dest) resolve models/<org> and <name> with ordinary symlink semantics, so an org symlink planted (or owner-swapped) in the 3775 models tree redirects every write, and the later Remove follows the same ancestor into os.RemoveAll. Rescan already refuses symlinked orgs, so the model then silently vanishes. Fix: open an os.Root at the models directory and create org/name root-relative with an Lstat is-a-directory check before descending.

## Grounds

- pursued: downloads and the later delete can no longer be redirected through a symlinked ancestor; a user who symlinks the whole models directory is unaffected because the models root path itself is still followed
