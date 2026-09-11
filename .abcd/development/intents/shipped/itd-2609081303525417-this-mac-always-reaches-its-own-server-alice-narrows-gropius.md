---
id: itd-2609081303525417
slug: this-mac-always-reaches-its-own-server-alice-narrows-gropius
spec_id: spc-2609091240035356
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# This Mac always reaches its own server: Alice narrows Gropius to one address so the rest of the network cannot reach it, and the app on her own Mac keeps working — as does the copy she runs from a second user account for testing, and the control panel she narrowed it from. Gropius listens on loopback whatever else it listens on, so choosing who on the network may reach the server never costs Alice the ability to reach it herself, and the panel's list of addresses stops being a list that includes one the server does not answer on.

## Press Release

Alice runs Gropius on a Mac that other people share. She narrows it so that only
one address on the network reaches the server — and then finds she cannot reach
it herself. The control panel she narrowed it from is served on this Mac's own
loopback address, and the narrowing took that away; the second user account she
keeps for testing cannot reach it either; and the panel goes on listing loopback
as somewhere to point a client, which it is not.

Gropius now listens on loopback whatever else it listens on. Choosing who on the
network may reach the server never costs Alice the ability to reach it from the
Mac it runs on. The panel stays reachable, the second account keeps working, and
the address the panel lists for this machine is one the server actually answers
on.

Nothing widens. Loopback is this Mac and only this Mac: no other machine gains
anything, on any network, under any bind.

## Why This Matters

Narrowing the bind is the one honest way to reduce who can reach the server —
adr-2609081118587999 rule 3 says so, because a bind is enforcement Gropius owns
end to end rather than an inference about something else. But the narrowing is
currently a trap: iss-7 records that binding a specific address leaves the
control panel unreachable from anywhere, so the setting that most deserves to be
used is the one that locks the operator out of their own app. Nobody uses it,
and the recorded advice is to hand-edit a configuration file.

It is also what makes a claim elsewhere true. itd-2609081015545349 requires the
panel to list only addresses the server answers on; the panel lists loopback
under every bind, which is false today under a specific bind. Binding loopback
always makes that criterion true by construction rather than by exception.

And it is an invariant the maintainer stated once already, for the
private-network bind (iss-2609081221415955): no other machine may reach the
server, but this Mac must, including a different user account on it. Stated for
one mode, it belongs to the bind generally.

## Mechanism

We expect that the reason nobody narrows the bind is that narrowing costs them
their own access, not that they do not want it — so removing that cost is what
makes the safer setting usable. This is wrong if operators still leave the bind
wide after loopback is guaranteed, which would mean the obstacle was never
access but something else: not knowing the setting exists, or not believing the
exposure matters.

## Scope Conditions

- macOS on a Mac serving one or more user accounts, where a second account <!-- cond: cond-2609091240039473 -->
  reaching the server over loopback is a case that occurs. Loopback is the
  boundary the claim rests on: this is a statement about one machine, and it
  says nothing about any network.

## Acceptance Criteria

- Given any bind Gropius accepts, When it starts serving, Then it answers on
  this Mac's loopback address as well as on whatever else that bind names.
- Given a bind to one specific non-loopback address, When Alice opens the
  control panel from the Mac itself, Then it is reachable.
- Given that same bind, When a client in a second user account on this Mac
  connects over loopback, Then it is served, and the API key exemption for
  loopback behaves exactly as it does under a wildcard bind.
- Given any bind, When the panel lists endpoints, Then every address it lists is
  one the server answers on, loopback included.
- Given a bind to one specific non-loopback address, When any machine other than
  this one connects to any address other than the bound one, Then it is refused.
  Guaranteeing loopback widens nothing.
- Given the second listener cannot be acquired at startup, When Gropius starts,
  Then it fails closed and says so rather than serving on one address while
  reporting both.

## Open Questions

All three resolved on 2026-09-09 by adr-2609091123526871, taken at interview:
two listeners, loopback acquired first as the singleton's contention point and
the challenge probe's target; a second address that cannot be acquired serves
loopback only, loudly, without exiting; iss-7 is resolved at its cause, and the
unbracketed IPv6 fault is closed by building every address with JoinHostPort.


- Whether this is two listeners or one wildcard listener with a refusal rule.
  Two listeners is the honest reading of "the bind is the narrowing"; a wildcard
  plus a filter puts the narrowing in a code path rather than in the socket,
  which is weaker in exactly the way adr-2609081118587999 rule 3 cares about.
  The spec settles it.
