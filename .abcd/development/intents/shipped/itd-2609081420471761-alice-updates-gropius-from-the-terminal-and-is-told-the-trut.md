---
id: itd-2609081420471761
slug: alice-updates-gropius-from-the-terminal-and-is-told-the-trut
spec_id: spc-2609111812370705
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609081259532589]
severity: minor
impact: additive
origin: researcher-authored
production_mode: dictated-and-formatted
---

# Alice updates Gropius from the terminal and is told the truth about what happened, including when another account is still serving the old version

## Press Release

Alice runs `gropius update`. It fetches the current release, checks it against
the checksums published beside it, and swaps the bundle without ever leaving
her Mac without an application. Then it tells her what actually happened: the
version it installed, the version serving right now — on her own Mac, the same
one — that the download was verified against the checksums published with the
release, that the firewall grant had to be re-made and why it has to be re-made
every single time, and that there is no way back to the version she has just
replaced.

On the Mac in the studio, Bob is still logged in with the server running in his
session. Alice runs the same command and gets a different, honest ending: the
new bundle is in place, and the Mac is still serving the old version out of
Bob's session, because the port is his until he logs out or restarts Gropius.
It does not quit Bob's server behind his back — it cannot, and it says so
rather than trying. It does not pretend the update took effect. It names the
two things that finish the job and stops.

Where the port is held by something that answers the identity challenge wrongly,
Alice gets no update at all: the command refuses before it touches the bundle,
names the port and says what it found. A machine with an impostor on the server
port is not a machine to install software on, and that is the one ending where
nothing is written.

## Why This Matters

An update that reports success while the machine serves the previous version is
worse than one that refuses: Alice makes decisions on that sentence. She tells
Carol the bug is fixed; Carol still gets the old behaviour. Every honest signal
Gropius has built — doctor separating verified from observed, the posture page,
the honesty scan — is undone by one line that reports what was written to disk
as though it were what is running. The shared Mac makes those two facts
genuinely different, and nothing in the product currently knows the difference:
the control plane carries no version field at all, so "which version is
serving" is not a question anything on this Mac can answer today.

## Mechanism

We expect reporting the installed version and the serving version as two
separate facts to remove the failure, because the failure is not a bad swap but
a true statement about a bundle offered as a statement about a machine: the
swap succeeds, the grant is renewed, and on a Mac where another account holds
the port every one of those is true while nothing about what answers requests
has changed.

We are wrong in the first way if the serving version cannot be obtained at all.
The instance challenge proves a shared data root and not an identity, so under
per-account roots the honest answer degrades to "something holds the port and
did not identify itself; the version serving cannot be determined from here",
and the value then sits entirely in the refusal to claim rather than in the
report — which is a smaller claim than this record makes.

We are wrong in the second way if re-making the path-keyed firewall grant takes
LAN reachability away from the still-running old server. The grant records the
new build's code identity, so Bob's server may stop answering the machines
across the room the moment Alice updates. If a measurement on a real shared Mac
confirms it, then two truthful lines are not enough: the update has not merely
failed to change what is serving, it has broken what was serving, and the
remedy belongs in the report and in doctor before this claim can stand. That
measurement is owed, not assumed, and is the first open question below.

## Scope Conditions

- macOS 26 or later, and Apple Silicon for the server. The chat client bundle <!-- cond: cond-2609111812375978 -->
  is placed by the bootstrap's own shell half and is out of scope for this
  record.
- Ad-hoc-signed, un-notarised bundles with no Developer ID. The administrator <!-- cond: cond-2609111812377938 -->
  panel on every single update, and the grant that has to be re-made with it,
  exist because of this and are expected to change only if the membership is
  ever bought.
- `curl`-fetch as the only channel, so an update is always an act Alice takes <!-- cond: cond-2609111812375223 -->
  at a terminal. Nothing here runs on a timer, and the release check stays
  opt-in and off by default under the no-public-telemetry commitment, so
  Gropius never learns that Alice is behind and never nags her about it.
- One release published at a time. There is no rollback target and the version <!-- cond: cond-2609111812374283 -->
  currently installed cannot be re-fetched once it is superseded; the tag
  survives and can be rebuilt from source, which is not something Alice can do
  at a terminal.
- Both destinations are in play and they are not symmetric: the system <!-- cond: cond-2609111812379843 -->
  applications directory holds ONE bundle that every account launches, while
  the per-account fallback changes nothing for anybody else.
- Shared Macs with fast user switching, where a second server does not start <!-- cond: cond-2609111812376310 -->
  but becomes a client of the one already running.
- The data root may be shared or per-account, and which it is decides how much <!-- cond: cond-2609111812370540 -->
  this command can say: the challenge proves a shared root rather than an
  identity, so the good cross-account report exists only where the root is
  shared.

## Acceptance Criteria

**What the terminal says**

- Given an update completes, when it reports, then it states five facts: the
  version installed, the version serving, that the download was verified
  against the checksums published with the release, that the firewall grant was
  re-made and why it has to be, and that there is no way back to the version
  just replaced. The installed version and the serving version are two separate
  lines, and neither is ever printed where the other was asked for.
- Given the bundle was verified, when the report names that check, then it says
  the download was verified against the checksums published with the release,
  and in no wording calls the release signed, notarised or trusted.
- Given the grant was re-made, when the report names it, then it says the grant
  had to be renewed because the build's code identity changed, so the
  administrator panel reads as expected rather than as a fault.
