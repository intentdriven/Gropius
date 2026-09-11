---
schema_version: 1
id: "iss-2609111240577746"
slug: "gropius-install-with-no-bundle-the-repair-path-skips-placeme"
severity: "major"
category: "observation"
source: "user-observation"
found_during: "merging lifecycle phases A and B, 2026-09-11"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/lifecycle/install.go"
resolution: "gropius install with no --bundle now verifies the installed bundle and its binary at the destination before anything happens to it, and refuses — naming the destination and the bootstrap command — ahead of any quit, authorisation panel, provisioning or launch. It refuses an absent destination, one that is not a directory, one carrying no binary, one whose binary is not a regular file, and one that is a symbolic link, because the applications directory is group-writable on a stock Mac. A companion test holds the positive repair case and a third asserts that nothing after a failed stage runs."
impact: fix
resolved_by:
  intent: "itd-2609081259532589"
  spec: "spc-2609111029315861"
  commit: "ec5ac5c"
---

gropius install with no --bundle (the repair path) skips placement and goes straight to the firewall elevation for <dest>/Contents/MacOS/gropius without checking that a Gropius bundle is installed there: on a Mac where the destination is an empty or foreign-owned directory it raised the administrator panel for a binary that does not exist. Reproduced by a unit test that dispatched the verb for real (see the sibling capture). The repair path must first verify the bundle and its binary at the destination and refuse, naming the destination and the bootstrap command, before any elevation, provisioning or launch.

## Grounds

- pursued: the repair path's precondition is that something is installed to repair, and checking it before every side effect is what keeps the one elevation tied to a binary that exists; wrong if a Mac turns up where the bundle is legitimately absent while the rest of the installation is present and worth repairing, which would mean the precondition belongs on each stage rather than at the top
