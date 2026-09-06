---
id: adr-2609061503319212
slug: no-public-telemetry-local-telemetry-only-as-a-strict-opt-in
status: accepted
date: 2026-09-06
supersedes: [itd-2609061521134968]
superseded_by: null
related_intents: [itd-2609061521082551, itd-2609061521102742]
related_rfcs: []
related_adrs: []
---

# ADR-2609061503319212: No public telemetry; local telemetry only, as a strict opt-in

## Context

Gropius is a local inference server whose promise is that prompts, models and
usage stay on the operator's Mac and their own network. The gateway already
refuses to read prompt content, error messages are redacted before they reach
a client, and nothing committed may carry a hostname, address or path. What
was not yet settled was whether the project itself may learn anything about
its installs, and whether the operator may see anything about their own
server beyond the per-model logs.

The question needed a standing answer before any observability feature was
designed, so that every later intent inherits the same boundary instead of
re-litigating it. A research pass on 2026-09-06 (research note
`2026-09-06-anonymous-telemetry-sota`) surveyed what comparable local LLM
tools and open-source developer tools do. Every opt-out design in that survey
produced a public backlash; a random install id is pseudonymous personal data
under current EDPB guidance; the one opt-in design that survived scrutiny at
scale, the Go toolchain's, needs a population an experimental menu-bar app
does not have; and users of local-LLM tools treat even an update check as
suspect.

## Decision

We will collect no public telemetry, ever. Nothing about usage, hardware,
errors, models or configuration leaves the machine to the project, to a
vendor or to any third party. This covers crash reports, update checks that
carry metadata, and the telemetry of child processes and libraries Gropius
runs.

We will offer telemetry only locally: recorded on that Mac, for the operator
of that Mac, and shown in Gropius's own control panel or logs.

We will make local telemetry a strict opt-in, off by default, with the switch
in Settings. Until the operator turns it on, Gropius records nothing beyond
the operational logs it already writes. Even when on, local telemetry never
records prompt text, completions, API keys, or anything the existing error
redaction removes.

## Alternatives Considered

1. **Anonymous opt-in public telemetry** with no identifier, a weekly cadence
   and published aggregates, in the style of the Go toolchain. Rejected: the
   population is too small for the numbers to mean anything, and for a
   local-LLM tool the mere existence of an upload path is brand-negative even
   when technically clean.
2. **Opt-out public telemetry**, as Homebrew, Next.js and the GitHub CLI do.
   Rejected: every surveyed case produced a backlash, and it contradicts the
   product's promise outright.
3. **No telemetry of any kind, including local.** Rejected: the operator has
   a legitimate need to see what their own server is doing. The boundary is
   the machine, not the feature.
4. **Local telemetry on by default, with an opt-out.** Rejected: a default
   that writes usage records to disk is a surprise on a shared Mac, and the
   shared-cache install mode makes the log directory readable by more than
   one account. Opt-in keeps the default state identical to today.

## Consequences

- Any intent that adds metrics, a request log or a statistics view is scoped
  to local storage and display, and must state how the opt-in gates it. The
  2026-09-06 research on local on-device observability is the input to that
  design.
- The outbound connections Gropius makes are exactly those needed to fetch
  models from HuggingFace and to provision its runtime. A docs page can state
  this as a promise, and a test in the architecture test package can hold it
  by refusing any other outbound host.
- Child processes inherit environment that disables their libraries' own
  telemetry where a switch exists, alongside the existing offline flag.
- GitHub release download counts and user-initiated diagnostics attached to
  issues are the project's only sources of install and failure data. That is
  a real cost: bugs a user would not bother to report stay unseen.
- Settings gains one switch, and the docs gain one sentence next to it saying
  what turning it on records and where.
