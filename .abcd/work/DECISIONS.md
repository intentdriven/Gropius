# Decisions — one dated line each; architecture-shaping entries graduate to ADRs

- 2026-07-29: Adopted the three-tier `.abcd/` working-state layout; the root `DECISIONS.md` (verified spike facts) moved to `.abcd/development/decisions/DECISIONS.md`; handover notes moved to the gitignored local tier.
- 2026-07-29: Deferred findings from the 2026-07 audit rounds captured as ledger issues under `.abcd/work/issues/` so they survive a clone; the handover file is no longer their home.
- 2026-07-29: History rewritten (git filter-repo, maintainer-pushed) to purge a committed handover file containing machine-local network details; handover notes live only in the gitignored local tier from now on.
