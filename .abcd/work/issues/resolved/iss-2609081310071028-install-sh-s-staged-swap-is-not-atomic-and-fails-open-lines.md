---
schema_version: 1
id: "iss-2609081310071028"
slug: "install-sh-s-staged-swap-is-not-atomic-and-fails-open-lines"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
resolution: "install.sh now stages into an unguessable mktemp -d directory inside DEST, renames the installed bundle aside rather than deleting it, moves the new one in, restores the retired copy if that rename fails, and only then removes the staging directory."
impact: fix
resolved_by:
  commit: "2e55d49fcaf35f97a8c6acefb83eb4ccb854bac9"
---

install.sh's staged swap is not atomic and fails open. Lines 160-164 do 'rm -rf DEST/App.app' then 'mv staged DEST/App.app'. Verified on this Mac: if the destination exists as a directory, mv NESTS the staged bundle inside it (App.app/staged/) and exits 0, leaving planted content in place; if the destination is a symlink to a directory, mv FOLLOWS it, writes into the link target, exits 0 and leaves the symlink. The script then runs socketfilterfw --add and open against that path, so both the firewall grant and the launch target attacker-chosen content. /Applications is drwxrwxr-x root:admin, so any admin-group account can win the window; ~/Applications is writable by the same user's other processes. Second defect, no attacker needed: the failure branch removes the staged copy after the old bundle was already deleted at line 160, so a failed rename leaves NO app at all, which is the outcome the comment at 148-153 claims staging prevents. Third: the staging name DEST/.App.app.incoming.$$ is predictable and pre-plantable. Fix in Go: os.MkdirTemp for an unguessable same-directory staging name, rename the old bundle aside, rename the new one in, then delete the aside copy.

## Grounds

- pursued: a failed rename now leaves the previously installed application in place rather than no application at all, and the staging name can no longer be pre-planted because it comes from mktemp rather than the pid; a swap path that deleted or overwrote the destination before the replacement landed, or a failure branch that dropped the retired copy, would show it wrong.
