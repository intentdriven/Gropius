# Native installer, updater and lifecycle verbs — feasibility

Whether Gropius should grow a native binary that installs, updates and
configures itself, and what shape that binary takes.

## Verdict

Feasible, and worth doing — but not in the shape the question was asked in.
A **separate** installer binary is the wrong answer: it is a named packaging
anti-pattern, none of the six comparable macOS products ship one, and it is
circular, because a user needs `curl` to obtain the installer and `curl` is
already the mechanism that installs the app. The right answer is the same
capability on the binary Gropius already ships: `gropius` gains lifecycle
subcommands, and the shell script shrinks to a bootstrap.

Two constraints decide most of the design, and neither can be engineered
away.

## The two constraints

### Ad-hoc signing makes curl the only working channel

Gropius bundles are ad-hoc signed and not notarised. On macOS 26.6.1 an
ad-hoc-signed application carrying the quarantine attribute is directed to
the Bin on launch: there is no "Open Anyway", and right-click-to-open was
removed in macOS 15.0. A download made with `curl` carries no quarantine
attribute, which is why the installer works where a browser download does
not.

This closes every alternative channel at once. A browser-fetched archive is
quarantined, and the attribute propagates to everything unpacked from it. A
disk image behaves the same. Homebrew removed its `--no-quarantine` option
in 5.0.0 and now audits casks for signing and notarisation, with
un-notarised casks leaving the official tap. A private tap does not help,
because the quarantine is applied by the cask installer rather than by the
tap.

The consequence for this note is narrow and useful: the existing install
command is not a compromise to be apologised for. It is the only channel
that functions, and the work is to make it feel deliberate.

This bears on a question the issue ledger left open, without closing it.
Ad-hoc signing plus quarantine is sufficient to explain the installed
application being moved to the Bin, which makes the recorded
bundle-identifier hypothesis unnecessary rather than refuted. The record
names the experiment that would settle it — a freshly downloaded build under
the previous name, quarantined the same way on the same macOS version — and
that experiment has not been run. It is one build and one download. The
rename remains the better explanation for the lost firewall and Local Network
grants, which is a separate and real problem.

### Every update is a new application, as far as macOS is concerned

An ad-hoc signature's designated requirement is its code-directory hash, and
that hash changes on every build. The macOS Application Firewall entry and
the Local Network Privacy grant are both keyed to code identity, so both are
lost on every update. Local Network Privacy is not part of TCC and cannot be
reset or pre-seeded with `tccutil`.

An updater must therefore re-add the firewall rule after every swap, which
needs an administrator password every time. That is a property of not
holding a Developer ID, not a defect in the updater.

## What a Developer ID would buy

Notarisation requires Apple Developer Program membership, a Developer ID
Application certificate, the hardened runtime, submission through
`notarytool`, and stapling. There is no substitute: `spctl --master-disable`
is no longer supported, adding the application to the Developer Tools list
makes the outcome worse rather than better, and the build-provenance
attestation Gropius publishes is invisible to macOS. The attestation remains
a genuine supply-chain control for anyone who runs the check; it buys nothing
from Gatekeeper, and user-facing prose should never call it "signed".

The decision is a purchase and stands outside this note. What belongs here is
the threshold: the recurring administrator prompt on every update is the
measurable cost of not holding one, and the day that cost is felt by enough
people is the day the membership pays for itself. Everything else follows
from it — browser downloads and disk images work again, the firewall and
Local Network grants survive updates, Homebrew reopens, and Sparkle becomes
available as an updater.

One free route exists and is a long shot. Mac Admins Open Source signs and
notarises open-source projects at no charge, but requires the project to
relate to Mac administration, to carry an MIT or Apache-2.0 licence, and for
the repository to move into their GitHub organisation. A model server for end
users almost certainly fails the relevance test. It is worth one email and
should not be planned around.

## Why the capability belongs on the existing binary

Every comparable product folds installation and updating into the product
itself. Tailscale, Rectangle, LM Studio, Ollama, Docker Desktop and Raycast
all do so, and not one ships a separate installer the user must obtain first.
The packaging guidance Mac administrators cite is explicit that custom
software installers are a bad idea, and reads a nested installer application
as a warning sign.

