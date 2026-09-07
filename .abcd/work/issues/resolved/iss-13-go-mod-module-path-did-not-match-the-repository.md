---
schema_version: 1
id: "iss-13"
slug: "go-mod-module-path-did-not-match-the-repository"
severity: "nitpick"
category: "tech-debt"
source: "agent-finding"
found_during: "2026-08 bug-hunt round 10"
found_at: "go.mod"
---

`go.mod` declared a module path under a personal account that did not match the
repository's actual location, so `go install <module>/cmd/...@latest` could
never resolve. Latent rather than breaking: no documented workflow relied on the
module path resolving, and `go build`, `go vet` and `go test` are indifferent to
it. Deferring it was deliberate, because fixing it properly meant rewriting the
import path in every internal package that imports another, and because it was
not safe to guess which name the maintainer intended to be canonical.

Resolution (2026-09-07): resolved by the rename. The module path is now
`github.com/intentdriven/Gropius`, matching the repository exactly, and every
import was rewritten mechanically in the same pass. The question the capture
said it could not answer, which name is canonical, was settled by the
maintainer: the product is Gropius and the repository is Gropius.
