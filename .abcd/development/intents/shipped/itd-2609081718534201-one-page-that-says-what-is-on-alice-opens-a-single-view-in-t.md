---
id: itd-2609081718534201
slug: one-page-that-says-what-is-on-alice-opens-a-single-view-in-t
spec_id: spc-2609081750377336
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
promoted_from: iss-2609081221484689
origin: researcher-authored
production_mode: hand-written
---

# One page that says what is on: Alice opens a single view and sees who can reach this server, what it requires, and what it records — stated as what it is, not as warnings she has to read past

## Press Release

Alice wants to know where she stands. Not while she is picking a model, not in a
banner above the thing she came to do — she wants one place she can open and
read, and then close.

Gropius has that page. It says who can reach this server right now and by which
addresses; whether a request from the network has to carry a key, and — a
different answer — whether a request from this Mac has to, including one from a
second account on it; whether the server is announcing its own existence to
every machine on the local network, and what that announcement carries; what the
request log on stderr writes down about each call and what it deliberately
leaves out; and what else is being recorded, where, and for how long. Each line
says what is, in the present tense. Nothing on it is phrased as a warning,
because a warning is a thing you dismiss and this is a thing you consult.

It says only what Gropius can actually observe. Where something is outside what
it can see — whether the private network it marked has since been shared, or
published to the internet by the VPN itself — the page says that plainly rather
than implying an assurance it cannot give. A page that tells Alice the limits of
its own knowledge is more use than one that sounds confident.

Nothing on this page interrupts her while she works. The page is somewhere she
goes; the one warning Gropius already raises when a server is exposed with no
key stays exactly where it is.

## Why This Matters

The facts an operator most needs are the ones nobody reads. Gropius already
knows them — the bind, the key, the mark on an endpoint, the statistics store's
retention — and today they are scattered across a settings pane, a warning
banner, a mark beside an address, and two documentation pages, each speaking
only when something is already wrong.

Four hazards are the case in point, and they come from three records rather than
one. iss-2609081221484689 records two: that publishing the address to the public
internet through the VPN's own tunnelling feature defeats the point of the mark,
and that the mark says which network an address is on and claims nothing about
who can reach it. adr-2609081118587999 rule 1 is where the second of those has
its sharper form — a sharing rule can hand that address to machines the operator
does not own, and neither that nor public tunnelling touches the interface or
the address — and the ADR's Context is where the fourth is on the record:
Gropius serves an OpenAI-compatible API over plain HTTP with no TLS anywhere, so
whatever protection exists is the VPN's and stops where the VPN stops.
itd-2609081015545349 is where the third is reasoned out: a label saying
"encrypted" survives being shared, published and logged out from, and then lies,
which is why the mark says only which network the address belongs to. The
2026-09-08 interview on iss-2609081221484689 adopted all four for this page. It
did not originate them, and each line the page writes answers to the record that
did.

Those four were routed to a documentation page full of warnings.
iss-2609081221484689's interview outcome records the steer that followed: keep
it user-friendly, do not over-explain or scare, do not interrupt the flow — and
it records the proposal of a security audit page, one place showing what is on
and what is not, rather than four warnings distributed through prose. The same
four facts, stated as state on a page somebody chooses to open, cost the reader
nothing until they want them.

The banner stays, and that is settled here rather than carried as an open
question. The warning for an exposed server with no key
(`internal/gateway/control.go`) fires on a state the operator created seconds
earlier in the pane next door; this page is somewhere they may not visit for
weeks. The timing is the whole difference between them, and it is why they are
not substitutes. It is also what keeps this intent's own Mechanism falsifiable:
"wrong if the page goes unopened" can only be read if the warning is still there
to be the evidence. And the banner is not the last line of defence it has been
described as — since iss-1, an exposed bind with no key generates and persists a
key before the gateway serves anything, and drops to a loopback bind if it
cannot (`cmd/gropius/main.go`); what the banner still covers is the runtime path
that start-up check does not, an operator clearing the key from Settings on an
already-exposed server. Removing it is not an `impact: additive` change and is
not this record's to make: adr-2609081118587999 rule 4 lets a warning soften
only on state Gropius owns.

