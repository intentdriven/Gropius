---
id: spc-2609111812370705
slug: alice-updates-gropius-from-the-terminal-and-is-told-the-trut
intent: itd-2609081420471761
origin: researcher-authored
production_mode: hand-written
---
# alice-updates-gropius-from-the-terminal-and-is-told-the-trut

## Summary

A fifth verb, `gropius update`, on the binary that already carries four. It
fetches the current release and verifies it exactly as the bootstrap does,
places it with the staged swap that shipped with the installing verbs, re-makes
the one grant that has to be re-made, and then does the thing that is new: it
reports the version it installed and the version this Mac is serving as two
separate facts, and says plainly when the second one cannot be known.

Almost none of the mechanism is new. The fetch, the verification, the swap, the
elevation, the link and the launch are the installing verbs' own code, reached
a second time. What this record adds is the ending — a report that can say "the
bundle is new and this Mac is still serving the old one" — and the smallest
thing that makes that ending truthful: a read-only version answer on the
control plane, which is a coordinated change in another session's lane.

## Depends on

- **itd-2609081259532589 / spc-2609111029315861** (shipped, closed).
  `internal/lifecycle` exists, with `PlaceBundle`'s staged swap, `grantFirewall`'s
  single elevation, `linkCommand`'s per-user link, the `Env`/`InstallEnv` seam
  shape, the exit-code rule and the live-path guard in `live.go`. `update` is
  already a word `cmd/gropius` knows and refuses with "not in this build yet";
  this spec is what puts something behind it.
- **`internal/instance`** carries the ours/foreign/none classification and the
  challenge. This spec asks it the same question the singleton election asks and
  adds no second classification of identity.
- **`internal/archtest/pinned_subprocess_test.go`** already scans every non-test
  Go file that imports `os/exec`, so every tool this verb runs is held to an
  absolute path the day the file exists. **`internal/archtest/lifecycle_boundary_test.go`**
  already computes the control plane's dependency closure, so the boundary
  criterion is armed for anything added here.
- **`internal/gateway`** is another session's lane. The read-only version answer
  below is a **coordinated change**, agreed with that lane before it is written,
  and this spec names the fallback that ships if it is not agreed.
- **adr-2609061503319212** (no public telemetry) keeps the release check opt-in
  and off; this verb contacts the forge only when a person types it.

## Scope

**In.** `gropius update` in `internal/lifecycle`: the fetch and verification of
the current release's assets, the unpack, the reuse of the staged swap, the
single elevation for the firewall grant, the launch, and the report. The
dispatch change in `cmd/gropius` that moves `update` from "a verb this build
does not carry" to a verb it runs. A read-only version answer on the control
plane, coordinated with the lane that owns it. The tests named below. The
update half of `docs/lifecycle.md` and the changed rows of
`docs/lifecycle-reference.md`.

**Out.**

- **Rollback of any kind.** One release is published at a time, so there is no
  target to roll back to; the verb says so rather than implying a path.
- **A release check, a timer, a nag or a banner.** Opt-in and off by default,
  under adr-2609061503319212, and unchanged by this work.
- **The client bundle.** See below: iss-2609111454146700 is not closed here.
- **The control panel's display of the two versions.** The three-surfaces
  obligation is itd-2609081259493890's, and the panel showing installed and
  serving alongside the command to run belongs to that record. What this spec
  owes it is the value: once the control plane answers which version is
  serving, the panel has the same fact the terminal has. The panel never
  performs the update, which would quit the application the panel is served
  from.
- **Doctor gaining a line about a stale grant on a still-running old server.**
  That depends on a measurement that has not been made (the intent's first open
  question), and no criterion here is written against a guess about it.

## Approach

### Where the verb lives, and how it is reached

`internal/lifecycle/update.go`, beside the other verbs, with `RunUpdate(Env,
[]string) int` at its edge and an `UpdateEnv` carrying every side effect as a
function — the shape `InstallEnv` already has, for the same reason: every case
below is then a test with no Mac, no panel and no network.

In `cmd/gropius`, `update` moves from `verbNotYet` in `args.go`'s table to
`verbLifecycle`, and gains its entry in `verbs.go`'s `lifecycleVerbs`. The test
that holds those two tables together is what proves the move is complete; the
fakes in `TestMain` gain an `update` entry at the same time, because a writing
verb with no fake is a test one call away from updating the developer's Mac.

