# Reference: the lifecycle verbs

The Gropius binary answers to a handful of verbs typed in a terminal: what this
installation is doing, why it is not working, how it got here and how it goes
away. This page is the contract — the verbs, their flags, their exit codes, and
the machine-readable output a script should read rather than the human text.

For how to use them, see
[Install, repair and remove Gropius](lifecycle.md).

A verb is reached through the `gropius` command, which the install links into
`~/.local/bin`. The same binary is inside the application bundle, at
`Gropius.app/Contents/MacOS/gropius`, and answers the same verbs when it is run
by its full path.

## The verbs

| Verb | What it does | Exits |
| --- | --- | --- |
| `gropius` | With no arguments at all, starts the server. This is how macOS launches the bundle, and it does not change. | the server's own |
| `gropius serve` | The bare invocation written out. Flags after it are the server's. | the server's own |
| `gropius version` | Prints which build this is, read from the binary. Contacts nothing. | `0` |
| `gropius status` | What this installation is doing right now. Reads state that already exists, so polling it is safe. | `0` |
| `gropius doctor` | The expensive checks, each labelled with how much its answer is worth. | `0` or `1` |
| `gropius config show` | The settings in force, read from the settings file the way the server reads it. Reads only; the API key and the HuggingFace token are shown as `********` and never as their values. | `0` |
| `gropius install` | Places the application, grants it through the firewall, provisions the MLX runtime, links the command. Repairs what is missing. | `0` or `1` |
| `gropius uninstall` | Removes what this installation put on the Mac. Leaves the downloaded models. | `0` or `1` |
| `gropius update` | Fetches the current release, verifies it against the checksums published beside it, places it with the staged swap, re-makes the firewall grant, and reports the version installed and the version this Mac is serving as two separate facts. | `0` or `1` |

A first argument that is not one of these words is refused, with exit `2`, and
no server is started. A first argument beginning with `-` is a server flag, so
`gropius -headless` and `gropius -root …` keep their meaning.

## The flags

| Verb | Flag | What it does |
| --- | --- | --- |
| `gropius status` | `--json` | Writes the machine-readable form on standard output. That form is the contract. |
| `gropius doctor` | `--json` | Writes the machine-readable form, which carries the severity of every finding. |
| `gropius config` | `--json` | Writes the machine-readable form, which is the contract: an object of settings keyed by the name `config.json` gives each one. |
| `gropius install` | `--bundle` | Takes a path: the verified bundle to place. The bootstrap passes it; a person repairing an installation does not. |
| `gropius install` | `--place-only` | Places the bundle and stops: no firewall grant, no provisioning, no launch. This is what the release gate runs, where there is no console to answer an authorisation panel. |
| `gropius uninstall` | `--purge` | Removes the downloaded models as well. |
| `gropius uninstall` | `--yes` | Answers the confirmation `--purge` would otherwise need a terminal for. |

A flag a verb does not carry is refused with exit `2`, and nothing else runs.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | The verb ran and answered. Whatever the answer says. |
| `1` | The verb ran and something Gropius verified is wrong, or a stage of the install failed. |
| `2` | The command line could not be honoured: an unknown verb, an unknown flag, an unexpected argument, or a verb this build does not carry. |

**Doctor's warnings exit zero.** The severity of a finding travels in the
report and never in the exit code, so a fresh Mac whose runtime is still
installing does not fail a script that runs the doctor. A script that wants
more than pass or fail reads the severities out of `--json`.

## What `gropius update` reports

The report is text, not a contract: there is no `--json` form, and `update`
takes no flags and no version. Every run carries five facts.

| Fact | The line it is on | What it means |
| --- | --- | --- |
| The version installed | `installed:` | The version the downloaded build reports about itself, and where it was placed. `cannot be determined` when that build would not answer its own `version` verb. |
| The version serving | `serving:` | The version this Mac is answering with. It is a different fact from the one above and is never substituted by it. |
| The download was checked | The download was verified against the checksums published with the release. | The whole of the check. Present wherever a bundle was placed. |
| The grant was re-made | The firewall grant is re-made on every update: the build's code identity changes with every build, so the entry that covered the previous build does not cover this one. | Followed by whether it was made, or by the commands that make it by hand. |
| There is no way back | There is no way back: only the current release is published, so the previous release cannot be fetched. | Every run. Typing a version is refused with the same sentence, and exit `2`. |

The serving line can take any of these values. **On this build it is always one
of the last four**: the running server does not publish which build it is, so
the first row is what the line will say once it does, and until then the
command says it cannot tell rather than repeating what it just installed.

