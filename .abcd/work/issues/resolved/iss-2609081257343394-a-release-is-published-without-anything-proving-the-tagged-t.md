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
resolution: "release.yml runs the tagged tree's own install.sh against the artefacts it just built before it publishes, and the gate has now executed on a real tag: the v0.4.0 run shows 'Gate the release on the tagged tree's own installer' succeeding in the release job ahead of 'Attest the release assets' and 'Publish the Release with both apps attached', and v0.4.0 was published at 2026-09-08T18:06:10Z."
impact: fix
resolved_by:
  commit: "d0ae84f1afed0fedffdc6386936ee0bc649afa51"
---

A release is published without anything proving the tagged tree works. v0.3.0 was tagged from the commit that cut it and published while the install.sh repair was still in flight, so the tagged tree carries a script that aborts with an unbound variable on its first run; the release chain built and published it without ever executing it. The website was unaffected because its one-liner fetches the script from the default branch, so the fault was reachable only by someone building from the tag, which is also why nothing caught it. Decided at interview on 2026-09-08: the release chain should build the tagged tree and run the install script against it before the release is published, rather than gating on which pull requests are open. That catches any broken artefact reaching a tag, not only the sequence that happened here.

## Grounds

- pursued: a tag whose installer is broken now publishes nothing, because the gate executes the script against the built artefacts and its failure precedes every publish route; the gate's own comment claiming it has NEVER EXECUTED is now stale, and a release appearing with no preceding successful gate step in its run — or a publish route added beside the release job — would show it wrong.
