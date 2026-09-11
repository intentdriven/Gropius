---
schema_version: 1
id: "iss-2609091013547659"
slug: "ensuredirs-refuses-to-start-under-a-real-shared-cache-its-se"
severity: "major"
category: "bug"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/config/config.go"
resolution: "EnsureDirs now applies the os.Root plant defence to the entries that are under the data root and creates this account's own directories plainly, so the shared-cache layout — which straddles the shared root and the account's own folder — is created rather than refused."
impact: fix
---

EnsureDirs refuses to start under a real shared cache: its setgid branch requires every layout entry to be inside the data root, but ExecRoot moved bin/venv/python into this account's home directory, so the first entry it checks reports "is outside the data root" and app.New fails before anything serves.

The two halves landed three days apart and neither test covers the shape they
make together. The os.Root hardening (2026-09-06) walks `layout` relative to the
data root and refuses any entry whose relative path escapes it — the plant
defence that stops a co-tenant redirecting a layout directory with a symlink.
`ExecRoot` (2026-09-08) then moved `bin`, `venv` and `python` out of the shared
root and into this account's own Application Support directory, so under the
shared root every one of those three entries now escapes by construction.
`TestEnsureDirsWidensDataDirsUnderSetgidSharedRoot` passes because its root is a
temp directory rather than `SharedRoot`, so `ExecRoot` returns the root itself
and the executables stay inside it — the one configuration in which the two
rules do not collide.

Reproduction (a Paths shaped the way NewPaths shapes it under the real shared
root: a setgid data root, the executables in a second directory):

```go
root := t.TempDir()
os.Chmod(root, 0o775|os.ModeSetgid|os.ModeSticky)
exec := t.TempDir() // stands in for ~/Library/Application Support/Gropius
p := Paths{Root: root, Bin: filepath.Join(exec, "bin"), /* venv, python likewise */
    Models: filepath.Join(root, "models"), HFCache: filepath.Join(root, "hf", "hub"),
    Logs: filepath.Join(root, "logs")}
p.EnsureDirs() // -> "<exec>/bin is outside the data root <root>"
```

`app.New` calls `EnsureDirs` first and returns that error, so a Mac with a
shared cache installed cannot start Gropius at all.

## Grounds

- pursued: we expect EnsureDirs to create the layout NewPaths produces under the real shared root, with the shared data directories still widened to the installer's mode and a planted non-directory under the root still refused; we are wrong if a directory marked shared can be created outside the root, or if a per-user install's layout changed
