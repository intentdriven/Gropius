---
schema_version: 1
id: "iss-10"
slug: "shared-root-state-files-are-single-writer-registry-config"
severity: "minor"
category: "bug"
source: "agent-finding"
found_during: "2026-07 bug-hunt round 6 (verification of the shared-cache directory-mode fix)"
found_at: "internal/registry/registry.go"
resolution: "Shared-cache installs now give every account its own config.json and registry.json in its own folder, so a second account can read its settings and persist its model list; the models stay shared and no token crosses accounts."
impact: breaking
---

In shared-cache mode the root's state files are effectively single-writer: `registry.json` and `config.json` are written 0600 via CreateTemp-then-rename (internal/registry/registry.go saveLocked; internal/config Save), so when account B becomes the server after account A has run one, B cannot read A's `config.json` (config.Load surfaces the read error) and B's every registry save fails EPERM — the shared root's sticky bit blocks `os.Rename` over a file owned by another account. Rescan rebuilds B's in-memory registry, so serving works, but B can never persist state, and each account's config diverges from the shared reality.

Round 6 fixed the *directory* modes (app-created data directories under a setgid root are now group-writable setgid, so cross-account model downloads work), which makes this residue the remaining blocker for true account-rotation in shared mode. Deferred rather than auto-patched because every fix is a trust-boundary design decision: group-readable/writable state files change what a hostile local account can read (the config holds the HuggingFace token and API key) or forge (the registry drives what the gateway serves); per-account state files under a shared root change the "one config, one registry" model; and the sticky bit's delete/rename protection is the same mechanism iss-5 already has under design review. Decide together with iss-5's singleton redesign.

## Grounds

- pursued: we expect a second account under a shared cache to load its own settings, save its own registry, and rebuild it from the shared models on first run, with no secret crossing accounts; we are wrong if an account can read another's config.json, or if a single-account install's layout changed
