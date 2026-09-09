---
id: spc-2609091703459471
slug: an-api-client-is-told-what-gropius-cannot-do-never-why-the-w
intent: itd-2609091412177263
origin: researcher-authored
production_mode: dictated-and-formatted
---
# an-api-client-is-told-what-gropius-cannot-do-never-why-the-w

## Summary

This spec delivers the server's own log: a file at `<account>/logs/gropius.log`,
0600, size-rotated with pruning, written alongside the stderr stream the app
already produces, at a level the operator chooses from `config.json` or the
control panel and can change without restarting. It adds the `log_level`
setting ("sparse" or "detailed"), a `slog.LevelVar` that carries the choice to
every handler in the process, and the lines themselves: one Info line per event
that mattered, and the figures those lines omit at Debug.

The client-facing half of itd-2609091412177263 — the generic refusal text an
unentitled client is answered with, the entitlement predicate, and the
rate-limited refusal line the gateway writes — arrives from
iss-2609062210238684 on the same integration branch. This spec does not change
a refusal's wording; it gives that line somewhere durable to land, a level to
be read at, and the figures at the level that carries them.

## Scope

In scope:

- A new package `internal/applog`: a size-rotating, pruning file writer opened
  under the account's own log directory, and the composition that fans one
  `slog.TextHandler` across stderr and that file behind a shared
  `slog.LevelVar`.
- `config.Config.LogLevel` (`json:"log_level,omitempty"`), its two constants,
  `EffectiveLogLevel`, a `sanitizeLogLevel` repair registered in `Load`'s
  `Repaired` notices, and the `Validate` refusal a save meets.
- The live apply: `app.Options.LogLevel *slog.LevelVar`, `App.applyLogLevel`,
  called from `app.New` and from `App.SetConfig` — the one function both the
  composition root and every save go through, on `applyStatistics`'s pattern.
- A "Logging" control in the settings pane of the web control panel, read from
  and posted with the rest of the form.
- The lines: a startup line naming the version and the log file, the existing
  shutdown line, a settings-changed line, load and unload lines from the pool
  observer, and the split of the pool's existing no-room refusal lines into a
  sparse half (model, which refusal) and a detailed half (protected set,
  budget, wait).
- `docs/logging.md`, a reference page, linked from `README.md` and
  `docs/getting-started.md`; a `### Added` bullet in `CHANGELOG.md`.

Out of scope:

- The refusal text a client reads, the `entitled` predicate, `genericRefusal`,
  `refusalClass` and the `logEvery` rate limiter. All of them are
  iss-2609062210238684's, land on the integration branch first, and are built
  on rather than duplicated.
- The per-model debug draft (itd-2609062346072707). Nothing here raises the
  model server's own log level, and the statistics switch's guard
  (`internal/archtest/statistics_switch_test.go`) stays exactly as it is: a
  `log_level` of "detailed" must never reach `internal/runtime/launcher.go`.
- Any change to the model servers' own log files: their names, their `O_TRUNC`
  open, and their absence of rotation are untouched.
- Sending anything anywhere. The log is a local file and nothing reads it off
  this Mac.

## Approach

**One log, two destinations, one level.** `applog.Open` builds a single
`slog.NewTextHandler` over `io.MultiWriter(stderr, rotatingFile)` with
`HandlerOptions.Level` set to a `*slog.LevelVar`. One handler rather than two
means a line cannot appear in one place and not the other, and the level is one
variable rather than a value copied into two handlers that could drift. stderr
comes first in the multi-writer deliberately: `io.MultiWriter` stops at the
first error, so a log directory that cannot be written costs the file and not
`make run`'s console.

