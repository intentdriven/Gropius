---
schema_version: 1
id: "iss-2609061334202885"
slug: "github-22-the-per-model-log-is-opened-o-create-o-wronly-o-tr"
severity: "critical"
category: "security"
source: "user-observation"
found_during: "github issue triage 2026-09-06 (adversarially verified)"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/runtime/launcher.go"
resolution: "Fixed: the per-model log is opened O_CREATE|O_WRONLY|O_TRUNC|O_NONBLOCK|O_NOFOLLOW with an fstat regular-file check; LogDir is Lstat-checked instead of MkdirAll'd. Tests pin symlink refusal (victim file intact) and FIFO no-block."
impact: fix
---

GitHub #22: the per-model log is opened O_CREATE|O_WRONLY|O_TRUNC without O_NOFOLLOW at a predictable name in the 3775 shared logs/ directory, so a planted symlink truncates and then streams INFO logs into any file the victim can write, under the victim's uid; with the default open LAN gateway the attacker triggers the load themselves via /v1/chat/completions. A planted FIFO wedges Launch. Fix: O_NOFOLLOW|O_NONBLOCK plus an fstat regular-file check.

## Grounds

- pursued: a planted symlink or FIFO under a predictable log name can no longer truncate a victim file or wedge Launch; a launch that fails because the logs directory legitimately vanished at runtime is the accepted trade
