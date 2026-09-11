---
id: spc-2609111029315861
slug: alice-installs-updates-removes-and-diagnoses-gropius-with-th
intent: itd-2609081259532589
origin: researcher-authored
production_mode: hand-written
---
# alice-installs-updates-removes-and-diagnoses-gropius-with-th

## Summary

Four lifecycle verbs on the binary Gropius already ships — `install`,
`uninstall`, `status`, `doctor` — with the shell script shrunk to the part that
has to happen before a Go binary exists. Provisioning moves into the
foreground, where a person can watch it; removal becomes one command that knows
about the firewall entry; and the two states nobody can query honestly get said
so rather than guessed.

The verbs live in one new package that `cmd/gropius` imports and nothing on the
control plane's path does, which is what keeps a route from ever driving a
self-replacement or an elevation.

## Depends on

- **adr-2609111126115848** settles what doctor may say about the firewall entry
  and the Local Network Privacy grant, and imposes two of the tests below
  (closure, wording) as conditions rather than as extras. Without it the doctor
  criteria are closed by adr-2609081118587999 rule 2.
- **`internal/archtest/pinned_subprocess_test.go`** and
  **`internal/archtest/walk_test.go`** are on `main`. The Go half of criterion
  23 is therefore already armed for any package this spec adds, and no scan
  here rolls its own tree walk.
- **itd-2609081259493890** (three surfaces, in sync) owns the Go/panel parity
  obligation. `status` is the first verb that makes a parity test possible, so
  that intent is planned first or alongside; this spec does not implement the
  parity test.
- **iss-2609081310071028** (the shell swap is not atomic) and
  **iss-2609081310119313** (`install.sh` resolves tools through PATH) are
  closed by this work rather than worked beside: both are defects in the part
  of `install.sh` that moves into Go.

## Scope

**In.** A new `internal/lifecycle` package holding install, uninstall, doctor
and status, with the decisions in them expressed as pure functions over values.
Subcommand dispatch in `cmd/gropius`. Foreground provisioning reported through
`runtime.SetupStatus`. A shrunken `install.sh`. The per-user bin link and the
search-path advice. `status --json` as the machine contract. Doctor's
observed-versus-verified labelling and its redaction. Uninstall, `--purge` and
the shared-root reporting. The tests named below. Two documentation pages, and
the replacement of `docs/getting-started.md`'s Uninstalling section.

**Out.** `update`, which is itd-2609081420471761 and blocked on its own
question. Any release check, which is opt-in, off by default, and belongs to
that intent (2026-09-08, under adr-2609061503319212). The control panel's
banner text and retry control beyond the `SetupStatus` seam this spec feeds.
The chat client. A terminal-UI dependency: the output is hand-written by
decision, and a toolkit would need sign-off.

## Approach

### The package, and why it is called `internal/lifecycle`

One package, `internal/lifecycle`, holding all four verbs. The name says what
the package is about — the installation's life, not the model server's — and it
collides with nothing: `internal/runtime` is the MLX subprocess, `internal/app`
is the running server's state, and the self-test work in flight adds a
`selftest` directory and a `config.self_test` field that this spec does not
touch. `internal/install` was considered and rejected: three of the four verbs
are not installation, and a package named for one of its verbs invites the
other three to be put somewhere else.

The verbs sit at the edge of the package as thin shells. Everything that
decides anything is a pure function over values: what to remove given a
resolved layout, what to report given a set of check results, what to print
given a terminal's capabilities, whether a bin directory is on a given PATH
string. That is what lets nearly every criterion be tested without a Mac, a
panel or a network — and it is the same shape the bind-plan spec
(spc-2609081750378874) used for the same reason.

**Nothing on the control plane's path may import it.** `internal/gateway`,
`internal/ui`, `internal/app` and `internal/config` import nothing from
`internal/lifecycle`; the dependency runs one way, from `cmd/gropius` down.
This is criterion 25 and it is armed rather than asserted (below).

### Dispatch in `cmd/gropius`

`refuseUnknownArgs` today refuses any first argument at all, and its own
comment names `gropius install` as the case it exists for. It narrows to
refusing an **unknown verb**:

- no arguments → run the server, unchanged, because macOS launches the bundle
  with none;
- a first argument beginning with `-` → flags, unchanged, so `-headless` and
  `-root` keep working permanently;
