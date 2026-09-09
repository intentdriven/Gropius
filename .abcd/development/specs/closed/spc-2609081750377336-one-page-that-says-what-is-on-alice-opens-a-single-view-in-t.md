---
id: spc-2609081750377336
slug: one-page-that-says-what-is-on-alice-opens-a-single-view-in-t
intent: itd-2609081718534201
origin: researcher-authored
production_mode: hand-written
---
# one-page-that-says-what-is-on-alice-opens-a-single-view-in-t

## Summary

One view in the control panel stating the server's posture as fact rather than
as warning: which addresses it answers on, what a key is required for, and what
is being recorded.

The design constraint that keeps it both cheap and honest is a single rule:
**the page is fed by `snapshot()` and nothing else.** Everything it states is
already on that payload, re-derived per render, so the page adds no new
observation, no new syscall on the render path, and no new way to be wrong.
Anything that would need a new source is out of scope by construction — which is
how "what leaves the Mac" was cut from the intent before planning.

## Scope

In: a view in the existing control panel; its strings; the honesty scan over
them; tests.

Out: any change to what is enforced. The page reports and gates nothing. Also
out: the warning banner at `internal/gateway/control.go`, which **stays** — see
below; the documentation half of `iss-2609081221484689`, which a status page
does not make redundant; and anything requiring a fact `snapshot()` does not
already carry.

## Approach

### What it says, and where each fact comes from

- **Who can reach it** — the endpoint list, with the mark `itd-2609081015545349`
  already ships. Not "who can reach the server", which is not observable: the
  bind is observable, reachability is not, and claiming it is rule 1's forbidden
  wording.
- **What a key is required for** — two facts, never collapsed into one.
  `gateway.go`'s loopback exemption means a key set is required from the network
  and not from this Mac, including other accounts on it. The collapsed sentence
  survives on the page and then misleads someone testing from a second account.
- **What is recorded** — the statistics switch, the retention settings, and that
  a record holds token counts and never prompt text. `StatsStore` is attached to
  the snapshot only when statistics are enabled, so absence *is* the
  observation, and the page needs nothing new to say "off".
- **What leaves the Mac** — only the two things that genuinely do and are
  observable: the **Bonjour advert**, which reaches the whole local network and
  carries hostname, port, model count and whether a key is required; and the
  **request log**, which is written locally and deliberately carries no client
  addresses. The outbound hosts Gropius contacts are static facts about the
  source rather than observations of state, and belong in documentation.
- **The limits** — where the honest answer is bounded, the page says what
  Gropius cannot see. A private-network mark cannot see public tunnelling or a
  sharing rule; both leave the interface untouched.

### The banner stays

The warning fires on a state the operator created seconds earlier in another
pane. This page is somewhere they may not visit for weeks. Removing an
interruption that reaches someone who is not looking, in favour of a page that
only reaches someone who is, is a security-control removal wearing an additive
intent's clothes — and it would destroy the evidence this intent's own Mechanism
is falsified against.

### The page is never an input to enforcement

Stated as a criterion in rule 2's own language, because the follow-on argument
writes itself: once the page says the control panel is reachable only from this
Mac, the next proposal is to reach it over the private network — the exact
relaxation adr-2609081118587999 rejects by name in its Alternatives. The page's
existence is never grounds for softening a refusal.

## How this satisfies the Acceptance Criteria

1. **States the four things, as fact** — asserted against the rendered view for
   a set of injected snapshots: exposed with a key, loopback-only, statistics on
   and off, a marked endpoint present and absent.
2. **Every line is an observation, and bounded answers say so** — each rendered
   line is traced to a snapshot field by test; a line with no field behind it
   fails.
3. **The private-network line names no vendor and claims no encryption** — the
   honesty scan already covers the panel's surfaces; this adds its strings to
   what the scan reads.
4. **No interruption** — asserted structurally: the view is reachable only by
   navigation, and renders nothing into any other view.
5. **Gates nothing** — an archtest rule that no enforcement decision reads this
   view's state, in the same shape as the detection rule, and with the same
   honest scoping: it catches an accidental coupling, not a determined one.
6. **Held to what the scan actually holds** — the criterion is worded to the
   scan's own contract: a clean run means these patterns do not appear in the
   passages scanned, not that the prose claims nothing.

## Open

- Where it lives: its own tab, or a section in an existing one. A tab is
  findable and adds a tab.
- Whether it reports only, or offers the fix for what it reports. Offering fixes
  makes it useful and makes it a settings page, which is what it was defined
  against.
- How much of `iss-2609081221484689` remains as documentation. The how-to for
  serving over a mesh VPN is not made redundant by a status page; the four
  warnings distributed through prose are.
