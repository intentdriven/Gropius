---
schema_version: 1
id: "iss-2609081014210884"
slug: "the-full-history-secret-scan-fails-a-branch-for-findings-tha"
severity: "major"
category: "process"
source: "user-observation"
found_during: "cloudflare-deploy-investigation"
origin: researcher-authored
production_mode: hand-written
found_at: ".github/workflows/ci.yml"
---

The full-history secret scan fails a branch for findings that live on a different branch, so any pushed branch can turn the default branch red. The gitleaks job checks out with fetch-depth 0, which fetches every ref, and the scan then walks all of that history rather than the ref under test. A branch pushed with a test fixture the generic-api-key heuristic dislikes therefore fails the default branch's own run, whose tree contains nothing of it. Observed: a run on the default branch went red with two findings, both resolving to a commit on an unrelated feature branch, while its build and workflow-audit jobs passed. The red gives no hint that the cause is elsewhere, so the reader's first move is to search a tree that cannot contain the finding. Scoping the scan to the ref under test, or naming the offending commit's branch in the failure, would each fix the diagnosis; the second also keeps whole-history coverage.

## What it cost, observed

Appended after the incident closed, because the cost is the argument.

Two sessions were blocked at once and the release chain was held. The clearing
move was not an edit: gitleaks anchors a finding to the commit that INTRODUCED
the string, so a later commit removing it leaves the finding standing. The
offending commit had to stop existing on any ref — which meant rebuilding the
branch onto a fresh base, folding the fixture change into the commit that
introduced it, pushing under a new name and deleting the old remote branch,
closing and reopening its pull request. A repository that refuses force-pushes,
rightly, has no cheaper route.

So the fault is not only that the diagnosis points at the wrong tree. It is
that a false positive on any branch escalates into a history rewrite before the
default branch can go green again.
