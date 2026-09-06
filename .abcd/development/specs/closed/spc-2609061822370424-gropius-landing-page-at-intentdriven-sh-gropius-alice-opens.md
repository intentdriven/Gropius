---
id: spc-2609061822370424
slug: gropius-landing-page-at-intentdriven-sh-gropius-alice-opens
intent: itd-2609061353254616
origin: researcher-authored
production_mode: hand-written
---
# gropius-landing-page-at-intentdriven-sh-gropius-alice-opens

## Summary

This spec delivers the public landing page at `intentdriven.sh/Gropius`: a
static page composed by abcd's `site` verb from text this repository already
holds, rendered inside the release chain and deployed to the Cloudflare Worker
named `gropius` by a job that is the only holder of the hosting credential. The
page carries the identity block's title, tagline and pitch, a download button
that resolves to the current release's `Gropius.app.zip`, the installer
one-liner, a link to the repository, the platform requirement, and the app's
four-form mark. Release-derived facts — version, date, asset sizes, the
checksums link — are not this record's; they arrive with
itd-2609061353258535.

## Scope

In scope:

- A composition manifest at `.abcd/site.json` and the page template and static
  inputs under `site-src/`, carried into the tree from the 2026-09-06
  prototype (currently only in the gitignored scratch tier).
- Registration of the page as an identity surface in `.abcd/positioning.json`.
- `wrangler.jsonc` at the repository root, naming the rendered assets directory
  and the route the Worker answers on.
- `.github/workflows/site.yml` — resolve, render and deploy jobs — and the
  non-gating `site` job in `.github/workflows/release.yml` that calls it.
- A test-only Go package `internal/sitetest` that holds the page's checkable
  properties against `build/icon.svg`, `build/Info.plist` and the identity
  block.

Out of scope:

- Every release-derived fact on the page, and the release-record read that
  supplies it (itd-2609061353258535).
- Any analytics, script or cookie. The page works with scripts disabled.
- Notarisation, TLS for the Gropius server itself, and the app-bundle minimum
  version raise (iss-2609061543584785, a dependency, not a deliverable).

## Approach

**Composition, not authorship.** The page is an `abcd site` composition. The
prototype's HTML becomes the template under `site-src/`, its CSS becomes
`site-src/site.css`, and the words it renders come from repository text
selected by path and heading through `.abcd/site.json`: the `identity` block
points at `.abcd/development/IDENTITY.md#identity-canonical`, the pillar and
requirement copy at spans of `README.md` and `docs/getting-started.md`, and
the interface strings the generator may add sit in the closed allowlist
`site-src/ui.json`. Display copy that is nobody's canonical text (the hero
headline, the section labels) is an interface string; the title, tagline and
pitch are the identity block's and are held to it by `abcd site check`'s hero
gate.

**Output shape.** `abcd site build --out site` renders into a `Gropius/`
subdirectory of the assets directory, so the served path matches the route and
every internal reference stays relative. `site/` is untracked; the build writes
nowhere else and reaches no network.

**Deploy chain, on abcd's pattern.** `.github/workflows/site.yml` carries three
jobs. `resolve` does no checkout and decides mode and tag. `render` runs in the
GitHub Environment `site-render`, which holds **no secret**, checks out the
released commit, renders the page and uploads it as an artefact. `deploy` runs
in the Environment `site`, downloads the artefact and runs `wrangler deploy`
with `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` from that environment;
it is the only job that ever holds a credential. Both environments are gated on
branch `main` and tags `v*`, so a dispatch from an arbitrary branch cannot run
edited workflow content with those secrets. `release.yml` gains a `site` job
with `needs: release` that calls `site.yml` with the released tag: a site
failure turns the run red and cannot change a byte of what the release
published. The Worker's own automatic builds on push stay disconnected; the
release chain is the only deployer, and `wrangler.jsonc` records that as the
tree's durable statement of dashboard state.

