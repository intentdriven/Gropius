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

<!-- abcd-review: INGESTED receipt=rcp-7a4b71f42085 -->
Fidelity review — receipt rcp-7a4b71f42085 (verifier intent-auditor claude-sonnet-5).

Provenance: intent-auditor@claude-sonnet-5 · rubric_hash sha256:4240571bf9f9909e199854e774e314c6a1146638300edac99f0fe1798692f6d4 · prompt_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5
Input attestations: diff:merge acc46d1 (PR #45, feat/update-verb into main; second parent d758b8f) diffed as acc46d1^1..acc46d1; rubric_hash and prompt_hash above are auditor-computed (sha256 of the request file and of the intent-auditor agent definition at /Users/dev/.claude/plugins/marketplaces/abcd-marketplace/agents/intent-auditor.md), not host-supplied@sha256:acc46d163f5dfffb65327f0199b7f1b0c43ba6e2; repo:worktree HEAD at .claude/worktrees/records, main after PR #45@sha256:acc46d163f5dfffb65327f0199b7f1b0c43ba6e2;

Acceptance rollup: MET 17 · MET_WITH_CONCERNS 0 · NOT_MET 3 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: updateLines always emits installed/serving on two distinct labelled lines plus the checksum, grant and no-way-back sentences when a bundle was placed, proven by a table test over ten report shapes
  evidence: internal/lifecycle/updatereport.go:196-225 — "out := []string{ installedLabel + " " + r.installedText(), servingLabel + " " + r.servingText(), }"
  evidence: internal/lifecycle/updatereport_test.go:114-164 — "func TestTheUpdateReportCarriesTheFiveFactsOnSeparateLines"
  evidence: internal/lifecycle/updatereport_test.go:170-182 — "func TestTheUpdateReportNeverPrintsOneVersionUnderTheOthersLabel"
- ac-2 — MET: checksumSentence names the verification and bannedTrustWords bans signed/notarised/notarized/trusted, enforced over every report shape and over both documentation pages
  evidence: internal/lifecycle/updatereport.go:59 — "checksumSentence = "The download was verified against the checksums published with the release.""
  evidence: internal/lifecycle/updatereport_test.go:209-221 — "func TestTheUpdateReportNeverCallsAReleaseSignedOrNotarised"
  evidence: internal/archtest/lifecycle_docs_test.go:307-330 — "func TestTheLifecyclePagesNeverCallAReleaseSignedOrNotarised"
- ac-3 — MET: grantLines always leads with grantReasonSentence naming the changed code identity as the reason, whether the grant was made or declined
  evidence: internal/lifecycle/updatereport.go:61-63 — "grantReasonSentence = "The firewall grant is re-made on every update: the build's code identity changes with " + "every build, so the entry that covered the previous build does not cover this one.""
  evidence: internal/lifecycle/updatereport.go:274-289 — "func (r updateReport) grantLines() []string { lines := []string{grantReasonSentence}"
- ac-4 — MET: a declined firewall panel still returns ExitOK with the swap reported done and by-hand commands printed, while a failed fetch/verify/swap returns ExitFailed, tested by the same boundary table
  evidence: internal/lifecycle/update.go:234-241 — "r.GrantAttempted = true binary := filepath.Join(ue.Dest, binaryInBundle) if err := ue.Firewall(binary); err != nil {"
  evidence: internal/lifecycle/update.go:266 — "return ExitOK"
  evidence: internal/lifecycle/update_test.go:352-409 — "func TestWhatFailsARunAndWhatDoesNot"
- ac-5 — MET: servingText never substitutes the installed version and always states a reason, across all three unknown causes, asserted directly by a dedicated test
  evidence: internal/lifecycle/updatereport.go:241-250 — "func (r updateReport) servingText() string {"
  evidence: internal/lifecycle/updatereport_test.go:187-203 — "func TestAnUndeterminableServingVersionIsSaidAndNeverSubstituted"
- ac-6 — NOT_MET: the report can only ever say the peer's serving version once gateway.State carries a version field, and it does not: no build emits it today so the peer-serving-version half of this criterion is unreachable in delivered reality, as iss-2609111942567418 records and as fetchServingVersion's own comment admits
  evidence: internal/gateway/control.go:276-280 — "type State struct { Models []registry.Model `json:"models"` Resident []runtime.Resident `json:"resident"` Setup runtime.SetupStatus `json:"setup"` Config config.Config `json:"config"`"
  evidence: internal/lifecycle/update.go:333-337 — "the contract test beside ServerState holds every field that struct declares to a field gateway.State publishes... answers "" for every build that does not publish a version — which is every build today"
  evidence: .abcd/work/issues/open/iss-2609111942567418-the-control-plane-publishes-no-version-field-so-gropius-upda.md:35-36 — "6 — "the report states the version that holder is serving". The decoder is tested against a snapshot carrying the field... today no build emits it, so only the unknown half is reachable."
- ac-7 — NOT_MET: servedByAnother's version-differs trigger (r.Serving != r.Installed) can never fire today because ServingVersion always returns empty for a peer sharing this account's data root (portOurs), so only the portSilent trigger reaches this ending; the criterion's first clause is unreachable, exactly as iss-2609111942567418 records for criterion 7
  evidence: internal/lifecycle/updatereport.go:165-170 — "func (r updateReport) servedByAnother() bool { if r.Holder == portSilent { return true } return r.Serving != "" && r.Installed != "" && r.Serving != r.Installed"
  evidence: .abcd/work/issues/open/iss-2609111942567418-the-control-plane-publishes-no-version-field-so-gropius-upda.md:38-40 — "7 — the (installed differs from serving) trigger. The cross-account ending still fires on the OTHER trigger... so the criterion's sentences are reachable and its first clause is not."
- ac-8 — MET: the quit seam is called exactly once and only when a bundle exists at the destination, and the archtest package scan proves no control-plane call beyond the one read site exists in the package, so a cross-account quit route cannot exist
  evidence: internal/lifecycle/update_test.go:318-345 — "func TestTheQuitReachesOnlyThisSessionAndIsSentOnce"
  evidence: internal/archtest/lifecycle_removal_test.go:176-223 — "func TestTheLifecycleVerbsOnlyEverREADTheControlPlane"
- ac-9 — MET: a wrong-answer or unwritable-root holder ends the run before Staging/Fetch/Place/Firewall/Launch are ever called, naming the port and the finding, reusing instance's own classification rather than a second one
  evidence: internal/lifecycle/update.go:132-154 — "if classifyPort(ue) == portUnproven {"
  evidence: internal/lifecycle/update_test.go:173-263 — "func TestTheUpdateRefusesBeforeAnythingIsWrittenWhenThePortCannotProveItself"
- ac-10 — MET: a holder that accepts a connection and answers no challenge (portSilent) still gets Place called and the report states cannot-be-determined rather than refusing
  evidence: internal/lifecycle/update.go:302-314 — "func classifyPort(ue UpdateEnv) portHolder {"
  evidence: internal/lifecycle/update_test.go:270-292 — "func TestAHolderThatAnswersNoChallengeStillGetsTheUpdate"
- ac-11 — MET: perAccountInstall/perAccountSentence separate this account's copy being updated from this Mac's server not being this account's, and a machine-wide case is asserted not to carry the sentence
  evidence: internal/lifecycle/updatereport.go:184-188 — "func (r updateReport) perAccountInstall() bool {"
  evidence: internal/lifecycle/updatereport_test.go:328-339 — "func TestAPerAccountInstallationSaysWhoseCopyWasUpdated"
- ac-12 — MET: all four named bad inputs (mismatch, no-target checksums, empty checksums, HTML error page) are run through the real /usr/bin/shasum against a fake loopback release server, each naming its cause and placing nothing
  evidence: internal/lifecycle/updatefetch.go:159-176 — "func checksumFailureCause(output, sums string) string {"
  evidence: internal/lifecycle/update_test.go:539-606 — "func TestTheUpdateFailsClosedOnEveryBadDownload"
  evidence: internal/lifecycle/updatefetch_test.go:101-199 — "func TestTheChecksumVerificationFailsClosedOnEveryBadInput"
- ac-13 — MET: the origin is a constant baked into curlArgs, proven unaffected by every plausible environment variable, and an archtest scan asserts the three update files read no environment variable on the fetch path
  evidence: internal/lifecycle/updatefetch.go:89-104 — "func curlArgs(name, dest string) []string {"
  evidence: internal/lifecycle/updatefetch_test.go:205-221 — "func TestTheAssetOriginIsFixedInTheBinary"
  evidence: internal/archtest/lifecycle_removal_test.go:237-254 — "func TestTheUpdatePathReadsNoEnvironmentVariable"
- ac-14 — MET: ue.Place is bound to PlaceBundle by function pointer, and an archtest scan proves the swap's staging name is declared and used in exactly one file (swap.go)
  evidence: internal/lifecycle/update.go:387 — "Place: PlaceBundle,"
  evidence: internal/lifecycle/update_test.go:665-681 — "func TestTheUpdateUsesTheSwapTheInstallerAlreadyPerforms"
  evidence: internal/archtest/lifecycle_removal_test.go:270-287 — "func TestTheStagingNameIsDeclaredAndUsedInOneFile"
- ac-15 — MET: every tool update.go/updatefetch.go run (curl, shasum, ditto, xattr) is a string literal beginning with '/', held by the repo-wide absolute-path scan, and the package-wide elevation-site scan finds exactly one site (elevate.go), unchanged by this PR
  evidence: internal/lifecycle/updatefetch.go:91 — ""/usr/bin/curl","
  evidence: internal/archtest/pinned_subprocess_test.go:37-49 — "func TestEverySubprocessIsPinnedToAnAbsolutePath"
  evidence: internal/archtest/lifecycle_removal_test.go:114-141 — "func TestTheLifecycleVerbsElevateInExactlyOnePlace"
- ac-16 — MET: a failed swap reports the version still at the destination, or the exact path of a kept staging copy, and never both; covered by four dedicated tests over the two outcomes
  evidence: internal/lifecycle/updatereport.go:256-268 — "func (r updateReport) swapFailureLines() []string {"
  evidence: internal/lifecycle/update_test.go:440-484 — "func TestAStoppedSwapThatKeptTheOnlyCopyNamesWhereItIs"
- ac-17 — MET: previousReleaseSentence appears in every report rendering and a version-shaped argument is refused with that sentence rather than fetching anything, and no rendering contains rollback/downgrade/--version wording
  evidence: internal/lifecycle/update.go:118-127 — "if versionish.MatchString(arg) { writeLine(env.Err, "gropius update: "+previousReleaseSentence)"
  evidence: internal/lifecycle/updatereport_test.go:226-237 — "func TestTheUpdateReportOffersNoWayBack"
- ac-18 — MET: the pre-existing computed-closure boundary test covers internal/lifecycle generically (not by filename), so update.go/updatefetch.go/updatereport.go are judged by it the day they exist; the suite is green with them present
  evidence: internal/archtest/lifecycle_boundary_test.go:69-79 — "func TestTheControlPlaneCannotSeeTheLifecycleVerbs"
  evidence: internal/archtest/lifecycle_boundary_test.go:55-56 — "lifecyclePkg: "is the package the rule is about","
- ac-19 — NOT_MET: the coordinated read-only version route on the control plane was not placed: gateway.State carries no version field and no handler exposes one, so the promised outcome (a read-only version answer) is simply absent, exactly as iss-2609111942567418 records ('the route does not exist, so nothing about it is armed either way')
  evidence: internal/gateway/control.go:276-280 — "type State struct { Models []registry.Model `json:"models"`..."
  evidence: .abcd/work/issues/open/iss-2609111942567418-the-control-plane-publishes-no-version-field-so-gropius-upda.md:42-43 — "19 — "the control plane answers which version is serving". The route does not exist, so nothing about it is armed either way."
- ac-20 — MET: RunUpdate/fetchAsset are reachable only through cmd/gropius's lifecycleVerbs dispatch table gated on the verb a person typed; no timer, goroutine or opt-in release-check feature exists anywhere in the repository to be turned on by an untouched save (grep for the feature's own issue id and for 'releaseCheck' returns nothing), so nothing added here can ask the forge unasked
  evidence: cmd/gropius/verbs.go:18-22 — "var lifecycleVerbs = map[string]func(lifecycle.Env, []string) int{ "status": lifecycle.RunStatus, ... "update": lifecycle.RunUpdate, }"
  evidence: internal/lifecycle/update.go:27-29 — "Nothing here elevates for anything but the // firewall grant. Nothing here contacts the forge unless a person typed the // verb."

