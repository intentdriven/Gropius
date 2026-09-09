---
id: itd-2609081259532589
slug: alice-installs-updates-removes-and-diagnoses-gropius-with-th
spec_id: null
kind: null
suggested_kind: null
reclassification_history: []
builds_on: []
severity: major
impact: additive
origin: researcher-authored
production_mode: dictated-and-formatted
---

# Alice installs, removes and diagnoses Gropius with the app's own commands, and a friendly terminal shows her what is happening

> The slug still carries "updates". Updating was split into
> itd-2609081420471761 after review, because its blast radius is different and
> its blocking question — what an update does when another account holds the
> port — is unanswered. The identity is left alone rather than rewritten, since
> a record's id is not a description.

## Press Release

Alice has a Mac she wants other people's machines to talk to. She pastes one
line into a terminal, and instead of a wall of silent output she gets a page
that tells her what is happening: the download checked, the app placed, one
system panel asking for permission to let the app through the firewall and
saying why, and then the part that used to be a mystery — the private Python
and MLX runtime installing, with real progress rather than a spinner and the
words "a few minutes". When the command finishes, Gropius is serving.

Later, something is wrong: a machine across the room gets an empty response.
Alice runs `gropius doctor`. It tells her what it can actually determine — the
runtime is healthy, the root is writable, this build is current, and another
account on this Mac is holding the port — and, for the firewall grant, it tells
her the truth: that it can see the entry is listed but cannot verify the grant
still covers this build, and here are the two commands that re-grant it.

When she is done with it, `gropius uninstall` removes the app, the runtime, and
the firewall entry that every current instruction forgets. It leaves the models
she downloaded, because those are the expensive thing, and tells her how much
space they take and what to run if she wants them gone too.

## Why This Matters

The install is the only part of Gropius most people will ever judge it by, and
today it is the least considered part of it. Provisioning happens invisibly
after the terminal has already exited, behind a banner that reports no
proportion and no size and offers no retry — the documented remedy for a failed
setup is to quit the app and open it again. Nothing removes the firewall entry.
Nothing tells anyone why the LAN sees an empty response. The shell script that
does the work cannot be tested, cannot report progress, and cannot be reused by
the product itself.

## Mechanism

We expect visible foreground provisioning to fix abandonment because the
failure being reported is opacity rather than duration: the current banner
gives no proportion complete, states no size anywhere in the documentation, and
offers no retry control, so a person cannot distinguish "working" from "stuck"
at any point in a multi-minute install. We are wrong if people abandon installs
at the same rate once progress is visible, which would mean the wait itself is
the problem and the answer is to make it smaller — pre-warming, a smaller
default runtime — rather than to narrate it.

## Scope Conditions

- macOS 26 or later, which both bundles already declare as their floor and
  which the installer already refuses below.
- Apple Silicon for the server. The client is universal and is out of scope for
  this record.
- Ad-hoc-signed, un-notarised builds with no Developer ID. Every criterion
  about the firewall grant, the authorisation panel and the un-verifiable
  doctor check exists because of this and is expected to change if the
  membership is ever bought.
- Installation by the documented bootstrap. A bundle placed by any other route
  is expected to work but is not what these criteria are written against.
- Both install destinations are in play, and they are not symmetric: the system
  applications directory holds ONE bundle that every account on the Mac
  launches, while the per-user fallback is genuinely per-account.
- Shared Macs with several accounts, including fast user switching, where a
  second server does not start but becomes a client of the one already running.
- One release published at a time, so there is no rollback target and no way to
  fetch the version currently installed.
- The verbs are reached through a per-user link, so they are as available as
  that account's own bin directory is — which is not a given on every Mac.

## Acceptance Criteria

**Installing**

- Given a Mac with no Gropius on it, when Alice runs the documented one-line
  install, then the bundle is placed, the firewall grant is requested once
  through the system authorisation panel with a stated reason, the MLX runtime
  is provisioned with progress that reports proportion rather than a spinner,
  and Gropius is serving when the command returns.
- Given the bootstrap has downloaded and verified the archive, when it hands
  over to the binary, then it executes the binary from the directory it
  verified rather than from the bundle it just installed, so that the bootstrap
  and the binary it calls are always the same build.
- Given a bundle whose binary predates the lifecycle verbs, when the bootstrap
  hands over to it, then the run fails with a message naming the mismatch, and
  in no case does a handover start a server.
- Given standard input is the installer script itself, as it is under a piped
  shell, when any verb would otherwise prompt, then nothing reads standard
  input: the run either uses the system authorisation panel, or refuses and
  names the flag that would have answered it.
- Given the install completes, when it puts the verbs within reach, then it
  creates a link in this account's own bin directory and elevates for nothing:
  the authorisation panel exists for the firewall, which is a machine-wide
  setting with no per-account equivalent, and a per-user link has one.
- Given that bin directory is not on this account's search path, when the
  install finishes, then it says so and names the line that would put it there,
  rather than leaving a link that resolves for nobody.
