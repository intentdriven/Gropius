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

_Empty. Populated by intent-auditor when intent moves to shipped/._

## Grounds

- pursued: the four facts an operator needs have no home. adr-2609081118587999 routed them to documentation, that documentation does not exist, and itd-2609081015545349 deliberately makes the endpoint mark say the minimum an app can honestly say — which is right and leaves the question it raises unanswered anywhere. So the operator who wants to know where they stand has nowhere to look, and the only surface that speaks is a banner that fires after something is already wrong. Shown wrong if, once the page ships, those facts still have to be repeated in the banner and in the docs to reach anyone: that would mean the page did not become the single place they live, and the information had to be in the operator's way after all.
