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
- As built: the renderer, the identity check and the checks behind criteria 1
  and 7 differ from what the spec described. The divergences are stated in the
  spec's "As built (2026-09-06)" section.

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-4e582cc5457f -->
Fidelity review — receipt rcp-4e582cc5457f (verifier intent-auditor claude-opus-5[1m]).

Provenance: intent-auditor@claude-opus-5[1m] · rubric_hash sha256:78c8f85f7882affc26cf3006d8bce73e21732c38d608d3b4f5069c67a9f3cae8 · prompt_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5
Input attestations: tree:HEAD 18f1a4e286fd85f6e94bb2ae1d2ecba40e13cc77 (working tree; history rewritten during a rename, so no per-spec commit range exists)@sha256:unknown; render:make site -> site/Gropius/index.html, rendered by cmd/gropius-site from .abcd/site.json@sha256:unknown;

Acceptance rollup: MET 4 · MET_WITH_CONCERNS 3 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET_WITH_CONCERNS: The action row precedes the install section and a stylesheet-derived height model puts the download button and the repository link 665px down an 800px viewport, but nothing observes a real 1280x800 render: the spec's `abcd site check` browser leg does not exist in this repository.
  evidence: internal/sitetest/site_test.go:102 — "func TestDownloadAndSourceSitAboveTheFold(t *testing.T) {"
  evidence: internal/sitetest/site_test.go:137 — "action row starts 665px down a 800px viewport (test log; PASS)"
  evidence: site-src/index.html.tmpl:51 — "< div class="cta-row">"
  evidence: internal/sitetest/site_test.go:5 — "no network, no browser, no running server"
- ac-2 — MET: The rendered download button's href is the forge's version-free latest-release redirect for Gropius.app.zip, and a test forbids any version literal anywhere in the page, so it resolves to whatever release is marked latest.
  evidence: site/Gropius/index.html:49 — "< a class="btn btn-primary" href="https://github.com/intentdriven/Gropius/releases/latest/download/Gropius.app.zip">"
  evidence: internal/sitetest/site_test.go:219 — "func TestDownloadButtonResolvesTheLatestRelease(t *testing.T) {"
  evidence: internal/sitetest/site_test.go:232 — "the page carries the version literal %q; a static page cannot keep one current"
  evidence: .abcd/site.json:18 — ""download_asset": "Gropius.app.zip""
- ac-3 — MET_WITH_CONCERNS: The page is registered as the `landing-hero` surface and `abcd identity` run against the render reports it ok with 0 surfaces adrift; the concern is that the surface names a build artefact, so on an unrendered checkout it reports *absent* (green by vacuity), and no CI job invokes the identity binary at all.
  evidence: .abcd/positioning.json:landing-hero — ""id": "landing-hero", "files": ["site/Gropius/index.html"], "requires": ["tagline"]"
  evidence: site/Gropius/index.html:35 — "abcd identity output: ✓ [landing-hero] site/Gropius/index.html:35 — ok ... 0 surface(s) adrift of the block"
  evidence: internal/sitetest/site_test.go:312 — "func TestPageIsAnIdentitySurfaceAndCarriesTheTagline(t *testing.T) {"
  evidence: .github/workflows/ci.yml:59 — "run: go test ./... (no `abcd identity` step exists in any workflow)"
- ac-4 — MET: The tagline is a span selected from .abcd/development/IDENTITY.md by the manifest, and a passing test renders the composition against a fixture tree whose identity block carries an altered tagline and asserts the output carries the new one and no longer the old.
  evidence: internal/sitetest/site_test.go:384 — "func TestEditingTheIdentityBlockChangesThePage(t *testing.T) {"
  evidence: internal/sitetest/site_test.go:400 — "the page rendered from an edited identity block does not carry the edited tagline"
  evidence: .abcd/site.json:4 — ""identity": { "file": ".abcd/development/IDENTITY.md", "heading": "Identity (canonical)" }"
  evidence: site-src/index.html.tmpl:38 — "< p class="lede">{{ .Identity.Tagline }}< /p>"