- Given a Mac where provisioning already completed, when Alice runs the install
  again, then it repairs what is missing rather than reinstalling what is not,
  and says which of the two it did.
- Given provisioning fails part way, when the command returns, then it exits
  non-zero, says which stage failed and why, and names the command that retries
  it. The app's own check on the next launch may still repair it and report
  ready; the terminal's verdict is what was true when it exited and is not
  revisited.

**Reporting**

- Given something calls it repeatedly — the menu bar, a script, a person — when
  `gropius status` runs, then it answers only what is true right now: serving or
  not, on which address, which models are resident. It runs no check that costs
  more than reading state that already exists, so that polling it is safe.
- Given a caller that parses rather than reads, when `gropius status --json`
  runs, then the same facts are emitted as machine-readable output, and that
  output is the contract rather than the human text.
- Given a Mac where the LAN receives empty responses, when Alice runs
  `gropius doctor`, then it runs the expensive checks and the report
  distinguishes what was verified from what was only observed. The firewall
  entry is reported as observed and never as a verdict, because the system
  query answers "permitted" for a path that has no entry and for a path that
  does not exist — so a check built on it would confirm a healthy grant at
  exactly the moment an update invalidated one.
- Given a check that cannot be determined at all, of which Local Network
  Privacy is the known case, when doctor reports, then it says the state cannot
  be determined from here rather than omitting the check or guessing it.
- Given another account on the Mac is holding the port, when doctor runs, then
  it reports that, because the version this build reports is its own and not
  necessarily the version being served.
- Given doctor's output is written to be pasted into a bug report, when it
  prints paths and accounts, then home directories are abbreviated and other
  accounts are counted rather than named.
- Given doctor finds only warnings, when it exits, then it exits zero, and the
  severity is carried in the machine-readable output rather than inferred from
  the exit code.

**Removing**

- Given Alice runs `gropius uninstall`, when it completes, then the bundle, the
  runtime, the configuration, the firewall entry and the per-user link are
  gone, the downloaded models are still there, and the output states their
  total size and the flag that would have removed them.
- Given `--purge`, when standard input is not a terminal and `--yes` was not
  passed, then nothing is deleted and the refusal names the flag.
- Given a shared-cache installation, when Alice runs uninstall, then the shared
  root is not touched at all, and the output names what remains there, its
  size, and the one deliberate command that removes it. Removing another
  account's models is a separate act, not the unelevated half of this one.
- Given any removal, when it runs, then it never elevates, never invokes an
  external removal command, and never derives a deletion path from an
  environment variable or a flag, because a directory that any local account
  can pre-create as a symlink would otherwise choose what is deleted.

**Holding the boundary**

- Given any lifecycle verb, when it invokes a system tool, then the tool is
  named by absolute path — every one of them, not only those an attacker could
  reach. Two records make that the rule rather than the cautious reading:
  iss-2609081435387952, where a group-writable directory on a shared Mac is an
  account-to-account boundary, and iss-9, where a release election silently got
  a different `grep` than it meant with no attacker involved at all.
- Given a bundle is being replaced, when the swap runs, then the staging name
  is unguessable, the installed bundle is renamed aside before the new one is
  moved into place, and the set-aside copy is removed only after the new one is
  in place, so that no failure leaves the Mac with no application. The rename
  itself must be one that refuses an existing directory and replaces a symlink
  rather than following it, which the shell cannot express and which is why
  this criterion lives here rather than in the installer script.
- Given the lifecycle verbs, when their packages are placed, then none is
  reachable from the HTTP control plane, so that no route can ever drive a
  self-replacement or an elevation.

## Open Questions

- **The enforcement tests for the last two boundary criteria are deliberately
  not in the first cut.** Both want an architecture test that walks the tree,
  and `internal/archtest` currently holds seven hand-rolled walkers that
  disagree about what to exclude — a problem recorded as iss-2609081427104462,
  with iss-2609081441311030 for the one that walks out of the repository
  entirely. Writing these two now makes an eighth and ninth. The order that
  costs one conversion instead of two: the in-flight change to that package
  lands, the shared walker replaces the hand-rolled exclusions, and these
  criteria are then written against it from the start. Until then the two
  criteria are held as behaviour with no armed detector, and this note is the
  record saying so rather than a gap nobody declared.
- **The doctor carve-out needs its own decision before the spec is written.**
  Reporting the firewall entry as observed is a detection over state Gropius
  does not own, and adr-2609081118587999 closes a warning's firing condition to
  exactly that class of signal. The argument for a carve-out has to stand on
  its own: a diagnostic a person invokes deliberately, that changes no
  behaviour, admits no request and gates nothing, is not a warning firing on
  inferred state — it is a report of what was seen, labelled as such, next to
  the commands that would settle it. That reasoning is untested. It cannot lean
  on the way the installer's authorisation panel avoided the same tension,
  because that avoided it by never reading the state at all, which a diagnostic
  cannot do and remain a diagnostic.

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._
