---
schema_version: 1
id: "iss-5"
slug: "cross-account-singleton-client-mode-is-unreachable"
severity: "major"
category: "bug"
source: "agent-finding"
found_during: "2026-07 bug-hunt round 1"
found_at: "cmd/gropius/singleton.go"
---

The documented cross-account "become a client" mode can never succeed. probePortHolder classifies the port holder as ours only when readInstanceToken returns a token matching the one the running server serves — but with per-user roots the second account never has the first account's token, and in shared-cache mode writeInstanceToken creates the token 0600, so a peer account's read fails EACCES. Either way the probe returns holderForeign, acquireListener errors, and main exits 1 with an impersonation warning; only a same-account relaunch reaches client mode. Aggravation in shared mode: the root's sticky bit blocks os.Rename over a token owned by another account, so once account B has run the server, account A's stale-token cleanup fails and even A's own live server is classified foreign. The decisions record ("one server, one GPU, N user accounts") and the main.go package doc promise the opposite.

Fix needs a design decision, so it is deferred rather than auto-patched: making the token group-readable would let any local account impersonate the server (reading the token is all a squatter needs to answer the probe), defeating the 0600 anti-forgery choice. A sounder shape is a same-root proof that keeps no shared secret at rest — e.g. the prober drops a random nonce file into the root and asks the server over loopback to echo it back — plus EPERM-tolerant cleanup of stale tokens. Documenting client mode as same-account-only would also resolve the contradiction, at the cost of the feature.
