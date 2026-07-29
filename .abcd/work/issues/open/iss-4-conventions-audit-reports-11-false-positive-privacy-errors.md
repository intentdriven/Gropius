---
schema_version: 1
id: "iss-4"
slug: "conventions-audit-reports-11-false-positive-privacy-errors"
severity: "nitpick"
category: "process"
source: "agent-observation"
found_during: "conventions-adoption"
---

The conventions audit reports 11 false-positive privacy-hygiene errors for the /Users/Shared/Gropius shared-cache path — a macOS system directory that is part of the product design, not a username. Detector fix filed upstream in the audit tooling's own ledger; until it lands, the audit stays red on these known-benign findings.
