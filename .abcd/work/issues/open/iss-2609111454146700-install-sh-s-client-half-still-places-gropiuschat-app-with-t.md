---
schema_version: 1
id: "iss-2609111454146700"
slug: "install-sh-s-client-half-still-places-gropiuschat-app-with-t"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "security review of the lifecycle branch, 2026-09-11"
origin: researcher-authored
production_mode: hand-written
found_at: "install.sh"
---

install.sh's client half still places GropiusChat.app with the shell's mv (rename aside, then mv the staged bundle in), which nests into an existing directory and follows a symlink at the destination — the shape iss-2609081310071028 named and the server half no longer has, because the server is placed by gropius install's staged swap in Go. The client has no binary of its own to place itself, so the shell is the only thing that can place it; on a shared Mac whose applications directory another admin-group account can write, the client's swap can still be raced. Recorded in the script's own comment; a Go placer for the client (a second verb on the server binary, or a placement helper the bootstrap calls twice) is the remedy.
