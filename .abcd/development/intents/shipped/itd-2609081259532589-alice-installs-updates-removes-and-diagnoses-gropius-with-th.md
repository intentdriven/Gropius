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
- Shared-cache mode splits the data root in two, and not where a reader would <!-- cond: cond-2609111029317056 -->
  guess. `config.DefaultRoot` does return the shared directory once an
  administrator has created it, but `config.accountDir` sends everything
  belonging to one account back to that account's own Application Support
  directory: the configuration file, the registry, the private Python
  runtime, the logs and the statistics store. What lives in the shared root is
  the models and the download cache they arrive through — the expensive thing,
  and the thing this intent's criteria already leave in place. Three of the
  removing criteria turn on that split, and they are written against the code.

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
  it did not remove and what is still in it. This follows from the never-derive
  criterion above rather than qualifying it, and is written out because a reader would
  otherwise expect the variable to be honoured.
- Given the firewall entry is machine-wide state that no per-account route can
  remove, when uninstall removes it, then it raises exactly one system
  authorisation panel, states there why it is asking, and a refusal leaves
  everything else removed and reports the entry as the one thing that remains
  with the command that removes it. That panel is the single exception to the
  never-elevate criterion above, which governs deletion paths on this Mac's
  filesystem; no
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

<!-- abcd-review: INGESTED receipt=rcp-d73a16c4b607 -->
Fidelity review — receipt rcp-d73a16c4b607 (verifier intent-auditor claude-sonnet-5).

Provenance: intent-auditor@claude-sonnet-5 · rubric_hash sha256:d7fc71b29aaf52d0a961de933d2179a2fb650b6a0ace4af219fa51658abdc769 · prompt_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5
Input attestations: diff:PR #43 merge commit 59106e7c17ddaf87e4142db137e68e6bc19e7d9c (parents 6960f82, f7bc91a), diffed as 59106e7^1..59106e7, ancestor of HEAD d33dbc7 in worktree .claude/worktrees/next. policy.rubric_hash and policy.prompt_hash were not supplied by the host for this run and are auditor-computed: rubric_hash is the sha256 of .abcd/.work.local/reviews/rcp-d73a16c4b607.request.md as read from the worktree; prompt_hash is the sha256 of the intent-auditor agent definition at /Users/dev/.claude/plugins/marketplaces/abcd-marketplace/agents/intent-auditor.md@sha256:09298b44cf5acef62282240d71293704e846197c50e8e780cb4f67707edd7a7d;

Acceptance rollup: MET 22 · MET_WITH_CONCERNS 2 · NOT_MET 0 · INCONCLUSIVE 1

Per-criterion verdicts:
- ac-1 — INCONCLUSIVE: the spec itself says this compound end-to-end criterion has no automated test and is armed only by a written manual procedure; that procedure's results table is empty and its own text says the criterion rests on nothing but code review until a run is recorded
  evidence: .abcd/development/procedures/installer-authorisation-panel.md:83-85 — "| _not yet run_ | | | | | | | | | | | |"
  evidence: .abcd/development/procedures/installer-authorisation-panel.md:93-95 — "a release that has never been checked this way is a release whose headline installing criterion rests on nothing but the code review"
  evidence: internal/lifecycle/elevate.go:56-58 — "Gropius needs administrator rights to allow itself through the macOS firewall, so other machines on your network can reach it"
  evidence: internal/lifecycle/term.go:93-115 — "Step() renders a percentage, not a spinner"
- ac-2 — MET: install.sh executes the binary from the verified extraction directory, never the installed destination, and a test asserts the recorded exec path is the staging tree
  evidence: install.sh:234-243 — "VERIFIED_BIN="$tmp/extract/$APP.app/Contents/MacOS/gropius""
  evidence: internal/archtest/installer_handover_test.go:123-149 — "TestTheBootstrapHandsOverToTheBinaryItVerified fails if the executed path starts with /Applications/Gropius.app or $HOME/Applications"
