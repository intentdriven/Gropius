---
id: itd-2609061353254616
slug: gropius-landing-page-at-intentdriven-sh-gropius-alice-opens
spec_id: spc-2609061822370424
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Gropius landing page at intentdriven.sh/Gropius: Alice opens one link and gets the current release download, the installer one-liner, and the repository, on a page whose copy renders from the canonical identity block

## Press Release

Alice hears about Gropius and opens intentdriven.sh/Gropius. One page tells her
what it is, in the project's own words, and gives her three things to act on: a
download of the current release, the one-line installer command, and the
repository. She does not read a README to find the download, and she is not
sent to a release listing to guess which asset is the app. The page tells her
plainly that she needs an Apple Silicon Mac running macOS 26, and that the
downloaded app can be checked against the published checksums before she opens
it.

The page looks like the app she is about to install: the same four-form mark
and the same primary colours, so the Dock icon she sees ten minutes later is
the one the page promised.

## Why This Matters

The README is a developer surface. Someone who is not going to build from
source needs a single page that answers "what is it" and "how do I get it"
without scrolling past badges and build targets. A landing page also gives the
project one stable public address to hand out, independent of the repository
host. The design is founded on the 2026-09-06 prototype, which this intent
brings into the tree as the rendered page's template.

Release-derived facts on the page — the version, the asset sizes, the link to
the checksums — are kept current by itd-2609061353258535, which builds on this
intent rather than duplicating it.

## Mechanism

We expect one public page carrying a working download and the installer line to
reach people who never open a README, because the current release's asset sits
one click below the project's own address; we are wrong if asset download
counts stay flat after the page ships.

## Scope Conditions

- Visitors arriving on a desktop or mobile browser; the download itself is only <!-- cond: cond-2609061822377282 -->
  useful on an Apple Silicon Mac running macOS 26, which the page states.
- The page is static, served over HTTPS under a path rather than a domain root, <!-- cond: cond-2609061822377621 -->
  and works with scripts disabled.
- The release marked latest is v0.1.2 or later, so the assets the page names <!-- cond: cond-2609061822378401 -->
  are the attested set and no signature tool is required to verify them.
- The title, tagline and pitch shown on the page come from the canonical <!-- cond: cond-2609061822374967 -->
  identity block; the surrounding display copy is free text and is not held to
  it.

## Acceptance Criteria

- Given Alice opens the page at a 1280x800 viewport, when it has loaded, then
  the download button and the link to the repository are both visible without
  scrolling.
- Given the release marked latest is a given version, when Alice activates the
  download button, then the browser fetches that release's `Gropius.app.zip`.
- Given the page is registered as an identity surface, when the identity check
  runs, then it reports no drift between the page and the canonical block.
- Given the tagline in the canonical identity block is edited, when the page is
  rendered again, then the page carries the new tagline.
- Given the platform requirement printed on the page, when it is compared with
  the minimum the shipped app bundle declares, then both say macOS 26.
- Given the page rendered in its dark theme, when its four-form mark is
  compared with the app icon source, then the arrangement and the four fill
  colours are the same.
- Given the page is viewed at a 375-pixel-wide viewport, when it renders, then
  nothing scrolls horizontally and the download button is the first action.

## Open Questions

- Resolved: the page source lives in this repository and is rendered by the
  release job; a separate deploy job holds the hosting credential, so no
  standing secret sits beside the render.
- Resolved: abcd's site verb owns the page; the prototype's HTML is its
  template and the identifying copy comes from the canonical identity block.
- Resolved: intentdriven.sh/Gropius is the final address and is kept, so no
  move condition and no redirect are needed.
- Resolved: the two web faces keep loading from Google Fonts; self-hosting them
  was considered and declined.
- Resolved: the page says "Requires macOS 26" and the app bundle's declared
  minimum is raised to match, tracked as iss-2609061543584785.
- Resolved: the trust model is attested checksums, so the page names
  `Gropius.app.zip`, `GropiusChat.app.zip` and `SHA256SUMS.txt` and the
  installer note no longer mentions a signature tool.
- Resolved: release-derived content on the page is owned by
  itd-2609061353258535; this intent ships the page, not the refresh.
- Resolved: the end-to-end site setup for the rendering tool is tracked in that
  tool's own repository as itd-2609061543533170; this intent does not wait
  for it.
- Depends on: iss-2609061543584785, which raises the app bundle's declared
  minimum to the version the page promises.

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._

## Grounds

- pursued: we expect one public page with a working download to reach people who will not read a README, and rendering it at release to keep it correct without upkeep; we are wrong if downloads from the page stay flat against the release page's, or if the page drifts from the README within two releases
