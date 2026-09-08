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
---

internal/archtest/repo_id_fold_test.go's second walker starts from '..' rather than the repository root, so it walks OUT of the repository entirely. On a machine where the parent directory holds other checkouts, that test's subject includes unrelated projects, and what it asserts depends on what happens to sit beside the repository on that developer's disk. This is filed separately from the dot-directory skip inconsistency (iss-2609081427104462) on purpose: a shared walker fixes the four call sites that disagree about what to skip and does NOT fix this one, because the question here is not which directories to exclude but what this test is meant to look at, which cannot be answered from the code. Filing it apart so it cannot be closed by that refactor and quietly considered handled. Surfaced by a peer session reviewing the walker inconsistency.
