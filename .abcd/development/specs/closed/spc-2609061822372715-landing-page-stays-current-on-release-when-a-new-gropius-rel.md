---
id: spc-2609061822372715
slug: landing-page-stays-current-on-release-when-a-new-gropius-rel
intent: itd-2609061353258535
origin: researcher-authored
production_mode: hand-written
---
# landing-page-stays-current-on-release-when-a-new-gropius-rel

## Summary

This spec makes the landing page a view over the release record. When the
release chain publishes a release, the render job reads the release the forge
flags as latest, writes it beside the page as a machine-readable input, and the
composition renders a release section: the version, the publication date, every
asset with its size, and a link to the checksums file. No release-dependent
fact is written into the page source by hand, nothing is fetched at request
time, and no standing token exists — the read happens once, inside the release
run, under the workflow's own token.

## Scope

In scope:

- A step in `site.yml`'s `render` job that resolves the release flagged latest
  and writes it as a release record file beside the page inputs.
- A `release` block in `.abcd/site.json` and the release section in the
  `site-src/` template that renders from it.
- Graceful absence: when the record cannot be read, the page renders without a
  release section and the deploy proceeds.
- The static origin audit: the rendered page's external references, and the
  `site-src/headers` map it ships.
- Fixture-driven tests of the selection rule and the rendering, in
  `internal/sitetest`.

Out of scope:

- The page itself, the wrangler configuration, the workflow's three-job shape
  and the deploy credential — all shipped by itd-2609061353254616.
- A what-changed excerpt. Release notes today are generated pull-request
  titles rather than reviewed prose, so the page shows none.
- Any request-time read. There is no Worker script, no cache-freshness bound to
  test, and no release API dependency at page load.

## Approach

**Where the facts come from.** `.github/workflows/site.yml`'s `render` job
gains one step before the build. Under the job's floor of `contents: read`, with
`GH_TOKEN` set to the workflow's own `github.token`, it calls the forge's
`releases/latest` endpoint — the same endpoint the installer follows, and the
one that excludes drafts and pre-releases — and writes `site-src/release.json`
holding the tag, the publication date, and for each asset its name, its size in
bytes and its browser download URL. The value is derived from the release the
forge flags as latest, never from the newest created release and never from the
tag the run happens to carry, so a re-published older tag cannot move the page.

**No new credential.** The read runs in `site-render`, which holds no secret;
the workflow's own token is scoped to this repository and is read-only here.
The deploy job still holds the only credential in the chain. Nothing about this
step reverses the "the render job holds no credential" property.

**Rendering.** `.abcd/site.json` gains a `release` block naming
`site-src/release.json` as the source for the release section, and the template
gains that section: a heading carrying the version and the publication date, a
list of assets each with its name and its human-readable size, and a link to
`SHA256SUMS.txt` on that release. The checksums shown are informational; the
page links the release's own published attested checksums, which is what a
reader verifies against.

**Graceful absence.** If the resolve step fails, or the file is missing or does
not parse, the build renders the page without the release section and the deploy
continues. The download button is unaffected — it is the forge's
`releases/latest/download/…` redirect and carries no version. This is the
property that keeps a forge outage from taking the page down: the page is
static files, and the last good render stays served either way.

**Region ownership.** The static four-name asset list
spc-2609061822370424 ships is replaced by this section, not duplicated
alongside it. After this record there is exactly one place on the page that
names assets.

## How each acceptance criterion is satisfied

1. _Given a release named on the page, when Bob merges a changelog roll that
   publishes a newer release, then the page names the newer release and the only
   change to the repository since the previous release is that changelog roll._
   The auto-release chain tags the newest dated changelog heading, `release.yml`
   publishes, and its `site` job calls `site.yml`, whose render resolves
   `releases/latest` afresh. No page source is edited. The bar is checkable from
   the commit log; in CI it is held by an end-to-end test in `internal/sitetest`
   that runs the render against a fixture release record and asserts the section
   names that release's version with no repository edit in between.
2. _Given a published release, when Alice reads the release section, then the
   version, the publication date, every asset with its size and the checksums
   link match that release's own record._ Every one of those values is read
   from `site-src/release.json`; none is a literal in the template.
   `internal/sitetest` renders against a fixture record and asserts a field-by-
   field match, including that each asset in the record appears exactly once
   with its size and that the checksums link points at the record's
   `SHA256SUMS.txt` URL.
3. _Given a release created more recently than the one flagged latest, when the
   page is produced, then the page names the release flagged latest._ The resolve
   step reads only `releases/latest`; the selection is a pure function of the
   fetched record. `internal/sitetest` holds it as a fixture test: a records
   fixture whose newest-created entry is not the flagged one, asserting the
   flagged one is chosen. This is testable as a unit precisely because tags are
   immutable under the release gate and the case cannot be produced in
   production without hand-creating a stale release.
4. _Given the release record cannot be read while the page is being produced,
   when Alice loads the page, then it serves successfully with its download
   button and without a release section._ The build treats a missing or
   unparseable `site-src/release.json` as "no release block" rather than an
   error. `internal/sitetest` renders with the file absent and with the file
   holding malformed JSON, and asserts in both cases that the page renders, the
   download button's href is unchanged, and no release section is emitted.
