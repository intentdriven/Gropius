---
schema_version: 1
id: "iss-2609061334202451"
slug: "github-33-defaultroot-adopts-redacted-path-on-exists-and-wri"
severity: "critical"
category: "security"
source: "user-observation"
found_during: "github issue triage 2026-09-06 (adversarially verified)"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/config/config.go"
resolution: "Fixed: DefaultRoot adopts [redacted-path] only when sharedRootShape passes — a real directory (Lstat), owned by uid 0, not other-writable — then the writability probe; minimum-shape chosen over exact-mode. Test pins self-owned/other-writable/missing/symlink refusals and a root-owned pass; docs section 7 explains."
impact: fix
---

GitHub #33: DefaultRoot adopts [redacted-path] on 'exists and writable' alone; /Users/Shared is 1777 on stock macOS, so any unprivileged account can pre-create the directory and own every other account's data root (config, tokens, registry, and via #31 the interpreter). No admin install required; a later make install-shared never chowns. Fix: adopt only a directory that is root-owned and not other-writable, checked on an O_NOFOLLOW handle; otherwise fall back to the per-user root. Design decision left: exact 3775 match vs minimum shape (recommended: root-owned, not other-writable).

## Grounds

- pursued: an attacker-created shared root is ignored and each account falls back to its own; an administrator who tightened the mode still passes; an admin-created root that is refused would show it wrong