What the page says about recording is not free prose either.
adr-2609061503319212 is the standing answer that nothing about usage, hardware,
errors, models or configuration leaves this Mac to the project, to a vendor or
to any third party, and that local statistics are a strict opt-in that never
records prompt text, completions or API keys. adr-2609061610107154 fixes the
store's shape: append-only JSON Lines, per account under the per-user data root,
bounded by a months figure and a size cap with the size cap the hard bound, and
the date of the oldest record shown so the operator knows how far back it
reaches. The page reports those as they are rather than restating them in words
of its own that could drift from the store.

It also closes a gap the marking work opened. itd-2609081015545349 deliberately
makes the endpoint mark say the minimum an app can honestly say, which is
correct and leaves the operator with a question the app never answers. This is
where it gets answered.

## Mechanism

We expect that an operator will consult a page that tells them where they stand,
and will not read a warning placed in front of something they are trying to do —
so stating the facts as state, in one place, informs more people than warning
them does. This is wrong if the page goes unopened: if the operators who most
need it are exactly the ones who never look, then the information had to be in
their way after all, and the warnings this page replaces were doing work. The
warnings it replaces are the four routed to documentation, not the banner: the
banner stays, which is what leaves anything in an unopened operator's way at
all, and so is also what the falsification is read against.

## Scope Conditions

- An operator who has chosen to run a server and wants to know its posture. Not <!-- cond: cond-2609081750374720 -->
  a first-run tutorial, and not a substitute for refusing a dangerous
  configuration — anything Gropius should prevent it must still prevent, and
  this page never becomes the place a hazard is disclosed instead of stopped.
- Facts Gropius can observe on this Mac. State held by another product, another <!-- cond: cond-2609081750370330 -->
  machine, or a remote service is out of scope except as a stated limit. So is a
  static fact about the source dressed as an observation: Gropius can name the
  hosts its own code contacts, but that is true of the binary rather than of
  this running server, and the per-model log files it opens carry another
  process's stdout, which it can locate and cannot characterise. That is why the
  page reports the two things Gropius itself emits and can therefore account for
  — the broadcast it sends to every machine on the local network, which does
  leave this Mac, and the request log it writes to stderr, which does not —
  instead of a summary of everything that leaves the Mac, which it is in no
  position to write.

## Acceptance Criteria

- Given Alice opens the page, When it renders, Then it states who can reach the
  server and by which addresses; whether a request arriving from the network has
  to carry a key and whether one is set; whether a request arriving from this
  Mac has to, including one from another account on it; whether the server is
  broadcasting itself on the local network and what that broadcast carries; what
  the request log records about a call and what it leaves out; and what else is
  recorded, where, and for how long — each in the present tense, as a fact
  rather than as a warning or a recommendation.
- Given a key is set, When Alice reads the two key lines, Then they are two
  lines and not one, because they have different answers: `withAuth`
  (`internal/gateway/gateway.go`) admits a loopback connection with a loopback
  `Host` without the key, so a second account on this Mac reaches the API
  unauthenticated while every machine on the network is refused. A single
  sentence saying "a key is required" is false of half of that.
- Given any line on the page, When Alice reads it, Then it says something
  Gropius observed, and where the honest answer is bounded the line says what
  Gropius cannot see rather than implying it is fine.
- Given the server is on a private network, When Alice reads the relevant line,
  Then it states that public tunnelling and sharing rules can change who reaches
  that address and that Gropius cannot observe either — without naming a vendor
  and without claiming the connection is encrypted.
- Given Alice is doing anything else in the app, When she does it, Then this
  page adds no interruption: it introduces no banner, no modal and no step in
  any task. The warning that already fires for an exposed server with no key is
  untouched — this page neither replaces it nor changes when it fires.
- Given a configuration Gropius refuses today, When Alice reaches it, Then it is
  still refused — the page reports posture and gates nothing.
- Given any line on this page, When Gropius decides who may reach the server,
  what a request may do, or whether a warning fires, Then that line is not an
  input to the decision: presentation may branch on what was observed and
  enforcement may not (adr-2609081118587999 rules 2 and 4). And the existence of
  this page is never grounds for softening an enforcement rule. "The page shows
  the control panel is loopback-only, so let me reach it over the private
  network" is a relaxation that ADR rejects by name, and documenting the posture
  does not make it admissible — it is the same inference over state Gropius does
  not own, arriving by a longer route.
- Given the page's own strings, When the honesty scan in `internal/archtest`
  runs over them, Then it reports nothing — which is what that scan actually
  holds and no more: no wording from its closed list of exposure claims in a
  passage about the private-network mark, and no vendor name on a line without a
  written reason beside it. The scan's own header says a clean run does not mean
  the prose claims nothing, so the criterion the prose is really held to is the
  observation criterion above, read by a person.

