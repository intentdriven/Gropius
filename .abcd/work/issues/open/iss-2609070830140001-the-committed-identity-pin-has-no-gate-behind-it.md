---
schema_version: 1
id: "iss-2609070830140001"
slug: "the-committed-identity-pin-has-no-gate-behind-it"
severity: "minor"
category: "tech-debt"
source: "agent-finding"
found_during: "2026-09-07 repository move"
origin: researcher-authored
production_mode: hand-written
found_at: ".abcd/config/identity.json"
---

The committed identity pin has no gate behind it. `.abcd/config/identity.json`
records the author identity every commit is expected to carry, and
`abcd ahoy identity-check` reads it and exits non-zero on divergence, but
nothing invokes that command: the scaffolded `pre-commit` hook never mentions
identity, no workflow calls it, and there is no Makefile target for it. The pin
is a record with nothing enforcing it.

The tooling solves this in its own repository, at `.githooks/pre-commit`, in a
block fenced as an identity gate. That gate is self-contained shell with no
binary dependency: it reads the pin with `sed` and compares against `git
config` directly, so it holds before the tool is built, without it on PATH, and
cannot be broken by a stale copy of it. That property matters here, because a
stale symlink on this machine already makes the bare command unresolvable.

Two details of that block are load-bearing and a reimplementation would get
them wrong. It runs first, ahead of the name guard, because the name guard
early-exits when no banlist is present and that must not skip the identity
check. And it fails closed: a pin that is present but unreadable blocks rather
than falling through.

Continuous integration is ruled out. The runner has no copy of the tool, the CI
workflow never invokes it, and an author-identity gate that runs in CI can only
report that the history is already wrong, by which point the fix is a rewrite.
The check belongs at commit time.

Two traps for whoever picks this up.

Do not adopt the tool's own hook wholesale. Both files are 854 lines and both
carry the same `# abcd-name-guard: v1` marker, so the marker does not
distinguish them, but they diverge by 66 lines each way. The scaffolded hook is
the better hardened of the two: it also unsets `command`, `read`, `echo`,
`exit` and `test`, sets noglob, and unsets every shell function it can
enumerate, none of which the tool's own hook does. Copying that file would buy
the gate by regressing the hardening. Port the fenced block and change nothing
else.

Do not tighten hook classification, and do not build a mechanism to preserve a
ported block from being overwritten, because it cannot be. Classification reads
the marker line only, with no content hash and no comparison against the
generated body, so a hand-ported block survives every later install untouched.
The real consequence runs the other way: any hook matching the marker, whether
hand-edited or simply an older generation, reports installed for ever and is
never upgraded, so future hardening silently never arrives. That looseness is
deliberate. The marker regex matches any version number precisely so that a
later template cannot reclassify every hook an earlier version scaffolded as
someone else's file. Tightening it would recreate exactly that failure.

The seam an upgrade path needs already exists: recognition and currency are
already separate values in the source, a loose regex used for identity and a
literal constant naming the version actually written. An upgrade path can
compare the found marker against that constant and report a stale-but-ours
hook while classification keeps using the regex untouched. The tool also
already reserves the right to heal a hook it recognises, which is what its
line-ending pin does in practice, so extending healing from line endings to
body is within the grain rather than against it.

Preferred fix: graduate the fenced gate into the generator, so no repository
carries it by hand. Second: an explicit upgrade path kept separate from
classification, so upgrading is a decision rather than a side effect of
identity.

Note that `abcd ahoy identity-check` returns success both when the pin matches
and when no pin exists. A green run before the pin lands is the check declining
to have an opinion, so the pin and the gate have to be reasoned about as one
unit rather than as two independent changes.