- ac-3 — MET: install.sh dies naming the mismatch on an exit-2 handover and prints that nothing was launched, exercised by a stub binary
  evidence: install.sh:263-269 — "does not carry the lifecycle verbs: its binary refused `install` with exit 2 ... Nothing was launched."
  evidence: internal/archtest/installer_handover_test.go:177-193 — "TestTheBootstrapRefusesAnOldBinarysHandover asserts the network-reached sentinel was never touched"
- ac-4 — MET: no os.Stdin read exists anywhere in internal/lifecycle or install.sh (the only stdin touch is a TTY Stat check), and a scan test enforces it; uninstall's --purge path refuses and names the flag when consent cannot be obtained
  evidence: internal/archtest/lifecycle_removal_test.go:139-153 — "TestNoLifecycleVerbReadsStandardInput scans every lifecycle source line, whitelisting only isTerminal(os.Stdin)"
  evidence: internal/lifecycle/uninstall.go:143-146 — "Pass --yes to confirm it without a terminal: gropius uninstall --purge --yes"
- ac-5 — MET: linkCommand touches only home-derived paths via os.MkdirAll/Symlink/Rename with no exec.Command or elevation call anywhere in the file
  evidence: internal/lifecycle/binlink.go:43-59 — "linkCommand creates the per-user bin link with no subprocess or elevation call"
  evidence: internal/lifecycle/elevate.go:83-85 — "elevateFirewall is the ONLY place in this package that asks for administrator rights"
- ac-6 — MET: onSearchPath compares PATH entries by resolved identity and pathAdvice returns the exact export line to add, wired into the install verb and asserted by a table test
  evidence: internal/lifecycle/binlink.go:82-99 — "onSearchPath compares PATH entries via os.SameFile"
  evidence: internal/lifecycle/install_test.go:269-289 — "TestInstallSaysWhenTheCommandWillNotResolve asserts the advice line appears when PATH lacks the dir"
- ac-7 — MET: missingRuntimeParts is computed before provisioning and the outcome is reported as repaired-vs-nothing-to-do, backed by Ensure's idempotent early returns and a table test covering both branches
  evidence: internal/lifecycle/install.go:222-230 — "was already complete; nothing was reinstalled. / installed what was missing (< list>)."
  evidence: internal/lifecycle/install_test.go:136-168 — "TestInstallSaysWhetherItRepairedOrFoundNothingToDo covers both branches"
- ac-8 — MET: fail() writes the stage, the redacted cause and the retry command and returns ExitFailed; install.go states explicitly that nothing after a failed stage runs, and injected-failure tests confirm zero further panels/launches
  evidence: internal/lifecycle/install.go:296-303 — "gropius install: "+stage+" failed: ... Retry with: "+retryInstall"
  evidence: internal/lifecycle/install_test.go:419-477 — "TestInstallStopsAtTheStageThatFailed confirms zero launches on a link-stage failure"
- ac-9 — MET: StatusOf calls only the holder probe and, when the holder is ours, one loopback state read — no filesystem walk, no provisioning check — asserted by a call-count test
  evidence: internal/lifecycle/status.go:91-130 — "StatusOf calls env.Holder() and, only if HolderOurs, env.State()"
  evidence: internal/lifecycle/status_test.go:100-129 — "TestStatusAsksTheTwoCheapQuestionsAndNoOthers asserts State is called zero times on a non-ours holder"
- ac-10 — MET: status --json is a golden-JSON test comparing decoded values against committed fixtures, with the JSON struct documented as the contract and RenderStatus as a separate rendering of the same value
  evidence: internal/lifecycle/status_test.go:32-77 — "TestStatusJSONIsTheContract decodes both the marshalled Status and testdata/status_serving.json and compares via reflect.DeepEqual"
  evidence: internal/lifecycle/status.go:26-42 — "the JSON is the contract... the human rendering is a rendering of this same value"
- ac-11 — MET: the three-label scheme exists exactly as specified; checkFirewall is labelled Observed, issues no verdict, and Diagnose structurally overrides severity to undetermined for any non-Verified finding, adversarially tested
  evidence: internal/lifecycle/doctor.go:44-57 — "Verified / Observed / Undeterminable labels"
  evidence: internal/lifecycle/doctor.go:190-193 — "Diagnose forcibly overrides severity to SeverityUndetermined for any non-Verified finding"
  evidence: internal/lifecycle/doctor_test.go:174-196 — "TestAnObservedFindingCannotReportAFault uses an overreaching fake check to prove the override is real"
