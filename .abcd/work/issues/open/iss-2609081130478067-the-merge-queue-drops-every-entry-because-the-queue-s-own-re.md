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
