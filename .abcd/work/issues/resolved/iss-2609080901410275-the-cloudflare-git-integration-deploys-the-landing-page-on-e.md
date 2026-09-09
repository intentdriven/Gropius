---
schema_version: 1
id: "iss-2609080901410275"
slug: "the-cloudflare-git-integration-deploys-the-landing-page-on-e"
severity: "critical"
category: "security"
source: "user-observation"
found_during: "cloudflare-deploy-investigation"
origin: researcher-authored
production_mode: hand-written
found_at: "wrangler.jsonc"
resolution: "Verified on the forge on 2026-09-09: the default-branch head carries no Workers Builds check-run and no legacy status; the Cloudflare Git integration no longer deploys. The site is deployed by the release chain's site jobs alone."
impact: internal
---

The Cloudflare Git integration deploys the landing page on every push to the default branch, contradicting the recorded design that GitHub Actions is the only deployer. wrangler.jsonc states the Worker's automatic production builds and branch builds are disabled; the forge shows the check 'Workers Builds: gropius' succeeding on the default branch head and producing the live version, so the dashboard state is the opposite of what the tree records. The consequence is visible on the page: the Git integration runs a build command only, never the release-record steps in the site workflow, so the deployed page carries no release facts at all. Nothing in the repository detects the drift; the claim lives in a comment.

## Grounds

- pursued: the maintainer disabled the Git integration on the dashboard and the forge shows no Workers Builds check on the default branch head or on the last three merged pull requests; shown wrong if a Workers Builds check reappears on any future commit, which would mean the dashboard state drifted again and a repository-side detector is needed after all