- What the port-ownership challenge in `cmd/gropius/singleton.go` means for a
  pair of listeners: it proves this process owns the port, and there would be
  two.
- Whether this supersedes iss-7 or merely resolves its cause, given iss-7 also
  records unbracketed IPv6 literals failing at startup, which is a separate
  fault living in the same setting.

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-adf339cc8e59 -->
Fidelity review — receipt rcp-adf339cc8e59 (verifier intent-auditor claude-opus-5[1m]).

Provenance: intent-auditor@claude-opus-5[1m] · rubric_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5 · prompt_hash sha256:86002089c90dbd48a4cfaeae04ac090203c9ce5cdf5fc029b50639b8ca4e1563
Input attestations: diff:63474a5^1..63474a5 (merge of feat/always-bind-loopback-and-private-network into integrate/issue-sweep-2026-09-09)@sha256:b071b017e85613d5cdc3b1827ea1f96b131bfb78d8596dac6ac3c312fb3d43ac; repo:worktree at branch feat/security-posture-page, HEAD contains 0edbf01@-;

Acceptance rollup: MET 3 · MET_WITH_CONCERNS 3 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: Every constructor sets Loopback unconditionally and acquireBind takes addrs[0] (loopback) before any second address; the whole-mode table and a test that actually binds every address a plan names are both present and green.
  evidence: internal/bind/bind.go:81 — "p := Plan{Loopback: LoopbackAddr, Mode: config.BindModeHost}"
  evidence: internal/bind/bind.go:103 — "p := Plan{Loopback: LoopbackAddr, Mode: config.BindModePrivateNetwork, Candidates: candidates}"
  evidence: internal/bind/bind.go:154 — "out := []string{net.JoinHostPort(p.Loopback, strconv.Itoa(port))}"
  evidence: cmd/gropius/bind.go:48 — "ln, claimed, err := acquireListener(addrs[0], time.Until(deadline), holder)"
  evidence: internal/bind/bind_test.go:19 — "func TestEveryPlanAcquiresLoopbackFirst(t *testing.T) {"
  evidence: internal/bind/bind_test.go:79 — "func TestEveryAddressAPlanNamesIsOneAListenerTakes(t *testing.T) {"
  evidence: cmd/gropius/bind_test.go:64 — "func TestTheBindAcquiresLoopbackFirstAndThenTheSecondAddress(t *testing.T) {"
- ac-2 — MET: The panel's only reachability requirement is a genuine loopback connection (loopbackOnly), and a specific-address bind now holds the loopback socket, so the two halves compose exactly; the acquisition half is measured against a real listener.
  evidence: internal/gateway/control.go:104 — "return loopbackOnly(mux)"
  evidence: internal/gateway/control_security_test.go:56 — "func TestControlAPIAllowsLoopback(t *testing.T) {"
  evidence: cmd/gropius/bind_test.go:112 — "func TestASecondAddressThisMacDoesNotHoldServesLoopbackAndSaysSo(t *testing.T) {"
  evidence: cmd/gropius/main.go:345 — "serveAll(srv, lns, func(err error) {"
  evidence: cmd/gropius/bind.go:209 — "func serveAll(srv interface{ Serve(net.Listener) error }, lns []net.Listener, onErr func(error)) {"
- ac-3 — MET_WITH_CONCERNS: The loopback socket now exists under a narrow bind and the bearer exemption is byte-for-byte untouched by the diff, so the exemption behaves identically to the wildcard case; the concern is that no test stands up a second macOS account or a second process, so the second-account half rests on socket indistinguishability plus documentation rather than on measurement.
  evidence: internal/gateway/gateway_test.go:768 — "func TestLoopbackIsExemptFromAuth(t *testing.T) {"
  evidence: internal/gateway/control.go:98 — "loopback means only this machine — including its other user accounts, which"
  evidence: docs/bind-address.md:28 — "What it does give is every account on this Mac. A request arriving over loopback"
  evidence: .abcd/development/specs/closed/spc-2609091240035356-this-mac-always-reaches-its-own-server-alice-narrows-gropius.md:131 — "Two real processes, or two accounts, are not stood up against the singleton:"
