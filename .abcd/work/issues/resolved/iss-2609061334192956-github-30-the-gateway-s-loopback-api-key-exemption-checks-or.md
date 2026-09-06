---
schema_version: 1
id: "iss-2609061334192956"
slug: "github-30-the-gateway-s-loopback-api-key-exemption-checks-or"
severity: "minor"
category: "security"
source: "user-observation"
found_during: "github issue triage 2026-09-06 (adversarially verified)"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/gateway/gateway.go"
resolution: "Fixed: with a key configured, the gateway's loopback exemption also requires a loopback Host; a foreign Host from loopback falls through to the bearer check. Existing loopback tests set Host; new test pins 401/200/open-mode cases; docs updated."
impact: fix
---

GitHub #30: the gateway's loopback API-key exemption checks Origin but never Host, unlike the control plane's loopbackOnly. A DNS-rebound page's same-origin GET carries no Origin, so with a key configured it reads /v1/models and /health unauthenticated. Bounded disclosure (model ids, ready count). Fix: exempt loopback only when no key is set or Host is loopback; otherwise fall through to the bearer check so same-machine proxies keep working with the key.

## Grounds

- pursued: a rebound page's same-origin GET is now refused without the key while localhost callers and key-bearing local proxies keep working; a legitimate local client that sends a non-loopback Host and cannot add the key would show it wrong
