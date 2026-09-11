---
id: itd-2609081718469419
slug: serve-only-over-the-private-network-a-bind-mode-that-narrows
spec_id: spc-2609081750378874
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609081303525417]
severity: minor
impact: additive
promoted_from: iss-2609081221415955
origin: extracted-from-record
production_mode: hand-written
---
# Serve only over the private network: Alice chooses one setting, and Gropius stops answering on the local network — while her own Mac, including a second account on it, still reaches the server

## Press Release

Alice runs models on a Mac other people share, and reaches them from her laptop
over a mesh VPN. She would like the server reachable that way and not from the
office Wi-Fi. Today her only options are everyone on the network, or nobody but
this Mac.

Gropius offers a third. With it chosen, the gateway answers on the private
network and on this Mac, and stops answering on the local one. Her laptop
reaches it. The machine at the next desk does not. And she keeps what the narrow
option has always cost her: the control panel still opens, and the second
account she keeps for testing still works, because this Mac is always part of
the bind.

Gropius names the address it chose, in Settings and on the posture page. If more
than one address looks like a private network — a mesh VPN and a corporate VPN
can look identical to it — it does not guess. It refuses to start the mode and
says why.

## Why This Matters

Narrowing the bind is the one honest way to reduce who can reach the server.
adr-2609081118587999 rule 3 says so and says why: a bind is enforcement Gropius
owns end to end and fails closed, where an inference about another process's
state fails open the moment that state changes without Gropius being told.

The narrowing that already exists is unusable. iss-7 records that binding a
specific address leaves the loopback-only control panel unreachable, so the
option that most deserves to be used locks the operator out of their own app.
itd-2609081303525417 removes that cost by guaranteeing this Mac is always in the
bind. This intent is what makes the narrowing worth choosing once it is
possible: an operator who has a private network should not have to know its
address, watch it change, and re-enter it.

The gateway serves plain HTTP with no TLS. On the local network the prompts, the
completions and the API key cross the wire in the clear; over a mesh VPN the
same traffic is inside an encrypted tunnel. This is the difference between that
being a choice and being a theoretical one.

## Mechanism

We expect that an operator who has a private network will narrow to it if the
narrowing does not require them to track an address that changes — that the
obstacle to the existing specific-address bind is not the exposure judgement but
the bookkeeping. This is wrong if operators who have this mode available choose
a wildcard bind anyway once itd-2609081303525417 has made the specific-address
bind usable, which would mean the address bookkeeping was never what stopped
them and this mode buys nothing the plain bind does not.

## Scope Conditions

- A Mac running a mesh VPN installed the ordinary way, where the private address <!-- cond: cond-2609081750377395 -->
  has the shape those products normally give it, and where exactly one address
  has that shape. Two matching addresses is not a degraded case of this
  condition — it is outside it, and the mode refuses rather than degrades.
- macOS serving one or more user accounts, where a second account reaching the <!-- cond: cond-2609081750373398 -->
  server over loopback is a case that occurs and must keep working.
- IPv4. `internal/netshape` enumerates IPv4 only, so a private network reached <!-- cond: cond-2609081750373739 -->
  over IPv6 has no candidate address and this mode cannot select it. Stated so a
  later IPv6 mesh is a visible re-decision rather than a silent miss.

## Acceptance Criteria

- Given the mode is chosen and exactly one address has the private-network
  shape, When the gateway starts, Then the set of addresses it answers on is
  exactly that address and this Mac's loopback address.
- Given that state, When a connection arrives on any other address this Mac
  holds, Then it is refused.
- Given the mode is chosen and more than one address has the private-network
  shape, When the gateway starts, Then it refuses to start the mode, names the
  candidates, and serves this Mac only — it never picks one.
- Given the mode is chosen and no address has the private-network shape, When
  the gateway starts, Then it serves this Mac only and never a wider set.
- Given the mode is running, When Alice opens Settings or the posture page, Then
  the address the mode selected is named there.
- Given the mode is chosen, When a second instance of Gropius starts in any
  account on this Mac, Then exactly one of them serves — the singleton holds
  across differing bind addresses.
- Given the mode is running, When the private network's address changes or
  disappears, Then the set of addresses served never widens, and the panel does
  not go on offering an address the server no longer answers on.
- Given the mode is chosen, When Gropius advertises over Bonjour, Then it does
  not advertise to networks the bind excludes — or, if that is not achievable,
  the mode states plainly that discovery is not narrowed.

## Open Questions

- Bonjour. `ExposedToLAN()` is true for a private-network address, so the advert
  goes out on every interface: hostname, port, model count and whether a key is
  required reach the LAN this mode exists to exclude. Either advertising is
  scoped to the bound interface, or it is off in this mode, or the intent says
  the bind narrows connections and not discovery. The last is honest and the
  weakest.