Gap audit:
- honoured:
  - the two-line report separating installed from serving, with the five facts and the wording rules (no 'signed'/'notarised'/'trusted', no rollback offer)
    evidence: internal/lifecycle/updatereport.go:196-225 — "func updateLines(r updateReport) []string {"
  - the port-holder classification narrows the brief's refusal to exactly what instance distinguishes: wrong-answer/unwritable-root refuses, silent-holder still updates and reports unknown
    evidence: internal/lifecycle/update.go:302-314 — "func classifyPort(ue UpdateEnv) portHolder {"
  - a declined firewall panel is not a failed run, matching the install verb's own rule, with by-hand commands and the symptom printed
    evidence: internal/lifecycle/update_test.go:398-409 — "if !strings.Contains(out, "socketfilterfw") {"
  - one verification path shared with the bootstrap (curl + shasum), origin fixed in the binary, no second staging implementation, single elevation site
    evidence: internal/lifecycle/updatefetch.go:36-49 — "const ( releaseAssetBase = "https://github.com/intentdriven/Gropius/releases/latest/download/""
  - no rollback of any kind, and the wording says so rather than implying a path
    evidence: internal/lifecycle/updatereport.go:67-68 — "previousReleaseSentence = "There is no way back: only the current release is published, so the previous " + "release cannot be fetched.""
  - docs/lifecycle.md and docs/lifecycle-reference.md were updated with the update verb, its five facts and its exit codes, held to the same wording rule as the code
    evidence: docs/lifecycle.md:130-165 — "## Update it"
    evidence: docs/lifecycle-reference.md:27 — "| `gropius update` | Fetches the current release, verifies it against the checksums published beside it..."