**Routing.** `wrangler.jsonc` sets `"name": "gropius"`, an `assets` block whose
`directory` is the rendered output, and a route pattern matching the
`/Gropius` path under the site's zone. The deploy asserts the route, so a
drifted attachment is reclaimed on the next release rather than left wherever
the dashboard last put it.

**Checks in this repository.** `internal/sitetest` parses the committed
template and the rendered output and holds the properties below. It is a test
package in the `archtest`/`mlxtest` mould: no network, no browser, and it fails
`make test` when the page and the app disagree.

## How each acceptance criterion is satisfied

1. _Given Alice opens the page at a 1280x800 viewport, when it has loaded, then
   the download button and the link to the repository are both visible without
   scrolling._ The hero and the action row are the first two sections; the
   installer block moves below them, which is where the prototype already put
   it. `internal/sitetest` asserts the action row precedes the install section
   in document order and that the cumulative fixed block heights above it stay
   under the fold budget; the rendered-overflow audit at 1280x800 runs as
   `abcd site check`'s browser job in CI.
2. _Given the release marked latest is a given version, when Alice activates the
   download button, then the browser fetches that release's `Gropius.app.zip`._
   The button's href is the forge's `releases/latest/download/Gropius.app.zip`
   redirect, which carries no version literal. `internal/sitetest` asserts the
   href shape and that no version string appears anywhere in the page source.
3. _Given the page is registered as an identity surface, when the identity check
   runs, then it reports no drift between the page and the canonical block._ A
   `landing-hero` surface is added to `.abcd/positioning.json` requiring
   `tagline`; `abcd identity` runs in CI on the rendered output, and
   `abcd site check`'s hero gate holds eyebrow, tagline and pitch against the
   block through the same parser. `internal/sitetest` asserts the tagline in the
   rendered page equals the block's, so the bar is also held by `make test`.
4. _Given the tagline in the canonical identity block is edited, when the page is
   rendered again, then the page carries the new tagline._ The tagline is a
   selected span, not template text. The test renders the composition against a
   fixture identity block with an altered tagline and asserts the output carries
   it — this is what distinguishes a render from a lint.
5. _Given the platform requirement printed on the page, when it is compared with
   the minimum the shipped app bundle declares, then both say macOS 26._ The
   page says "Requires macOS 26"; `internal/sitetest` reads
   `LSMinimumSystemVersion` from `build/Info.plist` and asserts the two agree.
   The raise itself is iss-2609061543584785, and this test is what keeps them
   together afterwards.
6. _Given the page rendered in its dark theme, when its four-form mark is compared
   with the app icon source, then the arrangement and the four fill colours are
   the same._ The mark is inline SVG using theme tokens. `internal/sitetest`
   parses `build/icon.svg` and the page's mark and asserts the same 2x2
   arrangement — triangle top-left, grey square top-right, blue square
   bottom-left, red circle bottom-right — and that the dark-theme token values
   equal the icon's `#F5C518`, `#B4B6BB`, `#2B5DAA`, `#E63329`. Geometry
   (cell-to-gutter ratio) deliberately is not compared: the prototype's
   proportions are a design choice.
7. _Given the page is viewed at a 375-pixel-wide viewport, when it renders, then
   nothing scrolls horizontally and the download button is the first action._
   The template carries the viewport meta, an `overflow-x: auto` container above
   the install block, `img { max-width: 100% }`, and no inline fixed width above
   390px; `internal/sitetest` asserts all four statically and asserts the
   download button is the first element of the action row. `abcd site check`'s
   `mobile` gate holds the same set, and its browser leg audits real overflow.

## Trust-boundary review notes

No Go package inside a trust boundary changes: `internal/gateway`,
`internal/runtime`, `internal/config` and `internal/hub` are untouched. The
boundary this record does move is the release chain's:

- The `render` job takes `contents: read` only and holds no secret, so a
  compromise of the render step yields no credential and cannot write to a
  release.
- `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` live only in the `site`
  environment, scoped to the one Worker, and are read by exactly one step. They
  are never exposed to the render, and the render's output crosses to deploy as
  an artefact, not as a shared checkout.