| Value | What was found |
| --- | --- |
| a version, and the port | This account's Gropius answered the identity challenge and published its version. No build publishes it yet. |
| the running server does not publish its version | It answered the challenge and is a build with no version to give. |
| the server did not answer the control plane | It answered the challenge and then did not answer the snapshot read. |
| something holds the port and answered no identity challenge | Something is accepting connections and would not identify itself. Under per-account data roots that is what another account's Gropius looks like from here. |
| nothing is serving on this Mac | Nothing is accepting connections on the port. |

Where the version serving is not the version installed — or where the holder
would not identify itself — the report adds the cross-account ending:

```
This Mac is serving a version this command did not install.
The bundle just placed takes effect when that session logs out, or when
Gropius is restarted there.
port 11535 is held by 1 process that did not identify itself; it is counted
here and not named.
Quitting it is not something this command can do: a quit request reaches only
this login session.
```

Exit `1` is a download, a verification or a swap that stopped, and a port held
by a process that could not prove it is this account's Gropius — which refuses
before anything is downloaded. A declined authorisation panel is exit `0`: the
swap stands, and the grant is reported as not made.

## `gropius status --json`

The fields below are the contract. The human rendering is a rendering of this
same value and may change.

| Field | Type | What it carries |
| --- | --- | --- |
| `version` | string | The build that answered — this binary, which on a Mac where another account holds the port is not the build that is serving. |
| `serving` | boolean | Whether this account's Gropius answered the instance challenge on the port. |
| `port` | number | The port this installation's settings name. |
| `address` | string | `host:port` as the server bound it. Absent when nothing is serving, or when the server would not say. |
| `reason` | string | Why this is not the plain answer: nothing on the port, somebody else on it, or a server that did not answer the control plane. Absent when there is nothing to explain. |
| `resident` | array | The models in memory. Always a list, never null. |

Each entry of `resident` carries:

| Field | Type | What it carries |
| --- | --- | --- |
| `repo_id` | string | The model's HuggingFace repository id. |
| `state` | string | Where that model is: `loaded` can serve a request straight away, `loading` has a server up that has not answered its readiness probe, `not_loaded` is held without one. The value is the running server's own word, so a build serving a word this list does not carry is possible; treat an unknown value as unknown rather than as absent. |

```json
{
  "version": "0.4.0",
  "serving": true,
  "port": 11535,
  "address": "your-mac.local:11535",
  "resident": [
    { "repo_id": "mlx-community/Qwen3-8B-4bit", "state": "loaded" }
  ]
}
```

Progress and warnings are written to standard error, so a caller may pipe
standard output straight into a decoder.

## `gropius doctor --json`

| Field | Type | What it carries |
| --- | --- | --- |
| `version` | string | The build that ran the checks. |
| `findings` | array | One entry per check, in the order the checks are asked. |

Each finding carries:

| Field | Type | What it carries |
| --- | --- | --- |
| `name` | string | Which check this is, in the words the human rendering prints. |
| `label` | string | `verified`, `observed` or `undeterminable`. Read this before the severity. |
| `severity` | string | `ok`, `warning`, `failed` or `undetermined`. |
| `summary` | string | What the check found, with home directories abbreviated to `~`. |
| `commands` | array | What to run to establish or restore the state. Absent when there is nothing to run. |

```json
{
  "version": "0.4.0",
  "findings": [
    {
      "name": "MLX runtime",
      "label": "verified",
      "severity": "ok",
      "summary": "installed and usable"
    }
  ]
}
```

## The three labels

Every finding carries exactly one, and it says how much the answer is worth.

| Label | What it means | The severity it can carry |
| --- | --- | --- |
| `verified` | State Gropius owns and read for itself. A severity here means what it says. | `ok`, `warning` or `failed` |
| `observed` | A query about state Gropius does not own answered, and what it answered could not be established. The line reports what came back and is **never a verdict**, in either direction. | `undetermined`, always |
| `undeterminable` | There is no query at all. The check is reported rather than dropped or guessed. | `undetermined`, always |

A finding that is not `verified` carries `undetermined` whatever its check
found, and never contributes to the exit code. That is the rule the report is
assembled under rather than a habit of each check.

## The checks

