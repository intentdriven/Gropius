---
schema_version: 1
id: "iss-2609081257347705"
slug: "abcd-does-not-detect-an-adr-that-was-filed-but-never-added-t"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
---

abcd does not detect an ADR that was filed but never added to its index. adr-2609070004056820 was filed on 2026-09-07 and its row was missing from the index in the ADRs README until 2026-09-08; the README states that appending the row is a hand edit, and nothing checks that the hand edit happened. The cost is specific to what an index is for: that ADR records a lock order two independent reviewers each proposed inverting, and its own closing line is that the constraint is now findable, which from the index it was not. The row is added by hand in this change; this capture is the guard, and it belongs in abcd's record-lint alongside record_provenance rather than as a bespoke test in this repository, because every repo abcd manages has the same hand-edited index and the same way to forget it.
