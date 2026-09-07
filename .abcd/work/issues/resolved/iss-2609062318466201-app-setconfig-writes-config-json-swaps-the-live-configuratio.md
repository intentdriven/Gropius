---
schema_version: 1
id: "iss-2609062318466201"
slug: "app-setconfig-writes-config-json-swaps-the-live-configuratio"
severity: "minor"
category: "tech-debt"
source: "agent-finding"
found_during: "2026-09-06 pinned-models security review"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/app/app.go"
resolution: "App.SetConfig now takes a saveMu for the whole of its body — validate, canonicalize, fit-check, write config.json, swap the live value, set the Hub token, tell the pool — so two overlapping settings saves can no longer interleave across those steps and leave the pool enforcing a pinned set the file, the panel and /v1/models all say does not exist. A lock of its own rather than cfgMu held wider, because cfgMu cannot be held across Pool.SetPinned: startLocked runs under the pool's p.mu and calls SamplingFor, which takes cfgMu.RLock, so p.mu -> cfgMu is an established order and cfgMu -> p.mu would invert it into a deadlock. Nothing taken under p.mu or cfgMu takes saveMu, so it adds no order. Held by TestOverlappingSavesLeaveThePoolAgreeingWithTheSettings, which was watched failing under -race on the unsynchronised a.Hub.Token write (iss-2, incidentally closed on this path) as well as on the divergence."
impact: fix
resolved_by:
  intent: "itd-2609061441241254"
  spec: "spc-2609061822370978"
---

App.SetConfig writes config.json, swaps the live configuration and calls Pool.SetPinned as three separate steps, so two overlapping settings saves can leave the pool protecting a model the stored settings say is unpinned. Unlike the lost update in iss-2609062045106963, which loses a field in a file, this divergence is in live pool state: the panel and /v1/models report the pin as gone while eviction still honours it, and it persists until the next save or a restart. Same shape as the unsynchronised a.Hub.Token write recorded in iss-2, now with a second observer. Found by the adversarial security review of the pinned-models branch (itd-2609061441241254); the fix belongs with whatever synchronises SetConfig rather than with the pinned list alone.

## Grounds

- pursued: we expect the pool's protected set, the settings file and every surface that reports a pin to agree after any number of concurrent saves, because a pin the panel says does not exist while eviction honours it is unexplainable to the operator; we are wrong if the divergence reappears, which the concurrent-save test would show by the pool and the stored settings differing