- The singleton. Measured on this hardware: a process holding the wildcard and a
  process binding loopback on the same port BOTH succeed, because
  `acquireListener` has only `EADDRINUSE` to go on and that signal exists only
  while every instance binds the same address. With differing binds two
  instances both believe they won and both load models. The candidate design is
  that loopback is acquired first and is the contention point for every mode,
  with the second listener acquired only by the winner and failure to acquire it
  failing closed — which also repairs the probe, which contacts loopback only.
  This is itd-2609081303525417's design to make; this intent inherits it.
- Where the mode lives in the configuration. A sentinel value in `Host` is a
  landmine: a word like "private" passes host validation, then fails to listen,
  and the app exits with no panel and no recovery but editing the file — the
  precise failure this intent exists to remove. It wants its own field.
- Whether the Settings form can carry a third option at all today: it hard-codes
  two, reads the stored host into a `<select>` that may not offer it, and posts
  the result back, so a host the form does not know is posted as empty and
  refused. Captured separately; it blocks the Settings half of this intent.

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-c2bdc5190c49 -->
Fidelity review — receipt rcp-c2bdc5190c49 (verifier abcd:intent-auditor claude-opus-5[1m]).

Provenance: abcd:intent-auditor@claude-opus-5[1m] · rubric_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5 · prompt_hash sha256:d08a3f07c04c01317901b0fb50251dde9ee5e448c5825a0632d445cc3c05251d
Input attestations: diff:63474a5^1..63474a5 (merge of feat/always-bind-loopback-and-private-network into integrate/issue-sweep-2026-09-09)@sha256:b071b017e85613d5cdc3b1827ea1f96b131bfb78d8596dac6ac3c312fb3d43ac; repo:worktree .claude/worktrees/posture at branch feat/security-posture-page (HEAD), which contains 0edbf01@-;

Acceptance rollup: MET 6 · MET_WITH_CONCERNS 2 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: With exactly one candidate the resolver builds a plan whose addresses are the selected address and loopback and nothing else, and the acquisition takes exactly those two listeners; both are measured without a second machine
  evidence: internal/bind/private/private.go:50 — "return bind.Private(found[0], found, "")"
  evidence: internal/bind/bind.go:153 — "func (p Plan) Addrs(port int) []string"
  evidence: cmd/gropius/bind.go:48 — "ln, claimed, err := acquireListener(addrs[0], time.Until(deadline), holder)"
  evidence: internal/bind/private/private_test.go:45 — "func TestOneCandidateIsBoundBesideLoopback(t *testing.T)"
  evidence: cmd/gropius/bind_test.go:64 — "func TestTheBindAcquiresLoopbackFirstAndThenTheSecondAddress(t *testing.T)"
- ac-2 — MET: A socket-level test dials a non-loopback IPv4 this Mac holds against the narrow bind and requires the connection to be refused, with the same dial against a wildcard bind as a control so a passing refusal cannot be the dial failing for its own reasons; run at HEAD it executed rather than skipped and passed
  evidence: cmd/gropius/bind_test.go:236 — "func TestAnAddressOutsideTheBindRefusesTheConnection(t *testing.T)"
  evidence: cmd/gropius/bind_test.go:266 — "t.Fatal("an address outside the bind accepted a connection — the narrowing is not in the socket")"
  evidence: cmd/gropius/bind.go:78 — "second, err := net.Listen("tcp", addrs[1])"
- ac-3 — MET: More than one candidate yields a plan with no second address, carrying every candidate it saw and a refusal that says Gropius does not choose; the pane names the candidates rather than counting them
  evidence: internal/bind/private/private.go:52 — "return bind.Private("", found, "more than one address on this Mac is on a private network, and Gropius does not choose between them — serving this Mac and nothing else")"
  evidence: internal/bind/private/private_test.go:68 — "func TestSeveralCandidatesRefuseTheModeAndNameThem(t *testing.T)"
  evidence: internal/ui/static/app.js:734 — "if (found.length > 1) return `A private network — ${found.join(', ')} all match, so Gropius will not choose`"
  evidence: docs/bind-address.md:49 — "Gropius does not choose. It names the addresses it found, serves this Mac"
- ac-4 — MET: Zero candidates returns a loopback-only plan with the reason recorded, and the only mutation a plan admits after it is built drops the second address rather than adding one, so no path widens
  evidence: internal/bind/private/private.go:48 — "return bind.Private("", nil, "no address on this Mac is on a private network — serving this Mac and nothing else")"
  evidence: internal/bind/bind.go:120 — "func (p Plan) WithoutExtra(reason string) Plan"
  evidence: internal/bind/private/private_test.go:89 — "func TestNoCandidateServesThisMacAndNeverWider(t *testing.T)"
