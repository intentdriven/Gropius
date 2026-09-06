---
schema_version: 1
id: "iss-2609061334469875"
slug: "github-26-hub-wantedfiles-de-duplicates-paths-case-sensitive"
severity: "minor"
category: "bug"
source: "user-observation"
found_during: "github issue triage 2026-09-06 (adversarially verified)"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/hub/hub.go"
resolution: "Fixed: hub.Download refuses a repo whose wanted paths collide case-insensitively, naming the pair, before any file is written; test pins the refusal."
impact: fix
---

GitHub #26: hub.WantedFiles de-duplicates paths case-sensitively, so a repo with two files differing only in case races two goroutines over one .gropius-part on macOS's case-insensitive APFS: the first two attempts abort with ENOENT on the shared part file and a third can finalise an interleaved non-LFS file (LFS weights fail closed on SHA-256). Reachability is the round-5/17 refutation (only the repo author can publish the colliding names, inside their own os.Root), so this is a robustness defect, not a security one. Fix: refuse a repo whose wanted paths collide case-insensitively, with an error naming the pair, since such a tree cannot be materialised on a case-insensitive volume.

## Grounds

- pursued: such a tree cannot be materialised on a case-insensitive volume, so a loud refusal replaces two aborted attempts and a possibly interleaved file; a legitimate repo refused by this would show it wrong