| Check | Label | What it answers |
| --- | --- | --- |
| MLX runtime | `verified` | Whether the private Python and MLX runtime is installed and usable. A runtime that is not there yet is a warning: it is the ordinary state of a Mac that has not finished its first start. |
| data root | `verified` | Whether this account's data directory can be written to, and what stopped it. |
| settings file | `verified` | What `config.json` did when it was read: absent (so the shipping defaults are in force), loaded, loaded with settings this build could not use as written, or unusable. What it says is never printed — it holds the API key and the HuggingFace token. |
| this build | `verified` | Which build this binary is. It reads itself and contacts nothing. |
| server port | `verified` | Who holds the port: this account's Gropius, one process that is not it, or nothing. A foreign holder is counted and not named, and the line says the build reported above is this binary's own. |
| firewall entry | `observed` | What the macOS Application Firewall's query returned for this binary's path. It establishes nothing: the query answers "permitted" for a path it has no entry for and for a path that does not exist, and an ad-hoc build's code identity changes with every build. The finding carries the two commands that make the grant again. |
| Local Network Privacy | `undeterminable` | Nothing. macOS offers no way to read this grant: it is not part of the privacy database and cannot be reset or pre-seeded. The finding carries the command that opens the settings pane. |

## Where the command goes

The install creates a symbolic link at `~/.local/bin/gropius`, pointing at the
binary inside the installed bundle, and elevates for nothing to do it. That
directory is chosen because it is where per-user tools go on a Mac today, so it
is the one most likely to be on the search path already; an install that finds
it is not says so and prints the line that adds it.

Uninstall removes that link only when it is a symbolic link into a Gropius
bundle. A real file of that name belongs to whoever put it there.

## `gropius config show --json`

An object under `settings`, keyed by the name `config.json` gives each setting.
A nested setting is keyed by its path, so a per-model figure names the model it
belongs to. Every setting Gropius holds is in it, including the ones that have
no control in the panel and the ones whose value is at its default — a setting
the file leaves out is still in force, and its default is what is reported.

Two settings never carry their value. The API key and the HuggingFace token are
shown as `********` when one is set and as an empty string when none is, so a
reader can tell "no key" from "a key you are not being shown" without the value
travelling into a terminal, a pipe or a bug report.

Two settings are reported as what is in force rather than as what the file
holds: the log level, whose blank means the sparse default, and the chat rule,
whose blank means the shipped rule. The memory budget is not: a zero there means
the default share of this Mac's memory, and no verb works that figure out.

When `config.json` could not be used as written, a `settings_problem` field
says so and the settings reported are the ones the server would start from —
the bind locked down to this Mac, announcing off — rather than what the file
holds. A script that reads an address out of this without checking that field
will believe a locked-down server is what is running. The field is absent when
the file loaded cleanly.

What every setting means is on the
[getting started page](getting-started.md).

## What no verb does

- **Nothing reads standard input.** Under `curl … | bash` the script's own
  remaining text is standard input. A verb that needs consent either raises the
  system authorisation panel or refuses and names the flag that would have
  answered it.
- **Nothing elevates but the firewall entry.** That entry is machine-wide state
  with no per-account route, and it is the single exception: one system
  authorisation panel, with the reason on it, in the install and in the
  uninstall. Every other step acts on files this account owns.
- **No deletion path comes from the environment or the command line.**
  Uninstall acts on the fixed locations this account's install uses.
  `GROPIUS_ROOT` is read only so the output can name the root it did not
  remove.
- **Only `gropius update` contacts the network, and only when it is typed.**
  Every other verb answers from state that is already on this Mac: which build
  this is comes from the binary itself. Nothing runs on a timer, nothing checks
  for a release at launch, and Gropius never learns that a Mac is behind.
- **Nothing else reaches the forge, and the address cannot be moved.** The
  release `update` fetches from is fixed in the binary: nothing Gropius reads —
  no environment variable, no flag, no setting — changes where the archive or
  the checksums that verify it are asked for. Your own proxy and certificate
  settings still apply, exactly as they do to the install command, because the
  fetch is the same `curl` the installer uses.
- **No verb is reachable from the control panel.** The HTTP surface cannot see
  the package these verbs live in, so no request arriving on this Mac can drive
  a removal, a replacement or an elevation.
- **No verb writes a setting.** The control panel is the settings file's only
  writer, so the two cannot race on the file that decides who can reach this
  server. `gropius config show` reads it and answers.

## Where to go next

- [Install, repair and remove Gropius](lifecycle.md) — the how-to for each of
  these verbs.
- [Getting started](getting-started.md) — the walk-through from an install to
  an answer.
- [Reference: the server's log](logging.md) — what the running server writes
  down while it serves.