- ac-4 — MET_WITH_CONCERNS: Endpoints is derived from the acquired plan rather than the stored configuration and appends loopback unconditionally, with tests for every bind, for a narrowed bind and for a departed address; the concern is the named residual that stillHeld only drops departed IPv4 literals, so a bound name or IPv6 literal that goes away is still listed as an address the server no longer answers on.
  evidence: internal/gateway/control.go:840 — "out = appendEndpoint(out, plan.Loopback, cfg.Port, "")"
  evidence: internal/gateway/endpoints_test.go:406 — "func TestLoopbackIsListedUnderEveryBindBecauseEveryBindAnswersOnIt(t *testing.T) {"
  evidence: internal/gateway/endpoints_test.go:429 — "func TestABindThatNarrowedListsOnlyWhatItAnswersOn(t *testing.T) {"
  evidence: internal/gateway/endpoints_test.go:455 — "func TestAnAcquiredAddressThatWentAwayIsNoLongerOffered(t *testing.T) {"
  evidence: internal/gateway/control.go:861 — "if ip == nil || ip.To4() == nil { return true"
  evidence: docs/bind-address.md:64 — "offering it — for an IPv4 address, which is what Gropius enumerates. A bind"
- ac-5 — MET_WITH_CONCERNS: The narrowing is in the socket — a plan names at most loopback plus one address, a failed second listener is proved not to have taken the wildcard, and a dial to an address outside the bind is required to be refused with the wildcard case beside it as a control; the concern is that no second machine is involved, the refusal is dialled from this Mac, and the test skips where the Mac holds no non-loopback IPv4 address.
  evidence: internal/bind/bind.go:153 — "func (p Plan) Addrs(port int) []string {"
  evidence: cmd/gropius/bind_test.go:132 — "func TestAFailedSecondListenerNeverWidensTheBind(t *testing.T) {"
  evidence: cmd/gropius/bind_test.go:236 — "func TestAnAddressOutsideTheBindRefusesTheConnection(t *testing.T) {"
  evidence: cmd/gropius/bind_test.go:237 — "t.Skip("this Mac holds no non-loopback IPv4 address, so there is nothing outside the bind to dial")"
  evidence: internal/bind/bind.go:120 — "func (p Plan) WithoutExtra(reason string) Plan {"
- ac-6 — MET: A second listener that cannot be taken narrows the plan through WithoutExtra with the address and reason recorded, the startup log says so at error level, the panel renders the same refusal, and the endpoint list is rebuilt from the narrowed plan — so nothing serves one address while reporting two; the key-lockdown path closes the second socket rather than merely unreporting it.
  evidence: cmd/gropius/bind.go:85 — "return []net.Listener{ln}, plan.WithoutExtra(fmt.Sprintf( "could not listen on %s (%v) — serving this Mac and nothing else", addrs[1], err)), true, nil"
  evidence: cmd/gropius/main.go:183 — "log.Error("the bind narrowed to this Mac: "+plan.Refusal, "port", cfg.Port)"
  evidence: internal/gateway/control.go:768 — "st := BindState{Mode: cfg.BindMode, Refusal: plan.Refusal, Candidates: private.Candidates()}"
  evidence: internal/ui/bindmode_test.go:246 — "func TestThePaneSaysWhenTheBindNarrowed(t *testing.T) {"
  evidence: cmd/gropius/bind.go:172 — "func closeExtra(lns []net.Listener) []net.Listener {"
  evidence: cmd/gropius/bind_test.go:112 — "func TestASecondAddressThisMacDoesNotHoldServesLoopbackAndSaysSo(t *testing.T) {"

Gap audit:
- honoured:
  - Gropius now listens on loopback whatever else it listens on
    evidence: internal/bind/bind.go:30 — "const LoopbackAddr = "127.0.0.1""
    evidence: internal/bind/bind_test.go:19 — "func TestEveryPlanAcquiresLoopbackFirst(t *testing.T) {"
  - The panel stays reachable: narrowing the bind no longer takes away the control panel it was narrowed from
    evidence: internal/gateway/control.go:104 — "return loopbackOnly(mux)"
    evidence: cmd/gropius/bind.go:29 — "loopback, the returned plan says which address was dropped and why, and"
  - Nothing widens: loopback is this Mac and only this Mac, under any bind
    evidence: cmd/gropius/bind_test.go:132 — "func TestAFailedSecondListenerNeverWidensTheBind(t *testing.T) {"
    evidence: internal/bind/bind.go:136 — "func (p Plan) ReachesOtherMachines() bool {"
  - The address the panel lists for this machine is one the server actually answers on
    evidence: internal/gateway/control.go:797 — "func Endpoints(cfg config.Config, plan bind.Plan) []Endpoint {"
    evidence: internal/gateway/endpoints_test.go:406 — "func TestLoopbackIsListedUnderEveryBindBecauseEveryBindAnswersOnIt(t *testing.T) {"
  - iss-7's second fault is closed: every listen address is built with net.JoinHostPort, so an IPv6 literal no longer kills startup
    evidence: internal/bind/bind.go:156 — "out = append(out, net.JoinHostPort(p.Extra, strconv.Itoa(port)))"
    evidence: internal/config/host_test.go:1 — "package config"
  - The narrowing is announced on both surfaces from one source, so it cannot happen on one and not the other
    evidence: cmd/gropius/main.go:183 — "log.Error("the bind narrowed to this Mac: "+plan.Refusal, "port", cfg.Port)"
    evidence: internal/gateway/control.go:768 — "st := BindState{Mode: cfg.BindMode, Refusal: plan.Refusal, Candidates: private.Candidates()}"
  - The bind change is legible to the operator in their own terms, including the cost it carries
    evidence: docs/bind-address.md:18 — "## This Mac is always in the bind"