- diverged:
  - the intent's own open question named the coordinated gateway version field as something that might or might not land in the same release, and named the fallback that ships without it
    evidence: internal/lifecycle/update.go:328-337 — "WHY IT IS DECODED HERE RATHER THAN ON ServerState... answers "" for every build that does not publish a version — which is every build today."
    evidence: .abcd/development/intents/shipped/itd-2609081420471761-alice-updates-gropius-from-the-terminal-and-is-told-the-trut.md:221-224 — "Whether the lane that owns the control plane accepts the version field... If it does not land, the fallback above is what ships."
- missing:
  - a read-only version field on gateway.State / the /api/state route, which criteria 6, 7 and 19 depend on to move past their fallback endings
    evidence: internal/gateway/control.go:276-280 — "type State struct { Models []registry.Model `json:"models"` Resident []runtime.Resident `json:"resident"` Setup runtime.SetupStatus `json:"setup"` Config config.Config `json:"config"`"
    evidence: .abcd/work/issues/open/iss-2609111942567418-the-control-plane-publishes-no-version-field-so-gropius-upda.md:14 — "The control plane publishes no version field, so gropius update reports the serving version as "cannot be determined" on every Mac"

Scope-condition dispositions:
- cond-2609111812375978 — survived: the delivered verb places only the server bundle (Gropius.app.zip) and never touches the client, leaving the chat client bundle exactly as out of scope as the condition assumed
  evidence: internal/lifecycle/updatefetch.go:43-44 — "updateArchiveName is the server bundle. The client bundle is the // bootstrap's to place; a verb on the server binary cannot be the remedy"