- ac-12 — MET: checkLocalNetwork is unconditional and always present with the undeterminable label and a stated remedy, and a test confirms it survives into the human-text rendering
  evidence: internal/lifecycle/doctor.go:350-358 — "cannot be determined from here — macOS offers no way to read this grant"
  evidence: internal/lifecycle/doctor_test.go:153-167 — "TestLocalNetworkPrivacyIsReportedAsUndeterminable"
- ac-13 — MET: checkPort's foreign branch reports the holder is not named and that the reported version is this build's own; a test asserts the count-not-name wording
  evidence: internal/lifecycle/doctor.go:292-313 — "it is not named here, and the build reported above is this one's own and not necessarily the build being served"
  evidence: internal/lifecycle/doctor_test.go:334-348 — "TestTheForeignPortHolderIsCountedAndNotNamed"
- ac-14 — MET: redact() replaces every occurrence of the home directory centrally over every summary and command, and other-account counting reuses the same not-named mechanism as ac-13, both covered by multiple tests including one against the committed golden fixture
  evidence: internal/lifecycle/doctor.go:413-420 — "redact replaces every occurrence of the account's home directory with ~"
  evidence: internal/lifecycle/doctor_test.go:781-790 — "TestTheGoldenReportCarriesNoAccount checks testdata/doctor_report.json against this machine's home dir"
- ac-15 — MET: ExitCode returns non-zero only for a Verified+Failed finding, so every Observed/Undeterminable and every Verified-Warning finding exits zero, with severity carried in Finding's JSON field
  evidence: internal/lifecycle/doctor.go:220-227 — "ExitCode() returns non-zero only when Label == Verified && Severity == SeverityFailed"
  evidence: internal/lifecycle/doctor_test.go:201-247 — "TestExitCodeFollowsTheVerifiedChecksAlone table-tests all four cases"
- ac-16 — MET: removalTargets excludes Models/HFCache by construction, the firewall entry and per-user link are removed, and reportModels states the size and the --purge flag
  evidence: internal/lifecycle/uninstall.go:218-232 — "The data root itself is NOT on the list... because the models live inside it"
  evidence: internal/lifecycle/uninstall.go:263-265 — "The downloaded models are still there: ... Remove them too with: gropius uninstall --purge"
  evidence: internal/lifecycle/uninstall_test.go:75-100 — "TestUninstallRemovesTheApplicationAndLeavesTheModels"
- ac-17 — MET: the terminal/--yes gate is the first statement in runUninstall, before any removal, and names the flag on refusal
  evidence: internal/lifecycle/uninstall.go:143-148 — "Pass --yes to confirm it without a terminal: gropius uninstall --purge --yes"
  evidence: internal/lifecycle/uninstall_test.go:105-139 — "TestPurgeRefusesWithoutATerminalUnlessToldYes asserts models and bundle survive"
- ac-18 — MET: the account/shared-root split is real in config.go, reportSharedRoot names what remains, its size, an account count (never names) and the removal command, and a test with a separate account directory confirms the shared root and other account's file survive
  evidence: internal/config/config.go:203-236 — "accountDir / NewPaths split: models and HFCache resolve through root, everything account-owned through accountDir(root)"
  evidence: internal/lifecycle/uninstall.go:317-324 — "This Mac uses a shared model cache at ..., which uninstall never touches."
  evidence: internal/lifecycle/uninstall_test.go:205-242 — "TestSharedCacheUninstallLeavesTheSharedRootAlone asserts the other account's model name is not in the output"