Putting the verbs on `gropius` also satisfies the repository's own
one-canonical-primitive rule. The staged-swap logic — verify, quit the
running copy, stage beside, rename into place — already exists in the shell
script, and an updater needs the same sequence. It is not correct, and an
adversarial review established that empirically rather than by reading: the
sequence removes the installed bundle and only then renames the staged one
into its place, so a destination that exists as a directory has the staged
bundle nested inside it, and a destination that is a symlink is written
through and left standing, both exiting successfully. The firewall grant and
the launch then follow that path. A rename that simply fails leaves no
application at all, which is the outcome the code's own comment says staging
prevents. This is recorded as its own defect against the shipped installer.
The consequence for this note is that the sequence must be rewritten in Go
rather than ported — renaming the old bundle aside, renaming the new one in,
and only then deleting what was set aside — and the argument for one copy of
it is stronger, not weaker, for that. Two copies of that logic in two languages is the outcome to avoid.
One copy, in Go, called by both the bootstrap and the updater, is the point
of the exercise.

A Rust binary was considered and set aside. It buys a smaller artefact and
stricter error handling, at the cost of a second toolchain, a second CI path,
a dependency sign-off, and install semantics maintained in a language nothing
else in the project uses.

## The surface rule this rests on

Gropius has three surfaces and they are not interchangeable:

- **Go is the power tool.** It carries the whole of the functionality, and
  the terminal is a legitimate place for the person running a server to see
  it.
- **The web control panel is the accessible layer.** It carries the most
  important settings, and it must be kept in sync with what Go can do.
- **Swift is the client, and only the client.** Someone using the chat
  application never sees the server side.

An ANSI-illustrated installer running in a terminal is consistent with this
rule rather than an exception to it, which is what makes the friendliness
goal reachable without arguing against the architecture.

The rule also indicts the present state. `advertise`, `preload` and
`upstream_header_timeout_sec` are settable only by hand-editing JSON, and the
data-root choice (`GROPIUS_ROOT`, `-root`) sits outside the configuration
file entirely and is documented nowhere. Those are Go capabilities with no
web equivalent — the second layer is out of sync today. That gap is its own
record, not part of this one.

## What the first cut covers

Installing, uninstalling and diagnosing. Updating is deferred to a record of
its own, because its blast radius is different and its blocking question is
unanswered; terminal configuration and installing the chat client are
deferred too.

**Install.** The shell bootstrap keeps only what must happen before a Go
binary exists: fetch the archive, verify it against the release's published
checksums, clear the quarantine attribute, and place the bundle. Everything
after that is a subcommand — the firewall rule, the runtime provisioning, and
the closing instructions.

Provisioning moves into the foreground, which fixes the worst moment in the
product today. A first launch currently shows a panel banner saying it takes
a few minutes, with no proportion complete, no size figure anywhere in the
documentation, and no retry control if it fails; the documented remedy is to
quit and reopen the application. Running it in the terminal with real
progress replaces all of that. The application keeps its existing check on
every launch as a safety net, because the routine is already idempotent and
because someone who installs by another route still needs provisioning to
happen.

**Update, deferred.** The verb carries the whole of the new trust boundary —
a process that fetches code and replaces the application it is running from,
with an elevation step attached — and it is the one part with an unanswered
question underneath it, so it becomes a record of its own rather than
travelling with three verbs that are ready. Re-running the existing one-liner
already performs a correct update, and it works precisely because an external
process drives it.

The check that tells someone they are behind is opt-in and off until they turn
it on, and the panel is where it is offered.

Off by default is what keeps the promise literally true. This does not, as
first drafted here, leave the no-public-telemetry decision untouched. That decision covers update checks by name, and enumerates the
outbound connections the product makes as exactly those needed to fetch models
and provision the runtime. A release check is a third outbound host on a
schedule the product chooses, whatever it carries, and gating it on an open
panel changes when it happens rather than whether it exists. The decision also
anticipated this class directly, observing that users of local-inference tools
treat even an update check as suspect. So the check is opt-in and off by
default: anyone who never turns it on makes no connection the decision did not
already enumerate, and the promise in the README stands unqualified for them. The swap itself is a deliberate act in a
terminal, where an administrator prompt is expected rather than intrusive,
and the prompt states plainly why it is needed.

**Uninstall.** There is an `Uninstall` routine in the provisioning code with
no caller outside its tests, and the documented removal steps miss the
firewall allow entry and the client's stored preferences and keychain item
entirely. A real uninstall verb closes both gaps and should distinguish
removing the application from removing downloaded models, which are the
expensive thing to re-fetch.

**Doctor.** One command answering "why is this not working": whether the
runtime is healthy, whether the root is writable, whether the configuration
parses, which process holds the port, and whether this version is current.
Most of these exist already as internal code with no user-facing surface.