- ac-5 — MET: The bundle declares LSMinimumSystemVersion 26.0 and the rendered page prints "Requires macOS 26"; a passing test derives the expected string from the plist so the two cannot drift apart silently.
  evidence: build/Info.plist:22 — "< string>26.0< /string>"
  evidence: site/Gropius/index.html:65 — "An < strong>Apple Silicon< /strong> Mac (M1 or later). < strong>Requires macOS 26.< /strong>"
  evidence: internal/sitetest/site_test.go:446 — "func TestPageAndBundleAgreeOnTheMinimumMacOS(t *testing.T) {"
- ac-6 — MET: The page's inline mark places the same four forms in the same quadrants as build/icon.svg and fills them from theme tokens whose values in both dark blocks are exactly the icon's four fills.
  evidence: build/icon.svg:7 — "< polygon points="302,132 472,472 132,472" fill="#F5C518"/>"
  evidence: site/Gropius/index.html:39 — "< polygon points="90,10 170,170 10,170" fill="var(--mark-yellow)"/>"
  evidence: site-src/site.css:49 — "--mark-yellow: #F5C518; --mark-grey: #B4B6BB; --mark-blue: #2B5DAA; --mark-red: #E63329; (@media prefers-color-scheme: dark)"
  evidence: site-src/site.css:70 — "--mark-yellow: #F5C518; ... (:root[data-theme="dark"])"
  evidence: internal/sitetest/site_test.go:470 — "func TestMarkMatchesTheAppIcon(t *testing.T) {"
- ac-7 — MET_WITH_CONCERNS: The viewport meta, the scrolling container above the install block, img max-width:100%, the absence of any fixed width above 390px, and the download button being the first action are all asserted and pass; the concern is that no rendered 375px viewport is ever observed, so real overflow is modelled rather than measured.
  evidence: internal/sitetest/site_test.go:597 — "func TestNothingScrollsSidewaysOnAPhone(t *testing.T) {"
  evidence: site-src/index.html.tmpl:5 — "< meta name="viewport" content="width=device-width, initial-scale=1">"
  evidence: site-src/site.css:322 — "overflow-x: auto;"
  evidence: site-src/site.css:93 — "img { max-width: 100%; }"
  evidence: site-src/index.html.tmpl:52 — "< a class="btn btn-primary" href="{{ .Links.Download }}">"

Gap audit:
- honoured:
  - One page carries the download, the installer one-liner and the repository link
    evidence: site/Gropius/index.html:49 — "releases/latest/download/Gropius.app.zip"
    evidence: site/Gropius/index.html:56 — "< a class="btn" href="https://github.com/intentdriven/Gropius">"
    evidence: site/Gropius/index.html:80 — "curl -fsSL https://raw.githubusercontent.com/intentdriven/Gropius/main/install.sh | bash"
  - The copy renders from the canonical identity block and repository text, not from prose written into the template
    evidence: .abcd/site.json:3 — "It names WHERE each block of the page comes from; it carries no prose."
    evidence: internal/sitetest/site_test.go:238 — "func TestInterfaceStringsAreLabelsNotProse(t *testing.T) {"
  - The page tells Alice plainly she needs an Apple Silicon Mac running macOS 26
    evidence: site/Gropius/index.html:65 — "An < strong>Apple Silicon< /strong> Mac (M1 or later). < strong>Requires macOS 26.< /strong>"
  - The page names the attested asset set and the installer note no longer mentions a signature tool
    evidence: site/Gropius/index.html:87 — "Gropius.app.zip / GropiusChat.app.zip / SHA256SUMS.txt"
    evidence: site/Gropius/index.html:62 — "The binaries are ad-hoc signed, not notarized; because the installer has verified the download..."
  - The page looks like the app: same four-form mark, same primary colours
    evidence: site-src/site.css:7 — "THE FOUR MARK COLORS ARE NOT FREE CHOICES."
  - Rendered inside the release chain by a deploy job that is the only holder of the hosting credential
    evidence: .github/workflows/site.yml:27 — "site-render the render job. NO SECRETS AT ALL."
    evidence: .github/workflows/release.yml:320 — "site:\n needs: release\n uses: ./.github/workflows/site.yml"
    evidence: wrangler.jsonc:36 — "{ "pattern": "intentdriven.sh/Gropius*", "zone_name": "intentdriven.sh" }"
  - The checks reach no network and no browser and gate every CI run
    evidence: Makefile:test — "go test -race ./..."
    evidence: .github/workflows/ci.yml:59 — "run: go test ./..."
