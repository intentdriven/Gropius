---
id: adr-2609091123526871
slug: gropius-binds-loopback-alongside-every-other-address-with-a-p
status: accepted
date: 2026-09-09
supersedes: null
superseded_by: null
related_intents: [itd-2609081303525417, itd-2609081718469419, itd-2609081015545349]
related_rfcs: []
related_adrs: [adr-2609081118587999]
---

# ADR-2609091123526871: Gropius binds loopback alongside every other address, with a private-network mode that fails closed to loopback

## Context

Gropius binds one address. `cmd/gropius/main.go` builds `"<host>:<port>"` from
`config.Config.Host` and hands it to `acquireListener`, which either takes the
port or works out who already holds it. Everything else follows from that single
socket: `Config.ExposedToLAN` decides how open the server is, `gateway.Endpoints`
decides what the panel offers a client, and `gateway.Control` refuses anything
that does not arrive over loopback.

Three records converge on that one socket.

**iss-7.** A hand-edited `Host` of `192.0.2.5` passes `Validate`, binds, serves
`/v1` — and strands the control panel. The panel is loopback-only by design; the
bind took loopback away; the menu bar's "Open Control Panel" opens `localhost`
and is refused at the TCP layer, while browsing the bound address from the same
Mac arrives with a non-loopback remote address and is refused by
`loopbackOnly`. The recovery is editing the same file back. The issue also
records a second fault in the same setting: `fmt.Sprintf("%s:%d", …)` cannot
build an IPv6 listen address, so an unbracketed `::1` fails at startup with "too
many colons in address". iss-7 was deferred because every available fix is a
design decision on a declared trust boundary, and the maintainer settled it at
interview on 2026-09-08: **loopback is always in the bind, whatever else is.**

**itd-2609081718469419 / spc-2609081750378874.** A third bind mode: serve on the
private-network address and on this Mac, and on nothing else. It is admissible
under adr-2609081118587999 rule 3 because it is a bind rather than an inference,
and under that ADR's 2026-09-08 amendment it may resolve its address from
`internal/netshape` on two conditions — ambiguity is refused rather than
resolved, and the selection is always shown. It fails closed to loopback-only
when the private network is absent at launch, and it is never automatic.

**itd-2609081015545349, already shipped.** Its fourth criterion is that the
panel lists only addresses the server answers on. That is false today: loopback
is listed under every bind, including the specific bind that refuses it —
`TestLoopbackIsListedUnderEveryBindIncludingOneItDoesNotAnswerOn` records the
divergence and hands it to iss-7.

All three want the same thing, and it is structural: **a bind is a set of
listeners, not an address.** Which forces the singleton to be re-reasoned,
because the singleton was designed for one.

### What the singleton rests on, and what was measured