- ac-19 — MET_WITH_CONCERNS: ownership-based purge filtering (purgeOwned/partitionByOwner) is real and the sticky-3775 backstop is a verified Makefile mode, but the spec's described test shape ('skips cleanly where it cannot create the fixture') does not exist in the code: there is no t.Skip anywhere in uninstall_test.go, and the multi-owner case is instead armed only through dependency-injected fake OwnerOf maps rather than a real multi-uid filesystem fixture with a skip escape hatch
  evidence: internal/lifecycle/uninstall.go:368-405 — "purgeOwned walks via os.Root and partitionByOwner splits mine/theirs"
  evidence: internal/lifecycle/uninstall_test.go:326-330 — "A fixture with two owners cannot be built without root — a test cannot give a file away — so the two halves are tested separately"
  evidence: internal/lifecycle/uninstall_test.go:247-280 — "TestSharedPurgeRemovesOnlyWhatThisAccountOwns injects a fake ue.OwnerOf rather than creating real multi-uid files"
- ac-20 — MET: uninstall.go imports no os/exec, every removal is in-process (os.RemoveAll/os.Remove/root.Remove), and symlink targets are refused rather than followed, armed by allowlist and stdin-scan tests plus a redirect-attack test
  evidence: internal/lifecycle/uninstall.go:237-250 — "removeLink checks fi.Mode()&os.ModeSymlink == 0 before removing"
  evidence: internal/lifecycle/uninstall_test.go:375-413 — "TestThePurgeCannotBeRedirectedByARenamedComponent swaps a directory for a symlink mid-walk and asserts the victim survives"
  evidence: internal/archtest/lifecycle_removal_test.go:48-95 — "TestTheLifecycleVerbsStartOnlyTheToolsTheyDeclare allowlist contains no removal tool"
- ac-21 — MET: InstalledRoot explicitly ignores GROPIUS_ROOT, which is captured only for reporting, and -root is not even wired to the uninstall verb's flag set; a test sets GROPIUS_ROOT to a decoy with a witness file and confirms it survives
  evidence: internal/config/config.go:99-107 — "It exists for the one caller that must not honour GROPIUS_ROOT — gropius uninstall"
  evidence: internal/lifecycle/uninstall.go:197-201 — "Uninstall never derives a deletion path from the environment, so nothing there was removed."
  evidence: internal/lifecycle/uninstall_test.go:286-324 — "TestGropiusRootIsNeverADeletionPath asserts the witness file survives"
- ac-22 — MET: elevateFirewall is documented and scanned as the sole elevation site, its prompt states the reason, and a refused panel still lets the rest of removal proceed while reporting the entry as remaining with the recovery command
  evidence: internal/lifecycle/elevate.go:60-62 — "Gropius needs administrator rights to remove its own entry from the macOS firewall. This is the last step of uninstalling it."
  evidence: internal/archtest/lifecycle_removal_test.go:103-130 — "TestTheLifecycleVerbsElevateInExactlyOnePlace greps for 'administrator privileges' outside elevate.go"
  evidence: internal/lifecycle/uninstall_test.go:144-165 — "TestUninstallSurvivesARefusedAuthorisation asserts ExitOK and the bundle still removed"
- ac-23 — MET: the Go side is covered repo-wide by the pinned-subprocess scan and every internal/lifecycle exec.Command call is already absolute-pathed; the shell side's widened test scans every command position in install.sh and the nine named bare commands are now all absolute-pathed
  evidence: internal/lifecycle/elevate.go:95,149,159 — "exec.Command("/usr/bin/osascript", ...) / exec.CommandContext(ctx, "/usr/bin/open", bundle)"
  evidence: internal/archtest/privileged_shell_test.go:120 — "TestInstallerPinsEveryCommandItRuns scans every command position in install.sh, exempting only shell builtins/keywords"
  evidence: install.sh:97 — "/usr/bin/sw_vers -productVersion"
