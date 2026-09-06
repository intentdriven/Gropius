---
schema_version: 1
id: "iss-2609061332089555"
slug: "github-19-pid-ledger-readlocked-opens-running-servers-pids-w"
severity: "major"
category: "security"
source: "user-observation"
found_during: "github issue triage 2026-09-06 (adversarially verified)"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/runtime/reaper.go"
resolution: "Fixed on fix/github-issues-19-34: readLocked reads through config.OpenRegular (O_NONBLOCK|O_NOFOLLOW, fstat regular-file), capped at 1 MiB; tests pin no-block-on-FIFO and no-follow-symlink."
impact: fix
---

GitHub #19: PID-ledger readLocked opens running-servers.pids with a plain os.Open (no O_NONBLOCK/O_NOFOLLOW/fstat). In shared-cache mode any staff account can plant a FIFO at that name in the lazily-created window; startup (reapOrphans, after the port is claimed) and every model launch (add) then block forever, holding the port and the ledger mutex, and the sticky bit blocks in-app recovery. A symlink is followed. Fix: read through a hardened regular-file helper.

## Grounds

- pursued: a planted FIFO or symlink under running-servers.pids now reads as an empty ledger and never blocks; a startup or launch that still hangs on a planted ledger would show it wrong
