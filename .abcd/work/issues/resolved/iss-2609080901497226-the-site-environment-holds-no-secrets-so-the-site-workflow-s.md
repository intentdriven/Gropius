---
schema_version: 1
id: "iss-2609080901497226"
slug: "the-site-environment-holds-no-secrets-so-the-site-workflow-s"
severity: "major"
category: "bug"
source: "user-observation"
found_during: "cloudflare-deploy-investigation"
origin: researcher-authored
production_mode: hand-written
found_at: ".github/workflows/site.yml"
resolution: "Verified on the forge on 2026-09-09: the site environment holds the two secret names the deploy job reads, both created on 2026-09-08, and the release chain run for v0.4.0 completed its site deploy job successfully with a site deployment record in the success state."
impact: fix
---

The site environment holds no secrets, so the site workflow's deploy job has never once succeeded. The v0.2.0 release chain ran resolve and render green — render elected v0.2.0, validated the release record and rendered the page with three assets — and then deploy failed at wrangler with 'In a non-interactive environment, it's necessary to set a CLOUDFLARE_API_TOKEN environment variable'. The deploy step reads CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID from the site environment, which the forge reports as holding zero secrets. Only the maintainer can create them.

## Grounds

- pursued: with the secrets present the deploy job is expected to succeed on every release cut from now on; shown wrong if a release run's site deploy job fails on the wrangler authentication step again, which would mean the secrets expired or the environment was recreated