- ac-24 — MET_WITH_CONCERNS: the rename-aside-then-in ordering, unguessable staging name, and delayed cleanup all match the promise and are covered by the three required behavioural cases, but a same-day open issue records that the file's own claim ('no failure path leaves the Mac with no application') is overstated against a co-resident admin-group actor who can delete the unbounded-wait staging directory, at no new privilege boundary but a real residual exposure the fix left unclosed
  evidence: internal/lifecycle/swap.go:91-96,118 — "installed bundle renamed aside first (Lstat then rename(dest, retired)); new bundle renamed in (rename(staged, dest))"
  evidence: internal/lifecycle/swap_test.go:67,88,124 — "TestSwapReplacesAnInstalledBundleRatherThanNestingInsideIt / TestSwapReplacesASymlinkRatherThanFollowingIt / TestSwapLeavesTheInstalledBundleWhenTheSecondRenameFails"
  evidence: .abcd/work/issues/open/iss-2609111755330533-internal-lifecycle-swap-go-claims-no-failure-path-leaves-the.md — "a co-resident admin-group account can delete the staging directory and leave no application for as long as the wait lasts... the file's claim is not strictly true against that actor"
- ac-25 — MET: a dependency-closure test proves neither internal/gateway nor internal/ui reach internal/lifecycle (and the reverse), and a completeness test forces every module package onto one side of the boundary or the other
  evidence: internal/archtest/lifecycle_boundary_test.go:69-79 — "TestTheControlPlaneCannotSeeTheLifecycleVerbs walks go list -deps on internal/gateway and internal/ui"
  evidence: internal/archtest/lifecycle_boundary_test.go:98-140 — "TestEveryPackageIsJudgedByTheBoundary fails loudly on any unjudged package"

Gap audit:
- honoured:
  - the lifecycle verbs (install/uninstall/status/doctor) replace the old shell-only flow and are unreachable from the control plane
    evidence: internal/archtest/lifecycle_boundary_test.go:69-140 — "TestTheControlPlaneCannotSeeTheLifecycleVerbs / TestEveryPackageIsJudgedByTheBoundary"
  - doctor reports observed/undeterminable state for the firewall and Local Network Privacy without ever issuing a verdict, per adr-2609111126115848's carve-out
    evidence: internal/lifecycle/doctor.go:190-193 — "Diagnose forcibly overrides severity to SeverityUndetermined for any non-Verified finding"
    evidence: internal/lifecycle/doctor_test.go:588-634 — "TestNoObservedLineIsPhrasedAsAConclusion"
  - uninstall removes the application and firewall entry but leaves the models, stating their size and the purge flag
    evidence: internal/lifecycle/uninstall.go:218-232,263-265 — "models excluded from removalTargets; size and --purge stated"
  - the two build-time defects (repair-path-skips-placement-check, live-verb-runner-reachable-from-tests) found during the merge were fixed rather than left open
    evidence: internal/lifecycle/install.go:171-178 — "there is nothing to repair at ... refuses before quit/panel/provisioning/launch"
    evidence: cmd/gropius/verbfakes_test.go:56-111 — "TestMain rewires verbEnvFor and lifecycleVerbs to fakes; TestTheLiveVerbsRefuseToRunInsideATest"
  - install.sh is shrunk to bootstrap-only work and the staged swap moves entirely into Go, closing iss-2609081310071028 and iss-2609081310119313
    evidence: internal/lifecycle/swap.go:91-118 — "os.Rename-based rename-aside-then-in sequence"
    evidence: internal/archtest/privileged_shell_test.go:120 — "TestInstallerPinsEveryCommandItRuns"
- diverged:
  - the spec's own prose for criterion 19's test says it 'skips cleanly where it cannot create the fixture'; the shipped test instead splits into a real single-owner filesystem read plus a pure-function test over injected fake ownership, with no t.Skip anywhere in the file
    evidence: internal/lifecycle/uninstall_test.go:326-330 — "A fixture with two owners cannot be built without root ... so the two halves are tested separately"
  - swap.go's own commentary states plainly that no failure path leaves the Mac with no application; a same-day open issue narrows that to a claim that is overstated against a co-resident admin-group actor during the unbounded staging-directory wait
    evidence: internal/lifecycle/swap.go:25-26 — "So no failure path leaves the Mac with no application"
    evidence: .abcd/work/issues/open/iss-2609111755330533-internal-lifecycle-swap-go-claims-no-failure-path-leaves-the.md — "the fix made the window unbounded"
  - the shell-side absolute-path test the spec names as TestInstallerPinsTheCommandsItTrusts shipped under the name TestInstallerPinsEveryCommandItRuns, with the same widened behaviour
    evidence: internal/archtest/privileged_shell_test.go:120 — "TestInstallerPinsEveryCommandItRuns"