- a first argument matching a known verb → dispatch to `internal/lifecycle`
  (plus `serve` as the explicit form of the bare invocation, and `version`);
- anything else → the existing refusal, naming the first offending argument
  only, with exit code 2 as today.

The mapping from a command line to "server, verb, or refusal" is a pure
function; `main` acts on what it returns.

### Provisioning progress through `runtime.SetupStatus`

`internal/runtime` already carries the stages and a `Provisioner.Status()`
returning `SetupStatus{Stage, Detail, Err}`. The terminal renderer consumes
that same value, so the words on the terminal and the words in the panel cannot
drift: one source, two renderers. Where the criterion asks for proportion
rather than a spinner, the proportion is a field on the status the panel gains
at the same time — a Go capability with no panel equivalent is a gap, not a
feature tier.

`Provisioner.Ensure` is already idempotent, which is what makes "repair rather
than reinstall" (criterion 7) a report of what `Ensure` did rather than a
second code path.

### Terminal output

Hand-written, about eighty lines, no new dependency: a terminal check, a colour
helper that emits escape codes only when the stream is a terminal and `TERM` is
not `dumb` and `NO_COLOR` is unset, and a progress line. Progress goes to
standard error so `status --json` stays clean for a pipe. `ACCESSIBLE` selects
the same plain, one-line-per-step path that a non-terminal stream selects,
because a redrawn region is re-announced by a screen reader.

**Nothing reads standard input.** Under `curl … | bash` the script's remaining
text *is* standard input, so a read corrupts the run. A verb that needs consent
either raises the system authorisation panel or refuses and names the flag that
would have answered it.

### What `install.sh` keeps

Fetch the archive, verify it against the release's published checksums, clear
the quarantine attribute, place the bundle, hand over to the binary **in the
directory it verified** — never to the bundle it just installed, so the
bootstrap and the binary it calls are always the same build. Every tool it
invokes is named by absolute path, including the nine that are still bare
today (`mv`, `open`, `mkdir`, `curl`, `gh`, `sleep`, `seq`, `sw_vers`,
`sysctl`).

The staged swap leaves the shell entirely. It is rewritten in Go rather than
ported, because the shipped sequence removes the installed bundle and only then
renames the staged one into place: a destination that exists as a directory
ends up with the staged bundle nested inside it, and a destination that is a
symlink is written through and left standing, both exiting successfully
(iss-2609081310071028). The Go rename refuses an existing directory and
replaces a symlink rather than following it, renames the installed bundle aside
first, and removes the set-aside copy only once the new one is in place.

### The per-user bin link

The install creates a link in this account's own bin directory and elevates for
nothing to do it. Whether that directory is on this account's search path is a
pure function of a PATH string; when it is not, the install says so and names
the line that would put it there.

### `status --json` is the contract

`status` answers only from state that already exists — serving or not, on which
address, which models are resident — so that the menu bar and scripts can poll
it. The JSON is the contract; the human text is a rendering of the same value
and may change. It runs no check that costs more than a read: that is what
makes it different from `doctor`, and it is why the carve-out in
adr-2609111126115848 does not reach it.

### Doctor: verified, observed, undeterminable

Three labels, and every check carries exactly one.

- **Verified** — the runtime, the root's writability, the configuration
  parsing, the port's holder. Gropius owns this state; a severity here means
  what it says.
- **Observed** — the firewall entry. The system query answers "permitted" for a
  path with no entry and for a path that does not exist, so the line reports
  what the query returned, says it cannot establish that the grant still covers
  this build, and prints the two commands that re-grant it. It issues no
  verdict in either direction.
- **Cannot be determined from here** — Local Network Privacy, which has no
  query interface at all, is not part of TCC, and cannot be reset or
  pre-seeded. The check is reported, never omitted and never guessed.

Severities live in the JSON, not in the exit code: warnings exit zero. Output
is written to be pasted into a bug report, so home directories are abbreviated
and other accounts are counted rather than named.

### Uninstall

Removes the bundle, the private runtime, the configuration, the registry, the
logs, the per-user link and the firewall entry; leaves the downloaded models
and states their total size and the flag that would have removed them.

Every deletion path is derived from the fixed locations this account's install
actually uses — never from `GROPIUS_ROOT`, never from a flag — because a
directory any local account can pre-create as a symlink would otherwise choose
what is deleted. Removal is done in-process; no external removal command is
invoked, and nothing elevates.

