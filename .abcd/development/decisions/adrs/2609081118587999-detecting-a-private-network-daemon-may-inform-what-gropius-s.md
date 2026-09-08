---
id: adr-2609081118587999
slug: detecting-a-private-network-daemon-may-inform-what-gropius-s
status: accepted
date: 2026-09-08
supersedes: null
superseded_by: null
related_intents: [itd-2609081015545349]
related_rfcs: []
related_adrs: [adr-2609061503319212]
---

# ADR-2609081118587999: Detecting a private-network daemon may inform what Gropius says, never what it enforces

## Context

Gropius serves an OpenAI-compatible API over plain HTTP with no TLS anywhere.
Everything that decides who may reach it is derived from two pieces of local
state: the bind address (`config.Config.Host`, default `0.0.0.0`, with
`Config.ExposedToLAN` treating anything non-loopback as exposed) and a shared
bearer token (`Gateway.withAuth`). The token is optional only on a loopback
install: since iss-1, an exposed bind with no key generates and persists one
before the gateway serves anything, and drops to a loopback bind if it cannot
(`cmd/gropius/main.go`). The control plane is separately locked to loopback by
both remote address and `Host` header.

Operators commonly run a mesh VPN — Tailscale is the case in front of us —
across the machines they serve models to. That already changes what Gropius
reports without anyone having decided it should: `lanIPs` walks every interface,
so the tailnet address is listed in the control panel's endpoints beside the
Wi-Fi addresses, with nothing distinguishing an encrypted path from a cleartext
one. Two proposals follow from that observation, and they pull in opposite
directions:

- **Marking.** Detect the address on the private network and say so
  (itd-2609081015545349). The intent was first drafted as "label it private and
  encrypted, under the stable name the tailnet resolves"; both halves were cut
  before planning — the wording by rule 1 below, and the name because obtaining
  it means reading another vendor's daemon over an interface that differs
  across three ways of installing it.
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

1. **Presentation may branch on it, and may state only what was observed.**
   Labelling an endpoint, ordering the endpoint list and the wording of advice
   may all say which network an address belongs to. They may not say what that
   network is worth. "On a private network" is an observation and is allowed;
   "private and encrypted", "only your devices can reach this", or any wording
   an operator would read as a statement about their exposure is not — those
   are the same claim rule 4 refuses, moved one surface over.

   The test is whether the words survive the transitions Gropius cannot see. A
   mesh VPN can publish that exact address to the public internet, and a
   sharing rule can hand it to machines the operator does not own; both leave
   the interface and the address untouched. A label saying where the address
   lives is still true afterwards. A label saying it is private is not.
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
   does not start half-protected. A future "private network only" bind mode is
   admissible under this rule precisely because it is a bind, not an inference.

   This escape hatch does not work today, and the rule names its prerequisite
   rather than assuming it: **iss-7** records that a specific-address bind
   leaves the control panel unreachable from anywhere, that the settings UI
   offers only the wildcard and loopback so such a bind needs a hand-edited
   configuration file, and that unbracketed IPv6 literals fail at startup.
   iss-7 is deferred because every fix is a design decision touching a declared
   trust boundary. Until it is resolved, rule 3 states the intended shape of
   the narrowing, not a route an operator can currently take.
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
  address believing it is the encrypted one. Marking is the fix; the docs
  follow it rather than replacing it.

## Consequences

- itd-2609081015545349 is bounded by rule 1 as amended. It marks which network
  an address belongs to, names no vendor, and claims nothing about encryption
  or reachability; it carries a criterion requiring that authentication,
  admission and every other enforcement path behave exactly as on a build with
  no detection at all. It also carries the dead-address fix, because a mark on
  an address the server does not answer on is rule 1's failure mode in the
  other direction: an observation that is not even true of the machine.
- A "private network only" bind intent is admissible under rule 3 and must be
  built as a bind, not as a detection. Whatever it does when the interface is
  absent at launch must fail closed, and it inherits iss-7 as a prerequisite.
- Any future integration with another environment-detection signal — a corporate
  VPN, a firewall's state, a network's SSID — inherits this rule. The rule is
  about inferences over state Gropius does not own, not about Tailscale.
- The rule is not mechanically enforced, and cannot be. `internal/archtest`
  holds rules that fail when the enforcement path names the classifier, the
  field its answer travels on, or the type that carries it; those catch a
  maintainer coupling enforcement to the classifier by accident, which is worth
  having and is all they are. They are not a barrier against code that means to
  read the classification, because the classification is not secret information:
  anything linked into this process can call `net.Interfaces()` and re-derive it
  in three lines without touching `internal/netshape` at all. No scan over
  identifiers can prevent that. This record and review of the trust-boundary
  packages are the guard; the tests are the accidents review need not catch.
  `internal/archtest/enforcement_detection_test.go` lists in full what they do
  not close.

## Amendment (2026-09-08): the classifier may choose a bind address, under two conditions

Rule 2 above closes every enforcement decision to the detection. That was
written against the case where the detection *relaxes* something — waive the
key, admit a peer, soften a warning — and it is right for every one of those.

It also closed a case nobody had in view when it was written: an operator who
asks to serve **only** over their private network. That mode has to resolve an
address from the classification, and resolving it is choosing what to bind,
which is enforcement. So the rule as written forbids the one feature that uses
the detection to narrow rather than to widen.

The maintainer amended it rather than route around it. The route around was
available and was declined with the reasoning recorded: the operator could pick
the address herself from the panel's marked list, which keeps rule 2 whole,
costs one choice, and costs a second choice every time the private network hands
out a different address.

**The carve-out.** When the operator has explicitly chosen the
private-network-only mode, the classifier may resolve the address that mode
binds. Nothing else changes: the mode is still a bind, still the narrowing rule
3 blesses, and every other enforcement decision named in rule 2 stays closed to
the detection.

**Condition 1 — ambiguity is refused, never resolved.** If more than one address
matches the private-network shape, Gropius does not choose between them. It
refuses to start the mode and says why. The classifier cannot tell one product
on that address range from another — its own comment concedes that other VPN
clients take `utun` and some hand out addresses from the same range — so an
arbitrary pick between a mesh VPN and a corporate VPN would bind the operator to
their employer's network while they believed they had narrowed to their own.
That is a fail-open reached through the door this ADR calls fail-closed, and
refusing is what keeps the door shut.

**Condition 2 — the choice is always shown.** The mode names the address it
selected, in Settings and on the posture page, so a wrong selection is visible
rather than inferred. This costs nothing under rule 1: saying which network an
address sits on is an observation, and it is the observation the operator needs
in order to notice that the classifier picked the wrong network.

Together the two turn the accepted risk from "it may pick the wrong network and
nothing says so" into "it picks only when there is one answer, and it always
shows which."

**What this costs, stated plainly.** A rule that was absolute is now a rule with
an exception, and an exception is a thing future work will reason from. The
boundary is deliberately narrow — one mode, chosen by a person, resolving one
address, under two conditions — and anything wider is a new decision, not an
extension of this one. `internal/archtest/enforcement_detection_test.go`
currently refuses `cmd/gropius` from naming the detection at all, on the ground
that it "decides whether an exposed bind may run at all"; that test must state
this carve-out explicitly rather than quietly widen, because a guard that grows
a silent exception is the failure this repository spent a day correcting
elsewhere.