`live.go`'s guard gains the verb: `liveUpdateEnv` refuses to be built inside a
test binary, with `verbNoun` spelling `runUpdate` in the refusal. This is the
backstop that iss-2609111240578491 bought, and an update is the one verb where
reaching the live path in a test would download a release and replace the
application it is running from.

### The order of a run

1. **Ask who holds the port, once, before anything is fetched.** A holder that
   answers the identity challenge wrongly — or a data root no proof can be
   written into — ends the run here: nothing is downloaded, nothing is placed,
   the port and what was found are named, and the exit is non-zero.
2. **Fetch** `Gropius.app.zip` and `SHA256SUMS.txt` from the current release
   into a temporary directory.
3. **Verify** the archive against the checksums, and stop on any failure with
   nothing placed.
4. **Unpack**, clear the quarantine attribute on the verified bundle, and
   refuse a bundle whose `Contents/MacOS/gropius` is a symbolic link — the same
   three acts, in the same order, that the bootstrap performs.
5. **Read the version being installed** by running the staged binary's own
   `version` verb, by absolute path, inside the directory just verified. That
   is the bootstrap's rule — execute only from the directory that was verified,
   never from the bundle on the Mac — and it means the version reported is the
   one that build reports about itself, in the same vocabulary `status` and
   `doctor` use. A staged binary that refuses the verb is a build older than it
   (exit 2), and the version installed is then reported as unknown rather than
   guessed.
6. **Ask this account's running copy to quit.** This is `quitRunningCopy`
   unchanged, and the fact that it reaches only this login session is not a
   limitation to work around: it is the mechanical reason this command cannot
   quit another account's server behind their back, and the report says so
   rather than the code trying.
7. **Place the bundle** with `PlaceBundle`. No second implementation.
8. **Re-make the firewall grant** with `grantFirewall`, for the binary inside
   the bundle just placed. This is the one elevation, and the panel appears on
   every update because an ad-hoc designated requirement is the code-directory
   hash, which changes on every build.
9. **Leave the per-user link alone** unless it is missing. The link names a
   path inside the bundle, not a build, so a swap does not invalidate it;
   `linkCommand` is re-asserted only when the link is gone.
10. **Launch, and wait for an answer**, exactly as the install's ending does.
11. **Ask who holds the port again, and what version it is serving**, and
    report. The verdict is what was true when the command returned, not what
    was true before the swap.

### Fetching and verifying, exactly as the bootstrap does

`/usr/bin/curl` with `-q --proto =https --proto-redir =https -fsSL`, and
`/usr/bin/shasum -a 256 -c --ignore-missing SHA256SUMS.txt` run in the staging
directory, both by absolute path, with the output captured so the failure can
say which of the causes fired. Not `net/http` and `crypto/sha256`, and that is a
choice with a reason: the bootstrap and the verb must agree on what "verified"
means, and one verification path is one thing to keep right rather than two
that can drift. The behaviours that path already has — a curlrc that cannot
re-point the connection, HTTPS pinned across redirects, `--ignore-missing`
proven against that exact binary — are behaviours this verb inherits rather
than re-establishes. The cost is two subprocesses, which
`internal/archtest/pinned_subprocess_test.go` already holds to absolute paths.

**The origin is fixed in the binary.** There is no `GROPIUS_ASSET_DIR` here and
no flag that names a URL. The bootstrap's seam is refused outside CI because a
caller who can set one variable would otherwise substitute the whole integrity
control silently — the checksums would be read from the same place as the
bundle, and the verification would prove only that a directory is
self-consistent. A verb that a person types on a Mac has no CI case at all, so
the seam is a Go struct field the live builder fills in and a test substitutes,
and nothing reads it from the environment or the command line.

### The port-holder classification decides the ending

The same primitive, asked twice, with one bit added that is not a second
classification of identity: whether anything is accepting connections on the
port. `instance` folds "nothing is there" and "something is there that did not
answer" into `HolderNone`, and an update has to tell those two apart to say a
true sentence — not to decide whether the holder is trustworthy, which is the
question `instance` answers and this verb does not re-ask.