**The rotating writer.** `internal/stats/store.go` already rotates by size, but
its writer is not a size-rotating file writer with a store on top: its file
names carry a UTC day and a counter, its pruning is a two-bound retention
(months and bytes) that folds what it drops into a summary file which itself
counts toward the cap, and it is driven by an entry channel and an `os.Root`
opened for the store's directory. Extracting a shared primitive from it would
mean pulling the day-numbering, the summary fold and the retention arithmetic
apart from the rotation, inside the package that holds this repository's
durable data format — a change far larger than the writer this record needs and
one that would land in the same diff as a new setting and a new package. So the
smallest possible rotating writer goes into `internal/applog` instead, and the
duplication is filed as iss-2609091714393599, naming both sites, with a dated line in
`.abcd/work/DECISIONS.md`. That is the one-canonical-primitive rule's escape
hatch used deliberately and recorded, not skipped.

The writer itself is a `Write([]byte) (int, error)` behind a mutex: it appends
to `gropius.log`, and when the next line would take the file past
`RotateBytes` it closes it, shifts `gropius.log` → `gropius.1.log` →
`gropius.2.log` …, removes anything past `Keep`, and opens a fresh
`gropius.log`. Every open, rename and remove goes through an `os.Root` on the
log directory, and the open uses exactly the launcher's discipline —
`O_CREATE|O_WRONLY|O_APPEND|O_NONBLOCK|O_NOFOLLOW`, mode 0600, followed by an
`fstat` on the opened handle refusing anything that is not a regular file —
with `O_APPEND` in place of the launcher's `O_TRUNC`, because this log outlives
one model server's run and rotation is what bounds it. Defaults: 5 MB a file,
five files kept, which is the same ceiling per file the statistics store uses
and a total under 25 MB.

**Failing to log is not failing to serve.** `applog.Open` returns a logger
whatever happens: a log directory that cannot be opened yields the
stderr-only logger and a warning on it. Nothing in the startup path exits
because a log file could not be created, and a write that fails mid-run is
dropped rather than propagated — a full disk must not turn into a refused
request.

**The setting.** `LogLevel` is a string enum on `Config`, empty meaning sparse,
so every `config.json` written before this field means what a fresh install
means and `Default()` needs no entry — `BindMode`'s precedent exactly.
`EffectiveLogLevel()` resolves the empty case in one place, following
`GraceSeconds()`. Load repairs an unusable value to sparse and reports it in
`Notices.Repaired` — sanitisation runs before `Validate`, so a hand-edited
`"log_level": "verbose"` never reaches the fail-closed loopback branch; the
panel then shows the repaired-settings warning it already shows. `Validate`
refuses an unknown value, which is what a save meets, on `sanitizeStats`'s
stated rule: a file is repaired, a save is told, because at a save the operator
is there to read why.

**A save never refuses an untouched field.** Nothing new is needed for this and
that is the point: `Control.applySettings` decodes the posted body into
`current.Clone()`, so a body that omits `log_level` keeps the value in force,
and a scalar field needs no handler change at all. The test for it posts a body
with no `log_level` against a config that has one and asserts both that the
save succeeds and that the level survives.

**Applied live.** `app.Options` gains a `LogLevel *slog.LevelVar` seam — nil in
tests, the one `applog` made in `cmd/gropius`. `App.applyLogLevel(c)` sets it
from `c.EffectiveLogLevel()` and is called from `New` and from the bottom of
`SetConfig`, beside `applyStatistics`, `Hub.SetToken` and the pool's setters.
`log_level` is therefore **not** added to the handler's `restart` disjunction:
it takes effect on the next line written.

**The lines.** Sparse is Info and above; detailed is Debug. What sparse carries:

| Event | Where | Line |
| --- | --- | --- |
| startup | `cmd/gropius/main.go` | version and the log file's path |
| shutdown | `cmd/gropius/main.go` | the existing "shutting down" |
| settings changed | `App.SetConfig` | that a save happened, and the level now in force |
| model loaded / load failed | `app.poolObserver.LoadFinished` | the model, and whether it became ready |
| model unloaded | `app.poolObserver.EntryStopped` | the model and the reason it left |
| launch failure | `internal/gateway` | the model (the wrapped error moves to Debug) |
| refusal | `internal/gateway`, `internal/runtime/pool.go` | the model and which refusal |

What only detailed carries: how long a load took; the number of requests
already in flight behind a busy refusal; the memory budget in bytes and the
protected set behind a no-room refusal; how long a request waited; the wrapped
launch error; and the drain — the model that left, and the load that was held
waiting for its memory to come back.