- diverged:
  - The page is composed by abcd's `site` verb
    evidence: Makefile:27 — "go run ./cmd/gropius-site --out site"
    evidence: cmd/gropius-site/main.go:1 — "a standard-library renderer in this repository composes the page instead of `abcd site`"
  - `abcd identity` runs in CI on the rendered output
    evidence: .github/workflows/ci.yml:59 — "run: go test ./... (no identity step in any workflow)"
  - Criteria 1 and 7 are audited against a real rendered viewport by `abcd site check`'s browser leg
    evidence: internal/sitetest/site_test.go:5 — "no network, no browser, no running server"
    evidence: internal/sitetest/site_test.go:118 — "the height of everything above the action row ... from the stylesheet's own numbers"
- missing:
  - `abcd site check`'s hero gate, mobile gate and rendered-overflow browser job
    evidence: .github/workflows/site.yml:52 — "on: workflow_call / workflow_dispatch — render and deploy only; no `site check` step exists"
  - Any observation that the delivered page is served at intentdriven.sh/Gropius over HTTPS
    evidence: wrangler.jsonc:36 — "the route is declared in the tree; no delivered artefact attests the live attachment"

Scope-condition dispositions:
- cond-2609061822377282 — survived: The page states the Apple Silicon / macOS 26 requirement in its own requirements block, and the mobile-viewport properties the condition depends on are asserted and pass, so desktop and mobile visitors both get the page and the caveat.
  evidence: site/Gropius/index.html:65 — "An < strong>Apple Silicon< /strong> Mac (M1 or later). < strong>Requires macOS 26.< /strong>"
  evidence: internal/sitetest/site_test.go:597 — "func TestNothingScrollsSidewaysOnAPhone(t *testing.T) {"
- cond-2609061822377621 — narrowed: The static, script-free, path-routed shape is delivered — the render carries zero < script> elements, the Worker is assets-only and the route is a path under the zone — but HTTPS and the path attachment are asserted only as declarations in the tree, never observed against a served page.
  narrowing: Holds as a property of the rendered artefact and the declared route; the served-over-HTTPS-under-a-path half rests on wrangler.jsonc and the deploy job, with no delivered check that observes the live surface.
  evidence: wrangler.jsonc:36 — "{ "pattern": "intentdriven.sh/Gropius*", "zone_name": "intentdriven.sh" }"
  evidence: wrangler.jsonc:2 — "The landing page's Worker: static assets only, no script."
  evidence: site/Gropius/index.html:5 — "rendered page contains zero < script> elements"
- cond-2609061822378401 — untested: Nothing in the delivered tree exercises or contradicts the assumption about which release is marked latest: HEAD carries no tags, the page deliberately holds no version literal, and the download resolves through the forge's latest redirect whatever that release turns out to be.
- cond-2609061822374967 — survived: The manifest selects title, tagline and pitch from the canonical block while every other sentence is a named span or a closed-allowlist interface string, and a test fails the day a product claim is written into ui.json instead.
  evidence: .abcd/site.json:4 — ""identity": { "file": ".abcd/development/IDENTITY.md", "heading": "Identity (canonical)" }"
  evidence: internal/sitetest/site_test.go:238 — "func TestInterfaceStringsAreLabelsNotProse(t *testing.T) {"
  evidence: .abcd/positioning.json:landing-hero — ""requires": ["tagline"]"
## Grounds

- pursued: we expect one public page with a working download to reach people who will not read a README, and rendering it at release to keep it correct without upkeep; we are wrong if downloads from the page stay flat against the release page's, or if the page drifts from the README within two releases
