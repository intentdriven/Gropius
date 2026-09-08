---
id: adr-2609081118587999
slug: detecting-a-private-network-daemon-may-inform-what-gropius-s
status: accepted
date: 2026-09-08
supersedes: null
superseded_by: null
related_intents: [itd-2609081015545349]
related_rfcs: []
related_adrs: []
---

# ADR-2609081118587999: Detecting a private-network daemon may inform what Gropius says, never what it enforces

## Context

Gropius serves an OpenAI-compatible API over plain HTTP with no TLS anywhere.
Everything that decides who may reach it is derived from two pieces of local
state: the bind address (`config.Config.Host`, default `0.0.0.0`, with
`ExposedToLAN` treating anything non-loopback as exposed) and an optional shared
bearer token (`Gateway.withAuth`). The control plane is separately locked to
loopback by both remote address and `Host` header.

Operators commonly run a mesh VPN — Tailscale is the case in front of us —
across the machines they serve models to. That already changes what Gropius
reports without anyone having decided it should: `lanIPs` walks every interface,
so the tailnet address is listed in the control panel's endpoints beside the
Wi-Fi addresses, with nothing distinguishing an encrypted path from a cleartext
one. Two proposals follow from that observation, and they pull in opposite
directions:

- **Naming.** Detect the tailnet address and label it as private and encrypted,
  under the stable name the tailnet resolves (itd-2609081015545349). The
  `.local` name Bonjour supplies for the LAN does not exist here — mDNS is
  link-local multicast and does not traverse a tailnet.
- **Relaxing.** Treat a detected tailnet as evidence the server is safely
  reached, and on the strength of it suppress the "reachable by anyone on your
  network and requires no API key" warning, waive the API key requirement that
  `validate` imposes on eviction grace, or admit the loopback-only control plane
  to tailnet peers.

The second is the one that needs a rule, because the detection is not a fact
about Gropius. It is an inference about another process's state, and that state
changes without Gropius being told: the operator logs out of the tailnet, a node
key expires, an ACL is edited, the daemon is stopped. The bind address is
unchanged through all of it; the protection the inference assumed is gone. A
control-flow branch that reads "a tailnet is present, therefore relax" fails
open on every one of those transitions, silently, on a machine already reachable
by anyone on the network. The address range is not even a reliable signal on its
own: `100.64.0.0/10` is the shared CGNAT range, handed out by ISPs as well.

`internal/gateway` and `internal/config` are trust-boundary packages.

## Decision

Detection of a third-party private-network daemon may inform what Gropius
**reports**. It may never be an input to what Gropius **enforces**.

1. **Presentation may branch on it.** Labelling an endpoint, ordering the
   endpoint list, choosing which name to display, and the wording of advice are
   all free to say "this one is a private, encrypted network".
2. **Enforcement may not.** No authentication decision, no admission decision,
   no validation rule, and no warning's firing condition may read the presence,
   absence or shape of such a daemon. In particular: the bearer-token check, the
   loopback exemption and its Origin/Host guards, `ExposedToLAN`, the eviction
   grace API-key requirement, and the control plane's loopback-only rule are all
   closed to it.
3. **The bind address is the only admissible narrowing.** An operator who wants
   the exposure genuinely reduced narrows it by binding to a specific address
   rather than the wildcard. That is enforcement Gropius owns end to end, and it
   fails closed: if the interface is not there, the bind fails and the server
   does not start half-protected. A future "tailnet only" bind mode is
   admissible under this rule precisely because it is a bind, not an inference.
4. **A warning may soften only on state Gropius owns.** Wording that follows
   from the bind address is fine; wording that follows from "a tailnet daemon
   seems to be running" is not, because the operator reads a softened warning as
   a statement about their exposure.

## Alternatives Considered

- **Waive the API key on a detected tailnet.** Convenient, and it matches what
  the operator believes their setup to be. Rejected: it makes a security
  property depend on another process's liveness, and every way that process goes
  away leaves the waiver in place on an exposed server.
- **Admit tailnet peers to the loopback-only control plane.** The control plane
  changes settings, pins models and holds the redacted API key; tailnet ACLs are
  a real access control and could in principle carry it. Rejected on the same
  fail-open ground, and because the loopback rule is currently defensible in one
  sentence — a property worth more here than remote administration.
- **Make Gropius a tailnet node itself, via `tsnet`.** Gropius would hold its
  own identity and ACLs, and the inference problem disappears because the state
  is its own. Rejected for now: a large new dependency and a whole new trust
  boundary in an app whose value is that it is a menu-bar item serving models on
  a Mac. Revisit only if remote access becomes the primary use.
- **Do nothing at all, and document it.** No code, no risk. Rejected as
  insufficient on its own: the tailnet address is *already* in the endpoint list
  today, unlabelled, which is a small but real way to hand someone a cleartext
  address believing it is the encrypted one. Naming is the fix; the docs follow
  it rather than replacing it.

## Consequences

- itd-2609081015545349 is bounded by rule 1: it is a labelling change with an
  acceptance criterion asserting that enforcement is byte-for-byte unaffected.
- A "tailnet only" bind intent is admissible under rule 3 and must be built as a
  bind, not as a detection. Whatever it does when the interface is absent at
  launch must fail closed.
- Any future integration with another environment-detection signal — a corporate
  VPN, a firewall's state, a network's SSID — inherits this rule. The rule is
  about inferences over state Gropius does not own, not about Tailscale.
- The rule is not mechanically enforced. There is no test that fails when
  someone reads a detection result inside `withAuth`; this record and review of
  the trust-boundary packages are the guard.