| What was found | What `update` does |
| --- | --- |
| Answers the challenge wrongly, or no proof can be written | Refuses before anything is fetched. Names the port and what it found. Non-zero. |
| A Gropius sharing this account's data root | Updates, then reports the version it is serving from the control plane. |
| The port is idle | Updates. The launch is what makes the new version serve, and the report says whether it did. |
| The port is busy and silent | Updates, and reports that the version serving cannot be determined from here. |

The last row is where this spec **corrects the planning brief**. The brief asks
for a refusal wherever the holder "cannot identify itself as Gropius". Under a
per-account data root that description fits another account's Gropius exactly —
Bob's server cannot read the challenge Alice wrote into her own root — so that
refusal would make a shared Mac unupdatable for as long as a colleague stays
logged in, which is option (a) the same brief declines. The refusal is
therefore narrowed to what `instance` actually distinguishes: a responder that
answered and answered wrongly, or a root no proof can be written into. The
brief's own mechanism clause already describes the busy-and-silent ending as a
degraded report rather than a refusal, so this resolves the brief against
itself in favour of the option it recommends.

### The report: two lines, and where "serving" comes from

The report is a value — installed version, serving version or the reason there
is none, the holder's classification, the grant's outcome, the destination —
rendered by a pure function. Every assertion below is made against that value
and its rendering, with no process and no Mac.

**Serving comes from the control plane.** The running server is asked for its
version over loopback, and `internal/lifecycle` decodes it structurally from the
snapshot it already reads for `status`, never by importing the gateway's type:
the dependency may not run that way (adr-2609111126115848 condition 3). The
coordinated change on the gateway side is a `version` field on the existing
state snapshot — the smallest addition that makes the report truthful, and one
the panel gets for free because it already polls that route. It is read-only in
the strong sense: it reveals what a world-readable bundle already reveals, it
needs no shared root, and no request to it reaches an update, a quit or an
elevation.

**"Version unknown" is a first-class answer**, not a failure. It is what the
report says when the holder is this account's Gropius by the challenge and the
field is absent (a build older than the field), when the holder is busy and
silent, and when the control plane does not answer at all. In none of those
cases is the version just installed printed in its place.

**How the two lines differ tells the cross-account story.** A serving version
that is not the installed one means this Mac is answering from something this
command did not put there, and the report says that, names the two things that
finish the job — the other account logging out, or restarting Gropius — and
counts the holder rather than naming it. Doctor's redaction helper is applied
over every line, so no account name, no home directory and no uid reaches a
report written to be pasted into a message to a colleague.

**The grant is reported as made or not made**, which is a verified fact about an
act this command performed and not the observed firewall query doctor is
carefully hedged about. A declined panel is not a failed run: the swap stands,
the report names the commands that make the grant by hand and the symptom to
expect until they are run, and the exit follows the rule the installing verbs
already hold. This is the spec's **second correction to the brief**, which asked
for a non-zero exit on a grant that could not be re-made; the shipped rule is
that a declined authorisation panel is never a failed run, and an update that
contradicted the install beside it would be two rules for one panel.

### What the verb never does

Never quits another account's server, and never asks the control plane to quit
anything — the route that would make that cheap is exactly the route the
parent's boundary criterion forbids, and it would hand every local account a
cross-account stop button on a plane that has no bearer check. Never elevates
for anything but the firewall grant. Never reads standard input. Never contacts
the forge unless a person typed the verb. Never keeps a copy of the previous
release, offers a downgrade, or implies one exists. Never calls a release
signed, notarised or trusted. Never writes outside this account's own
directories and the one destination its installation uses, and never derives
that destination from an environment variable or a flag.

### The client bundle, and iss-2609111454146700

`update` places the server bundle and nothing else, so
**iss-2609111454146700 stays open**. The issue is about `install.sh`'s client
half, which places `GropiusChat.app` with the shell's `mv` — nesting into an
existing directory, following a symbolic link — on Macs that may carry no
server binary at all. A verb on the server binary cannot be the remedy for a
machine that has only the client. What this work does do is make the remedy
cheaper to reach later: `PlaceBundle` is already a general placer, and a
placement helper the bootstrap could call for either bundle is a small change
once somebody decides where a client-only Mac gets a Go binary from. Named
here so the issue is not quietly assumed closed by a verb with "update" in its
name.

### The set-aside window is inherited, not re-claimed

