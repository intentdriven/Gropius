---
schema_version: 1
id: "iss-2609061334195872"
slug: "github-31-the-venv-interpreter-and-uv-are-executed-on-a-bare"
severity: "critical"
category: "security"
source: "user-observation"
found_during: "github issue triage 2026-09-06 (adversarially verified)"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/runtime/provision.go"
---

GitHub #31: the venv interpreter and uv are executed on a bare existence check; EnsureDirs never creates venv/ or python/, and uv ships its CPython tree 0775, so under a setgid staff root any account can overwrite libpython/python3.12 in place (no planting needed) or plant venv/bin/python plus the marker before first provision, gaining code execution under every other account's uid. Shippable now: create venv/ and python/ closed (0755) in EnsureDirs, strip group/other write from the provisioned trees, and refuse a non-regular or group/other-writable interpreter or uv before exec. Design decision left: whether executables stay shared (owner-trust residue: the venv owner can rewrite their own files) or move per-account under the user root while models stay shared (recommended).

Scope note (2026-09-06, same session): the mechanically safe half shipped on fix/github-issues-19-34 — EnsureDirs creates venv/ and python/ closed (0755, never widened); Provisioner.Ensure runs a lockdown that strips group/other write from the venv, python, and bin trees before judging the install and again after provisioning; installed(), Precheck and the uv short-circuit all require a regular, non-group/other-writable executable (following the venv's interpreter symlink to its uv-managed target), and Ensure fails loudly if the result is still untrusted. Tests pin the 0775 refusal and the 0755 acceptance. Left open — a maintainer design decision: the account that provisioned the shared runtime owns those files and can rewrite them at any time, and every later account executes them (option A: per-account executables under the user root with models shared, recommended; option B: shared executables with an owner==self-or-root check, which breaks A-provisions-for-B rotation). Verified on this machine that uv accepts an existing empty venv directory; the pinned uv release should be confirmed with one fresh install.

Scope note (2026-09-06, security review of the fix): the mode check alone still passed the issue's own plant (an attacker-owned 0755 interpreter is not group-writable), so trustedExecutable now also requires the file to be owned by the executing account or by root. Consequence in shared-cache mode: a runtime one account provisioned is refused, loudly, when another account tries to run it — which is the state the open design decision above must resolve (per-account executables under the user root is the recommended shape; a root-owned, admin-provisioned runtime passes today). Account rotation in shared mode was already failing at HEAD before this branch (a second account cannot open the first account's 0600 registry.json), so no working flow regresses.
