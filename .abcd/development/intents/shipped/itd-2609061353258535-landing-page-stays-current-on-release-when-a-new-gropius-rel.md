---
id: itd-2609061353258535
slug: landing-page-stays-current-on-release-when-a-new-gropius-rel
spec_id: spc-2609061822372715
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609061353254616]
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Landing page stays current on release: when a new Gropius release is cut, the page at intentdriven.sh/Gropius reflects it without anyone editing the page

## Press Release

A new Gropius release is cut. Nobody opens the landing page's source. When
Alice next visits intentdriven.sh/Gropius, the page names the new version and
the day it was published, lists each file the release contains with its size,
and links the checksums she can verify her download against. Bob, who cut the
release by merging the changelog roll, did nothing else afterwards.

## Why This Matters

A landing page that names a version goes stale the day after the next release,
and a stale version number is worse than none: it tells Alice the project is
abandoned. The release run already produces everything the page needs, so this
intent closes the loop and makes the page a view over the release record rather
than a copy of it.

Builds on itd-2609061353254616, the landing page itself.

## Mechanism

We expect the page to name a new release with no hand edit because the release
chain already publishes a machine-readable record of the version, its assets
and their checksums, and the page is produced from that record inside the same
chain; we are wrong if the page's facts diverge from the release record within
two releases.

## Scope Conditions

- Releases are cut through the changelog-driven release gate, so a release <!-- cond: cond-2609061822379351 -->
  reaches the page only when that chain has run to completion.
- The release marked latest is v0.1.2 or later, so the asset set the page <!-- cond: cond-2609061822379885 -->
  describes is the attested-checksums set.
- No release-dependent fact is written by hand into the page source; version, <!-- cond: cond-2609061822374667 -->
  date, asset names, asset sizes and the checksums link all come from the
  release record.
- The rendered page is served as static files under its address, so the page <!-- cond: cond-2609061822379119 -->
  loads even when the release record cannot be reached.

## Acceptance Criteria

- Given a release named on the page, when Bob merges a changelog roll that
  publishes a newer release, then the page names the newer release and the
  only change to the repository since the previous release is that changelog
  roll.
- Given a published release, when Alice reads the release section, then the
  version, the publication date, every asset with its size and the checksums
  link match that release's own record.
- Given a release created more recently than the one flagged latest, when the
  page is produced, then the page names the release flagged latest.
- Given the release record cannot be read while the page is being produced,
  when Alice loads the page, then it serves successfully with its download
  button and without a release section.
- Given the deployed page, when it is loaded with the network inspector open,
  then no request goes anywhere other than the page's own origin and the web
  font service, and the page sets no cookie of its own.

## Open Questions

- Resolved: the page is produced when the release is cut and deployed as
  static files; nothing is fetched at request time, so there is no runtime
  dependency on a release API and no standing token.
- Resolved: the page shows the version, the publication date, each asset with
  its size and a link to the checksums file; the what-changed excerpt is
  dropped, because release notes today are generated pull-request titles
  rather than reviewed prose.
- Resolved: "current" means the release flagged latest, never the most
  recently created release, matching the rule the installer already follows.
- Resolved: the render lives beside the page in this repository; a
  configuration file names the directory of static files, and a separate
  deploy job holds the hosting credential so the render job holds none.
- Resolved: there is no cache-freshness bound to test, because the page is
  produced and deployed inside the release run rather than refreshed on a
  timer.
- Resolved: the checksums shown on the page are informational; verification
  is against the release's own published attested checksums, which the page
  links to.
- Resolved: the title no longer names a hosting mechanism; the promise is that
  the page reflects the release without a page edit.
- Depends on: itd-2609061353254616, the landing page this intent keeps current.

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-1b8dfae179dc -->
Fidelity review — receipt rcp-1b8dfae179dc (verifier intent-auditor claude-opus-5[1m]).

