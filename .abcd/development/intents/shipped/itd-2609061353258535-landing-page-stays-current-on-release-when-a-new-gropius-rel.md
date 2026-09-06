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

<!-- abcd-review: OWED receipt=rcp-1b8dfae179dc -->
Fidelity review OWED (receipt rcp-1b8dfae179dc). The audit runs after the
branch merges: it reads the delivered diff against these acceptance criteria,
and the diff is not final until review closes.

## Grounds

- pursued: we expect one public page with a working download to reach people who will not read a README, and rendering it at release to keep it correct without upkeep; we are wrong if downloads from the page stay flat against the release page's, or if the page drifts from the README within two releases
