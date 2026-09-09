---
schema_version: 1
id: "iss-2609081441311030"
slug: "internal-archtest-repo-id-fold-test-go-s-second-walker-start"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
resolution: "Both walkers are rooted at the checkout through the shared walker instead of a working-directory-relative '..', and the two allow-lists are collapsed into one repoIDFoldAllowList, so the drifted three-of-five copy is gone."
impact: internal
---

internal/archtest/repo_id_fold_test.go's second walker starts from '..' rather than the repository root, so it walks OUT of the repository entirely. On a machine where the parent directory holds other checkouts, that test's subject includes unrelated projects, and what it asserts depends on what happens to sit beside the repository on that developer's disk. This is filed separately from the dot-directory skip inconsistency (iss-2609081427104462) on purpose: a shared walker fixes the four call sites that disagree about what to skip and does NOT fix this one, because the question here is not which directories to exclude but what this test is meant to look at, which cannot be answered from the code. Filing it apart so it cannot be closed by that refactor and quietly considered handled. Surfaced by a peer session reviewing the walker inconsistency.

## 2026-09-09 — routed through the shared walker; the scope question is still open

Both walkers in `internal/archtest/repo_id_fold_test.go` now go through
`walkRepoFiles`, the one walker `iss-2609081427104462` introduced, so each skips
every dot-directory. That closes the worktree hazard here and nothing else: the
root is still `".."` and the subject is unchanged.

Read again while making that change, three things this record should carry.

- `".."` is `internal/`, not the parent of the repository. The tests live in
  `internal/archtest`, `go test` runs with the package directory as the working
  directory, and both walkers were written that way deliberately — the original
  commit spells it `filepath.Join("..") // internal/` and both walks report
  failure as `walking internal/`. So the premise above, that the test walks out
  of the repository and scans whatever sits beside it on a developer's disk, does
  not hold. The finding it was filed under still does: the walk was unconditional
  and would have descended into a dot-directory under `internal/` had one
  existed.
- What the checks assert. `TestRepoIDFoldHasOneHome` fails on any case fold in
  the four packages that key by repo id (`registry`, `runtime`, `app`, `gateway`)
  unless the source line is on an allow-list, and on any case fold anywhere else
  under `internal/` whose argument is spelled like a repo id.
  `TestRepoIDFoldAllowListIsNotStale` walks the same tree collecting every source
  line, and fails on an allow-list entry that no longer matches one.
- The intent, as far as the code and its history show it: every non-test Go file
  under `internal/`. All five allow-listed lines live in `internal/gateway`, and
  `cmd/` performs no case fold at all today, so `internal/` and the whole
  checkout are the same subject in practice — widening would change no result
  now, and would change one the day `cmd/` folds anything.

Recommendation, for the maintainer to accept or reject. Root both walkers at the
checkout (`repoRootDir(t)`) and scope the keyed-package half by package name as
it already does, so the backstop half covers `cmd/` too; the root then stops
depending on the test binary's working directory, and the "everywhere else"
half means everywhere. Two things to settle with it, neither of which this
change touched: the second walker keeps its OWN copy of the allow-list, and that
copy has drifted — it carries three of the five entries, so the two added later
(`gropiusHeaders[strings.ToLower(k)]` and `strings.EqualFold(k, field)`) are
exempted by the first check and watched by nothing. A staleness check that has
itself gone stale is worth a line in whichever direction the scope goes.

## Grounds

- pursued: the fold checks now have a subject that does not depend on the test binary working directory, the backstop half covers cmd/, and one list is watched by the staleness check rather than a copy of it; the conjecture is that widening changed no result because cmd/ folds nothing today, shown wrong the moment a legitimate fold appears outside internal/ and the single allow-list has to grow to carry it.
