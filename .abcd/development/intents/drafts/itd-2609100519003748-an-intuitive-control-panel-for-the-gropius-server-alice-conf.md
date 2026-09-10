---
id: itd-2609100519003748
slug: an-intuitive-control-panel-for-the-gropius-server-alice-conf
spec_id: null
kind: null
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# An intuitive control panel for the Gropius server: Alice configures the server from a page that shows what is on before it asks what to change, finds each setting by the task it serves rather than by its key in the file, and is told in the server's own words, as she types, why a value cannot be saved — a state-of-the-art site that stays a page served from this Mac alone, reachable by every account on it and by nothing on the network, so that Bob at the next desk and Carol on the mesh see the API and never the panel

## Press Release

> _Seeded from a quoted-text intent capture. Expand into the full press-release narrative before planning._

## Why This Matters

An intuitive control panel for the Gropius server: Alice configures the server from a page that shows what is on before it asks what to change, finds each setting by the task it serves rather than by its key in the file, and is told in the server's own words, as she types, why a value cannot be saved — a state-of-the-art site that stays a page served from this Mac alone, reachable by every account on it and by nothing on the network, so that Bob at the next desk and Carol on the mesh see the API and never the panel

## Mechanism

> _Prompted (the claim-recording gradient): why the authors expect this to work, as a falsifiable "we expect X because Y" — not the outcome restated. Replace this line with the claim, or with the exact token `None stated.` alone on its line to record the claim as considered and declined._

## Scope Conditions

> _Required (the claim-recording gradient): the population, platform, scale, or assumptions this claim holds under, one per top-level bullet — `abcd intent plan` stamps each with a persistent identity. Replace this line with those bullets, or with the exact token `None stated.` alone on its line._

## Acceptance Criteria

> _Required (the itd-1 discipline): add at least one Given-When-Then bullet describing the verifiable bar for "shipped" before this draft can be planned._

## Open Questions

- **Whether the panel may take a build step, a UI framework, or any asset
  fetched from outside the Mac.** Opened as a decision to take before this
  draft is planned (DECISIONS.md, 2026-09-10), and it is two decisions in one.
  It is a dependency decision: AGENTS.md requires explicit sign-off before any
  new dependency, and the panel today is static vanilla files served from the
  binary, with no build step and a test in `internal/ui` that binds `app.js`
  to Go by lifting its functions into node. And it is a boundary decision: an
  asset fetched from a CDN is a new outbound host, which the no-telemetry
  decision refuses, and the page is served from a Mac that may have no
  internet at all, so a page that needs one to render is a page that does not
  render. The maintainer decides with both costs visible.
- **What the redesign re-homes rather than rebuilds.** The resources block on
  the Models tab (itd-2609091903463596) and the usage-measurement additions to
  the Usage tab (itd-2609091712141073) land before this is planned; the
  interview treats them as inputs the redesign reorganises, never as things
  to build twice, and the three-surfaces sync test (itd-2609081259493890)
  should exist first so a redesign cannot drop a control unnoticed.
- Whether "intuitive" needs a written principle, or stays as this press
  release's stance: state before settings, facts not warnings, nothing
  interrupts the task.

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._
