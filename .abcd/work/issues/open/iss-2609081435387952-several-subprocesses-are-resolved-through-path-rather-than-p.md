---
schema_version: 1
id: "iss-2609081435387952"
slug: "several-subprocesses-are-resolved-through-path-rather-than-p"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
---

Several subprocesses are resolved through PATH rather than pinned to an absolute path, on a product designed for shared multi-account Macs. internal/capability pins /usr/sbin/sysctl; other call sites invoke scutil, open and pbcopy by bare name and take whatever PATH resolves. On a single-user Mac this is hygiene: a shim planted in a PATH directory gains an attacker nothing they did not already have, because it runs as the same user. This product is explicitly not that case. Its design premise is one Mac serving several user accounts — the shared-cache install mode with its deliberate 3775 directory semantics, per-account MLX runtime executables, and the gateway's loopback exemption which exists because requests arrive from other macOS accounts on the same machine. The question is therefore not whether an attacker could already run code as this user, but whether a different account on this Mac can plant something the server account's process will execute; a group-writable directory on that PATH answers yes, and the boundary crossed is account-to-account, which this product claims. Not elevation, and a different class from an installer shim that receives elevation. The fix is to extend the existing precedent rather than to invent one. internal/runtime and internal/capability are both declared trust boundaries. Surfaced while reviewing a peer session's privileged-AppleScript guard.

## Call sites, verified

Every non-test `exec.Command` in the tree, at the time of writing:

| Site | First argument |
| --- | --- |
| `internal/capability/capability_darwin.go:68` | `"/usr/sbin/sysctl"` — pinned; the precedent |
| `internal/config/hostname_darwin.go:38` | `"scutil"` — bare |
| `cmd/gropius/main.go:291` | `"open"` — bare |
| `cmd/gropius/clipboard.go:11` | `"pbcopy"` — bare |
| `internal/runtime/launcher.go:234` | a variable — the managed runtime's own interpreter, not a PATH lookup |

So the inconsistency is three call sites against one precedent, and the
precedent is in the package the conventions already name as a trust boundary.

Checked rather than taken on report: the finding reached this ledger from
another session's message, and the call sites above were confirmed here before
it was written down.
