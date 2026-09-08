---
schema_version: 1
id: "iss-2609081310119313"
slug: "install-sh-resolves-sudo-shasum-ditto-and-xattr-through-path"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
---

install.sh resolves sudo, shasum, ditto and xattr through PATH, which curl-pipe-bash inherits from the invoking user. Verified on this Mac: three PATH entries sit ahead of /usr/bin, and /opt/homebrew/bin is drwxrwsr-x admin:admin. Malware already running unprivileged as the user plants a shim earlier in PATH for whichever of these the privileged path runs, and is handed the administrator password for a panel that looks genuine.

Amended 2026-09-08: install.sh no longer executes `sudo` — the firewall grant now goes through an `osascript` authentication panel, and `sudo` survives only in echoed fallback text. The vector moved rather than closing: `osascript` is PATH-resolved at three sites (the capability probe, the grant, and the quit-running-app call), so a planted `osascript` shim draws its own panel and harvests the password. That is worse than what it replaced, because the change deliberately teaches the user that a GUI authentication panel is the expected part of installing Gropius, which makes a counterfeit more convincing rather than less. The argument-escaping in the new code is correct and does not help here: `quoted form of` protects the argument, not the interpreter. A planted 'shasum' defeats the checksum verification at line 104 outright, which is the only integrity control in the install path. Fix: absolute paths for every system tool (/usr/bin/osascript, /usr/bin/ditto, /usr/bin/xattr, /usr/bin/mktemp, /usr/bin/pgrep), matching the rule internal/capability/capability_darwin.go already states for /usr/sbin/sysctl, and do SHA-256 in Go via crypto/sha256 rather than shelling out. A planted `shasum` defeats the verification at line 159 silently, because that check's output is discarded. The new internal/archtest/privileged_shell_test.go is the place to arm this: a rule that every command on the privileged path is invoked by absolute path. The existing exec.Command calls for open, pbcopy and scutil are the same latent shape.
