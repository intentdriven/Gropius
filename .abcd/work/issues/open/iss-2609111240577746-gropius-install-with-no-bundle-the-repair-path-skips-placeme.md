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
---

gropius install with no --bundle (the repair path) skips placement and goes straight to the firewall elevation for <dest>/Contents/MacOS/gropius without checking that a Gropius bundle is installed there: on a Mac where the destination is an empty or foreign-owned directory it raised the administrator panel for a binary that does not exist. Reproduced by a unit test that dispatched the verb for real (see the sibling capture). The repair path must first verify the bundle and its binary at the destination and refuse, naming the destination and the bootstrap command, before any elevation, provisioning or launch.
