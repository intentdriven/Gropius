---
schema_version: 1
id: "iss-13"
slug: "go-mod-declares-areppel-gropius-but-the-repo-is-reppl-gropius"
severity: "nitpick"
category: "tech-debt"
source: "agent-finding"
found_during: "2026-08 bug-hunt round 10"
found_at: "go.mod"
---

`go.mod` declares `module github.com/intentdriven/Gropius`, but the repository's
actual GitHub location (confirmed via `git remote -v`) is `REPPL/Gropius` —
the same location `install.sh`, `README.md`, and AGENTS.md/CLAUDE.md's own
prose all reference for downloads and clone URLs. AGENTS.md's own module
identity line ("module `github.com/intentdriven/Gropius`") is therefore
inconsistent with where the code actually lives; the two names look like a
GitHub username/org rename (`areppel` → `REPPL`) that never propagated to
the module path.

Latent today: no documented workflow (`install.sh`, `Makefile`, the CI
workflows) does `go install github.com/intentdriven/Gropius/...` or otherwise
relies on the module path resolving to the real repository, and Go module
paths do not need to match their GitHub casing to build locally — `go
build`/`go vet`/`go test` are unaffected. If anyone ever did try `go install
github.com/intentdriven/Gropius/cmd/gropius@latest` (the idiomatic way to fetch a
public Go CLI, and a reasonable inference from the module path AGENTS.md
publishes), it would fail to resolve.

Deferred rather than auto-patched: fixing it properly means renaming the
module path in `go.mod` and mechanically rewriting the
`github.com/intentdriven/Gropius/internal/...` import in every internal package
that imports another (19 non-test/non-doc `.go` files per a repo-wide
grep) — a large mechanical diff, not the "smallest diff" a single bug-hunt
round's fixes should be. It is also not obviously safe to guess the
replacement (`REPPL/Gropius`, `reppl/gropius`, or something else the
maintainer intends) without confirming which name is meant to be canonical
going forward.
