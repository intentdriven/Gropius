---
schema_version: 1
id: "iss-8"
slug: "reaper-boottime-exact-equality-breaks-on-macos-clock-steps"
severity: "minor"
category: "bug"
source: "agent-finding"
found_during: "2026-07 bug-hunt round 4"
found_at: "internal/runtime/reaper.go"
resolution: "The orphan reaper's same-session test now keys on kern.bootsessionuuid, which does not move within a boot, instead of exact equality of the clock-derived kern.boottime."
impact: fix
---

The orphan-reaper keys its "same boot session?" test on exact nanosecond equality of `kern.boottime`, but that value is not stable within a single boot on macOS, so a clock step silently defeats reaping and leaks GPU memory. `bootTimeNs()` (reaper.go:200-206) returns `tv.Nano()` = `sec*1e9 + usec*1000`, and both call sites compare it with `!=`: `add()` (reaper.go:61) drops every recorded entry when `boot != bootTimeNs()`, and `reapOrphans()` (reaper.go:174) returns 0 (reaps nothing) when `boot != bootTimeNs()`.

The premise is that `kern.boottime` only changes across reboots, so a mismatch means a dead prior session. That is false on Darwin. `kern.boottime` is defined as `walltime - uptime`, and XNU maintains it as mutable globals (`clock_boottime`/`clock_boottime_usec`) that `clock_set_calendar_microtime` adjusts by the correction delta on every calendar clock *step* — the path taken on the initial post-boot NTP sync, larger NTP corrections, `timed` re-disciplining after sleep/wake, and manual clock changes. So the value drifts within one boot (documented real-world reports show ~3s of movement within minutes of startup, and sub-second `tv_usec` jitter from smaller corrections). Because `tv.Nano()` carries the microsecond field and the comparison is exact, any drift at all breaks it.

Consequences, both fail-safe (the reaper never kills the wrong process — it only fails to act):
- **add() path:** a clock step between two model launches makes `add()` see `boot != now`, set `entries = nil`, and re-stamp — permanently forgetting the process groups of servers launched before the step. If Gropius later crashes, those servers are never reaped and hold gigabytes of GPU memory until reboot.
- **reapOrphans() path:** a clock step between a crash/quit and the next launch makes startup reaping a no-op, leaving orphaned `mlx_lm` servers pinned — the exact failure the ledger exists to prevent.

On an Apple Silicon laptop (the target hardware) sleep/wake and NTP steps are routine, so this is a real reliability hazard, not merely theoretical.

Deferred rather than auto-patched: `internal/runtime` is a declared trust boundary (subprocess management), the code is darwin-only and cannot be exercised on the Linux hunt runner, and every candidate fix is a design decision. Comparing only whole seconds still fails on the documented multi-second post-boot step; a tolerance window weakens the "different boot ⇒ different value" discrimination that guards the `startNs == 0` entries (reaper.go:185-189 skips the per-pid start-time check when no start time was recorded, so for those the boot stamp is the only backstop against SIGKILLing a recycled pgid after a fast reboot). The principled fix is to stop keying on a clock-derived value: `kern.bootsessionuuid` is a per-boot UUID, stable within a boot and immune to clock steps — exactly the session marker this code wants — with the per-pid `P_starttime` check remaining the anti-recycle authority. Confirming that redesign needs a Mac (e.g. to check whether `P_starttime` itself moves on a clock step), so it is a maintainer decision.

## Grounds

- pursued: orphaned model servers are reaped after any calendar clock step, because the ledger's stamp is the per-boot UUID and only a real reboot changes it; a reaper that stops acting within one boot, or one that acts on a ledger from another session or from an older version's format, would show it wrong.
