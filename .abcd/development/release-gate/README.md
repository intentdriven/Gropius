# Release gate — the pre-tag procedure

This repo cuts releases the changelog-driven way (adr-37): rolling the
accumulated `## [Unreleased]` section into a dated `## [X.Y.Z] - <date>` heading,
in an ordinary reviewed PR, **is** the release decision. On merge to
`main`, `auto-release.yml` reads the newest dated heading, tags
exactly that commit, and calls `release.yml` to verify and publish. Nothing else
declares a release; an ordinary push with no new dated heading is a no-op.

The whole path runs on the built-in `GITHUB_TOKEN` — no personal access token,
no standing secret. Tags are immutable; a publish is idempotent and self-healing
(a missing Release is re-cut from the tagged commit, never a moved-on HEAD).

## Before the first real release: run the rehearsal

`release.yml` carries a `workflow_dispatch` **rehearsal**. Trigger it once before
the first public release. It arms the full gate against a simulated changelog
roll and reviewed-content commit, asserts the gate admits it, and **publishes
nothing** — no tag, no Release, no attestation. A green rehearsal is the
precondition for trusting the gate with a real tag: it proves the gate can be
satisfied before the repo goes public, closing the private→public activation gap
that otherwise surfaces only at the first real release.

## Deterministic gates (CI-enforced)

The `release.yml` `verify` job runs these, in order, on the released commit.
`release.yml` is authoritative; this list is the human-readable mirror.

1. Format (gofmt)
2. Build
3. Vet
4. Test
5. Test (race)

## Semantic gates (host-run, before the tag)

No semantic detector is configured for this repo, so the deterministic gates
alone admit a release and the receipt gate requires nothing — no host-run pass is
silently treated as missing. Opt in later by adding detectors to the release
gate; until then the release publishes on the deterministic gates.

## Reviews charter — sha-keyed receipt directories

Should this repo adopt a dated-review-dir discipline, the sha-keyed receipt
directories under `.abcd/work/reviews/<commit-sha>/` are **exempt** from the
dated-directory shape: they are keyed by commit, not date, so the two review
conventions do not collide.

## Procedure

1. Land all work; open the release the normal way (branch → PR → merge). The
   `verify` job gates the merge.
2. `auto-release.yml` tags `vX.Y.Z` on the merged commit and publishes, gated on
   the deterministic `verify` job alone.
