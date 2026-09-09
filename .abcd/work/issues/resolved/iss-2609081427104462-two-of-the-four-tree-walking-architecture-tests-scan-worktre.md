---
schema_version: 1
id: "iss-2609081427104462"
slug: "two-of-the-four-tree-walking-architecture-tests-scan-worktre"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
resolution: "One shared walker in internal/archtest now owns the skip rule (every dot-directory, plus node_modules, dist, bin and site) and all six full-tree scans go through it, with the walker itself tested against a file planted under a dot-directory."
impact: internal
---

Two of the four tree-walking architecture tests scan worktrees checked out under .claude/, so their result depends on untracked files that are not repository content. Measured in the main checkout: a walk excluding only .git, node_modules, dist, bin and site sees 12 shell scripts, 8 of which sit inside .claude/worktrees/ — two-thirds of what internal/archtest/shell_expansion_test.go scans is not tracked. Three copies of install.sh are visible where exactly one is tracked. A non-compliant line written in any agent worktree turns the main checkout's test red for a file nobody can locate in the repository, and conversely a worktree copy can hold a test green. internal/archtest/repo_id_fold_test.go has the same shape, and its second walker starts from '..' rather than the repo root, which is wider again. The repository already learned this: internal/archtest/prompt_content_test.go and internal/archtest/statistics_switch_test.go both skip every dot-directory and both carry a comment naming the worktree hazard explicitly. The guard was armed in two walkers of four. This is the one-canonical-primitive case — four hand-rolled WalkDir bodies each deciding its own exclusions — so the fix is one shared walker in the archtest package owning the skip rule, not a fifth copy of it.

## Grounds

- pursued: a scan of the tree now sees only this checkout's files, so an agent worktree under .claude/ can neither fail nor hold green a scan in the main checkout; we are wrong if a scan starts hand-rolling its own WalkDir again, or if excluding every dot-directory silently drops tracked content a scan needed (the shell scan names .github and .githooks back in for exactly that reason).
