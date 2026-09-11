---
id: itd-2609081718469419
slug: serve-only-over-the-private-network-a-bind-mode-that-narrows
spec_id: spc-2609081750378874
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609081303525417]
severity: minor
impact: additive
promoted_from: iss-2609081221415955
origin: extracted-from-record
production_mode: hand-written
---
# Serve only over the private network: Alice chooses one setting, and Gropius stops answering on the local network — while her own Mac, including a second account on it, still reaches the server

## Press Release

Alice runs models on a Mac other people share, and reaches them from her laptop
over a mesh VPN. She would like the server reachable that way and not from the
office Wi-Fi. Today her only options are everyone on the network, or nobody but
this Mac.

Gropius offers a third. With it chosen, the gateway answers on the private
network and on this Mac, and stops answering on the local one. Her laptop
reaches it. The machine at the next desk does not. And she keeps what the narrow
option has always cost her: the control panel still opens, and the second
account she keeps for testing still works, because this Mac is always part of
the bind.

Gropius names the address it chose, in Settings and on the posture page. If more
than one address looks like a private network — a mesh VPN and a corporate VPN
can look identical to it — it does not guess. It refuses to start the mode and
says why.

## Why This Matters

Narrowing the bind is the one honest way to reduce who can reach the server.
adr-2609081118587999 rule 3 says so and says why: a bind is enforcement Gropius
owns end to end and fails closed, where an inference about another process's
state fails open the moment that state changes without Gropius being told.

The narrowing that already exists is unusable. iss-7 records that binding a
specific address leaves the loopback-only control panel unreachable, so the
option that most deserves to be used locks the operator out of their own app.
itd-2609081303525417 removes that cost by guaranteeing this Mac is always in the
bind. This intent is what makes the narrowing worth choosing once it is
possible: an operator who has a private network should not have to know its
address, watch it change, and re-enter it.

The gateway serves plain HTTP with no TLS. On the local network the prompts, the
completions and the API key cross the wire in the clear; over a mesh VPN the
same traffic is inside an encrypted tunnel. This is the difference between that
being a choice and being a theoretical one.

## Mechanism

We expect that an operator who has a private network will narrow to it if the
narrowing does not require them to track an address that changes — that the
obstacle to the existing specific-address bind is not the exposure judgement but
the bookkeeping. This is wrong if operators who have this mode available choose
a wildcard bind anyway once itd-2609081303525417 has made the specific-address
bind usable, which would mean the address bookkeeping was never what stopped
them and this mode buys nothing the plain bind does not.

## Scope Conditions

- A Mac running a mesh VPN installed the ordinary way, where the private address <!-- cond: cond-2609081750377395 -->
  has the shape those products normally give it, and where exactly one address
  has that shape. Two matching addresses is not a degraded case of this
  condition — it is outside it, and the mode refuses rather than degrades.
- macOS serving one or more user accounts, where a second account reaching the <!-- cond: cond-2609081750373398 -->
  server over loopback is a case that occurs and must keep working.
- IPv4. `internal/netshape` enumerates IPv4 only, so a private network reached <!-- cond: cond-2609081750373739 -->
  over IPv6 has no candidate address and this mode cannot select it. Stated so a
  later IPv6 mesh is a visible re-decision rather than a silent miss.

## Acceptance Criteria

- Given the mode is chosen and exactly one address has the private-network
  shape, When the gateway starts, Then the set of addresses it answers on is
  exactly that address and this Mac's loopback address.
- Given that state, When a connection arrives on any other address this Mac
  holds, Then it is refused.
- Given the mode is chosen and more than one address has the private-network
  shape, When the gateway starts, Then it refuses to start the mode, names the
  candidates, and serves this Mac only — it never picks one.
- Given the mode is chosen and no address has the private-network shape, When
  the gateway starts, Then it serves this Mac only and never a wider set.
- Given the mode is running, When Alice opens Settings or the posture page, Then
  the address the mode selected is named there.
- Given the mode is chosen, When a second instance of Gropius starts in any
  account on this Mac, Then exactly one of them serves — the singleton holds
  across differing bind addresses.
- Given the mode is running, When the private network's address changes or
  disappears, Then the set of addresses served never widens, and the panel does
  not go on offering an address the server no longer answers on.
- Given the mode is chosen, When Gropius advertises over Bonjour, Then it does
  not advertise to networks the bind excludes — or, if that is not achievable,
  the mode states plainly that discovery is not narrowed.

## Open Questions

- Bonjour. `ExposedToLAN()` is true for a private-network address, so the advert
  goes out on every interface: hostname, port, model count and whether a key is
  required reach the LAN this mode exists to exclude. Either advertising is
  scoped to the bound interface, or it is off in this mode, or the intent says
  the bind narrows connections and not discovery. The last is honest and the
  weakest.
- The singleton. Measured on this hardware: a process holding the wildcard and a
  process binding loopback on the same port BOTH succeed, because
  `acquireListener` has only `EADDRINUSE` to go on and that signal exists only
  while every instance binds the same address. With differing binds two
  instances both believe they won and both load models. The candidate design is
  that loopback is acquired first and is the contention point for every mode,
  with the second listener acquired only by the winner and failure to acquire it
  failing closed — which also repairs the probe, which contacts loopback only.
  This is itd-2609081303525417's design to make; this intent inherits it.
- Where the mode lives in the configuration. A sentinel value in `Host` is a
  landmine: a word like "private" passes host validation, then fails to listen,
  and the app exits with no panel and no recovery but editing the file — the
  precise failure this intent exists to remove. It wants its own field.
- Whether the Settings form can carry a third option at all today: it hard-codes
  two, reads the stored host into a `<select>` that may not offer it, and posts
  the result back, so a host the form does not know is posted as empty and
  refused. Captured separately; it blocks the Settings half of this intent.

## Audit Notes

<!-- abcd-review: OWED receipt=rcp-c2bdc5190c49 -->
Fidelity review OWED (receipt rcp-c2bdc5190c49).

## Grounds

- pursued: the specific-address bind that adr-2609081118587999 rule 3 already admits becomes usable the moment itd-2609081303525417 lands, and we expect it still will not be used — because it asks the operator to know an address a mesh VPN can change under them, and to notice when it does. This mode exists to remove that bookkeeping, not to remove an exposure judgement the operator has already made. Shown wrong if operators with this mode available choose a wildcard bind anyway once the plain narrowing works, which would mean the bookkeeping was never the obstacle and the mode buys nothing the plain bind does not. Shown wrong a second way if the ambiguity refusal fires often in the field: a mode that refuses more than it serves is a worse answer than asking the operator to pick once.
