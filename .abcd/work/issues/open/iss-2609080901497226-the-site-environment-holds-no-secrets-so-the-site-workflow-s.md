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
---

The site environment holds no secrets, so the site workflow's deploy job has never once succeeded. The v0.2.0 release chain ran resolve and render green — render elected v0.2.0, validated the release record and rendered the page with three assets — and then deploy failed at wrangler with 'In a non-interactive environment, it's necessary to set a CLOUDFLARE_API_TOKEN environment variable'. The deploy step reads CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID from the site environment, which the forge reports as holding zero secrets. Only the maintainer can create them.
