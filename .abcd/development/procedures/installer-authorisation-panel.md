# Manual procedure — the install, the one panel, and visible provisioning

The evidence for itd-2609081259532589's headline installing criterion:

> Given a Mac with no Gropius on it, when Alice runs the documented one-line
> install, then the bundle is placed, the firewall grant is requested once
> through the system authorisation panel with a stated reason, the MLX runtime
> is provisioned with progress that reports proportion rather than a spinner,
> and Gropius is serving when the command returns.

## Why this is a procedure and not a test

A runner has no console for a system authorisation panel, so no automated run
can answer one. `install.sh` says so itself: under `GITHUB_ACTIONS` it hands the
install over with `--place-only` and prints two warnings stating that a green
run says nothing about the firewall grant, the provisioning or the launch. Every
other criterion in spc-2609111029315861 is armed by a test the spec names; this
one is evidenced by a run a person makes.

The shape of that gap is what produced iss-2609080855033159, so the procedure is
written down rather than left to be remembered.

## Preconditions

- An Apple Silicon Mac running macOS 26 or later.
- Two accounts on it: one administrator (Alice) and one standard account with no
  administrator rights (Bob). The second is not optional — it is the account the
  panel exists for, and the one a `sudo` prompt could never serve.
- A published release, or a local asset directory built from the tree under
  test. Note which, and the version, in the record below.
- No Gropius installed for the account being tested: no bundle in either
  applications directory, no `~/Library/Application Support/Gropius`, and no
  firewall entry (`/usr/libexec/ApplicationFirewall/socketfilterfw --listapps`).
  `gropius uninstall --purge` clears the first three; the entry goes with it.

## Run A — the administrator account

1. Run the documented one-line install from the README, in a terminal, as
   Alice. Do not run it under `sudo`.
2. **One panel, with a reason.** Exactly one system authorisation panel appears,
   and it names the firewall and why it is being asked for. Record the wording
   it showed. A second panel anywhere in the run is a failure of this criterion
   even if the install succeeds.
3. **Progress with a proportion.** While the MLX runtime installs, the terminal
   names the stage AND how many of the stages are done. A spinner with no
   proportion is a failure. Record the elapsed time and the peak on-disk size of
   `~/Library/Application Support/Gropius`, which is the figure the record has
   never had.
4. **Serving when it returns.** When the command returns, the terminal says
   Gropius is serving. Check it from a second machine on the same network, not
   only over loopback: the empty-response failure this whole intent opens on is
   invisible from the Mac itself.
5. **The command resolves.** `gropius status` runs and answers. If the install
   said the bin directory is not on the search path, check that the line it
   printed is the line that fixes it.

## Run B — the standard account

Log in as Bob and repeat steps 1 to 5, with two differences to check:

6. **The panel accepts somebody else's credentials.** The panel asks for an
   administrator's name and password, and Alice's credentials satisfy it from
   Bob's session. This is the case `sudo` cannot serve at all.
7. **The per-user destination.** With no write access to the machine-wide
   applications directory, the bundle lands in Bob's own `~/Applications`, and
   the firewall entry is keyed to that path.

## Run C — the refusals

8. Decline the panel in step 2. The install must carry on and finish, report
   the grant as not made, and print the two commands that make it by hand. The
   server must still answer over loopback.
9. Run the install a second time on a Mac that is now fully provisioned. It must
   report that it repaired nothing rather than reinstalling, and must finish in
   seconds rather than minutes.

## What to record

One row per run, in the table below: the date, the build, which account, and
whether each numbered check held. A check that did not hold gets an issue in
`.abcd/work/issues/open/`, and its id goes in the row.

| Date | Build | Account | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | Notes |
| ---- | ----- | ------- | - | - | - | - | - | - | - | - | ----- |
| _not yet run_ | | | | | | | | | | | |

## Still open

Whether a release is BLOCKED on a run of this procedure is not settled, and this
file does not settle it — it is the remaining open question on
itd-2609081259532589. What is settled is that the procedure exists, that its
results are recorded here, and that a reader can see when it was last run and
against which build. Until it is decided, a release that has never been checked
this way is a release whose headline installing criterion rests on nothing but
the code review, and the table above is what makes that visible instead of
assumed.