Provenance: intent-auditor@claude-opus-5[1m] · rubric_hash sha256:f3bba86a84b329fcbfbd3df64ec5d77d382dda9b4b1a61baf887e316bc41f38e · prompt_hash sha256:83c0c74bd1d356dfb03b6d482d85aca53008f1542de87abf4886aaee742481ff
Input attestations: tree:HEAD 18f1a4e286fd85f6e94bb2ae1d2ecba40e13cc77 (branch main; history rewritten during a rename, so no per-spec commit range exists and the tree as shipped was audited)@sha256:5f3256dd3718af34e459add3c3c30b5d631e90af;

Acceptance rollup: MET 2 · MET_WITH_CONCERNS 3 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET_WITH_CONCERNS: The committed tree renders twice against two records and names each record's own release with no source edit between, and release.yml's site job calls site.yml after the publish, so a newer release reaches the page unattended; the criterion's second clause — that the only repository change since the previous release is the changelog roll — is not checkable here because the history was rewritten to a single commit, and the tree can only show that no page edit is needed, not that none was made.
  evidence: internal/sitetest/release_test.go:91 — "func TestANewerReleaseChangesThePageAndNothingElse(t *testing.T)"
  evidence: internal/sitetest/release_test.go:113 — "if strip(first) != strip(second) { t.Error("a new release changed the page outside the release region; only the release facts may move with the record") }"
  evidence: .github/workflows/release.yml:320 — "site: needs: release uses: ./.github/workflows/site.yml"
  evidence: internal/sitetest/release_test.go:337 — "func TestTheReleaseChainRendersThePage(t *testing.T)"
  evidence: .github/workflows/auto-release.yml:72 — "version="$(grep -m1 -E '^## \[v?[0-9]+\.[0-9]+\.[0-9]+\] - ' CHANGELOG.md"
- ac-2 — MET: Every value in the release region is read from the record and none is a literal: the template interpolates version, published date, each asset name/size/URL and the checksums href, loadRelease derives them from the record file, a field-by-field test asserts the rendered region against the fixture record (including each asset exactly once with its size and the checksums link equal to the record's checksums_url), and a separate test refuses any version, size, date or download URL written into the template.
  evidence: site-src/index.html.tmpl:96 — "< p class="release-line">< a class="mono" href="{{ .Release.HTMLURL }}">{{ .Release.Version }}< /a>< span>{{ .UI.ReleasePublished }} {{ .Release.Published }}< /span>< /p>"
  evidence: site-src/index.html.tmpl:99 — "< li>< a class="mono" href="{{ .URL }}">{{ .Name }}< /a>< span>{{ .Size }}< /span>< /li>"
  evidence: site-src/index.html.tmpl:102 — "< p class="release-note">< a href="{{ .Release.ChecksumsURL }}">{{ $.UI.ChecksumsLabel }}< /a>< /p>"
  evidence: cmd/gropius-site/release.go:84 — "func loadRelease(path, repoURL string) (*releaseView, error)"
  evidence: internal/sitetest/release_test.go:141 — "func TestTheReleaseSectionMatchesTheRecordFieldByField(t *testing.T)"
  evidence: internal/sitetest/release_test.go:186 — "href := attr(t, region, `<a href="([^"]*SHA256SUMS[^"]*)"`) if href != record.ChecksumsURL"
  evidence: internal/sitetest/release_test.go:650 — "func TestTheReleaseRegionCarriesNoLiteralFact(t *testing.T)"
  evidence: cmd/gropius-site/release.go:162 — "if r.ChecksumsURL != checksums { return zero, fmt.Errorf("checksums_url %q is not this release's %s (%q)""
- ac-3 — MET: electLatest picks the entry the forge flags as latest and refuses a list with none or two flagged, or a flagged draft, pre-release or non-release tag; its fixture test elects v1.4.0 over two entries published later that are not flagged, and the workflow is held to listing with isLatest, electing through `gropius-site select`, fetching the elected tag, and reading no name the run's own tag could arrive under.
  evidence: cmd/gropius-site/release.go:230 — "func electLatest(list []forgeRelease) (forgeRelease, error)"
  evidence: cmd/gropius-site/release_test.go:211 — "func TestTheFlaggedLatestIsElectedAndNotTheNewest(t *testing.T)"
  evidence: cmd/gropius-site/release_test.go:214 — "{TagName: "v2.0.0-draft", IsDraft: true, PublishedAt: "2026-09-09T00:00:00Z"}, {TagName: "v1.4.0", IsLatest: true, PublishedAt: "2026-09-01T00:00:00Z"}"
  evidence: internal/sitetest/release_test.go:230 — "func TestTheReleaseRecordIsTheFlaggedLatestRelease(t *testing.T)"
  evidence: internal/sitetest/release_test.go:249 — "for _, steer := range []string{"INPUT_TAG", "inputs.tag", "needs.resolve.outputs.tag", "GITHUB_REF_NAME", "github.ref_name"}"
  evidence: .github/workflows/site.yml:288 — "tag="$(go run ./cmd/gropius-site select --from "$list")""
- ac-4 — MET_WITH_CONCERNS: The page renders and serves with its download button unchanged when no record is passed, the record step is continue-on-error so a forge that cannot be read costs the facts and not the deploy, and the render only passes --release when the file exists; the concerns are two signed-off as-built divergences — what stands in the region's place is not an absent section but the static list of what a release contains (heading plus three file names), and a record that exists but does not parse or validate stops the render rather than degrading, so only ABSENCE is graceful.
  evidence: internal/sitetest/release_test.go:276 — "func TestThePageRendersWithoutAReleaseRecord(t *testing.T)"
  evidence: internal/sitetest/release_test.go:303 — "href := attr(t, page, `<a class="btn btn-primary" href="([^"]+)"`) if !strings.HasSuffix(href, "/releases/latest/download/Gropius.app.zip")"
  evidence: .github/workflows/site.yml:277 — "continue-on-error: true"
  evidence: .github/workflows/site.yml:341 — "if [ -s "$RELEASE_JSON" ]; then go run ./cmd/gropius-site --out site --release "$RELEASE_JSON" else"
  evidence: .github/workflows/site.yml:346 — "go run ./cmd/gropius-site --out site"
  evidence: site-src/index.html.tmpl:103 — "{{- else }} < h2>{{ .UI.AssetsHeading }}< /h2> < ul class="assets">"
  evidence: cmd/gropius-site/release.go:16 — "Every check below therefore STOPS the render. The ABSENCE of a record is the graceful case"
  evidence: .abcd/development/specs/closed/spc-2609061822372715-landing-page-stays-current-on-release-when-a-new-gropius-rel.md:202 — "The record's absence leaves the static asset list, not an empty region."
- ac-5 — MET_WITH_CONCERNS: The rendered page (with and without a record) is walked for every subresource, link that fetches, preconnect/dns-prefetch hint and stylesheet url(), and fails on any host outside {own origin, fonts.googleapis.com, fonts.gstatic.com}; it is asserted to carry no < script> element and no cookie-setting markup, and site-src/headers ships a matching default-src 'none' policy that the render writes to _headers at the assets root and a test holds directive by directive. The concerns: the criterion is stated over the DEPLOYED page under a network inspector, and the only deployed-page check is a continue-on-error diagnostic that cannot fail the run, so serve-time conformance is asserted rather than gated; and by signed-off divergence the walk deliberately excludes < a href> anchors, which point off-origin to the forge.
  evidence: internal/sitetest/release_test.go:441 — "func TestThePageRequestsNothingOffItsOwnOrigin(t *testing.T)"
  evidence: internal/sitetest/release_test.go:453 — "for _, cookie := range []string{"document.cookie", `http-equiv="set-cookie"`, `http-equiv="Set-Cookie"`}"
  evidence: internal/sitetest/release_test.go:462 — "func TestTheOriginAuditCatchesAnAddedOrigin(t *testing.T)"
  evidence: internal/sitetest/release_test.go:500 — "func TestTheContentSecurityPolicyMatchesThePagesReferences(t *testing.T)"
  evidence: site-src/headers:33 — "Content-Security-Policy: default-src 'none'; script-src 'none'; style-src 'self' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
  evidence: internal/sitetest/release_test.go:633 — "func TestTheRenderWritesTheHeadersFileAtTheAssetsRoot(t *testing.T)"
  evidence: .github/workflows/site.yml:456 — "- name: Check that both spellings of the address answer"
  evidence: .github/workflows/site.yml:465 — "continue-on-error: true"
  evidence: .abcd/development/specs/closed/spc-2609061822372715-landing-page-stays-current-on-release-when-a-new-gropius-rel.md:215 — "The origin audit walks subresources, not every href."

Gap audit:
- honoured:
  - The page is a view over the release record rather than a copy of it: version, publication date, every asset with its size, and the checksums link all come from the record.
    evidence: cmd/gropius-site/release.go:84 — "func loadRelease(path, repoURL string) (*releaseView, error)"
    evidence: site-src/index.html.tmpl:94 — "{{- if .Release }}"
  - "Current" means the release flagged latest, never the most recently created — the same election the download button's releases/latest redirect and install.sh follow.
    evidence: cmd/gropius-site/release.go:215 — "electLatest picks the release the page names: the one the forge FLAGS as latest, never the most recently created"
    evidence: install.sh:102 — "fetch "SHA256SUMS.txt" "$tmp/SHA256SUMS.txt""
  - Nothing is fetched at request time and no standing token exists: the forge is read once inside the release run, the render reaches no network, and the page is static files.
    evidence: cmd/gropius-site/release.go:8 — "Nothing in this file reaches the network. The release run reads the forge once, writes the record as a file, and hands over the path"
    evidence: wrangler.jsonc:1 — ""assets": { "directory": "./site", "html_handling": "auto-trailing-slash" } — no worker script is declared"
  - The render job holds no deploy credential; the deploy job holds the only one in the chain.
    evidence: .github/workflows/site.yml:218 — "environment: site-render"
    evidence: .github/workflows/site.yml:394 — "environment: site"
  - The checksums link is the release's own SHA256SUMS.txt asset, looked up by name, so a release without one produces no record instead of a link that 404s.
    evidence: cmd/gropius-site/release.go:157 — "if checksums == "" { return zero, fmt.Errorf("assets carries no %s; the page links the checksums a reader verifies against", checksumsAsset) }"
  - README states in one sentence that the page shows the release flagged latest, lists every file with its size, and links the checksums.
    evidence: README.md:85 — "latest, lists every file it carries with its size, and links the checksums to"
- diverged:
  - The record reaches the build as site-src/release.json, and .abcd/site.json gains a `release` block naming it.
    evidence: cmd/gropius-site/main.go:118 — "release := fs.String("release", "", "release record to render the release facts from; without it the page carries none")"
    evidence: .abcd/site.json:13 — ""headers": "site-src/headers" — the manifest carries no `release` key; the record is an argument, written to the runner's temp directory"
    evidence: .github/workflows/site.yml:280 — "RELEASE_JSON: ${{ runner.temp }}/release.json"
  - When the record cannot be read the page renders "without a release section".
    evidence: site-src/index.html.tmpl:104 — "< h2>{{ .UI.AssetsHeading }}< /h2>"
    evidence: .abcd/development/specs/closed/spc-2609061822372715-landing-page-stays-current-on-release-when-a-new-gropius-rel.md:202 — "The record's absence leaves the static asset list, not an empty region."
  - Markup in a record value is escaped, asserted on a fixture with markup in an asset name.
    evidence: cmd/gropius-site/release.go:50 — "var assetNameRe = regexp.MustCompile(`^[A-Za-z0-9] [A-Za-z0-9._+-]*$`)"
    evidence: cmd/gropius-site/release_test.go:89 — "func TestTheRecordIsRefused(t *testing.T)"
  - A single `releases/latest` read resolves which release the page names.
    evidence: .github/workflows/site.yml:250 — "- name: Read the release the forge flags as latest — three moves: list, elect through `gropius-site select`, then fetch the elected tag"
    evidence: .github/workflows/site.yml:288 — "tag="$(go run ./cmd/gropius-site select --from "$list")""
  - The election's fixture test lives in internal/sitetest.
    evidence: cmd/gropius-site/release_test.go:211 — "func TestTheFlaggedLatestIsElectedAndNotTheNewest(t *testing.T)"
  - docs/getting-started.md's download step says the page always shows the release flagged latest.
    evidence: README.md:85 — "the sentence went to README.md; docs/getting-started.md is the build-from-source tutorial and has no download step"
- missing:
  - A runtime observation of the deployed page's network activity — the criterion's own "loaded with the network inspector open" — that can fail the chain.
    evidence: .github/workflows/site.yml:465 — "continue-on-error: true — the deployed-page check is a diagnostic, not a gate"
    evidence: .github/workflows/site.yml:456 — "- name: Check that both spellings of the address answer"
  - Evidence in the repository that the only change since the previous release was the changelog roll.
    evidence: CHANGELOG.md:312 — "## 0.1.2 - 2026-09-06 — the history was rewritten to a single commit, so no commit range demonstrates the claim"

Scope-condition dispositions:
- cond-2609061822379351 — survived: The page is rendered only from a release the forge has published and flagged latest — drafts and pre-releases are refused by the election, and the deploy is reached through release.yml's site job which needs the release job, so a release reaches the page only after the changelog-driven chain has completed.
  evidence: .github/workflows/release.yml:320 — "site: needs: release uses: ./.github/workflows/site.yml"
  evidence: cmd/gropius-site/release.go:244 — "case r.IsDraft: ... case r.IsPrerelease: ... return forgeRelease{}, fmt.Errorf("the release flagged latest (%s) is a pre-release", r.TagName)"
  evidence: .github/workflows/site.yml:101 — "refusing to run the site workflow from $REF: the site-render and site environments admit the default branch and v* tags only"
- cond-2609061822379885 — survived: The newest dated release in the changelog is 0.1.2, and the release workflow publishes SHA256SUMS.txt alongside the two app bundles, so the asset set the page describes is the attested-checksums set; the renderer additionally refuses any record that carries no SHA256SUMS.txt asset, which makes a pre-v0.1.2 asset set unrenderable rather than merely unlikely.
  evidence: CHANGELOG.md:312 — "## 0.1.2 - 2026-09-06"
  evidence: .github/workflows/release.yml:202 — "shasum -a 256 Gropius.app.zip GropiusChat.app.zip | tee SHA256SUMS.txt"
  evidence: .github/workflows/release.yml:231 — "assets="Gropius.app.zip GropiusChat.app.zip SHA256SUMS.txt""
  evidence: cmd/gropius-site/release.go:158 — "assets carries no %s; the page links the checksums a reader verifies against"
- cond-2609061822374667 — survived: No release-dependent fact is a literal in the page source: a test refuses any version, size, asset URL or date written into the release region of the template, and a second test fails if the rendered page names a version anywhere outside that record-driven region.
  evidence: internal/sitetest/release_test.go:650 — "func TestTheReleaseRegionCarriesNoLiteralFact(t *testing.T)"
  evidence: internal/sitetest/release_test.go:201 — "t.Errorf("the page names the version %q outside the release region; only the region may", m)"
  evidence: site-src/index.html.tmpl:86 — "Every value below comes from the release record the release run writes and hands to cmd/gropius-site with --release; not one of them may be written here."
- cond-2609061822379119 — survived: The deploy publishes a rendered directory of static assets with no worker script and no request-time fetch, and the page renders and serves with its download button when no release record exists at all, so an unreachable release record cannot stop the page from loading.
  evidence: wrangler.jsonc:1 — ""assets": { "directory": "./site", "html_handling": "auto-trailing-slash", "not_found_handling": "none" }"
  evidence: internal/sitetest/release_test.go:276 — "func TestThePageRendersWithoutAReleaseRecord(t *testing.T)"
  evidence: .github/workflows/site.yml:346 — "go run ./cmd/gropius-site --out site"
## Grounds

- pursued: we expect one public page with a working download to reach people who will not read a README, and rendering it at release to keep it correct without upkeep; we are wrong if downloads from the page stay flat against the release page's, or if the page drifts from the README within two releases