- cond-2609111812377938 — survived: the delivered verb re-makes the firewall grant on every single update and reports it as an act performed rather than an observed fact, matching the ad-hoc-signing assumption that made the panel unavoidable
  evidence: internal/lifecycle/update.go:231-241 — "EIGHT: the ONE elevation. The grant is re-made on every update because // the code identity it is keyed to changes with every build"
- cond-2609111812375223 — survived: curl is the only fetch channel used and no timer, launch-time check or release-check feature exists anywhere in the delivered code to contact the forge unasked
  evidence: internal/lifecycle/updatefetch.go:106-124 — "func fetchAsset(name, dest string) error {"
- cond-2609111812374283 — survived: a version-shaped argument is refused with the sentence that the previous release cannot be fetched, and no rendering of the report implies a downgrade path
  evidence: internal/lifecycle/update.go:118-127 — "writeLine(env.Err, "gropius update: "+previousReleaseSentence)"
- cond-2609111812379843 — survived: update reuses installDest unchanged from the installing verb, which still resolves to the system Applications directory when writable and the per-account fallback otherwise, and perAccountInstall keys the report off which one applies
  evidence: internal/lifecycle/install.go:312-317 — "func installDest(home string) string {"
  evidence: internal/lifecycle/updatereport.go:184-188 — "func (r updateReport) perAccountInstall() bool {"
- cond-2609111812376310 — untested: the fast-user-switching, second-server-becomes-a-client mechanism lives in internal/instance and is unchanged by this PR; the delivered tests exercise the consequence through fakes (a holder classification) but nothing here runs the actual multi-session scenario on a Mac
- cond-2609111812370540 — narrowed: the code correctly implements the shared-root/per-account split the condition describes (portOurs vs portSilent), but the 'good cross-account report' the condition says exists only where the root is shared is not actually reachable there either today, because the coordinated version field never landed
  narrowing: holds for the port-holder classification as designed, but even under a shared data root the report cannot yet name the peer's serving version — it degrades to the same 'cannot be determined' a per-account root produces, until iss-2609111942567418 closes
  evidence: internal/lifecycle/update.go:276-296 — "func finishUpdate(ue UpdateEnv, r *updateReport) {"
  evidence: .abcd/work/issues/open/iss-2609111942567418-the-control-plane-publishes-no-version-field-so-gropius-upda.md:33-37 — "Three of itd-2609081420471761's twenty, and they are the same one field"
## Grounds

- pursued: reporting the installed version and the serving version as two separate facts removes the failure, because the failure is not a bad swap but a true statement about a bundle offered as a statement about a machine — the swap succeeds, the grant is renewed, and on a Mac where another account holds the port every one of those is true while nothing about what answers requests has changed; wrong if the serving version cannot be obtained at all, since the instance challenge proves a shared data root rather than an identity and the honest answer then degrades to 'something holds the port and did not identify itself, version unknown', and wrong a second way if re-making the path-keyed firewall grant is measured on a real shared Mac to take LAN reachability away from the still-running old server, which would mean the update broke what was serving rather than merely failing to change it — planned autonomously on the maintainer's instruction of 2026-09-10, adopting the brief's recommendations