5. _Given the deployed page, when it is loaded with the network inspector open,
   then no request goes anywhere other than the page's own origin and the web
   font service, and the page sets no cookie of its own._ The page carries no
   script and no `Set-Cookie`; the only cross-origin references are the two web
   font hosts, kept by decision. `internal/sitetest` walks every `src`, `href`
   and `url()` in the rendered output and fails on any host outside the
   allowlist {own origin, the font stylesheet host, the font file host}, and
   asserts the output contains no `<script>` element and no cookie-setting
   markup. `site-src/headers` ships the matching content-security policy so the
   property is enforced at serve time, not only asserted at build time. Cookies
   set by the hosting platform itself are outside the project's control and
   outside this bar, which names the page's own.

## Trust-boundary review notes

No Go package inside a trust boundary changes. The boundary that moves is the
release chain's, and only within the shape spc-2609061822370424 established:

- The new read runs in the credential-free `site-render` environment with the
  workflow's own token at `contents: read`. It cannot write to a release, and a
  compromise of the step yields no hosting credential.
- `site-src/release.json` is generated by the workflow and is never committed;
  it is an input to a build that reaches no network, so the render itself stays
  offline and deterministic given that file.
- The values from the release record are rendered as text and as URLs into the
  page. The asset names and the download URLs come from a release this
  repository's own workflow published; the render escapes them as ordinary
  text, so a hostile asset name cannot inject markup. `internal/sitetest`
  asserts escaping on a fixture with markup in an asset name.
- Nothing about a visitor is recorded, which keeps adr-2609061503319212's
  no-public-telemetry commitment intact for this surface.

## Docs to change

- `README.md`: the release section is a rendered view of the release record —
  one sentence, present tense, beside the page's address.
- `docs/getting-started.md`: the download step says the page always shows the
  release flagged latest and links the checksums the reader verifies against.

## Dependencies and sequencing

- Depends on itd-2609061353254616 (spc-2609061822370424): the page, the
  composition manifest, the wrangler configuration and the three-job site
  workflow. This record edits those artefacts and ships none of them.
- Gated on the release marked latest being v0.1.2 or later, so the asset set
  the section describes is the attested-checksums set.
- Releases reach the page only through the changelog-driven release gate; a tag
  pushed by hand takes the same path, and nothing else deploys.
- The rendering tool's end-to-end site setup is itd-2609061543533170 in that
  tool's repository. It settles the input mechanism named in the open design
  point below.

## Open design points

- Whether the release record reaches the build as `site-src/release.json` or as
  build flags is settled by itd-2609061543533170. The implementer follows
  whichever exists; the criteria above are written against the values, not the
  transport.
- The human-readable size format (whether `1.2 GB` is computed at render time
  or the byte count is rendered and formatted in CSS) is the implementer's call;
  the bar is that the number matches the record's byte count.
- Whether the release section also names the previous release is deliberately
  unanswered and out of scope for the first cut; the page names the current one.

## As built (2026-09-06)

Six things this record describes were built differently. They are stated here
rather than edited into the paragraphs above, so what was planned and what
shipped both stay readable.

**The record is an argument, not a source file.** The open design point is
settled the other way from this record's own prose: the release facts reach the
render as `--release <file>`, and the workflow writes that file into the
runner's temp directory rather than into `site-src/release.json`. A file under
`site-src/` would sit among the files the composition manifest selects the
page's copy from, and nothing in the tree would say it is generated; a path
handed to the render says it plainly. The rendered values are unchanged, which
is what this record's criteria are written against.

**The record's absence leaves the static asset list, not an empty region.** This
record says the page renders "without a release section" when the record cannot
be read. What ships is the region spc-2609061822370424 already had — the
heading and the three file names a release carries, with no version, no size and
no date. It is what `make site` produces locally and what the workflow produces
when the forge cannot be read. The property the criterion is about holds either
way: the page serves, the download button is untouched, and nothing on it can go
stale. A blank column beside the install block would have been a worse page for
no gain in honesty.

**A malformed record fails the render; only a missing one is graceful.** The
graceful case is decided by the caller: the workflow's record step is
`continue-on-error`, and the render passes `--release` only when the file
exists. A file that exists and does not parse, or parses and is wrong, stops the
render — because at that point something in the chain is broken, and a page
quietly missing its facts would hide it.

**Markup in a record value is refused, not escaped.** This record asks for an
escaping assertion on a fixture with markup in an asset name. The renderer
refuses such a record instead: an asset name must match a release asset's file
name, a version must be a `vX.Y.Z` tag, and every URL must be `https`. The
fixture is still there (`TestTheRecordIsRefused`, cases "a version carrying
markup" and "an asset name carrying markup"); the outcome asserted is the
stronger one.

**The origin audit walks subresources, not every href.** Criterion 5's
satisfaction paragraph says the test walks every `src`, `href` and `url()`. It
walks `src`, the `<link>` elements that fetch something, and `url()` in the
stylesheet. An `<a href>` is a place a reader may choose to go, not a request the
page makes, so holding the anchors to the same allowlist would have failed the
page for linking its own repository. `site-src/headers` ships the matching
policy, and a test refuses a host in that policy the page never fetches.

**`docs/getting-started.md` is untouched.** This record's docs list names a
download step there that says the page shows the release flagged latest. That
guide has no download step: it is the build-from-source tutorial, and the
download and the installer live in `README.md`, which is where the sentence
went. Adding one to the guide would also have disturbed the spans the page
composes its platform requirement from.