- missing:
  - the headline installing criterion's designated evidence — a completed run of the manual authorisation-panel procedure on a real Mac, across an administrator and a standard account — has not been produced; the results table carries no row
    evidence: .abcd/development/procedures/installer-authorisation-panel.md:83-85 — "| _not yet run_ | | | | | | | | | | | |"

Scope-condition dispositions:
- cond-2609111029317707 — survived: the delivered install.sh still refuses below macOS 26 before any other work happens, matching the assumed floor
  evidence: install.sh:91-100 — "MIN_MACOS_MAJOR=26 ... die "$APP requires macOS $MIN_MACOS_MAJOR""
- cond-2609111029319670 — survived: install.sh still refuses a server install on non-Apple-Silicon hardware, checking both uname -m and the Rosetta-safe sysctl fallback, and does not touch the client's universal build
  evidence: install.sh:102-107 — "the Gropius server needs Apple Silicon (this Mac is $(/usr/bin/uname -m)). The GropiusChat client is universal"
- cond-2609111029317790 — survived: the delivered doctor/elevate machinery is built entirely around the ad-hoc-signed, un-notarised assumption: the observed/undeterminable labelling scheme and the firewall authorisation panel both exist because no Developer ID membership is assumed
  evidence: internal/lifecycle/doctor.go:48-52 — "an ad-hoc signature's designated requirement is its code-directory hash, so whatever was observed may already be about a bundle that is gone"
- cond-2609111029314175 — survived: the delivered criteria (handover, verified-binary execution, version-mismatch refusal) are all built around and exercised through the documented bootstrap path (install.sh); no alternate placement route is tested or claimed
  evidence: install.sh:234-245 — "VERIFIED_BIN=... handover executed against it"
- cond-2609111029312031 — survived: the delivered code still treats /Applications and $HOME/Applications as asymmetric destinations, choosing the per-user fallback only when the machine-wide directory is not writable
  evidence: install.sh:208-213 — "if [ -w /Applications ]; then DEST="/Applications" ... else DEST="$HOME/Applications""
- cond-2609111029311470 — survived: the delivered instance package still classifies a second account's attempt as HolderForeign rather than starting a competing server, and status/doctor both report against that classification
  evidence: internal/instance/instance.go:39-42 — "HolderForeign: something else owns the port and could not prove it is this"
  evidence: internal/lifecycle/status.go:98-105 — "case instance.HolderForeign"
- cond-2609111029315634 — untested: install.sh still resolves only releases/latest with no rollback mechanism, consistent with the assumption, but nothing in this delivery exercises or contradicts the one-release-at-a-time assumption directly — it is unaffected background state rather than something this PR's tests touch
- cond-2609111029317614 — survived: the verbs are reached exclusively through the per-user bin link created by binlink.go, and the delivered code itself reports when that directory is not on PATH rather than assuming it always is
  evidence: internal/lifecycle/binlink.go:107-115 — "pathAdvice returns the export line, home-relativized"
- cond-2609111029317056 — survived: config.go's accountDir/NewPaths split matches the condition exactly: models and the HF cache stay in the shared root, while configuration, registry, runtime, logs and statistics resolve through the account directory
  evidence: internal/config/config.go:203-236 — "accountDir(root) ... What is shared and what is this account's own is the whole of the split"
## Grounds

- pursued: visible foreground provisioning fixes abandonment because the reported failure is opacity rather than duration — today's banner names no proportion, no size and no retry, so nobody can tell working from stuck; wrong if the installs Alice, Bob and Carol report as stuck come at the same stage and frequency once the progress line names a proportion, a report collected by asking rather than by instrumenting, since adr-2609061503319212 forbids the telemetry that would yield a rate — planned autonomously on the maintainer's instruction of 2026-09-10, adopting the brief's recommendations
