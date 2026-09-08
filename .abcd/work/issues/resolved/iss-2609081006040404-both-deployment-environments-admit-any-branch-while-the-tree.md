---
schema_version: 1
id: "iss-2609081006040404"
slug: "both-deployment-environments-admit-any-branch-while-the-tree"
severity: "minor"
category: "security"
source: "user-observation"
found_during: "cloudflare-deploy-investigation"
origin: researcher-authored
production_mode: hand-written
found_at: ".github/workflows/site.yml"
resolution: "Both environments now carry a custom deployment branch policy admitting the branch main and the tag pattern v*, set through the forge API. The tree's claim and the forge's setting agree again, and the site environment's two secrets survived the change."
impact: internal
---

Both deployment environments admit any branch, while the tree records them as restricted to the default branch and v-tags. The site workflow's header and wrangler.jsonc each state the policy as the default branch AND v* tags for both environments, and name themselves the only durable record of it because the forge holds the real setting. The forge reports no deployment branch policy on either environment. The workflow's resolve job asserts the ref itself and refuses anything else, so the deploy is not exposed today; what is lost is the second barrier the record claims exists, and the claim is now false. A reader checking the tree would believe a restriction that is not set.

## Grounds

- pursued: the workflow header and wrangler.jsonc each name themselves the durable record of a restriction the forge alone enforces, so the record is only true if the forge is set to match; a later run dispatched from any other ref being admitted would show this wrong
