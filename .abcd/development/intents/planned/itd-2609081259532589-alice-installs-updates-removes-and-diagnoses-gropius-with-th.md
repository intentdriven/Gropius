---
id: itd-2609081259532589
slug: alice-installs-updates-removes-and-diagnoses-gropius-with-th
spec_id: spc-2609111029315861
kind: standalone
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
runtime is healthy, the root is writable, this is build *X*, and another
account on this Mac is holding the port — and, for the firewall grant, it tells
her the truth: that it can see the entry is listed but cannot verify the grant
still covers this build, and here are the two commands that re-grant it. It
says which build this is by reading itself, and it contacts nothing to do it:
whether that build is the current one is a question for `gropius update`, which
is a record of its own and which asks the network nothing until Alice turns it
on.

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
at any point in a multi-minute install. We are wrong if the installs Alice, Bob
and Carol report as stuck are reported at the same stage and at the same
frequency once the progress line names a proportion, which would mean the wait
itself is the problem and the answer is to make it smaller — pre-warming, a
smaller default runtime — rather than to narrate it.

The evidence for that is anecdotal by policy, and the record says so rather
than implying a measurement it will never have. An abandonment rate needs
instrumentation of the install, and adr-2609061503319212 refuses it: Gropius
makes no outbound connection beyond fetching models and provisioning the
runtime. So the falsifier is a report the maintainer collects by asking the
people who installed it — at which stage, and how often — not one the product
collects by watching them.

## Scope Conditions

- macOS 26 or later, which both bundles already declare as their floor and <!-- cond: cond-2609111029317707 -->
  which the installer already refuses below.
- Apple Silicon for the server. The client is universal and is out of scope for <!-- cond: cond-2609111029319670 -->
  this record.
- Ad-hoc-signed, un-notarised builds with no Developer ID. Every criterion <!-- cond: cond-2609111029317790 -->
  about the firewall grant, the authorisation panel and the un-verifiable
  doctor check exists because of this and is expected to change if the
  membership is ever bought.
- Installation by the documented bootstrap. A bundle placed by any other route <!-- cond: cond-2609111029314175 -->
  is expected to work but is not what these criteria are written against.
- Both install destinations are in play, and they are not symmetric: the system <!-- cond: cond-2609111029312031 -->
  applications directory holds ONE bundle that every account on the Mac
  launches, while the per-user fallback is genuinely per-account.
- Shared Macs with several accounts, including fast user switching, where a <!-- cond: cond-2609111029311470 -->
  second server does not start but becomes a client of the one already running.
- One release published at a time, so there is no rollback target and no way to <!-- cond: cond-2609111029315634 -->
  fetch the version currently installed.
- The verbs are reached through a per-user link, so they are as available as <!-- cond: cond-2609111029317614 -->
  that account's own bin directory is — which is not a given on every Mac.
- Shared-cache mode splits the data root in two, and the split is not where the <!-- cond: cond-2609111029317056 -->
  planning brief assumed. `config.DefaultRoot` does return the shared directory
  once an administrator has created it, but `config.accountDir` sends
  everything belonging to one account back to that account's own Application
  Support directory: the configuration file, the registry, the private Python
  runtime, the logs and the statistics store. What lives in the shared root is
  the models and the download cache they arrive through — the expensive thing,
  and the thing this intent's criteria already leave in place. Three of the
  removing criteria turn on that split, and they are written against the code
  rather than against the assumption.

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
- Given a shared-cache installation, when Alice runs uninstall, then this
  account's own directory is removed — its configuration, registry, runtime,
  logs and statistics all live there and not in the shared root — the shared
  root itself is not touched, and the output names what remains in it, its
  size, the accounts it belongs to counted rather than named, and the one
  deliberate command that removes the root.
- Given a shared-cache installation and `--purge`, when the models are removed,
  then only the files this account owns are removed and nothing another
  account's are, which the sticky bit on the shared directory's `3775` mode
  makes the only removal the filesystem permits anyway; the output then
  separates what it deleted from what it could not, with the size of each and
  the accounts the remainder belongs to counted rather than named. Removing
  another account's models is a separate act, not the unelevated half of this
  one.
- Given any file or directory removal, when it runs, then it never elevates,
  never invokes an external removal command, and never derives a deletion path
  from an environment variable or a flag, because a directory that any local
  account can pre-create as a symlink would otherwise choose what is deleted.
- Given `GROPIUS_ROOT` or `-root` names a data root, when uninstall runs, then
  that root is never a deletion path: the removal acts only on the fixed
  locations this account's install actually uses, and the output names the root
  it did not remove and what is still in it. This follows from the criterion
  above rather than qualifying it, and is written out because a reader would
  otherwise expect the variable to be honoured.
- Given the firewall entry is machine-wide state that no per-account route can
  remove, when uninstall removes it, then it raises exactly one system
  authorisation panel, states there why it is asking, and a refusal leaves
  everything else removed and reports the entry as the one thing that remains
  with the command that removes it. That panel is the single exception to the
  criterion above, which governs deletion paths on this Mac's filesystem; no
  other step of any lifecycle verb elevates.

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

- **What the manual authorisation-panel procedure is, exactly.** The headline
  installing criterion cannot be proven by the suite: a runner has no console
  for a system authorisation panel, `install.sh` skips both privileged steps
  under CI and prints two warnings saying a green run proves nothing about
  them, and that shape is what produced iss-2609080855033159. So the evidence
  for "installs cleanly, grants the firewall once, provisions with progress" is
  a run a person makes, across an administrator account and a standard account
  on a real Mac, and the spec carries a written procedure instead of a test.
  What is not settled is the procedure itself: what it checks in what order,
  where its result is recorded so a reader can see when it was last run and
  against which build, and whether a release is blocked on it or merely
  reported beside it. Every other criterion here is armed by a test the spec
  names.

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._

## Grounds

- pursued: visible foreground provisioning fixes abandonment because the reported failure is opacity rather than duration — today's banner names no proportion, no size and no retry, so nobody can tell working from stuck; wrong if the installs Alice, Bob and Carol report as stuck come at the same stage and frequency once the progress line names a proportion, a report collected by asking rather than by instrumenting, since adr-2609061503319212 forbids the telemetry that would yield a rate — planned autonomously on the maintainer's instruction of 2026-09-10, adopting the brief's recommendations