Two checks that seem obvious cannot be built and must not be claimed. The
firewall query answers that a connection is permitted for a path that has no
entry at all, and for a path that does not exist — so a check built on it
would report a healthy grant at exactly the moment an update invalidated one.
Local Network Privacy has no query interface of any kind. Both must be
reported as observed rather than verified, with the commands to re-grant
printed beside them, because a diagnostic that fails open is worse than no
diagnostic: it tells the operator the broken thing is fine.

That is also what the accepted decision on environment detection requires,
and this note first cited it too comfortably. That decision names a firewall's
state among the signals it governs and closes a warning's firing condition to
them. A diagnostic whose output is warnings fired by reading the system
firewall is on the wrong side of it as first drafted. Either doctor reports
observations and remedies with no verdict, or the carve-out for a
user-invoked diagnostic that enforces nothing is written down and agreed. That
is a decision, and it belongs in the ledger before any code.

## The verb set

The names below follow the conventions the recognised references settle on
and the practice of nineteen comparable tools.

```
gropius                          # no arguments: run the server
gropius serve [--headless]       # the explicit form of the same thing
gropius install [--yes]
gropius update [--check] [--yes]
gropius uninstall [--purge] [--yes] [--dry-run]
gropius status [--json]
gropius doctor [--json]
gropius version [--json]
```

**`update`, not `upgrade` and not `self-update`.** The update/upgrade
distinction is real, but it exists only in tools that manage other updatable
things: a package manager needs both words because one refreshes metadata and
the other replaces packages. Gropius manages models, not packages, and the
command-line guidelines name a tool carrying both words as actively
confusing. Anything model-related belongs under a `model` noun, where the
collision cannot arise. The `self` namespace is likewise for tools whose
plain `update` is already taken.

**`status` and `doctor` are two commands, not one.** `status` answers what is
true right now — running or not, which port, which model — cheaply, with
machine-readable output the menu bar and scripts can call. `doctor` runs the
expensive checks and is written for a person. Splitting them is what makes
the fast one safe to call often. `check` and `verify` are both rejected:
`check` means static checking elsewhere in this ecosystem, and `verify`
implies cryptographic verification, which this project genuinely does do
somewhere else.

**`uninstall --purge`, never a `purge` verb.** Removal of the application and
removal of the downloaded models are one operation with a modifier, not two
verbs. Homebrew's equivalent flag is the precedent, and its own documentation
warns that the aggressive form can destroy shared resources — so the purge
path states the size of the model cache it is about to delete before it does
it, and offers a dry run.

**The flags must keep working.** The bundle is launched by macOS with no
arguments at all, so a bare invocation has to go on running the server;
`serve` exists for scripts and launch agents that want to be explicit, and an
unrecognised first argument is a usage error rather than anything clever.
Dispatching on whether the first argument begins with a dash keeps
`-headless` and `-root` working permanently, which is what both of the
large-scale precedents for this transition did — neither ever removed the old
forms.

## Terminal craft, and the trap in it

The friendliness goal is reachable cheaply, and one hazard in it is not
obvious.

**Nothing may read standard input.** Under `curl … | bash` the script's own
remaining text *is* standard input, so a read consumes the rest of the
installer and corrupts the run. Any prompt must open the terminal device
explicitly — which is exactly how `sudo` reads a password, and why the
existing firewall step works at all.

The administrator prompt is the case where this matters most, and it has a
better answer than a terminal prompt. Raising the system's own authorization
dialog sidesteps the standard-input hazard entirely, and it does something
`sudo` cannot do at all: it lets someone using a standard account enter an
administrator's name and password. A non-admin account is not in the
sudoers set, so a terminal prompt there does not merely look worse — it
cannot succeed. Since the installer already falls back to a per-user
applications directory precisely because standard accounts exist, this is the
difference between a nicer prompt and an installation that works for the
account that could not install before. The dialog belongs after verification
and before the swap, so that a credential is never spent on a download that
then fails its checksum. A destructive verb invoked without a
terminal and without `--yes` exits non-zero naming the flag to pass, rather
than assuming consent or hanging.

**Progress goes to standard error.** That keeps `status --json` clean for a
pipe, and it is the stream a progress display belongs on regardless. Colour
and animation are decided per stream and switched off when the stream is not
a terminal, when `TERM` is `dumb`, or when `NO_COLOR` is set. Degrading to
one line per step rather than a redrawn animation is what makes the output
readable in a log, and it is also the largest accessibility win available
here, because a screen reader re-announces a redrawn region. Honouring an
`ACCESSIBLE` variable as an alias for that same plain path costs nothing,
since the code path exists anyway.