`acquireListener` has exactly one signal: `EADDRINUSE` from `net.Listen`. On
that signal `probePortHolder` asks whoever is there to echo a nonce written into
the data root, over `http://127.0.0.1:<port>/api/instance`. A match means a
Gropius sharing our root (defer to it as a client); a mismatch or a refusal to
identify means something else holds the port (refuse rather than hand it this
Mac's model traffic).

`EADDRINUSE` is only a signal while every instance binds the same address.
Measured on this hardware, with Go's default listener options:

| first listener | second listener | result |
|---|---|---|
| wildcard | loopback | both succeed |
| loopback | wildcard | both succeed |
| a specific interface address | loopback | both succeed |
| loopback | a specific interface address | both succeed |
| loopback | loopback | second refused, `EADDRINUSE` |
| wildcard | wildcard | second refused, `EADDRINUSE` |

The first four hold across two processes as well as within one. So today, two
instances configured with different binds — one wildcard, one loopback-only —
both believe they won the port, both load models, and the GPU carries two copies
of everything the singleton exists to prevent. That is a fault in the shipped
build, not a new cost of this change; it is simply not reachable from the
Settings pane, which offers two hosts and no way to differ.

The last two rows are what makes a fix available: an exact duplicate bind is
still refused, so any address every instance is guaranteed to take is a
contention point. There is exactly one such address, and it is loopback.

## Decision

### 1. A bind is a listener set: loopback first, then at most one more

Every mode resolves to a **bind plan** — a value naming the addresses to
acquire, in order. The first entry is always this Mac's loopback address. The
second is present only when the mode names an address that is not loopback.

- **Wildcard** (`host: "0.0.0.0"`, the shipping default): loopback, then the
  wildcard.
- **This Mac only** (`host: "127.0.0.1"`): loopback alone.
- **A specific address or name** (hand-edited, or arriving from a panel that
  round-trips it): loopback, then that address.
- **Private network** (`bind_mode: "private-network"`): loopback, then the one
  address `internal/netshape` reports on a private network — or loopback alone,
  when there is no such address or more than one.

The wildcard case acquires loopback redundantly: the wildcard listener would
have answered there anyway, and on this platform the kernel routes a loopback
connection to the more specific socket. It is acquired all the same, because
rule 2 needs it to be, and because a rule with one exception is a rule two
people will read differently.

The plan is a value rather than an address built inline in `main`, so that
criterion 1 of the intent is testable without a second machine and without a
listener.

### 2. Loopback is the singleton's contention point and the probe's target

Loopback is acquired first, in every mode, by every instance. It is therefore
the one address two instances are guaranteed to collide on, and the measured
table says an exact duplicate bind is refused. The `EADDRINUSE` signal
`acquireListener` rests on is restored for every combination of modes, including
the wildcard-versus-loopback pair that defeats it today.

It also repairs the probe by construction rather than by coincidence.
`fetchChallengeAnswer` contacts `127.0.0.1` and nothing else; the winner is now
guaranteed to be listening exactly there, whatever else it is listening on. The
challenge file, its permissions and its shared-root reasoning are untouched.

Loopback failing to bind for any reason other than `EADDRINUSE` remains fatal:
lo0 is always up, so a machine that cannot take its own loopback address is not
one this app should guess about.

### 3. The second listener never widens anything, and its failure is fail-closed

Two failure kinds, and they are not the same.

**The address is not there** (`EADDRNOTAVAIL`, and every error that is not
`EADDRINUSE`). Gropius serves loopback only. It logs at error level, naming the
address it could not take and the reason, and the panel says the same thing in
words the operator can act on. It does not exit, and it does not fall back to a
wider bind — the mode can only ever narrow.

Serving instead of exiting is the point, not a softening. Today a bind to an
address that has gone away is "cannot listen" and exit 1, which leaves the
operator with no panel, no app and a file to hand-edit — precisely the trap
iss-7 records. Loopback-only with a loud explanation gives them the panel the
setting is changed from.

**Something else holds the port on that address** (`EADDRINUSE`). Because every
current instance takes loopback first, this cannot be a peer of the same build —
it is a foreign process, or a Gropius old enough to bind one address. So the
loopback listener is released and the acquisition falls back to what shipped:
probe the port, defer as a client to a holder that proves it shares our root,
refuse a holder that cannot, wait out a predecessor still shutting down. Holding
loopback while a predecessor of an older build owns the wildcard would break its
control plane and load a second copy of every model, which is the outcome the
singleton exists to prevent, and it is worth one extra code path to avoid during
the upgrade in which it is possible.

**The residual this leaves, accepted.** A foreign process holding *only* the
second address — that exact address and port, and not loopback — is
indistinguishable at the socket from the older-build case that this path exists
for. Loopback is released, the probe contacts loopback and finds nothing there
any more, the wait expires, and Gropius exits 1 with no panel: the iss-7
outcome, on a machine where another program happens to have taken one address
and one port. It is accepted rather than fixed because the alternative is to
keep loopback and serve while an unidentified process holds the address the
operator asked for, which is the hijack `probePortHolder` exists to refuse. The
recovery is the same as for any held port — stop the other process, or change
the port — and the log line names the address it could not take.

A narrower fix exists and is deliberately not taken here: probe the *second*
address as well before releasing loopback, so a holder that is not a Gropius is
told apart from one that is. It is a change to the port-ownership challenge,
which contacts loopback by design, and it belongs to whoever revisits that
handshake rather than to this record.

### 4. The endpoint list is derived from what was acquired

`gateway.Endpoints` reads the bind, then asserts loopback separately. It now
reads the addresses actually acquired. Loopback is listed because it is always
answered — true by construction rather than by exception, which is what
itd-2609081015545349's fourth criterion asked for. The second address is listed
only when it was acquired, so a mode that fell back to loopback offers exactly
what it serves.

One refinement, for the address that goes away while the server runs: an
acquired IP address is listed only while this Mac still holds it. The socket
stays open and receives nothing, so continuing to offer it would be the dead
address the criterion refuses. An acquired *name* is listed unconditionally —
there is nothing to compare it against, and resolution is the client's business.

### 5. Every listen address is built with `net.JoinHostPort`

This closes iss-7's second fault. An IPv6 literal is bindable in either
spelling: `ValidBindHost` accepts `::1` and `[::1]` alike, and stays exactly as
wide as the listener — `internal/config/host_test.go` pins each row of its table
to a listener that was watched to open or to fail. `URLHost` continues to
produce the one spelling a URL may carry, and `endpointURL` continues to bracket
through `JoinHostPort`, so nothing downstream doubles a bracket.

A value carrying a colon that is neither an IP nor a name — `192.0.2.5:8080` —
is still refused, because `JoinHostPort` would bracket it into an address no
listener takes.

### 6. The mode is its own configuration field, never a sentinel in `Host`

`bind_mode`, empty by default, meaning "`Host` decides" — which is every
configuration written by every build before this one, so an old file loads
unchanged. The one other value is `private-network`. `Validate` refuses anything
else, which sends a hand-edited file down the existing narrow-to-loopback path
with the rest of the operator's settings kept.

A sentinel in `Host` was refused by the spec and the reasoning stands: `private`
passes host validation as a name, fails to listen, and exits the app with no
panel and no recovery but the file.

`config.json` stays hand-editable, and the settings save keeps the rule it has:
the form owns `bind_mode` and posts it explicitly; a body that omits the field
preserves what is stored. A save is never refused over a field the operator did
not touch.

### 7. What is exposed is what was acquired, and the key follows it

Everything that decides how open this server is asks the **bind plan**: does
this bind answer anywhere a machine other than this one can reach? The key
requirement, the panel's keyless warning, Bonjour and the endpoint list all read
that one answer, so they cannot disagree.

The order is part of the decision. The sockets are acquired **first**, the key
is settled against what was acquired **second**, and only then is anything
mounted and served: `app.New` and the gateway are constructed after that line in
`cmd/gropius`, so no request is ever answered under an empty key. A bind that
reaches other machines and cannot be given a persisted key is narrowed by
**closing** the second socket — not by leaving it open and reporting it as shut.
A key held only in memory would vanish at the next start and reopen the
endpoint, which is why the exposure goes rather than the key (iss-1, unchanged
in substance and only in what it reads).

What follows from asking the sockets: a private-network mode that found no
address to bind serves this Mac and nothing else, and needs no key. A specific
address that has gone away is the same case. A mode that *did* acquire its
address takes the iss-1 path exactly as a wildcard bind does — generate,
persist, announce — because that is a server other machines reach with no key.

This does not reopen rule 2 of adr-2609081118587999. The plan's addresses were
resolved under that ADR's amendment, and what is asked of the plan here is
whether the sockets this process holds reach anywhere else — a fact about our
own listeners, not an inference about another process's state. Nothing consults
the classifier to decide.

`config.ExposedToLAN` stays, and stays honest about what it is: the answer about
a *stored configuration*. Its one remaining reader is `Validate`'s
eviction-grace rule, which runs at a save and at a load, where no socket exists
and nothing can say what a mode would bind. There the private-network mode
counts as exposed, deliberately: "might serve network callers" is the strongest
thing a stored configuration can be asked, erring closed costs a key on a
feature that is off by default, and erring open costs the queue that rule
protects.

**A second cost, and it is the one that widens rather than tightens.** Every
bind now holds loopback, and the gateway exempts loopback from the bearer check
— that exemption is what makes a second user account on this Mac a client, and
it is why the private-network mode's interview named that account as a case
that must keep working. Put together: under the private-network mode, and under
a specific-address bind, **any other macOS account on this Mac reaches `/v1`
without the API key, and reaches the loopback-only control plane.** That was
already true of the wildcard bind, which holds loopback and always has; it is
new for the two narrow binds, which previously did not hold loopback and
therefore admitted nobody at all on it.

It is accepted because it is what "this Mac is always in the bind" *means* —
the panel, the menu bar and the second account all reach the server the same
way, and no check can tell them apart, because from the socket they are the
same connection. What it is not is a detail to leave implicit: an operator who
narrows the bind to keep other people out may be sharing the Mac with them.
`docs/bind-address.md` states it in the operator's own terms, under "This Mac is
always in the bind".

### 8. Bonjour does not advertise in the private-network mode

The advert is mDNS on the local link. The mode's whole content is that the local
link cannot reach this server, so every advert it produces is an address the
recipient cannot connect to, carrying this Mac's hostname, the port, the model
count and whether a key is required, to exactly the network the mode exists to
exclude. Both halves of that are faults the shipped code already refuses
elsewhere: a dead address offered to a client, and a disclosure to a network the
bind excludes.

This is the strong branch of the intent's eighth criterion — it does not
advertise to networks the bind excludes — reached by not advertising rather than
by scoping the advert, which `internal/discovery` cannot do today. It reads the
configured mode, never the detection.

**Scope, said plainly: this reasoning is applied to the private-network mode and
to a bind that narrowed to this Mac, and to nothing else.** A hand-edited
specific-address bind still advertises. The advert is honest there in the way
that matters — it goes out on the local link, and a specific LAN bind answers on
that link — but it is not narrowed to the bound address: the advert carries this
Mac's name, a client resolves that name to every address the machine holds, and
a client that picks a different one is refused. That is a dead address offered
by discovery rather than by the endpoint list, and it is a smaller version of the
same fault. It is left because the alternative today is to switch discovery off
for every narrow bind, including the LAN-narrowed install where the advert is
genuinely useful, and because scoping an advert to one interface is a change to
`internal/discovery` rather than to the bind.

**It is also a start-time property.** The advert is started or not started once,
in `runServer`, from the mode and the plan as they are at launch. Changing the
mode in Settings does not withdraw a running advert; the change takes effect at
the next start, like the bind it describes.

### 9. Nothing re-binds after launch

The plan is resolved once, at startup. An address that appears later is not
acquired, and an address that disappears is not released; the served set never
widens, which is criterion 7's requirement, and the endpoint list stops offering
a departed address by rule 4 above.

What is out of scope, said plainly: Gropius does not notice the disappearance,
does not warn about it, and does not report the acquired address as unreachable.
The spec places that report on the posture page (itd-2609081718534201), which is
planned and not built. Until it is, the panel's endpoint list going quiet about
an address is the only signal, and that is a gap this record names rather than
one it closes.

### 10. Where the resolution lives, and what the guard says about it

`cmd/gropius` may not name the classifier — `internal/archtest` refuses it on
the ground that it decides whether an exposed bind may run at all — and
`internal/config` and `internal/app` may not have it in their dependency
closures. So the plan splits in two:

- a package holding the plan **type**, the mode constants and the address
  building, importing nothing of the classifier, which `internal/app` and
  `internal/gateway` may import freely;
- a package holding the **resolver**, which imports the classifier, applies the
  ambiguity refusal, and returns a plan of plain strings. `cmd/gropius` imports
  the resolver and acquires what the plan names, knowing nothing about why.

`internal/archtest/enforcement_detection_test.go` gains an explicit carve-out
naming this ADR and the amendment it rests on: the resolver package is the one
place outside the endpoint list that may read the classification, it is listed
as such rather than dropped into the "not enforcement" list, and the resolver
package name is scanned for alongside the classifier's, so that reaching the
detection *through* the resolver from the gateway, the panel or the command is
as loud as reaching it directly. A guard that grows a silent exception is the
failure this repository spent 2026-09-08 correcting.

## Alternatives Considered

- **One wildcard listener with a refusal rule in the handler.** Simplest to
  build: bind everything, drop what the mode excludes. Rejected — it moves the
  narrowing from the socket into a code path, which is the difference
  adr-2609081118587999 rule 3 turns on. A bind fails closed when the code is
  wrong; a filter fails open.
- **Refuse specific-address binds in `Validate`.** Removes iss-7's fault by
  removing the configuration. Rejected: rule 3 names the specific bind as the
  one honest narrowing, and the private-network mode is that narrowing with the
  bookkeeping taken off the operator.
- **A startup warning only**, the stopgap iss-7 itself proposed. Rejected: warn
  or refuse, neither gives the operator back the panel they change the setting
  from, and the maintainer's decision was that the fault is fixed at its cause.
- **Loosen `loopbackOnly` so the panel answers on the bound address.** Rejected;
  this is the exact change that rule's adversarial review exists to prevent, and
  the loopback rule's value is that it is defensible in one sentence.
- **Acquire the second listener first, and loopback after.** Rejected: the
  contention point would then differ per mode, which is the singleton fault
  measured above, and the probe would have no guaranteed target.
- **Reading exposure from the configuration rather than from the acquired
  sockets** — `ExposedToLAN` true whatever `Host` says whenever the
  private-network mode is chosen, so that a mode which might bind a network
  address can never be read as closed. This record proposed it, on the ground
  that it errs closed and costs only a key nobody needed. **The maintainer
  declined it on 2026-09-09**: a key demanded of a server that is serving this
  Mac and nothing else is friction with no exposure behind it — there is no
  network caller to defend against, and a loopback connection is exempt from the
  bearer check in any case — and an operator who meets that friction learns that
  Gropius's warnings are about settings rather than about their exposure. The
  adopted rule 7 above reads the sockets instead, which required moving the key
  decision after acquisition; the sequencing worry that had it rejected is
  answered by the order that rule states, since nothing serves between the two.
- **Leave Bonjour advertising and document the limit.** The spec calls this the
  honest and weakest answer. Rejected because the strong answer costs one
  condition, and because the advert is a disclosure to the excluded network
  rather than merely an unhelpful one.
- **A sentinel value in `Host`.** Rejected by the spec, for a failure mode this
  record is otherwise built to remove.

## Consequences

- iss-7 is resolved at its cause. A specific-address bind keeps the control
  panel, the second user account's loopback client and the API-key loopback
  exemption, and its IPv6 spelling fault goes with `net.JoinHostPort`.
- itd-2609081015545349's fourth criterion becomes true by construction: the list
  is derived from what was acquired, so no exception has to be written into the
  criterion for a bind that does not answer on loopback.
- The singleton is stronger than it shipped: two instances with different bind
  modes now collide, where today they both serve. The one case that gets a new
  code path is the upgrade window in which an older build holds the wide address.
- Two new packages exist, and the archtest rule set gains a named carve-out. The
  boundary is one resolver package producing one value; anything wider is a new
  decision under the amendment, not an extension of this one.
- A key is required for what was acquired: a private-network mode that found no
  address, and a specific address that has gone away, serve this Mac and are
  asked for no key. The stored configuration is no longer what decides that, so
  a save that narrows the bind does not take effect until the next start — which
  is true of the bind itself and is said in the panel and in
  `docs/bind-address.md`.
- A mesh-VPN peer cannot discover this Mac over Bonjour in the private-network
  mode. It could not connect to what the advert named in any case; clients use
  an address or a name.
- An address that disappears after launch is still bound and no longer served,
  and nothing says so beyond the endpoint list dropping it. The posture page
  (itd-2609081718534201) is where that report belongs and it is not built. The
  dropping itself reaches IPv4 literals only: `internal/netshape` enumerates
  IPv4, so a bound name or IPv6 literal has nothing to be compared against and
  stays listed after it goes away. `docs/bind-address.md` says so rather than
  leaving the page's claim wider than the code.