The swap's guarantee is the parent's, and so is its one caveat:
iss-2609111755330533 records that where the destination reappears mid-swap the
set-aside bundle waits in a kept staging directory, and a co-resident
admin-group account can remove that directory because the applications
directory is group-writable with no sticky bit. This spec inherits the claim
and the caveat together and states neither more strongly than the parent does.

## How this satisfies the Acceptance Criteria

Criterion numbers follow the intent's order within its four groups. Every test
named here runs with no network and no Mac unless it says otherwise, and none
of them can reach the live path: `live.go`'s guard refuses to build the live
update environment inside a test binary at all.

**What the terminal says**

1. **Five facts, two separate lines** — a golden table over report values
   (installed/serving equal, differing, serving unknown, grant made, grant
   declined) asserting all five facts are present in every rendering, that the
   installed and serving versions are on separate lines with distinct labels,
   and that neither string is ever rendered under the other's label.
2. **Verified against published checksums, never "signed"** — a **wording
   test** over every line the report can produce, driven from the same table:
   "signed", "notarised", "notarized" and "trusted" appear nowhere, and the
   checksum sentence appears wherever a bundle was placed. The same test covers
   the two documentation pages this spec writes.
3. **The grant's reason** — the same table: the line that names the grant names
   the changed code identity as the reason, so the panel reads as expected
   rather than as a fault.
4. **A declined panel is not a failed run** — a behavioural test with the
   elevation seam returning a refusal: the swap is still reported as done, the
   by-hand commands and the symptom are present, and the exit code is the
   answer code. Paired with a failing-fetch and a failing-swap case in the same
   table asserting the non-zero codes, so the rule is tested by its boundary
   rather than asserted by one example.
5. **An undeterminable serving version is said, not substituted** — a table
   over the three ways it goes unknown (no version field, unanswered control
   plane, busy-and-silent holder) asserting the installed version's string
   appears nowhere on the serving line.

**The cross-account port**

6. **The serving version, and "unknown" for a build that has none** — the
   structural decoder tested against a snapshot with the field and one without;
   the report value carries the version in the first case and the unknown
   reason in the second.
7. **Serving a version this command did not install** — a table over
   (installed, serving) pairs: where they differ, the report states that this
   Mac is serving something this command did not install, names logging out and
   restarting as the two things that finish it, and counts the holder rather
   than naming it. A redaction assertion runs over every line of every case,
   reusing doctor's helper and its test corpus.
8. **Nothing is asked to stop** — a behavioural test asserting the quit seam is
   called at most once and only for this account's own copy, plus a package
   scan asserting no control-plane call in `internal/lifecycle` other than the
   two read-only reads (the challenge and the state snapshot), so a quit route
   cannot be added here without the test failing.
9. **The wrong answer refuses before anything is written** — the port-holder
   table driven through the update path with `HolderForeign`: the fetch seam,
   the place seam, the elevation seam and the launch seam are all asserted
   never called, the port and the finding are in the message, and the exit is
   non-zero.
10. **Busy and silent still updates, and says the version is unknown** — the
    same table with a holder that accepts a connection and answers no
    challenge: the place seam IS called, and the report carries the
    cannot-be-determined sentence rather than a refusal.
11. **A per-account installation** — the same table with the destination inside
    this account's home: the report says this account's copy was updated and
    this Mac's server was not.

**The fetch and the swap**

12. **Failing closed on every bad input** — a fake asset server and a temporary
    directory, with the **real** `/usr/bin/shasum` invocation exercised over
    four fixtures: an archive whose hash does not match, a checksums file
    naming no downloaded file, an empty checksums file, and an HTML error page
    served where an asset was asked for. Each asserts nothing was placed, the
    message names which cause fired, and the exit is non-zero. The verification
    is run for real because it is the only integrity control in the path; the
    download itself goes through the fetch seam, so no test reaches the
    network.
13. **The origin is fixed** — a test that sets `GROPIUS_ASSET_DIR` and every
    other plausible variable, and asserts the live fetch's argument list is
    unchanged, plus a scan asserting `internal/lifecycle` reads no environment
    variable on the fetch path. Paired with a pure-function test over the curl
    argument list: absolute binary, `-q` first, both `--proto` pins present.
