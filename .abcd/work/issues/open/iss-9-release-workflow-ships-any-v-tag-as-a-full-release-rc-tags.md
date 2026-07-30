---
schema_version: 1
id: "iss-9"
slug: "release-workflow-ships-any-v-tag-as-a-full-release-rc-tags"
severity: "minor"
category: "bug"
source: "agent-finding"
found_during: "2026-07 bug-hunt round 5"
found_at: ".github/workflows/release.yml"
---

The release workflow publishes any `v*` tag as a full release, so a prerelease-style tag (e.g. `v0.4.0-rc1`) can capture `releases/latest` and ship to every installer. The trigger matches all `v*` tags (release.yml:26-27), `gh release create` never passes `--prerelease` (release.yml:183-185), and the latest election compares tags with `sort -V` (release.yml:178-181) — which orders `v0.4.0-rc1` *after* `v0.4.0` (verified empirically on GNU coreutils 9.4; `sort -V` is not semver, where a pre-release precedes its release). Two consequences compound:

- Pushing `v0.4.0-rc1` while `v0.3.0` is current creates a normal (non-prerelease) release that wins the election and gets `--latest=true`; every `install.sh` run (`releases/latest/download`, install.sh:69) immediately ships the RC, validly signed.
- Because the RC was created as a *full* release, the election's `--exclude-pre-releases` never excludes it afterwards, so when `v0.4.0` final ships, `sort -V` still ranks `v0.4.0-rc1` newest and the final is published with `--latest=false` — `latest` stays pinned to the RC.

Latent today: the only tags ever pushed are `v0.1.0` and `v0.1.1`, the workflow's documented contract is plain `vX.Y.Z` tags, and no RC practice exists in this repo. Distinct from the round-2 fix, which addressed old-tag re-releases via the dispatch path.

Deferred rather than auto-patched because the fix encodes an unmade versioning policy on signed-release infrastructure: treating hyphenated tags as prereleases (`--prerelease` + excluding them from the election) adopts semver-ish semantics the repo never declared, while tightening the trigger pattern to `vX.Y.Z` forecloses RC tagging entirely. Either is a one-liner once the maintainer picks the policy; until then, avoid pushing hyphenated `v*` tags.