- ac-5 — MET_WITH_CONCERNS: At HEAD both surfaces name the selection — the Settings pane labels the choice with the address the running mode bound, and the posture page's private-network line states it — but only the Settings half is inside the audited merge
  evidence: internal/gateway/control.go:774 — "st.Selected = plan.Extra"
  evidence: internal/ui/static/app.js:730 — "if (bound) return `A private network (${bound}) — and this Mac`"
  evidence: internal/ui/bindmode_test.go:64 — "func TestThePrivateChoiceIsOfferedOnlyWhereThereIsAnAddressToChoose(t *testing.T)"
  evidence: internal/ui/static/app.js:535 — "? ` The private-network choice selected ${bind.selected}.`"
  evidence: internal/ui/posture_test.go:197 — "!strings.Contains(got, "The private-network choice selected 100.101.102.103.")"
- ac-6 — MET_WITH_CONCERNS: Every mode including this one acquires loopback first through the same acquisition path, which restores EADDRINUSE as a contention point across differing binds, and the kernel behaviour the design rests on is pinned by a test — but the property is never demonstrated with two processes or a second account, and adr-2609091123526871 rule 3 records an accepted residual where a foreign holder of only the second address ends in exit 1
  evidence: cmd/gropius/bind.go:48 — "ln, claimed, err := acquireListener(addrs[0], time.Until(deadline), holder)"
  evidence: internal/bind/bind_test.go:167 — "func TestLoopbackIsAContentionPointAndADifferingBindIsNot(t *testing.T)"
  evidence: internal/bind/bind_test.go:162 — "Two processes cannot be run from a unit test"
  evidence: cmd/gropius/bind_test.go:150 — "func TestAPeerHoldingLoopbackIsStillClientMode(t *testing.T)"
  evidence: cmd/gropius/bind_test.go:174 — "func TestAPeerHoldingOnlyTheSecondAddressReleasesLoopbackAndDefers(t *testing.T)"
- ac-7 — MET: Nothing re-binds and the only post-build change to a plan narrows it, so the served set cannot widen; the endpoint list is derived from what was acquired and drops an acquired IPv4 the Mac no longer holds, which is measured by stubbing the tunnel away
  evidence: internal/bind/bind.go:116 — "The narrowing can only ever narrow, which is why this is the only way a plan changes after it is built."
  evidence: internal/gateway/control.go:856 — "func stillHeld(addrs []netshape.Addr, bound string) bool"
  evidence: internal/gateway/endpoints_test.go:455 — "func TestAnAcquiredAddressThatWentAwayIsNoLongerOffered(t *testing.T)"
  evidence: internal/bind/bind_test.go:202 — "func TestWithoutExtraNarrowsAndRecordsWhy(t *testing.T)"
- ac-8 — MET: The advert is switched off outright under the mode — advertises() reads the configured mode and the plan and returns false — so nothing is announced to the local network the bind excludes, and the docs and the posture page both say why
  evidence: cmd/gropius/bind.go:196 — "if cfg.BindMode == config.BindModePrivateNetwork {"
  evidence: cmd/gropius/bind_test.go:312 — "func TestWhatGropiusAdvertisesItselfOn(t *testing.T)"
  evidence: cmd/gropius/main.go:353 — "if advertises(cfg, plan) {"
  evidence: docs/bind-address.md:70 — "Gropius advertises itself over Bonjour under the wildcard choice, and not under the private-network choice"
  evidence: internal/ui/static/app.js:576 — "mode: 'the announcement travels over the local network, which the private-network choice excludes'"

Gap audit:
- honoured:
  - With the choice made, the gateway answers on the private network and on this Mac, and stops answering on the local one
    evidence: internal/bind/private/private.go:50 — "return bind.Private(found[0], found, "")"
    evidence: cmd/gropius/bind_test.go:236 — "func TestAnAddressOutsideTheBindRefusesTheConnection(t *testing.T)"
  - The control panel still opens and a second account on this Mac still works, because this Mac is always part of the bind
    evidence: internal/bind/bind.go:30 — "const LoopbackAddr = "127.0.0.1""
    evidence: internal/bind/bind_test.go:19 — "func TestEveryPlanAcquiresLoopbackFirst(t *testing.T)"
  - If more than one address looks like a private network it does not guess: it refuses to start the mode and says why
    evidence: internal/bind/private/private.go:52 — "and Gropius does not choose between them — serving this Mac and nothing else"
    evidence: internal/bind/private/private_test.go:68 — "func TestSeveralCandidatesRefuseTheModeAndNameThem(t *testing.T)"
  - The mode lives in its own configuration field rather than as a sentinel in host, so a chosen mode can never be a host that validates and then fails to listen
    evidence: internal/config/config.go:502 — "BindMode string `json:"bind_mode"`"
    evidence: internal/config/host_test.go:244 — "func TestBindModeIsItsOwnFieldWithOnlyTwoValues(t *testing.T)"
    evidence: internal/ui/static/app.js:714 — "? { host: storedHost, bind_mode: PRIVATE_BIND }"
  - The classifier is read for this mode only under the amendment's carve-out, and the enforcement path still cannot see the detection
    evidence: internal/bind/private/private.go:22 — "internal/archtest/enforcement_detection_test.go names this package"
    evidence: internal/archtest/enforcement_detection_test.go:240 — "func TestTheClassifierAndTheResolverAreImportedOnlyWhereTheyMayBe(t *testing.T)"