The single exception is the firewall entry, which is machine-wide state with no
per-account route: one system authorisation panel, with the reason stated, and
a refusal that leaves everything else removed and reports the entry as the one
thing remaining, with the command that removes it.

Under shared-cache mode the account's own directory is what goes: `accountDir`
keeps the configuration, registry, runtime, logs and statistics in this
account's Application Support directory, and the shared root holds only the
models and the download cache. So the shared root is not touched, and the
output names what remains in it, its size, the accounts it belongs to counted
rather than named, and the one deliberate command that removes it. With
`--purge` in that mode only files this account owns can be removed — the sticky
bit on `3775` makes that the only removal the filesystem permits — and the
output separates what was deleted from what was not.

> **Correction to the planning brief.** The brief added a scope condition
> saying shared-cache mode moves the *whole* data root, and rewrote the
> shared-root criterion on that basis. `internal/config` says otherwise:
> `DefaultRoot` returns the shared directory, but `accountDir` sends everything
> belonging to one account back to that account's own directory. The intent's
> scope condition and criteria 18 and 19 are written against the code.

## How this satisfies the Acceptance Criteria

Criterion numbers follow the intent's order within its four groups.

**Installing**

1. **Installs cleanly, one panel, real progress** — *no automated test, by
   nature.* A runner has no console for an authorisation panel, and `install.sh`
   already skips both privileged steps under CI while printing two warnings
   that a green run proves nothing about them. This criterion gets a **written
   manual procedure** instead: run on a real Mac from an administrator account
   and from a standard account, check the panel appears once with its reason,
   the progress line names a proportion, and the server answers when the
   command returns. What the procedure records, and whether a release is
   blocked on it, is the intent's one open question and must be settled before
   this criterion is called met.
2. **Handover from the verified directory** — the stubbed-binary harness: a
   temporary directory holding a stub that records the path it was executed
   from, plus a scan of `install.sh`, asserting the recorded path is the
   verified staging directory and not the installed bundle. This is the trick
   the release gate already uses for `open`.
3. **Old binary refuses the handover** — the same harness with a stub exiting 2
   the way today's `refuseUnknownArgs` does; the bootstrap must report a named
   version mismatch and must not start a server. Asserted on the bootstrap's
   reading of the exit, since the refusal itself is shipped.
4. **Nothing reads standard input** — the installer scan, plus the gate that
   already runs the README's real piped form; the assertion is that no verb's
   consent path reads `os.Stdin`, which is a closure over the lifecycle package
   and a scan of the script.
5. **Per-user link, elevates for nothing** — pure functions over a temporary
   home: the link's location and the absence of any elevation on that path.
   Paired with the scan in criterion 20's test, which is what proves "for
   nothing".
6. **Not-on-PATH advice** — table test over PATH strings: present, absent,
   present with a trailing separator, present as a symlink to the same
   directory.
7. **Repair versus reinstall** — temp-root test over `Provisioner.Ensure`: a
   complete root repairs nothing and says so; a root with one piece missing
   repairs that piece and says which.
8. **Partial failure exits non-zero with a stage and a retry** — injected
   failure per stage; asserts exit code, the named stage, and the retry command
   in the output.

**Reporting**

9. **`status` is cheap** — a test that calls it against a fake state and
   asserts no provisioning check, no filesystem walk of the model directory and
   no network call is made; the seams are the ones `internal/app` already
   exposes.
10. **`status --json` is the contract** — golden JSON, compared as a decoded
    value against a fixture, so a field rename is a visible diff rather than a
    silent break in the menu bar.
11. **Verified versus observed** — table over a fake check set: every result
    carries exactly one of the three labels, and the firewall result carries
    "observed" plus its two commands. Paired with the **wording test** that
    adr-2609111126115848 condition 1 requires: no observed line is phrased as a
    conclusion.
12. **Undeterminable is reported, not omitted** — same table: the Local Network
    Privacy check is present in both renderings with the undeterminable label.
13. **Another account holds the port** — pure function over `probePortHolder`'s
    three classifications (ours, foreign, none), asserting the reported line
    and that the version doctor prints is its own.
14. **Paste-safe** — table over paths and account lists: home directories
    abbreviated, other accounts counted rather than named.
15. **Warnings exit zero** — table over check sets: severities appear in the
    JSON, the exit code is zero while no verified check failed.