- Both environments are gated on `{main, v*}`. The residual, stated plainly:
  whoever can merge to `main` can change the workflow and have it run with those
  secrets. That is this repository's ordinary review boundary, not a new one.
- The page is static, sets no cookie, runs no script and reaches only its own
  origin and the web-font service; it stores nothing about a visitor, which is
  what adr-2609061503319212 requires of every public surface.

## Docs to change

- `README.md`: add the page's address beside the install one-liner, in present
  tense, as where a non-developer starts.
- `docs/getting-started.md`: section 1 states "Requires macOS 26" so the page
  and the tutorial promise the same platform.
- No new page. The site's own text is composed from these two files, so a
  sentence written for the page is written into them.

## Dependencies and sequencing

- Depends on iss-2609061543584785 (raise the bundle's declared minimum to macOS
  26). Criterion 5 fails until it lands, which is the point of the test.
- Gated on the release marked latest being v0.1.2 or later, the first release
  whose assets are the attested-checksums set — a scope condition, not a code
  dependency.
- The end-to-end site setup for the rendering tool is tracked in that tool's own
  repository as itd-2609061543533170. This record does not wait for it; where a
  capability is missing, the open design point below says how to proceed.
- itd-2609061353258535 (spc-2609061822372715) builds on this record and adds the
  release section. It replaces the prototype's static "what a release contains"
  list; this record ships that list only as the four asset names, without sizes
  or a version, and hands the region over.

## Open design points

- Whether the release facts reach the render as a `site-src/release.json` input
  to `abcd site build` or as flags on the build command is settled by
  itd-2609061543533170 in the rendering tool's repository. This record's page
  works with neither; the implementer picks whichever exists when
  spc-2609061822372715 is implemented.
- The light-theme derivation of the four mark colours is the prototype's and is
  accepted as a design decision; only the dark-theme values are held to the icon.
- Whether `internal/sitetest` renders through the tool's binary or against a
  committed golden render is the implementer's call; the bars above must hold
  either way, and `make test` must not require a network.

## As built (2026-09-06)

Three things this record describes were built differently. They are stated here
rather than edited into the paragraphs above, so what was planned and what
shipped both stay readable.

**The renderer is `cmd/gropius-site`, not `abcd site`.** abcd's `site` verb
renders its own fixed site shape — a home page plus chapters from `docs/` — and
its manifest schema carries no template key, so it cannot render this page
today. A standard-library renderer in this repository composes it instead, from
the same `.abcd/site.json` manifest in abcd's identity/ui_strings shape, so the
verb can take the page over when it grows the capability
(itd-2609061543533170 in that repository). Consequently `abcd site check` — and
with it the hero gate, the mobile gate and the browser leg this record names —
does not exist in this repository's flow.

**`abcd identity` does not run in CI; `make site` plus a Go test hold the
surface.** The `landing-hero` surface is registered and names the rendered page,
which is a build artefact, so on an unrendered checkout the check reports it
*absent* and stays green. What holds the page to the block on every run is
`internal/sitetest`, which renders the page, compiles that surface's own
patterns from `.abcd/positioning.json`, runs them over the render and compares
the capture with the block — and which also binds the registration to the path
`make site` writes. Running the identity binary in CI was declined for now: it
is not installed on the runners, and fetching another repository's release into
this repository's release path needs its own sign-off.

**Criteria 1 and 7 are held by static models, with no browser leg.** Criterion 1
sums the stylesheet's own paddings, type sizes, leadings and tracking at
1280x800, estimates how many rows the two actions occupy, and fails if the
download button starts below the fold. Criterion 7 asserts the viewport meta,
the scrolling container above the install block, `img { max-width: 100% }`, the
absence of any fixed width above 390px, and that the download button is the
first action. Neither observes a rendered viewport: `make test` reaches no
browser and no network, which this record requires of it. The residual is
stated plainly — a font-metric change can move the real layout without moving
the model — and the models are labelled as models where they are implemented.
