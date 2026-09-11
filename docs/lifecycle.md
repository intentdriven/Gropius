# Install, repair and remove Gropius

Gropius puts itself on this Mac, repairs itself, takes itself away, and says
what is wrong with it. This page walks through each of those four tasks. For
the verbs, their flags, the exit codes and the machine-readable output, see
[Reference: the lifecycle verbs](lifecycle-reference.md).

## Install it

One line, in a terminal:

```sh
curl -fsSL https://raw.githubusercontent.com/intentdriven/Gropius/main/install.sh | bash
```

That command is a bootstrap and only a bootstrap: it does the part that has to
happen before a Gropius binary exists on this Mac. It downloads the current
release, verifies it against the checksums published beside it, clears the
quarantine attribute, and hands over to `gropius install` **inside the bundle
it has just verified** — never to a copy already on the Mac. The script and the
binary it calls are the same build, which is what makes the two halves of an
install one thing.

From there the binary does the work, in this order:

1. **Places the application.** `/Applications` when your account can write
   there, `~/Applications` when it cannot. The move is staged: the installed
   bundle is set aside, the new one is put in its place, and the set-aside copy
   goes only once the new one is there. No failure leaves this Mac without an
   application.
2. **Asks once for the firewall grant**, through the system authorisation
   panel, with the reason on the panel.
3. **Installs the private Python and MLX runtime**, in the foreground, naming
   the stage and how many of the three stages are done. This is the part that
   takes minutes.
4. **Links the `gropius` command** into `~/.local/bin`, and says so. Nothing is
   elevated for this.
5. **Opens the application** and waits for it to answer, then says whether it
   is serving.

