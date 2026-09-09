---
schema_version: 1
id: "iss-12"
slug: "discovery-updatetext-races-with-the-running-mdns-respond"
severity: "minor"
category: "bug"
source: "agent-finding"
found_during: "2026-08 bug-hunt round 10"
found_at: "internal/discovery/discovery.go"
resolution: "Advertiser.refresh now withdraws the advertisement and re-registers it on a TXT change, so nothing edits a service a running dnssd responder is reading."
impact: fix
---

`Advertiser.refresh` (internal/discovery/discovery.go, around line 175) calls
`h.UpdateText(cur, r)` on a live `dnssd.ServiceHandle` every time the
advertised TXT record changes (e.g. the API key is toggled at runtime, or the
servable-model count changes) — while a second goroutine started by `Start`
is concurrently running `responder.Respond(rctx)` for the same service.

Verified against the vendored `github.com/brutella/dnssd v1.2.14` source
directly (module cache): `serviceHandle.UpdateText`
(`serviceHandle.go:21-22`) sets `h.service.Text = text` with no locking at
all, while `responder.respond()` (`responder.go:208-230`) processes every
inbound mDNS query under `r.mutex`, and `handleQuery` → `handleQuestion`
reads that same `*Service`'s `Text` field (`responder.go:320-391`) — the same
pointer `UpdateText` mutates, not a copy. So the write and the read are
genuinely unsynchronized: a textbook Go data race, reachable whenever the
TXT content changes while a peer machine is actively browsing for the
service.

This is not a Gropius misuse — the library's own README documents exactly
this call pattern (`hdl.UpdateText(..., rsp)` against a running responder)
and offers no separate concurrency-safe update API. The mutex the read holds
(`r.mutex`) is an unexported field inside the library, unreachable from
Gropius's own code, so no local lock in `Advertiser` can close the gap by
itself.

Distinct from the round 7/8-refuted `discovery.Stop`-goroutine-join claim
(a different code path with no reachable shared-state race); this is a
different mechanism entirely, previously uncaptured.

Deferred rather than auto-patched because the only in-repo fix is
structural, not a one-line change: have `refresh` do a full `Stop()` (cancel
`rctx`, wait for `Respond` to actually exit) followed by a fresh
registration and `Respond` goroutine, instead of mutating the live handle in
place. That trades the in-place TXT reannounce (today, only on an actual
text change, roughly every 15s at most) for a full withdraw
("goodbye" packet) plus re-probe cycle on every change — more disruptive to
peers' mDNS caches and to any in-flight browse on the network, which is a
real tradeoff for the maintainer to weigh, not a size a bug fix should make
unilaterally. No new dependency and no vendored-library edit are required
either way.

## Grounds

- pursued: a TXT change publishes as goodbye then a fresh registration, serialised in one goroutine, not resurrected by a refresh racing Stop, and retried on the next tick if it fails; what would show it wrong is peers seeing the service blink during a browse often enough to lose or fail to resolve the entry around a settings change.
