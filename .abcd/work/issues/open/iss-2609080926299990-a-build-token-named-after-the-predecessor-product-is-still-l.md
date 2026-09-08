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
---

A build token named after the predecessor product is still live on the hosting account, carrying thirteen permissions, for a Git integration that no longer exists. Found while creating the deploy credential: the account's token list holds a build token under the predecessor name alongside one under the current name. The rename removed the name from the repository but left the credential behind it standing. A stale token is a live credential rather than mere debris, and its scope is far broader than any deploy needs. The sibling token under the current name, twenty-five permissions, belongs to the Git integration recorded as disconnected and is orphaned by that disconnection. Both should be revoked once the reviewed deploy path is proven, not before, so a failure has one cause and not three.
