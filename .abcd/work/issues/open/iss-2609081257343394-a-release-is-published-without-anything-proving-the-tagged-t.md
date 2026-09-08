---
schema_version: 1
id: "iss-2609081257343394"
slug: "a-release-is-published-without-anything-proving-the-tagged-t"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
---

A release is published without anything proving the tagged tree works. v0.3.0 was tagged from the commit that cut it and published while the install.sh repair was still in flight, so the tagged tree carries a script that aborts with an unbound variable on its first run; the release chain built and published it without ever executing it. The website was unaffected because its one-liner fetches the script from the default branch, so the fault was reachable only by someone building from the tag, which is also why nothing caught it. Decided at interview on 2026-09-08: the release chain should build the tagged tree and run the install script against it before the release is published, rather than gating on which pull requests are open. That catches any broken artefact reaching a tag, not only the sequence that happened here.
