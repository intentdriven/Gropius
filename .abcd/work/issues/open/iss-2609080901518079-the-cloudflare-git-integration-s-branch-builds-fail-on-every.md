---
schema_version: 1
id: "iss-2609080901518079"
slug: "the-cloudflare-git-integration-s-branch-builds-fail-on-every"
severity: "minor"
category: "process"
source: "user-observation"
found_during: "cloudflare-deploy-investigation"
origin: researcher-authored
production_mode: hand-written
found_at: "wrangler.jsonc"
---

The Cloudflare Git integration's branch builds fail on every pull request, leaving a permanently red check that no one can act on. The check 'Workers Builds: gropius' failed on the most recent pull request while succeeding on the default branch, so the integration has branch builds enabled as well as production builds. Both are recorded in wrangler.jsonc as disabled. A check that is always red on pull requests trains a reviewer to ignore the check list.
