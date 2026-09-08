# AGENTS.md — how to work in this repository

<!-- BEGIN ABCD -->
<!--
  Managed by abcd (Agent-Based Configuration for Development).
  Do NOT hand-edit content inside the abcd-managed fences — `/abcd:ahoy`
  silently overwrites this block on drift (per itd-3). Per-repo rule
  customisation goes in <repo>/.abcd/rules.json instead.
-->

## abcd rule loader

This repository uses the abcd modular rules loader. On `UserPromptSubmit`, a hook
recall-matches the prompt against keyword triggers declared in the plugin-bundled
default domains and `<repo>/.abcd/rules.json`, and injects only the matched
domain rules into context — instead of force-loading the full ruleset every turn.
A prompt that matches no domain injects nothing (zero added tokens).

- Inspect rules: `abcd rules` renders the active set; `abcd rules <DOMAIN>`
  (case-insensitive) scopes to one domain.
- Per-repo overrides: edit `<repo>/.abcd/rules.json`. It is
  `{"schema_version": 1, "disabled": false, "domains": {}}` — add a domain key to
  override a default per-field (e.g. `{"ROADMAP": {"state": "dormant"}}` silences
  it while keeping its rules) or to declare a custom domain
  (`{"recall": [...], "rules": [...]}`). A domain left with no rules at all
  (`{"rules": []}`, or a custom domain declared without any) is SKIPPED with a
  diagnostic on stderr naming it — it would otherwise inject a heading-only
  block, which reads as a domain that says nothing. The rest of the file still
  loads; `{"state": "dormant"}` is the way to silence a domain deliberately.
- Provenance: a domain the override names (rules replaced, state changed, or a
  custom domain) renders as `## NAME (repo override)` wherever it appears: the
  injected block, `abcd rules`, and the hook's diagnostic; `abcd rules --json`
  carries `"source": "repo"` for it and `"source": "bundled"` for an untouched
  default.
- Kill switch: set `"disabled": true` at the top of `.abcd/rules.json`.
- Explicit activation: start a prompt with `*<DOMAIN>` (e.g. `*COMMITTING`,
  `*PII`) to inject that domain unconditionally — overrides a `dormant` state,
  but never the kill switch.

### Default domains

`COMMITTING`, `DOCUMENTATION`, `ROADMAP`, `ISSUES`, `INTENTS`, `LIFEBOAT`, `PII`,
`OPINIONS`. Each carries recall keywords and its rules, bundled in the abcd
binary; a repo overrides them per-field via `.abcd/rules.json`. `OPINIONS`
points at the canonical conventions under `.abcd/development/principles/` rather
than copying them.

### Reset triggers

`SessionStart` and `PreCompact` clear the per-session dedup ledger, so a matched
domain re-injects on the next prompt (the event-driven refresh that recovers
after compaction). Within a session the hook does not re-inject unchanged rules.

For internals see `.abcd/development/brief/05-internals/03-configuration.md`.

<!-- END ABCD -->

Download MLX models on your Mac and serve them to the rest of your network — an OpenAI-compatible endpoint, in a menu-bar app.

## What this repository is

Gropius is a Go menu-bar app for Apple Silicon Macs (module
`github.com/intentdriven/Gropius`, Go 1.25). It downloads MLX models from
HuggingFace and serves them over an OpenAI-compatible API to the local network.
Entry point: `cmd/gropius`. Packages live under `internal/` (`app`, `capability`,
`config`, `discovery`, `gateway`, `hub`, `registry`, `runtime`, `ui`, plus the
`archtest`/`mlxtest` test packages). `client/` holds a small Swift chat client
built separately via `client/build.sh`. `docs/` is user-facing documentation;
`build/` holds packaging assets.

## Build, test, lint (verified)

```sh
make test                                  # go test -race ./...  — the full suite
go test -race -run TestDownloadResumesFromPartialFile ./internal/hub/   # a single test
make build                                 # dev binary at bin/gropius
make app                                   # signed .app bundle in dist/
gofmt -l .                                 # must print nothing
go vet ./...
```

CI (`.github/workflows/ci.yml`) gates on: `gofmt -l .` (must be empty),
`go build ./...`, `go vet ./...`, `go test ./...`,
`go test -race ./internal/...`, gitleaks (full history), and zizmor.
`make run` starts the server headless in the foreground for development.

## Boundaries

- Trust boundaries — changes here need an adversarial security review before
  they land: `internal/gateway` (network input), `internal/hub` (remote
  downloads), `internal/runtime` (subprocess management), `internal/config`
  (file parsing), `internal/capability` (executes `sysctl` to read this Mac's
  memory, which the pool's default budget and the app's ceiling are worked out
  from).
