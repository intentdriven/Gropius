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
resolution: "scutil, open and pbcopy are pinned to /usr/sbin/scutil, /usr/bin/open and /usr/bin/pbcopy, matching internal/capability's /usr/sbin/sysctl, and an architecture test now fails any non-test exec.Command/CommandContext whose program is a string literal not beginning with '/'."
impact: fix
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

## Measured, not hypothesised

The writable-PATH-directory premise is the state of a stock developer Mac, not a
contrivance. On the machine this was found on, `/opt/homebrew/bin` is
`drwxrwsr-x` owned `admin:admin` — group-writable, and setgid, so anything
planted there inherits the group too — and the local `admin` group has four
members. Any of them can write a directory that sits on another account's PATH
ahead of `/usr/sbin` and `/usr/bin`.

## The three sites are not equal

`open` (`cmd/gropius/main.go:291`) and `pbcopy` (`cmd/gropius/clipboard.go:11`)
both need a human to choose a menu item. `scutil`
(`internal/config/hostname_darwin.go:38`) does not: `LocalHostName` memoises
behind a `sync.Once`, and it is reached from the endpoint list, which the menu
bar builds and the control panel's state snapshot re-renders. So it runs once
per server process, early, with no user gesture at all — in every account on
the machine that launches Gropius. That is the one to fix first.

## Not the installer's class

An installer shim receives elevation and can harvest an administrator password.
A shim here executes with the victim account's own privileges. Same root cause,
different severity, and the two should not be recorded as one — an installer
fix must not be read as having covered this.

Verified in this repository and on this machine before being written down; the
finding arrived from another session and the directory mode, the group size,
the three call sites and the `sync.Once` were each checked here.

## Grounds

- pursued: no bare-name subprocess remains in shipping code, so another account on this Mac cannot answer for a command this account runs, and a name resolved on a CI runner is no longer ambiguous; we are wrong if a call site assembles a bare name into a variable, which the static check allows by design, or if one of these absolute paths is not where macOS keeps the tool on a supported release.
