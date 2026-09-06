---
schema_version: 1
id: "iss-2609061334194408"
slug: "github-20-the-reaper-trusts-planted-ledger-contents-kern-boo"
severity: "major"
category: "security"
source: "user-observation"
found_during: "github issue triage 2026-09-06 (adversarially verified)"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/runtime/reaper.go"
resolution: "Fixed: the ledger must be a regular file owned by our euid (Stat_t.Uid) and entries with pgid <= 1 are dropped at parse time; tests pin both with a live sleep child that must survive."
impact: fix
---

GitHub #20: the reaper trusts planted ledger contents. kern.boottime is world-readable, kill(-1,0) and kill(0,0) succeed unprivileged, and startNs=0 skips the identity check, so a regular running-servers.pids planted by a staff account (permanent: the sticky bit blocks the victim's os.Remove) makes every launch SIGKILL every process the victim's uid can signal. kern.proc.pid is readable cross-uid, so rejecting startNs=0 alone is insufficient; only provenance closes it. Fix: require the ledger to be a regular file owned by our euid and drop pgid <= 1.

## Grounds

- pursued: a planted ledger can no longer make the reaper signal anything; a reap of a genuine orphan after a crash still works (existing tests) — an orphan left unreaped on a real crash would show it wrong