- Given the grant could not be re-made — a declined panel, a standard account
  with nobody to answer it — when the command returns, then the swap is still
  reported as done, the grant is reported as not made with the commands that
  make it by hand and the symptom to expect until it is, and the exit code
  follows the rule the installing verbs already hold: a declined authorisation
  panel is not a failed run, while a fetch, a verification or a swap that
  stopped is.
- Given the serving version cannot be determined, when it reports, then it says
  so, and never substitutes the version it has just installed.

**The cross-account port**

- Given the port is held by a Gropius that shares this account's data root,
  when `update` finishes, then the report states the version that holder is
  serving; and given that holder is a build with no version to give, then the
  report says the version is unknown rather than reporting the one just
  installed.
- Given another account holds the port, when `update` finishes, then the report
  states that this Mac is serving a version this command did not install, names
  the two things that finish the job — that account logging out, or restarting
  Gropius — and counts other accounts rather than naming them.
- Given another account holds the port, when `update` finishes, then no request
  was made to stop that account's server, and the report says that quitting it
  is not something this command can do.
- Given the port is held by a process that answers the identity challenge
  wrongly, or a data root no proof can be written into, when `update` runs,
  then it refuses before touching the bundle and names the port and what it
  found, using the classification the singleton election already makes rather
  than a second one.
- Given something holds the port and answers no challenge at all, when `update`
  runs, then the bundle is still replaced and the report says that the version
  serving cannot be determined from here — because under a per-account root
  that description fits another account's Gropius exactly, and refusing there
  would make a Mac unupdatable for as long as a colleague stays logged in.
- Given a per-account installation, when another account is serving, then the
  report says this account's copy was updated and this Mac's server was not.

**The fetch and the swap**

- Given a download that does not match the published checksums, a checksums
  file naming no downloaded file, an empty checksums file, or an error page
  served where an asset was asked for, when `update` runs, then nothing is
  placed, the failure names which of those it was, and the command exits
  non-zero.
- Given the asset origin, when `update` fetches, then the origin is fixed in
  the binary: no environment variable and no flag can point the download or the
  checksums that verify it at anywhere else.
- Given a bundle is being replaced, when the swap runs, then it is the staged
  swap this account's installer already performs — unguessable staging name,
  the installed bundle renamed aside before the new one is moved in, the
  set-aside copy removed only once the new one is in place, a rename that
  refuses an existing directory and replaces a symbolic link rather than
  following it — and `update` adds no second implementation of it.
- Given the swap runs, when it invokes any system tool, then every tool is
  named by absolute path, and the command elevates for the firewall grant and
  for nothing else.
- Given the swap fails at any point, when the command returns, then either a
  working bundle is at the destination, or the failure names the path where the
  only remaining copy is and what to do with it; and the report says which
  version is at the destination either way.

**No rollback, and no route in**

- Given only the current release is published, when `update` reports, then it
  offers no way back and says the previous release cannot be fetched, rather
  than implying a downgrade path that does not exist.
- Given the lifecycle packages, when they are placed, then none is reachable
  from the HTTP control plane, so no route can drive a self-replacement, a
  self-quit or an elevation.
- Given the control plane answers which version is serving, when that route is
  placed, then it is read-only and drives nothing: it reveals what a
  world-readable bundle already reveals, and no request to it can reach an
  update, a quit or an elevation.
- Given nobody typed the command, when Gropius runs, then it asks the forge
  nothing: no timer, no launch-time check, and the opt-in release check stays
  off until somebody turns it on and is never turned on by a save that did not
  touch it.

## Open Questions

- **What re-making the firewall grant does to a server already running from the
  replaced bundle.** The grant is keyed to the path and records the code
  identity of the build behind it, so Bob's still-running old server may lose
  LAN reachability the moment Alice updates — the empty-response symptom doctor
  exists to explain. This is the mechanism's second falsifier and it must be
  measured on a real shared Mac, across an administrator account and a standard
  account, rather than asserted in either direction. Where the measurement is
  recorded, and whether the cross-account report and doctor gain a line about
  it, both follow from the result. No criterion above may be called met on a
  guess about this.
- **Whether a read-only version answer on the control plane counts against the
  parent's boundary criterion.** Decided here as: no. That criterion forbids a
  route that DRIVES a lifecycle act — a self-replacement, a self-quit, an
  elevation — and reporting a version drives nothing, needs no shared root, and
  reveals only what a world-readable bundle already reveals. What genuinely
  remains is that the maintainer may read it as a widening of a boundary they
  drew deliberately. If they do, the fallback costs no criterion above: the
  serving version is reported as unknown wherever it cannot be read from a
  process this command started, and the output is poorer rather than untrue.
- **Whether the lane that owns the control plane accepts the version field.**
  `internal/gateway` is another session's lane, so the field is a coordinated
  change and not this record's to make alone. If it does not land, the fallback
  above is what ships.

## Audit Notes

<!-- abcd-review: OWED receipt=rcp-7a4b71f42085 -->
Fidelity review OWED (receipt rcp-7a4b71f42085).

## Grounds

- pursued: reporting the installed version and the serving version as two separate facts removes the failure, because the failure is not a bad swap but a true statement about a bundle offered as a statement about a machine — the swap succeeds, the grant is renewed, and on a Mac where another account holds the port every one of those is true while nothing about what answers requests has changed; wrong if the serving version cannot be obtained at all, since the instance challenge proves a shared data root rather than an identity and the honest answer then degrades to 'something holds the port and did not identify itself, version unknown', and wrong a second way if re-making the path-keyed firewall grant is measured on a real shared Mac to take LAN reachability away from the still-running old server, which would mean the update broke what was serving rather than merely failing to change it — planned autonomously on the maintainer's instruction of 2026-09-10, adopting the brief's recommendations
