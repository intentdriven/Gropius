---
schema_version: 1
id: "iss-2609061334204042"
slug: "github-32-ensuredirs-creates-and-chmods-the-layout-directori"
severity: "critical"
category: "security"
source: "user-observation"
found_during: "github issue triage 2026-09-06 (adversarially verified)"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/config/config.go"
resolution: "Fixed: under a setgid root EnsureDirs creates and chmods each layout entry relative to an os.Root and refuses (fails startup) anything Lstat says is not a real directory; per-user roots keep path-based MkdirAll so a user's own symlinks still work; venv and python are now created closed. Tests pin both."
impact: fix
---

GitHub #32: EnsureDirs creates and chmods the layout directories through symlinks: a logs/, models/, or hf symlink planted in the setgid shared root (or owner-swapped after first launch) makes the victim's startup chmod an arbitrary victim-owned directory to 3775 — group-writable by every local account, code-execution-equivalent for ~/.ssh or LaunchAgents. Fix: create the layout root-relative via os.Root, Lstat each entry, and fail startup on anything that is not a real directory.

## Grounds

- pursued: a planted symlink under a layout name can no longer widen a victim directory to 3775; a per-user install with a symlinked models directory continues to start
