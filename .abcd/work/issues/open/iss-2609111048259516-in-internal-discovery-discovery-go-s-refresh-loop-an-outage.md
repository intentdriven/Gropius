---
schema_version: 1
id: "iss-2609111048259516"
slug: "in-internal-discovery-discovery-go-s-refresh-loop-an-outage"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "review of the discovery recovery fix, 2026-09-11"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/discovery/discovery.go"
---

In internal/discovery/discovery.go's refresh loop, an outage during which the TXT record also changes calls ad.withdraw() on a responder that has already died; withdraw marks the advertisement spent, which forces the default arm to build a fresh dnssd.NewResponder while the predecessor's socket stays open, because dnssd's respond closes its conn only on the cancellation path. Each such tick strands a socket pair. Found in review of the recovery-report fix; the new twenty-serves test churns the TXT record every tick and so drives this path repeatedly, which reads as coverage of the reuse arm when it is the opposite.
