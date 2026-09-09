---
schema_version: 1
id: "iss-2609091131311102"
slug: "under-a-shared-cache-the-per-model-server-logs-and-the-runni"
severity: "major"
category: "bug"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/runtime/launcher.go"
resolution: "The per-model server logs and the running-servers.pids ledger now live in the account's own directory alongside its settings, so a second account can launch a model the first has served and keeps its own reap ledger; the shared root holds the models and the download cache only."
impact: breaking
---

Under a shared cache the per-model server logs and the running-servers.pids ledger are still single-writer: internal/runtime/launcher.go opens <Paths.Logs>/<model>.log with O_CREATE|O_TRUNC at 0600 and internal/runtime/reaper.go keeps the pid ledger in Paths.Root, both in the sticky shared root, so the second account cannot open a log the first account already wrote (EACCES, and the model will not launch) and its ledger rename fails EPERM, silently killing orphan reaping.

Found by the security review of iss-10, which moved `config.json` and
`registry.json` to per-account state but left these two behind. The maintainer's
decision for iss-10 was "everything per-account", and these are the remaining
files it does not cover.

Two paths, one cause — a fixed name in a group-writable sticky directory that
only its creator can write:

- `internal/runtime/launcher.go` builds `<Paths.Logs>/<model>.log` and opens it
  `O_CREATE|O_WRONLY|O_TRUNC` at 0600. Once Alice has served a model, Bob's open
  of the same path returns EACCES and `Launch` fails with "create log:
  permission denied" — so Bob cannot serve any model Alice ever launched, which
  is every model in the shared cache she used.
- `internal/runtime/reaper.go` keeps `running-servers.pids` in `Paths.Root`.
  Bob's write is a CreateTemp-then-rename over Alice's file, which the sticky
  bit refuses with EPERM, and the failure is swallowed — so Bob records nothing
  and orphan reaping stops working for him.

Neither file has any reason to be shared: a log is one account's record of what
its own subprocess printed, and an orphan process group can only be signalled by
the uid that started it, so a ledger entry is useless to any other account.

## Grounds

- pursued: we expect a second account under a shared cache to launch a model the first account has already logged, and to record and reap its own orphans, with the shared root holding only what every account may write; we are wrong if any fixed-name file one account creates in the shared root is one another account must write
