---
schema_version: 1
id: "iss-2609111755330533"
slug: "internal-lifecycle-swap-go-claims-no-failure-path-leaves-the"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "second security pass on the lifecycle branch, 2026-09-11"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/lifecycle/swap.go"
---

internal/lifecycle/swap.go claims no failure path leaves the Mac with no application. When the destination reappears mid-swap the retired bundle now waits in the kept staging directory (mode 0700, unguessable name) until a person moves it back; but a directory entry is removed by write permission on the PARENT, and the applications directory is group-writable to admin accounts with no sticky bit, so a co-resident admin-group account can delete the staging directory and leave no application for as long as the wait lasts — the same privilege tier the reappearance itself needs, so no new boundary, but the file's claim is not strictly true against that actor and the fix made the window unbounded. Say so in the file's commentary or an ADR note; a stickier home for the set-aside copy (this account's own directory) would close it.
