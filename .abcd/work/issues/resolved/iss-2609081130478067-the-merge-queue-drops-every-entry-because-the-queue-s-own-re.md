---
schema_version: 1
id: "iss-2609081130478067"
slug: "the-merge-queue-drops-every-entry-because-the-queue-s-own-re"
severity: "major"
category: "bug"
source: "user-observation"
found_during: "merge-queue-enablement"
origin: researcher-authored
production_mode: hand-written
found_at: ".github/workflows/ci.yml"
resolution: "ci.yml's push trigger excludes gh-readonly-queue/**, and the queue has since been confirmed working end to end: ten merge_group runs on gh-readonly-queue refs all completed successfully with no competing push run on the same ref, and each entry merged — most recently run 34260414984 on gh-readonly-queue/main/pr-34-e004c551 succeeding at 2026-09-08T18:00:01Z with PR #34 merging at 18:01:59Z as cd2bd32."
impact: internal
resolved_by:
  commit: "2761990cd1688d65c0176e55e86cd93f27b6de0a"
---

The merge queue drops every entry because the queue's own ref also fires a push run that cancels the queue's checks. A queue entry is pushed to a temporary ref under gh-readonly-queue, which matches the workflow's push filter of all branches, so the same ref raises both a push run and a merge_group run. The two share a concurrency group, since the group is keyed on workflow and ref alone, and the push run cancels the merge_group run. The queue then never receives the required checks it is waiting for, waits, and drops the entry. Observed on three entries across two pull requests: each shows a cancelled merge_group run beside a successful push run on the identical gh-readonly-queue ref, and each pull request went from QUEUED to AWAITING_CHECKS and then out of the queue without merging. Exempting merge_group runs from cancel-in-progress does not fix it, because cancellation is decided by the incoming run, and the incoming run is the push.

## Why the first attempt missed it

The change that introduced the trigger also exempted merge_group runs from
cancel-in-progress, reasoning that a cancelled check reads to the queue as a
failed entry. That reasoning is correct and the exemption stays. It could not
have fixed this, because concurrency cancellation is decided by the INCOMING
run, and the incoming run is the push.

The generalisable fault is in the verification, not the reasoning: the trigger
was confirmed to exist, and an entry was never confirmed to merge. A feature
was declared usable on the evidence that its parts were present rather than on
the evidence that it worked once end to end.

This is the same shape as the secret-scan incident recorded in
[[the-full-history-secret-scan-fails-a-branch-for-findings-tha]], where a fix
verified locally did not exercise what the pipeline actually does. Both cost a
second round because the cheap check was mistaken for the real one.

## Grounds

- pursued: a queue entry's ref now raises only a merge_group run, so nothing shares its concurrency group and cancels the checks the queue waits for; this is confirmed by entries actually merging rather than by the trigger merely being present, which is the verification failure the record names — a queue ref showing both a push and a merge_group run, or an entry going QUEUED to AWAITING_CHECKS and out again, would show it wrong.