- diverged:
  - Gropius names the address it chose in Settings and on the posture page — the posture-page half is not part of merge 63474a5; it arrives with the current branch feat/security-posture-page, so as of the audited merge only Settings named the selection
    evidence: internal/ui/static/app.js:535 — "? ` The private-network choice selected ${bind.selected}.`"
    evidence: internal/ui/posture_test.go:196 — ""bind": {"mode":"private-network","selected":"100.101.102.103","candidates":["100.101.102.103"]}"
    evidence: internal/gateway/control.go:746 — "type BindState struct"
  - Bonjour: of the intent's three admissible answers the delivery took 'off in this mode' rather than scoping the advert to the bound interface, so discovery is not narrowed to the private network — it is removed there too, and clients on it are pointed at an address by hand
    evidence: cmd/gropius/bind.go:196 — "if cfg.BindMode == config.BindModePrivateNetwork {"
    evidence: docs/bind-address.md:73 — "Clients on a private network are"
  - The press release says the gateway 'stops answering on the local one' — the mechanism delivered is a narrowing of the sockets, and the panel and posture page state that Gropius cannot see which machines actually reach a given address
    evidence: internal/ui/static/app.js:514 — "Which machines can reach an address is decided by the network it is on, and Gropius does not see that."
    evidence: docs/mesh-vpn.md:53 — "binds the mesh address and this Mac, and no other address"
- missing:
  - No test demonstrates the singleton across two processes or across two accounts on this Mac; the property is argued from the acquisition order plus a single-process kernel measurement, and the two-process design is inherited from itd-2609081303525417
    evidence: internal/bind/bind_test.go:162 — "Two processes cannot be run from a unit test"
    evidence: cmd/gropius/bind_test.go:150 — "func TestAPeerHoldingLoopbackIsStillClientMode(t *testing.T)"
  - The mechanism's own falsifier is unmeasured: nothing in the delivery records whether operators with this mode available still choose a wildcard bind, or how often the ambiguity refusal fires in the field
    evidence: .abcd/development/intents/shipped/itd-2609081718469419-serve-only-over-the-private-network-a-bind-mode-that-narrows.md:71 — "This is wrong if operators who have this mode available choose a wildcard bind anyway"

Scope-condition dispositions:
- cond-2609081750377395 — survived: The delivery treats exactly one shaped address as the served case and two as outside the condition rather than a degraded one: it refuses and names the candidates instead of picking
  evidence: internal/bind/private/private.go:49 — "case 1:"
  evidence: internal/bind/private/private.go:51 — "default:"
  evidence: internal/bind/private/private_test.go:68 — "func TestSeveralCandidatesRefuseTheModeAndNameThem(t *testing.T)"
- cond-2609081750373398 — survived: Loopback is in every plan first, under every mode, so a second account on this Mac reaching the server over loopback keeps working by construction rather than by exception
  evidence: internal/bind/bind.go:39 — "Loopback string"
  evidence: internal/bind/bind_test.go:19 — "func TestEveryPlanAcquiresLoopbackFirst(t *testing.T)"
  evidence: internal/ui/static/app.js:548 — "Every account on this Mac can open it."
- cond-2609081750373739 — survived: The enumeration the mode selects from is still IPv4-only, and the delivery states the limit where it bites rather than papering over it, so an IPv6 mesh remains a visible re-decision
  evidence: internal/netshape/netshape.go:107 — "Addrs lists this machine's non-loopback IPv4 addresses on interfaces that"
  evidence: internal/gateway/control.go:851 — "IPv4 only, because that is all the enumeration covers"
  evidence: docs/bind-address.md:64 — "for an IPv4 address, which is what Gropius enumerates"
## Grounds

- pursued: the specific-address bind that adr-2609081118587999 rule 3 already admits becomes usable the moment itd-2609081303525417 lands, and we expect it still will not be used — because it asks the operator to know an address a mesh VPN can change under them, and to notice when it does. This mode exists to remove that bookkeeping, not to remove an exposure judgement the operator has already made. Shown wrong if operators with this mode available choose a wildcard bind anyway once the plain narrowing works, which would mean the bookkeeping was never the obstacle and the mode buys nothing the plain bind does not. Shown wrong a second way if the ambiguity refusal fires often in the field: a mode that refuses more than it serves is a worse answer than asking the operator to pick once.