The pool's two existing no-room lines are the shape of the split: today
`p.opts.Log.Info(...)` carries `waiting`, `limit`, `protected` and `waited` on
the same line as the model. After this change the Info line carries the model
and which refusal, and a second `Debug` line beside it carries the figures.
Both are written off `p.mu`, exactly as they are today — the pool's rule that a
slow log sink must not stall its one lock is not relaxed for this, so no line
is added inside a critical section.

**Nothing new is logged that was not loggable before.** No line added here
takes a request body, a response body, an API key, an HF token or an
`http.Request`. The names on every line are repo ids the registry resolved, and
the figures are the pool's own. `internal/archtest/prompt_content_test.go`'s
allowlists are untouched; `cmd/gropius/logging_test.go`'s client-address leak
check gains a case that reads the log **file** after a refusal.

## How each acceptance criterion is satisfied

1. _Given an unentitled client, when Gropius refuses a request, then the answer
   says what and never why, and one sparse log line names the model and which
   refusal._ The answer half is iss-2609062210238684's `genericRefusal` and is
   not touched. The log half is this record's: the gateway's refusal line is
   written at Info (so it survives sparse), carries `model` and `class` from
   `refusalClass`, and moves the pool's own message to a Debug line beside it.
   A test in `internal/gateway` drives a refusal through a gateway whose logger
   is at Info and asserts the line names the model and the class and carries no
   budget figure; the same test at Debug asserts the figure is there.
2. _Given an entitled client, when Gropius refuses, then the answer is today's
   informative one._ Held by iss-2609062210238684's existing
   `internal/gateway/refusal_text_test.go`, which this record leaves green. The
   merge is the evidence: no refusal text and no entitlement predicate changes
   here.
3. _Given the server runs, when it writes its log, then the file is in the
   account's own directory at 0600 and contains no prompt, answer, key or
   client address._ `applog` opens under `config.Paths.Logs`, which resolves
   through `accountDir` and is per-account already. A test in `internal/applog`
   stats the created file and asserts `0600` and that it is a regular file, and
   asserts the open refuses a symlink and a FIFO planted under the name. A
   second test, in `cmd/gropius`, runs a refusal end to end against a real
   temporary log directory and asserts the file's bytes contain neither the
   client's address (through `clientAddressLeaks`, reused) nor a prompt string,
   nor the configured API key or HF token.
4. _Given the level is switched in `config.json` or the panel, when the next
   event happens, then it is logged at the new level without a restart, and a
   save never refuses an untouched field._ Two tests. In `internal/app`: a
   `LevelVar` is handed to `New`, a save flips `log_level` to "detailed", and
   the var reads `slog.LevelDebug` with no restart — and the reverse. In
   `internal/gateway`: `applySettings` is posted a body with no `log_level`
   against a config holding "detailed", the save succeeds, and the level is
   still "detailed". A third, in `internal/config`: `Load` of a file carrying
   `"log_level": "verbose"` returns sparse in force and names `log_level` in
   `Notices.Repaired`.
5. _Given detailed, when a refusal or a load happens, then the line carries the
   figures that sparse omits._ Table tests over the pool and the gateway with
   a `LevelVar` at Info and at Debug: at Info the budget bytes, the in-flight
   count, the wait and the wrapped launch error are absent from the captured
   output; at Debug each is present. The load side asserts the same for how
   long a load took, and the unload side for the drain.
6. _Given the log grows, when it passes its size, then it rotates and old files
   are pruned._ `internal/applog` tests write past a small `RotateBytes` and
   assert: `gropius.log` exists and is under the cap, `gropius.1.log` holds the
   lines that were there before the rotation, the count of files never exceeds
   `Keep`, and the total on disk stays bounded across many rotations. A
   concurrent test writes from several goroutines under `-race` while
   rotating, because the writer is behind one mutex and that is the claim.