- diverged:
  - "the panel's list of addresses stops being a list that includes one the server does not answer on" — delivered for IPv4 literals only; a bound name or IPv6 literal that departs after launch stays listed
    evidence: internal/gateway/control.go:861 — "if ip == nil || ip.To4() == nil { return true"
    evidence: .abcd/development/specs/closed/spc-2609091240035356-this-mac-always-reaches-its-own-server-alice-narrows-gropius.md:133 — "`stillHeld` drops a departed address from the endpoint list for IPv4 literals"
  - "choosing who on the network may reach the server never costs Alice the ability to reach it herself" — one accepted residual still costs her exactly that: a foreign process holding only the second address releases loopback and exits with no panel
    evidence: cmd/gropius/bind.go:89 — "ln.Close()"
    evidence: cmd/gropius/bind.go:99 — ""port %s is busy but no Gropius server is responding on it", addrs[1])"
    evidence: .abcd/development/specs/closed/spc-2609091240035356-this-mac-always-reaches-its-own-server-alice-narrows-gropius.md:137 — "A foreign process holding *only* the second address exits the app with no"
  - "no other machine gains anything" holds at the network boundary, but the delivery widens what every other account on this Mac gains: keyless /v1 and the control plane under the two narrow binds, which previously admitted nobody on loopback
    evidence: docs/bind-address.md:28 — "What it does give is every account on this Mac. A request arriving over loopback"
    evidence: .abcd/development/decisions/adrs/2609091123526871-gropius-binds-loopback-alongside-every-other-address-with-a-p.md:1 — "A second cost, and it is the one that widens rather than tightens."
- missing:
  - "the second user account she keeps for testing" — no test or measurement stands up a second macOS account, or two real processes, against the loopback bind or the singleton; the criterion is carried by argument and documentation
    evidence: .abcd/development/specs/closed/spc-2609091240035356-this-mac-always-reaches-its-own-server-alice-narrows-gropius.md:131 — "Two real processes, or two accounts, are not stood up against the singleton:"
    evidence: internal/gateway/gateway_test.go:768 — "func TestLoopbackIsExemptFromAuth(t *testing.T) {"
  - A report that an acquired address has gone away — Gropius does not notice, warn, or say the address is unreachable; the endpoint list going quiet is the only signal, and the posture page that would carry it is not built
    evidence: internal/gateway/control.go:846 — "Nothing re-binds, so a listener outlives the address it was taken on: the"
    evidence: .abcd/development/decisions/adrs/2609091123526871-gropius-binds-loopback-alongside-every-other-address-with-a-p.md:1 — "An address that disappears after launch is still bound and no longer served,"

Scope-condition dispositions:
- cond-2609091240039473 — survived: The delivery makes loopback the boundary the condition rests on — it is in every plan, it is the only address a second account reaches the server on, and nothing outside the one bound address is opened — with the multi-account consequence stated outright rather than left implicit; the second-account case itself is argued from socket indistinguishability, not stood up.
  evidence: internal/bind/bind.go:28 — "LoopbackAddr is the address every plan acquires first: this Mac, and no // other machine, on any network, under any bind."
  evidence: internal/gateway/control.go:98 — "loopback means only this machine — including its other user accounts, which"
  evidence: docs/bind-address.md:28 — "What it does give is every account on this Mac. A request arriving over loopback"
## Grounds

- pursued: we expect operators to narrow the bind once narrowing stops locking them out of their own panel and second account; shown wrong if binds stay wide after loopback is guaranteed, which would mean the obstacle was never access.
