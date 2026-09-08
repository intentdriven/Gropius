---
schema_version: 1
id: "iss-2609080926299990"
slug: "a-build-token-named-after-the-predecessor-product-is-still-l"
severity: "major"
category: "security"
source: "user-observation"
found_during: "cloudflare-deploy-investigation"
origin: researcher-authored
production_mode: hand-written
found_at: "internal (hosting account, API tokens)"
resolution: "Both build tokens are deleted from the hosting account. The reviewed deploy path was proven first and re-proven after: a dispatched site deploy ran resolve, render and deploy green with the tokens gone, and the page still serves the current release, so the account-scoped deploy credential carries the work alone and neither deleted token was load-bearing."
impact: internal
---

A build token named after the predecessor product is still live on the hosting account, carrying thirteen permissions, for a Git integration that no longer exists. Found while creating the deploy credential: the account's token list holds a build token under the predecessor name alongside one under the current name. The rename removed the name from the repository but left the credential behind it standing. A stale token is a live credential rather than mere debris, and its scope is far broader than any deploy needs. The sibling token under the current name, twenty-five permissions, belongs to the Git integration recorded as disconnected and is orphaned by that disconnection. Both should be revoked once the reviewed deploy path is proven, not before, so a failure has one cause and not three.

## Grounds

- pursued: a credential for an integration that no longer exists is standing access and nothing else, so removing it should cost nothing; a deploy failing after the deletion would show this wrong, and one was run deliberately to give it the chance