**Removing**

16. **Uninstall removes the app and leaves the models** — temp-root test: the
    bundle, runtime, configuration, registry, logs and link are gone, the model
    directory is untouched, and the output carries its total size and the
    `--purge` flag.
17. **`--purge` refuses without a terminal** — table over (terminal, `--yes`):
    nothing is deleted unless one of the two is present, and the refusal names
    the flag.
18. **Shared-cache uninstall** — temp-root test with a fake shared root and a
    separate account directory: the account directory goes, the shared root is
    untouched, and the output names what remains, its size, a count of accounts
    and the deliberate command.
19. **Shared-cache `--purge` removes only what this account owns** — same
    fixture with files owned by two uids; asserts nothing another uid owns is
    removed and that the output separates deleted from retained with sizes.
    Ownership is read from the filesystem, so this test skips cleanly where it
    cannot create the fixture rather than asserting on a guess.
20. **Never elevates, never shells out, never derives a path from the
    environment** — a scan over `internal/lifecycle` for the elevation helper
    and for any external removal command, plus the symlink case of the swap
    test: a removal target that is a symlink is not followed.
21. **`GROPIUS_ROOT` is never a deletion path** — a test that sets the variable
    to a temporary directory holding a decoy, runs uninstall, and asserts the
    decoy survives and the output names the root it did not remove.
22. **Exactly one authorisation panel** — a scan asserting a single elevation
    site in the package, plus a behavioural test with the elevation seam
    returning refusal: everything else is still removed, and the firewall entry
    is reported as remaining with its command.

**Holding the boundary**

23. **Every system tool by absolute path** — the Go half is already armed by
    `internal/archtest/pinned_subprocess_test.go`, which scans every non-test
    Go file that imports `os/exec`, so the new package is covered the day it
    exists. The shell half is armed by **widening
    `TestInstallerPinsTheCommandsItTrusts`** from its five-name allowlist
    (`shasum`, `ditto`, `xattr`, `mktemp`, `pgrep`) to "every command not
    written as an absolute path", with a recorded, commented exemption list for
    shell builtins and keywords. That widening is what closes
    iss-2609081310119313.
24. **The swap** — a behavioural test in a temporary directory, which is what
    no tree walk can observe: destination pre-created **as a directory**,
    destination **as a symlink** to somewhere else, and the second rename made
    to fail. Asserts in each case that no state leaves the Mac without an
    application, that the symlink is replaced rather than followed, and that
    the staging name is unguessable.
25. **No lifecycle package on the control plane's path** — a **dependency
    closure test** in `internal/archtest`, in the shape
    `enforcement_detection_test.go` already uses: `go list -deps` over each
    control-plane package asserting `internal/lifecycle` is absent, plus a
    companion list putting **every** package in the module on one side of the
    rule or the other, so a package added later fails loudly rather than going
    unjudged. The same closure carries adr-2609111126115848's condition 3.

## Documentation

- **`docs/lifecycle.md` — how-to.** Installing, uninstalling and running the
  doctor: the commands, the one authorisation panel and why it appears, what
  uninstall leaves behind, and `--purge`. Task-shaped, one page, present tense.
- **`docs/lifecycle-reference.md` — reference.** The verbs, their flags, exit
  codes, the `status --json` fields, and doctor's check list with the three
  labels and what each means. This is the page a script author reads.
- **`docs/getting-started.md`** — its Uninstalling section is replaced by the
  verb, and its shared-cache paragraph corrected: it currently offers removing
  the whole shared root as the ordinary remedy, which is another account's data
  as well as this one's.

Neither new page claims doctor "checks" the firewall, and neither calls a
release signed.

## Open

- **The manual procedure for criterion 1** — the intent's open question, and
  the only criterion here with no armed detector. It is written, as
  `.abcd/development/procedures/installer-authorisation-panel.md`, and that
  file is also where its results are recorded: one row per run, with the date,
  the build and the account it was run from. What is still open is only the
  last part of the question — whether a release is blocked on a run of it —
  which the procedure states rather than settles.
- **An adversarial security review is a precondition of landing**, not a
  follow-up: this work adds a package that removes files, raises an
  authorisation panel and replaces the running application, beside
  `internal/runtime` and `internal/config`. The installer gate's history says
  to expect three rounds ending in a change of claim rather than another round
  of hardening.
