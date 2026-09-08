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
- Given a Mac where provisioning already completed, when Alice runs the install
  again, then it repairs what is missing rather than reinstalling what is not,
  and says which of the two it did.
- Given provisioning fails part way, when the command returns, then it exits
  non-zero, says which stage failed and why, and names the command that retries
  it — rather than leaving the app to report the failure later in a banner.

**Diagnosing**

- Given a Mac where the LAN receives empty responses, when Alice runs
  `gropius doctor`, then the report distinguishes what was verified from what
  was only observed, and the firewall entry is reported as observed rather than
  verified, because the system query answers "permitted" for a path that has no
  entry and for a path that does not exist.
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
  runtime, the configuration and the firewall entry are gone, the downloaded
  models are still there, and the output states their total size and the flag
  that would have removed them.
- Given `--purge`, when standard input is not a terminal and `--yes` was not
  passed, then nothing is deleted and the refusal names the flag.
- Given a shared-cache installation, when Alice runs uninstall, then files
  belonging to another account are not removed and the command says what it
  left behind and why, rather than reporting a success that removed half a
  directory.
- Given any removal, when it runs, then it never elevates, never invokes an
  external removal command, and never derives a deletion path from an
  environment variable or a flag, because a directory that any local account
  can pre-create as a symlink would otherwise choose what is deleted.

**Holding the boundary**

- Given any lifecycle verb, when it invokes a system tool, then the tool is
  named by absolute path, so that what runs does not depend on which directory
  a caller's search path happens to resolve first.
- Given the HTTP control plane, when its routes are enumerated, then no
  lifecycle verb is reachable from it, and a test fails if a lifecycle package
  becomes reachable from the gateway's dependency graph.
- Given a bundle is being replaced, when the swap runs, then the staging name
  is unguessable, the installed bundle is renamed aside before the new one is
  moved into place, and the set-aside copy is removed only after the new one is
  in place, so that no failure leaves the Mac with no application.

## Open Questions

- Whether the lifecycle verbs are reachable as `gropius` on the search path at
  all, or only at the binary's path inside the bundle. A symlink needs
  elevation, has to choose between two install destinations, and becomes a
  fourth thing uninstall must clean up.
- Whether `doctor` may report at all on a signal it cannot verify, or whether
  the accepted decision on environment detection (adr-2609081118587999) reads
  the firewall query as a warning's firing condition and closes it. The
  criteria above take the first reading — report as observed, never verdict —
  and that reading needs recording as a decision rather than assuming.

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._