14. **One swap, not a second** — the update path is asserted to call
    `PlaceBundle` itself (the seam's default is that function), and a scan
    asserts exactly one staging implementation exists in the package. The swap's
    own behavioural tests — destination pre-created as a directory, as a
    symbolic link, and the second rename made to fail — are the parent's and are
    inherited rather than rewritten.
15. **Absolute paths, one elevation** — armed already by
    `internal/archtest/pinned_subprocess_test.go` for every tool the new file
    runs, plus a scan asserting a single elevation site on the update path and
    that it is the firewall grant.
16. **A failed swap leaves an application or says where it is** — the parent's
    swap tests carry the behaviour; this spec adds a report-level case
    asserting that on a failed swap the report names the version at the
    destination, and on the kept-staging path names that path.

**No rollback, and no route in**

17. **No way back** — the wording test again: the report says the previous
    release cannot be fetched, and no rendering contains a downgrade
    instruction, a version flag, or the word "rollback" as an offer.
18. **Nothing lifecycle on the control plane's path** — already armed by
    `internal/archtest/lifecycle_boundary_test.go`'s computed closure, whose
    companion list puts every package in the module on one side of the rule or
    the other, so the new file is judged the day it exists.
19. **The version answer drives nothing** — on the gateway side, in the
    coordinated change: a test that the route is read-only, that it is inside
    `loopbackOnly`, and that its handler reaches nothing that writes. On this
    side, the closure test above is what proves no lifecycle package became
    reachable in exchange for it.
20. **Nobody typed it, nothing is asked** — a test that constructing and
    starting the server makes no outbound call on any path this work adds, and
    a settings test asserting the opt-in release check's stored value is
    unchanged by a save that did not touch it (the wedge iss-2609091751184914
    names, and the reason this criterion is written at all).

## Documentation

- **`docs/lifecycle.md`** gains an **Update it** section between installing and
  removing: the command, the administrator panel that appears every time and
  why it has to, the five facts the report carries, the shared-Mac ending in
  the words a person will actually see, and the plain statement that there is
  no way back. Task-shaped, present tense, one page.
- **`docs/lifecycle-reference.md`** changes the `gropius update` row from
  "named, and not in this build yet" to the verb and its exit codes, adds its
  flags to the flag table, documents the report's five facts and the values the
  serving line can take, and — if the coordinated change lands — the `version`
  field on the state snapshot.
- **`docs/getting-started.md`** gains one sentence pointing at the update
  section, because re-running the install command is currently the undocumented
  update path.
- Neither page calls a release signed or notarised, and the wording test in
  criterion 2 covers both of them.

## CHANGELOG

One entry under `[Unreleased]` → `### Added`, in the shape the lifecycle entry
already set:

> **Gropius updates itself from the terminal, and says which version is
> serving.** `gropius update` fetches the current release, verifies it against
> the checksums published beside it, and places it with the same staged swap
> the installer uses… Then it tells you two things rather than one: the version
> it installed, and the version this Mac is serving. On a Mac where another
> account is logged in with the server running, those are different facts, and
> the command says so instead of reporting success: the new bundle is in place,
> the Mac is still serving the old version, and it names what finishes the job.
> It never quits another account's server. The firewall grant is re-made on
> every update — the panel is expected, and the report says why. There is no
> way back: one release is published at a time.

If the coordinated control-plane change lands in the same release, a second
line under `### Changed` records that the state snapshot now carries the
running server's version, read-only, which the control panel shows too.

## Open

- **The firewall-grant measurement** (the intent's first open question) is a
  precondition for the cross-account wording, not a follow-up. Until it is made
  on a real shared Mac, the report says what it can verify — the grant was
  re-made for this build — and claims nothing about what that did to a server
  already running from the replaced bundle. The measurement belongs beside the
  manual procedure the parent already keeps, and its result may add a line to
  the report and to doctor.
- **The coordinated version field** needs the agreement of the lane that owns
  `internal/gateway`. If it is not agreed, everything here still ships with the
  serving version reported as unknown wherever it cannot be read — poorer
  output, no criterion lost.
- **An adversarial security review is a precondition of landing.** This verb
  downloads code over the network, executes a binary it has just downloaded to
  read its version, replaces the running application and raises an
  authorisation panel. The parent's history on exactly this ground says to
  expect findings that change a claim rather than harden one.