## Open Questions

- Where it lives: a tab in the control panel beside the others, or a section
  within an existing one. A tab is findable and adds a tab; a section is cheaper
  and easier to miss.
- Whether it reports posture only, or also offers the one-click fix for what it
  reports. Offering fixes makes it useful and makes it a settings page, which is
  the thing it was defined against.
- What it says about the statistics store when statistics are off: nothing, or
  "off". Saying "off" is a fact; saying nothing is a smaller page.
- How much of iss-2609081221484689 remains as documentation once this exists.
  The how-to for serving over a mesh VPN is not made redundant by a status page;
  the four warnings are.

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-7c7801af51c9 -->
Fidelity review — receipt rcp-7c7801af51c9 (verifier intent-auditor claude-opus-5[1m]).

Provenance: intent-auditor@claude-opus-5[1m] · rubric_hash sha256:2cdbbce50fc13dca3d08dcc78749119691ee02d9e587e33f854c26b7bc8abfee · prompt_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5
Input attestations: diff:1d94018^1..1d94018 (PR #36, feat/security-posture-page)@sha256:f53e79937bf4a4f3f5a5abc4e20ef778afc5b81b9fdf3cb7758c48e3f728a6e5; diff:9a5a6a4^1..9a5a6a4 (PR #37; carries only a captured test-flake note, no posture code)@-; tree:HEAD dd6463de7bddcceb1b303876efa8d4a0139f32ec (worktree .claude/worktrees/posture)@-;

Acceptance rollup: MET 7 · MET_WITH_CONCERNS 1 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: postureLines renders eight lines on a plain bind and nine with a private-network address, covering every clause of the criterion in the present tense: reach and addresses, key-from-network with whether one is set, key-from-this-Mac including another account, the Bonjour advert and its payload, the request log's contents and omissions, and the statistics store's contents, location and retention; TestThePostureLinesStateWhatIsOn pins each one by id and by wording, and style.css gives every line the same weight with no colour and no icon so none reads as a warning.
  evidence: internal/ui/static/app.js:535 — "lines.push({ id: 'reach', heading: 'Who can reach it', text: reach,"
  evidence: internal/ui/static/app.js:617 — "lines.push({ id: 'announce', heading: 'The local network', text: announce,"
  evidence: internal/ui/static/app.js:623 — "lines.push({ id: 'log', heading: 'The request log', reads: ['config.log_level'],"
  evidence: internal/ui/static/app.js:662 — "lines.push({ id: 'stats', heading: 'Request statistics', text: stats,"
  evidence: internal/ui/posture_test.go:147 — "func TestThePostureLinesStateWhatIsOn(t *testing.T) {"
  evidence: internal/ui/static/style.css:208 — "Posture: one fact per block, stated and not styled as a warning — no colour, no icon, the same weight for every line."
- ac-2 — MET: The page pushes two separate lines, key-network and key-local, with different answers, and the answers match what withAuth actually does: with a key configured, a loopback connection carrying a loopback Host is served without the bearer check while every other machine falls through to it, which is exactly what the two lines say.
  evidence: internal/ui/static/app.js:585 — "lines.push({ id: 'key-network', heading: 'A request from another machine', text: fromNetwork,"
  evidence: internal/ui/static/app.js:588 — "'A request from this Mac to a loopback address is served without the key, and that includes a request from another account on this Mac. The key applies to the network and not to this Mac.'"
  evidence: internal/gateway/gateway.go:176 — "if isLoopbackHost(r.Host) { next.ServeHTTP(w, r); return }"
  evidence: internal/ui/posture_test.go:179 — "func TestTheTwoKeyLinesHaveDifferentAnswers(t *testing.T) {"
- ac-3 — MET_WITH_CONCERNS: Every line that reports server state is derived from the snapshot and names the fields it read, and each bounded line states what Gropius cannot see rather than implying an assurance — but two of the eight lines, transport and panel, carry reads: [] and are static facts about the binary rather than anything this running server was observed to be, which the source itself admits and the page does not mark for the reader.
  evidence: internal/ui/static/app.js:471 — "a line with no fields is a fact about the binary rather than an observation of this server, and there are two of those."
  evidence: internal/ui/static/app.js:563 — "lines.push({ id: 'transport', heading: 'What carries a request', reads: [],"
  evidence: internal/ui/static/app.js:567 — "lines.push({ id: 'panel', heading: 'This control panel', reads: [],"
  evidence: internal/ui/static/app.js:533 — "reach += 'Which machines can reach an address is decided by the network it is on, and Gropius does not see that.';"
  evidence: internal/ui/posture_test.go:359 — "func TestEveryPostureLineTracesToTheSnapshot(t *testing.T) {"
- ac-4 — MET: The private line states that sharing with machines the operator does not own and a feature of the network publishing the port to the internet are both invisible to Gropius and change neither the address nor the mark; it names no vendor and makes no encryption claim, and the archtest claim scan over app.js confirms both, since postureLines is a top-level function mentioning the mark so every literal in it is in scope.
  evidence: internal/ui/static/app.js:552 — "Whether that network has since been shared with machines you do not own, or whether a feature of the network publishes this port to the internet, Gropius cannot see, and neither changes the address or the mark."
  evidence: internal/archtest/honest_marking_test.go:337 — "func markLiterals(src string) []string {"
  evidence: internal/ui/posture_test.go:253 — "func TestThePrivateNetworkLineStatesTheLimits(t *testing.T) {"
- ac-5 — MET: renderPosture writes into #posture and nowhere else, app.js never navigates to the view itself, and the diff adds no modal and no step in any task — only a tab button and an ordinary hidden panel section; the exposed-server-with-no-key warning in snapshot() is outside every hunk of the delivered control.go diff and still fires on the same condition it did before.
  evidence: internal/ui/static/app.js:681 — "$('posture').innerHTML = html;"
  evidence: internal/ui/static/index.html:54 — "< button class="tab" data-tab="posture">Posture< /button>"
  evidence: internal/gateway/control.go:419 — "if c.App.Bind().ReachesOtherMachines() && cfg.APIKey == "" {"
  evidence: internal/ui/posture_test.go:463 — "func TestThePostureViewIsReachedByNavigationAlone(t *testing.T) {"
- ac-6 — MET: The posture view issues no api(), fetch(), post or EventSource call, so it can change nothing; the only enforcement-path file the diff touches is cmd/gropius/bind.go, where advertises is moved verbatim into app.Advertises with an identical three-condition body, and the startup refusal path that generates a key or drops to loopback is untouched.
  evidence: internal/ui/posture_test.go:513 — "func TestThePostureViewReadsAndNeverWrites(t *testing.T) {"
  evidence: cmd/gropius/bind.go:185 — "return app.Advertises(cfg, plan)"
  evidence: internal/app/app.go:377 — "func Advertises(cfg config.Config, plan bind.Plan) bool {"
  evidence: cmd/gropius/bind.go:148 — "if !plan.ReachesOtherMachines() || cfg.APIKey != "" {"
- ac-7 — MET: The new BindState fields (InForce, Bound, Wildcard, ReachesOtherMachines, Port, Advertising) are written in snapshot() and read by no enforcement code anywhere in internal/ or cmd/ — a grep over non-test Go finds only the writes; the exposure warning still asks the plan directly rather than the published state, and the flow that was added runs the other way, with presentation reading enforcement's own Advertises rule, which rules 2 and 4 permit.
  evidence: internal/gateway/control.go:389 — "st.Bind.Port = c.App.BindPort()"
  evidence: internal/gateway/control.go:796 — "requirement, no admission, no warning's firing condition reads any of it."
  evidence: internal/app/app.go:360 — "func (a *App) Advertising() bool { return Advertises(a.startup, a.bindPlan) }"
  evidence: internal/archtest/enforcement_detection_test.go:131 — "var enforcementPath = []string{"
- ac-8 — MET: The claim scan's script scope derives from the source rather than a hand-written function list, and postureLines is a top-level function whose body names the mark, so every literal it renders is scanned; go test ./internal/archtest/ passes, and the panel-side twin test holds the same functions to the closed forbidden list.
  evidence: internal/archtest/honest_marking_test.go:344 — "if regionAboutTheMark || mentionsTheMark(lit) {"
  evidence: internal/archtest/honest_marking_test.go:224 — "func TestTheDocumentationAndThePanelClaimNothingAboutAPrivateNetwork(t *testing.T) {"
  evidence: internal/ui/posture_test.go:530 — "func TestThePostureStringsNameNoVendorAndPromiseNothing(t *testing.T) {"

Gap audit:
- honoured:
  - One page Alice opens and reads, reached by going there and not by being taken there
    evidence: internal/ui/static/index.html:193 — "< section id="tab-posture" class="panel">"
    evidence: internal/ui/posture_test.go:471 — "app.js navigates to the posture view itself — the page is somewhere the operator goes, never somewhere they are taken"
  - Two key answers, never one, because withAuth's loopback exemption makes them different
    evidence: internal/ui/static/app.js:587 — "lines.push({ id: 'key-local', heading: 'A request from this Mac', reads: ['config.api_key'],"
  - The running bind is published from the plan the sockets were acquired under, never from the stored configuration
    evidence: internal/gateway/control.go:803 — "ReachesOtherMachines: plan.ReachesOtherMachines(),"
  - The banner stays and this page neither replaces it nor changes when it fires
    evidence: internal/gateway/control.go:420 — "This server is reachable by anyone on your network and requires no API key. Set one in Settings to restrict access."
  - The page says where Gropius's view stops rather than implying an assurance
    evidence: internal/ui/static/app.js:552 — "Gropius cannot see, and neither changes the address or the mark."
  - Open question resolved: the statistics line says 'off' when statistics are off
    evidence: internal/ui/static/app.js:660 — "stats = 'Request statistics are off: no request is recorded.';"
- diverged:
  - It says only what Gropius can actually observe — delivered as six observation-derived lines plus two static facts about the binary (transport, panel) that carry no snapshot fields and are not marked as such on the page
    evidence: internal/ui/static/app.js:563 — "lines.push({ id: 'transport', heading: 'What carries a request', reads: [],"
    evidence: internal/ui/static/app.js:471 — "a line with no fields is a fact about the binary rather than an observation of this server, and there are two of those."
  - The four warnings routed to documentation are the ones this page replaces — delivered as the four staying in docs/mesh-vpn.md with a cross-link added to the new page rather than as the page becoming the single place they live
    evidence: docs/mesh-vpn.md:110 — "- [The posture page] (posture-reference.md) — the control panel's one-page"
    evidence: docs/mesh-vpn.md:112 — "with the four things above stated as the limits of what Gropius can see."
- missing: (none)

Scope-condition dispositions:
- cond-2609081750374720 — survived: The page reports and gates nothing: its three functions issue no call to the control plane, the exposed-with-no-key warning and the startup path that generates a key or narrows to loopback are both untouched, so nothing Gropius refuses today became a disclosure on this page instead.
  evidence: internal/ui/posture_test.go:513 — "func TestThePostureViewReadsAndNeverWrites(t *testing.T) {"
  evidence: internal/gateway/control.go:419 — "if c.App.Bind().ReachesOtherMachines() && cfg.APIKey == "" {"
  evidence: cmd/gropius/bind.go:148 — "if !plan.ReachesOtherMachines() || cfg.APIKey != "" {"
- cond-2609081750370330 — narrowed: The two things the condition named — the broadcast Gropius sends and the request log it writes — are both reported from observed state, and the per-model log files and the hosts the binary contacts are correctly absent; but the page also carries two lines that are static facts about the binary rather than observations of this running server, which is the shape the condition put out of scope.
  narrowing: The assumption holds for the six snapshot-derived lines (reach, private, key-network, key-local, announce, log, stats); the transport and panel lines carry reads: [] and are static facts about the source, stated on the page beside the observations with nothing distinguishing them to a reader.
  evidence: internal/ui/static/app.js:471 — "a line with no fields is a fact about the binary rather than an observation of this server, and there are two of those."
  evidence: internal/ui/static/app.js:567 — "lines.push({ id: 'panel', heading: 'This control panel', reads: [],"
  evidence: internal/ui/static/app.js:617 — "lines.push({ id: 'announce', heading: 'The local network', text: announce,"
## Grounds

- pursued: the four facts an operator needs have no home. adr-2609081118587999 routed them to documentation, that documentation does not exist, and itd-2609081015545349 deliberately makes the endpoint mark say the minimum an app can honestly say — which is right and leaves the question it raises unanswered anywhere. So the operator who wants to know where they stand has nowhere to look, and the only surface that speaks is a banner that fires after something is already wrong. Shown wrong if, once the page ships, those facts still have to be repeated in the banner and in the docs to reach anyone: that would mean the page did not become the single place they live, and the information had to be in the operator's way after all.