7. _Given the docs, when a reader opens the logging page, then it says what each
   level writes and where the file is._ `docs/logging.md` is a reference page:
   it names the file and the directory in repo-relative terms, states the mode,
   the rotation size and how many files are kept, and carries a two-column
   table of what sparse writes and what detailed adds. An archtest in
   `internal/archtest` holds the page against the code: the rotation size and
   the keep count printed on the page must equal `applog`'s defaults, the two
   level names must equal `config`'s two constants, and the page must say that
   no prompt, answer, key or client address is ever written. `README.md` and
   `docs/getting-started.md` each link it.

## Trust-boundary review notes

Three boundary packages change, each narrowly:

- `internal/config` gains one string field, one repair and one `Validate`
  clause. The field is not a path, is never interpolated into one, and reaches
  only a `slog.Level`. The repair means a hostile or corrupt value costs a
  notice rather than the fail-closed loopback start.
- `internal/gateway` gains no new reader of a request. The refusal line's
  fields are a repo id the registry resolved and a class name from a closed
  set; the pool's message moves to Debug rather than growing.
- `internal/runtime`'s pool splits two existing lines. No line is added under
  `p.mu`.

The new package is itself a boundary of a kind — it creates a file under a
predictable name in a directory an older install may have left behind — and it
is written to the launcher's rule: `O_NOFOLLOW` refuses a planted link,
`O_NONBLOCK` refuses to block on a planted FIFO, an `fstat` on the opened
handle refuses anything that is not a regular file, and every rename and remove
during a rotation goes through an `os.Root` on the log directory so a component
swapped underneath cannot redirect them. The rotation's renames are within one
directory and never cross it.

Residual, stated plainly: the log directory is created 0755 under an account
directory held at 0700, so on a shared-cache install the file's own 0600 is
what keeps it private, exactly as the model servers' logs already are. And a
detailed level left on writes more about this Mac to disk than a sparse one —
which is why sparse is the default, why nothing switches it on automatically,
and why the docs page says so.

## Docs to change

- **New**: `docs/logging.md` — a reference page. What each level writes, where
  the file is, the mode, the rotation and the pruning, and the explicit
  statement that no prompt, answer, key or client address is written at any
  level, and that this level is Gropius's own and never the model server's.
- `README.md`: a `## Features` bullet with a `([reference](docs/logging.md))`
  link, in the existing style.
- `docs/getting-started.md`: a pointer to the page where §9 already tells the
  reader what the account folder holds.
- `CHANGELOG.md`: one bullet under the existing `### Added`.

`docs/request-statistics.md` is deliberately not edited: its sentence about the
model server's own log level is still true, and
`internal/archtest/statistics_docs_test.go` holds it.

## Dependencies and sequencing

- Depends on iss-2609062210238684 (`fix/blind-get-and-pool-error-text`), which
  brings `entitled`, `genericRefusal`, `refusalClass` and `logEvery`. It is
  already merged into `integrate/issue-sweep-2026-09-09`; this branch merges
  that in before the gateway half is written, and builds on `logEvery` rather
  than adding a second rate limiter.
- The KV-budget record (iss-3) lands on the same integration branch and touches
  the pool's memory arithmetic. It is not a code dependency of this record;
  the pool lines here are log calls beside existing ones.
- itd-2609062346072707 (the per-model debug draft) stays separate, on the
  resolved open question's grounds: a shared level control would put the level
  within the runtime's reach and fail the statistics-switch guard.

## Open design points

- Whether the rotated files are numbered (`gropius.1.log`) or dated
  (`gropius-20260909-001.log`) is the implementer's call. Numbered is assumed
  above because it makes "the current log" one fixed name a person can `tail`,
  which is what an operator chasing a refusal actually does; dated would match
  the statistics store's spelling. Either must keep the pruning bound and the
  0600 mode.
- Whether the settings-changed line names which fields changed is left open.
  Naming them means a differ and a list that must never carry a value; the
  minimum that satisfies the criterion is that a save is visible at sparse.
- The defaults (5 MB, five files) are a starting point, not a measurement.
  Shown wrong if an operator running at detailed loses the beginning of an
  incident inside one session.
