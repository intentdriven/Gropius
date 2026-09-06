---
schema_version: 1
id: "iss-2609061332088894"
slug: "github-21-config-load-and-registry-open-read-via-raw-os-read"
severity: "major"
category: "security"
source: "user-observation"
found_during: "github issue triage 2026-09-06 (adversarially verified)"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/config/config.go"
resolution: "Fixed: config.Load and registry.Open read through config.ReadRegular with caps (1 MiB / 8 MiB); a non-regular or oversized file is an error (config fails closed to loopback in main; registry startup fails loudly). Tests pin FIFO, symlink, and oversize."
impact: fix
---

GitHub #21: config.Load and registry.Open read via raw os.ReadFile: a FIFO planted in the shared root wedges startup (config.Load runs before the port claim, so the fail-closed branch never executes), a symlink is followed, and there is no size cap (a symlink to /dev/zero never reaches EOF). The forged-content sub-claim is iss-10's first-writer-wins problem, not new here. Fix: hardened capped regular-file read; non-regular or oversized config fails closed to loopback.

## Grounds

- pursued: startup can no longer hang or balloon on a planted config.json/registry.json; a symlinked config.json used deliberately by a user now locks to loopback, which the docs troubleshooting explains — a user report of that being unacceptable would reopen the design