- Read `.abcd/development/decisions/DECISIONS.md` before touching model
  routing or the runtime: it records empirically verified constraints (the
  request's `model` field is a load instruction the gateway must rewrite;
  `HF_HUB_CACHE` must exist; `HF_HUB_OFFLINE=1` on child processes).
- The shared-cache mode (`make install-shared`) has deliberate permission
  semantics — directory mode `3775`, file modes left to the app — explained in
  the Makefile; do not "simplify" them.

## Definition of done

- `make test` green, `gofmt -l .` empty, `go vet ./...` clean.
- Every new behaviour has a test that was watched to fail before the change
  and pass after; bug fixes start with a failing reproduction.
- User-facing changes are reflected in `README.md` / `docs/`.

<!-- working-conventions 2026-07-29 -->
## Working conventions

- **Working state lives in three tiers.** `.abcd/development/` is the durable,
  committed record (decisions in `decisions/`, promoted to MADR ADRs as
  `decisions/adrs/NNNN-title.md` when architecture-shaping; dated plans and
  research notes as `YYYY-MM-DD-topic.md`). `.abcd/work/` is committed shared
  working state: `CONTEXT.md` (orientation for a fresh session), `DECISIONS.md`
  (append-only, one dated line per decision), `issues/` (the issue ledger —
  folder membership is the status signal). `.abcd/.work.local/` is gitignored,
  per-machine ephemera: `NEXT.md` handover, `scratch/`, `logs/` — runtime
  artefacts (logs, traces, scratch output) go here, never in tracked
  directories.
- **Decisions:** one dated line in `.abcd/work/DECISIONS.md` at the time the
  decision is made; promote architecture-shaping ones to an ADR.
- **Docs:** `docs/` is user-facing only — one Diátaxis type per page (tutorial,
  how-to, reference, or explanation), present tense only (what IS; history
  lives in git). User-facing prose in British English; identifiers, code
  comments, strings, and commit messages in US English. No stray markdown at
  the repo root beyond README, AGENTS, CLAUDE, GEMINI, CHANGELOG, CONTRIBUTING,
  SECURITY, LICENSE, ACKNOWLEDGEMENTS.
- **Names.** The committed name guard rejects superseded product names in
  user-facing content. Use the current name, or a generic term.
- **Privacy:** no absolute local paths, real hostnames, usernames, emails,
  tokens, IPs, or private repository names in anything committed —
  repo-relative paths only. (`/Users/Shared/…` is a macOS system path, not a
  username, and is part of this product's design.)
- **Examples and user stories** use the personas Alice, Bob, and Carol — never
  other names. Refer to the maintainer as they/them in every artefact.
- **Git:** never commit or push without being asked. Substantive work goes on
  a branch with a PR; small atomic commits with conventional prefixes
  (`feat`/`fix`/`chore`/`refactor`/`docs`/`test`), body explains why. Never
  force-push, never `--no-verify`. New dependencies need explicit sign-off
  before they are added.
- **Releases and tags are kept on different terms.** Only the current release
  stays published; an older one is deleted once it is superseded, and its TAG
  is kept. The tag is what makes a previous version investigable — it can be
  checked out, diffed and rebuilt — while a published release exists to be
  installed, and only the current one needs to be. Deleting a release destroys
  its built assets permanently, so it is deliberate and never takes the tag
  with it (`--cleanup-tag=false`). Never delete the current release:
  `install.sh` resolves `releases/latest`, so the documented install command
  stops working the moment none is published.
- **A merged branch is deleted, locally and on the forge.** What survives a
  branch is its commits on `main`; the branch itself is a handle that has done
  its job. Check content rather than the merged flag before deleting: a
  squash-merged branch is not an ancestor of `main` even though its work is in
  it, and a record that moved folder — an issue from `open/` to `resolved/` —
  reads as a file the branch has and `main` lacks. Never delete a branch a
  worktree holds.
- **Three surfaces, and they are not interchangeable.** Go is the power tool
  and carries the whole of the functionality; the terminal is a legitimate
  place for the person running a server to reach it. The web control panel is
  the accessible layer, carrying the most important settings, and it must be
  kept in sync with what Go can do — a Go capability with no panel equivalent
  is a gap, not a feature tier. `config.json` is a third surface, hand-editable
  by design: a save must never be refused over a setting the operator did not
  touch, which is a wedge this repository has built three times. Swift is the
  client and only the client; someone using the chat application never sees the
  server side. The sync obligation is armed the way every other cross-surface
  promise here is — a test, not a habit.
- **A build-provenance attestation is not a signature.** User-facing prose
  never calls a release "signed" on the strength of checksums or attestation:
  the bundles are ad-hoc signed, not notarised, and neither control is visible
  to Gatekeeper. Say what is true — verified against published checksums — or
  say nothing.

<!-- /working-conventions -->