When the command returns, Gropius is in the menu bar. Click its icon for the
control panel, and carry on at
[Getting started](getting-started.md#3-download-a-model).

### The one administrator panel

Exactly one panel appears in an install, and it is for the macOS Application
Firewall. Without that entry, Gropius accepts a connection from another machine
and then drops it, so the LAN sees an empty response while `localhost` on this
Mac works.

The panel rather than a password prompt in the terminal, for two reasons. It
asks for **an administrator's name and password**, so your own account does not
have to be an administrator — someone else can type theirs. And a terminal
prompt cannot be answered under `curl … | bash` at all: there the script's own
remaining text is standard input. No lifecycle verb reads standard input for
any purpose.

**The panel appears again on every update.** These builds are ad-hoc signed,
with no Developer ID, so the code identity the firewall keys its entry to
changes with each build. An entry made for one build does not carry over to the
next, and the grant is made again.

Decline the panel and the install carries on to the end. It prints a warning
and the two commands that make the grant by hand, and the server answers on
this Mac while other machines see nothing.

### When `~/.local/bin` is not on the search path

The `gropius` command is a link in your own bin directory, which is not on
every Mac's search path. When it is not on yours, the install says so and
prints the line that fixes it. Add it to `~/.zshrc` (or `~/.bash_profile`):

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Open a new terminal after saving, and `gropius status` answers. Until then the
command works by its full path, `~/.local/bin/gropius`.

### When your account is not an administrator

`/Applications` holds one bundle that every account on the Mac launches, and a
standard account cannot write there. The install puts the bundle in your own
`~/Applications` instead, which macOS indexes the same way, and keys the
firewall entry to that path. Installing for every account when one account
asked is not what was asked, so nothing is elevated to reach the machine-wide
directory.

## Repair an installation

Run the install verb again:

```sh
gropius install
```

It repairs what is missing rather than reinstalling what is not, and says which
of the two it did: either that the runtime was already complete and nothing was
reinstalled, or which pieces it put back. A complete installation takes seconds.

Repair acts on the bundle that is already installed. On a Mac with no Gropius
at either destination there is nothing to repair, and the verb refuses and
names the bootstrap — before it asks for anything, so a Mac with nothing
installed never raises an authorisation panel.

## When a stage fails

A stage that fails stops the run. The command exits non-zero, names the stage
and why it failed, and names the command that retries it:

```
gropius install: the MLX runtime failed: …
Retry with: gropius install
```

Retrying resumes rather than restarting: the stages already done are the ones
the repair above leaves alone.

Two things are warnings rather than failures, because the install is usable
without them. A firewall grant that was declined or could not be made prints
the two commands that make it by hand. An application that could not be opened
leaves everything installed.

The terminal's verdict is what was true when the command exited, and it is not
revisited. Gropius checks its own runtime whenever it starts, so a provisioning
run that failed in the terminal may well be finished by the app itself on the
next launch — the control panel is where that shows.

## Remove it

```sh
gropius uninstall
```

What goes: the application bundle from both fixed locations, the private Python
and MLX runtime, `config.json`, the model list, the logs, the request
statistics, the `gropius` command, and the firewall entry.

What stays: **the models you downloaded, and the cache they arrived through.**
They are the expensive thing to fetch again, so removing them is a decision of
its own. The output states their total size and the flag that removes them.

Two lines are worth reading when they appear:

- **"That was the copy every account on this Mac launches."** The bundle it
  removed was the one in `/Applications`, which is everybody's. No other
  account gets a message about it.
- **What is left.** A path this account may not delete is reported rather than
  elevated for, and the firewall entry is reported with the command that
  removes it when the panel is declined. Everything else is still removed.

`GROPIUS_ROOT` and `-root` are never deletion paths. Uninstall acts on the
fixed locations this account's install uses, and the output names the root it
did not remove.

### Remove the models too

```sh
gropius uninstall --purge
```

`--purge` deletes the downloaded models as well. It needs a terminal, because
deleting them is not something to do on somebody's behalf without asking; where
standard input is not a terminal — a script, a pipeline, a CI job — the run
deletes nothing and names the flag that answers for you:

```sh
gropius uninstall --purge --yes
```

### When this Mac uses a shared model cache

With the shared cache from
[Getting started, step 9](getting-started.md#9-sharing-across-user-accounts-optional),
the models live in `/Users/Shared/Gropius` and everything belonging to your
account — its settings, its model list, its runtime, its logs and its
statistics — lives in your own `~/Library/Application Support/Gropius`.

So uninstall removes your own directory and **leaves the shared root alone**.
The output names what remains in it, how much it holds, and how many other
accounts it belongs to, counted rather than named. Removing it removes every
account's models, and it is one deliberate command to run once everybody has
finished with it:

```sh
sudo /bin/rm -rf /Users/Shared/Gropius
```

`--purge` in this mode removes only the files your own account owns, which is
the only removal the shared directory's permissions allow anyway. The output
separates what it deleted from what it left, with the size of each. Another
account's models are that account's to remove.

## Diagnose it

```sh
gropius doctor
```

Doctor runs the expensive checks and labels every line with how much its answer
is worth. Read the label before the severity:

- **Verified** — state Gropius owns, so a severity means what it says: the MLX
  runtime, the data root's writability, the settings file, which build this is,
  and who holds the server port. On a Mac where another account is running the
  server, the port line says so, and says that the build named above it is this
  binary's own rather than the one being served.
- **Observed** — the firewall entry, and it is never a verdict in either
  direction. The system query answers "permitted" for a path it has no entry
  for and for a path that does not exist at all, and an ad-hoc build's identity
  changes with every build, so a "permitted" answer is compatible with a grant
  that covers nothing. The line reports what came back, says what it does not
  establish, and prints the two commands that make the grant again.
- **Cannot be determined from here** — Local Network Privacy, which macOS
  offers no way to read. It has no query interface, is not part of the privacy
  database, and cannot be reset or pre-seeded. The check is printed every time
  rather than dropped, and it carries the command that opens the settings pane
  where a person can look.

Warnings exit zero. Only a check Gropius verified and found wrong exits 1, so
`gropius doctor` is safe to put in a script that branches on the exit code —
and the severity of every finding is in `gropius doctor --json` for a script
that wants more than pass or fail.

For what is true right now rather than what is wrong — serving or not, on which
address, which models are in memory — `gropius status` answers from state that
already exists and is cheap enough to poll.

### Pasting the output into a bug report

Doctor's output is written to be pasted. Home directories are abbreviated to
`~`, so no account name travels with it; other accounts on this Mac are counted
rather than named; and the settings file is reported by what it did when it was
read, never by what it says, because it holds the API key and the HuggingFace
token.

The commands it prints are written to be pasted too: a path inside your home
directory is written as `"$HOME/…"`, which runs on the machine it is pasted
back into and carries no account name away.

## Where to go next

- [Reference: the lifecycle verbs](lifecycle-reference.md) — every verb, every
  flag, the exit codes, and the fields `--json` emits.
- [Getting started](getting-started.md) — from a fresh install to answering a
  prompt from another machine.
- [Reference: the server's log](logging.md) — where Gropius writes down what it
  did, and what it never writes.
- [The posture page](posture-reference.md) — the control panel's one page
  saying who can reach this server.