**The whole thing is about eighty lines of Go and no new dependency.** A
terminal check, a colour helper that emits escape codes only when they are
wanted, and a progress line. The established toolkit alternative pulls in
seven further modules against the three direct dependencies this project has
today, and its current major version resolves through a vanity domain its
maintainers control rather than through a forge — which is a supply-chain
point worth raising explicitly in a repository whose CI scans full history
for secrets and whose conventions require sign-off before a dependency is
added. A full terminal-application framework is the wrong shape regardless:
this progress reporting is linear and non-interactive, and the framework's
own escape hatch for that case is a plain writer.

One constraint has quietly lifted. The bundle already requires macOS 26, and
that release gave the system terminal 24-bit colour for the first time in
about two decades, so every target user has it. The remaining argument for
staying inside the sixteen base colours is a better one anyway: they respect
the theme the user chose, and hex codes do not.

## The install channel must be recorded

An updater that replaces a bundle underneath a package manager desynchronises
it, which is why several tools refuse to self-update when they were installed
by one. Gropius has no such channel today, and while un-notarised it cannot
have one — but the cheap defence is to record how the installation happened
at install time, and have `update` refuse, naming the right command, when it
was not the bootstrap that put the bundle there. Writing that down now costs
a field; discovering it later costs a support thread.

The staged swap should be ported rather than rewritten. It already quits the
running copy, stages beside the old bundle, and renames into place, so a
failed copy leaves a working installation rather than none — which is the
minimum bar the one comparable tool that does this well also sets.

## Risks and open questions

**An updater promotes a deferred issue to a blocker.** Any `v`-prefixed tag
currently ships as a full release, and a release candidate sorts after the
release it follows under version sort, so it can capture the "latest"
pointer. That is recorded as a deferred versioning-policy question today.
With an updater in the product it stops being deferrable: it would deliver
release candidates to every installation. This is a prerequisite, not a
consequence.

**Only the current release stays published.** The retention convention keeps
tags and deletes superseded releases, so an updater can resolve only the
latest release. There is no rollback target and no way to fetch the version
one is currently running. An update that goes wrong is recovered by
reinstalling, which means the bootstrap must stay independently usable.

**One bundle, many accounts — and sometimes two bundles.** The system
applications directory holds one bundle that every account launches, not one
copy per person, so an update there necessarily acts for everyone. The
per-user fallback adds a genuine second location when a standard account
installs alongside an administrator's copy. Both cases break the rule this
note first stated, which was that an update should act only on the copy it is
running from: in the shared case there is no such thing.

The case that decides the design is the port. A second server does not start;
it becomes a client of the one already running. So if Bob is logged in and
holding the port when Alice updates, her copy quits, the bundle is replaced,
the grant is renewed, the update reports success — and the new binary loses
the port to Bob's still-running old version and starts as a client. The Mac
goes on serving the previous version, from a bundle that no longer has a
name, until Bob logs out. Nothing in the update reports this. Until there is
an answer to it, an updater should not ship.

**Environment detection must inform, never decide.** A recently accepted
decision draws a line between what a detection over another process's or
system's state may inform — presentation, wording, ordering — and what it may
never decide: authentication, admission, validation, or the firing condition
of a warning. Both the update check and the doctor checks sit on that line. A
doctor that reports "the firewall grant is missing" is informing. A doctor
that suppresses a warning because it inferred the grant is present would be
deciding, and would fail open.

**Adding subcommands must not break the flags.** `-headless` and `-root` are
in use, and a binary that gains subcommands has to keep accepting them.

**One finding falls outside this note and should be captured separately.**
The control panel is served over a loopback TCP port, which any web page the
user visits can reach, and DNS rebinding defeats naive origin checks. Two
2025 CVEs are exactly this class in shipped software, and both were fixed
with an origin allowlist plus a per-launch token. It is unrelated to
installation and should not ride along here.

## What was rejected

- **A separate installer binary**, in Go or Rust: circular to obtain, a named
  anti-pattern, and without precedent among comparable products.
- **Disk images, browser downloads and Homebrew**: all blocked by quarantine
  while the bundles are un-notarised.
- **Sparkle, for now**: its EdDSA path is primary and its Developer ID path
  is only a fallback, so it may work without a Developer ID — but the
  ad-hoc-to-ad-hoc case with a changing code hash is undocumented, and the
  staged-swap logic already exists in this repository. Revisit if the
  membership is bought.
- **Instructing anyone to clear quarantine attributes or weaken Gatekeeper**:
  unsupported, globally weakening, and a poor look for software that opens a
  network listener.
- **Automatic background update checks**: rejected in favour of checking only
  while the panel is open.
