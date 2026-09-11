---
id: itd-2609081259493890
slug: every-setting-gropius-has-is-reachable-from-the-go-app-and-t
spec_id: spc-2609111941481833
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: major
impact: additive
origin: researcher-authored
production_mode: dictated-and-formatted
---

# Every setting Gropius has is reachable from the Go app, and the control panel is an optional view of the same settings rather than a smaller one

> The title says "reachable from the Go app", and on the product as it stands
> that reads backwards: the panel is the only interactive settings surface
> Gropius has, with `config.json` beside it, and the terminal writes no setting
> at all. The identity is left alone rather than rewritten — a record's id is
> not a description — and what this record actually arms is the obligation
> underneath the title: the panel and the configuration say the same thing, and
> a build notices when they stop.

## Press Release

Bob edits `config.json` by hand, because that is where the setting he wants
lives. Alice never opens it — she uses the control panel, and expects
everything that matters to be there. Today those two people see different
products: three settings exist that the panel cannot show or change, and
nothing in the build notices. When the next setting is added, nothing notices
then either.

Gropius keeps its promise mechanically. Every setting the Go configuration
holds has a control in the panel, or a written exemption saying why not and
signed by the record that decided it. A setting added without either fails the
build. Alice's panel is a full view of Bob's file rather than a smaller one,
and a save she makes is never refused over a setting she never touched. When
Bob wants to see what the file actually holds without opening it, `gropius
config show` tells him — reading only, with the secrets redacted, beside the
`status` and `doctor` verbs he already has.

## Why This Matters

The convention is written down in AGENTS.md and has already been broken three
times, each time as a save the operator could not make and a message naming a
field they had never seen; prose caught none of them. The three settings with
no panel control were not decisions either — two appear in the panel's own
source comment as fields "preserved server-side", which is a data-loss guard,
not an exemption.

The habit is currently holding: the last three settings to land — `log_level`,
the per-model `served_context`, and `self_test` on the lane in flight — each
arrived with a control beside it. That is exactly the state in which an
obligation looks unnecessary and is cheapest to arm. Until it is armed, the
gap widens one field at a time, in a diff where nobody was thinking about the
panel, and is only ever found by an operator who cannot save.

## Mechanism

We expect a reflection-driven sync test to keep the surfaces level because the
divergence is introduced field by field, in diffs where nobody is thinking
about the panel: all three current gaps entered as a Go field with a
`config.json` key and no UI work beside it, and a test enumerating
`config.Config` puts the panel question in the same diff as the field. We are
wrong if the divergences that actually hurt are not missing controls but
mismatched *semantics* — a control that exists and posts the wrong thing, which
is what all three wedge incidents were — in which case field enumeration passes
green over the real fault and the effort belongs in per-field round-trip tests
instead. The second criterion group is the hedge against exactly that, and if
the enumeration half never fails in a year while the round-trip half catches
something, the enumeration half was the wrong half.

## Scope Conditions

- The settings this holds are the json-tagged fields of `internal/config`'s <!-- cond: cond-2609111941481159 -->
  `Config` and the structs it nests — `Sampling`, `ChatRule` and
  `ModelSettings` — machine-wide and per-model. State that is not a setting —
  the registry, the statistics store, the PID ledger, `Paths` — is out.
- The panel means the Settings pane. A read-only view elsewhere in the panel — <!-- cond: cond-2609111941480600 -->
  the posture page's line for `advertise`, or the one the self-test lane adds
  for `self_test` — is an account of what is on, not a control, and does not
  satisfy the obligation.
- The terminal surface this record adds is read-only: one `gropius config show` <!-- cond: cond-2609111941484746 -->
  verb beside the lifecycle verbs. A verb that writes a setting is deliberately
  deferred, so `config.json` keeps one writer.
- The Swift client is out. Swift is the client and only the client, and <!-- cond: cond-2609111941489668 -->
  somebody using the chat application never sees the server side.
- Pre-1.0 file semantics are unchanged: a `config.json` written by a newer <!-- cond: cond-2609111941481733 -->
  build still loses unknown fields when an older build saves over it.
- A loopback-only control plane with one operator at a time. The overlapping <!-- cond: cond-2609111941486975 -->
  saves of iss-2609062045106963, where the last writer wins, are out of scope.
- A build-time architecture test, not a runtime check. It proves a control <!-- cond: cond-2609111941489272 -->
  exists and is wired to the key; it never proves the control is usable, well
  labelled, or in the right pane.
- Actions are not settings. "Generate a key" and "Clear records" are things the <!-- cond: cond-2609111941484015 -->
  panel does rather than values it holds, neither is a field of `Config`, and
  neither is in scope here.

## Acceptance Criteria

**Every setting has a panel control or a written exemption**

- Given a field with a json tag on `config.Config` or on a struct it nests,
  when the architecture test runs, then it fails unless the Settings pane's
  markup and script name that key, or the field appears in a declared exemption
  table carrying a written reason, the record id that decided it, and the
  surface an operator reaches the setting through instead.
- Given an exemption entry, when the test runs, then it fails if the field it
  names no longer exists, so a renamed field cannot inherit another field's
  exemption — the liveness rule `TestStatisticsSwitchReadersAllExist` already
  holds its own list to.
- Given the Settings pane names a settings key the configuration does not hold,
  when the test runs, then it fails, so drift is caught in both directions and
  a removed field cannot leave a dead control behind.
- Given a reader of the exemption table, when they ask why a setting has no
  control, then the answer is in the table beside the field — reason, deciding
  record, and where the setting is reached instead — and not in a source
  comment in the configuration package, which is the file the exemption is
  about.

**A save is never refused over a setting the operator did not touch**

- Given a stored configuration the load path accepted, when the Settings form
  is submitted with no field edited, then the save is accepted — and this holds
  for values only a hand edit or another Mac's file can produce, not only for
  values the panel itself can write.
- Given a setting the Settings form does not own, when any save is posted, then
  its stored value survives the save unchanged.
- Given a field the browser would refuse on its own validity rules, when the
  panel writes a stored value into that control, then the control accepts it,
  so the client never refuses a form the server would have accepted.
- Given a save whose only refusal is a cross-field rule, when it is refused,
  then the refusal names a field that changed in this save, or the field whose
  change made an unchanged one unsafe, and never an unchanged field alone.

**Advertising is a control, and the save tells the truth about it**

- Given the Settings pane, when Alice opens it, then advertising the service
  over Bonjour is a control she can switch, not a line she can only read on the
  posture page.
- Given a save that changes advertising, when the save succeeds, then the
  answer says a restart is needed, because the advert is started once at launch
  and a stored value changes nothing until then — the silence
  iss-2609091751184914 records.

**The terminal can read the settings**

- Given Bob at a terminal, when he runs `gropius config show`, then he is shown
  the settings in force, with the API key and the HuggingFace token redacted,
  and nothing is written.
- Given a script, when it runs `gropius config show --json`, then it receives
  the same settings as a machine-readable document with the same redactions.
- Given `gropius config show`, when the test suite runs, then no verb in the
  lifecycle package writes `config.json`, so the control plane remains its only
  writer.

**The record says what it did**

- Given the documentation, when this work lands, then the `config.json` section
  of the getting-started page names every setting with no panel control and
  says how to set it by hand, and the lifecycle reference carries `gropius
  config show` in its verb table.

## Open Questions

- Whether a writing verb (`gropius config set`) is ever wanted. Deferred here
  rather than declined: it would make the terminal a fourth surface this same
  obligation must then cover, and would put a second writer on a file
  iss-2609062045106963 already records as single-writer state. The question
  returns if an operator is found who cannot reach the panel at all.
- What the safe range of `upstream_header_timeout_sec` is. `Validate` does not
  bound it today, and the exemption this record grants it rests on that: a
  control would publish a number with no established safe range. Establishing
  one is the precondition for ever giving it a control, and nobody has measured
  it.
- Whether reaching the panel-only actions — "Generate a key" and "Clear
  records" — from a terminal is wanted. Out of scope here; it is a
  lifecycle-verb question.

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._

## Grounds

- pursued: we expect a reflection-driven sync test over config.Config to keep the Go configuration and the control panel level, because every divergence so far entered field by field in a diff where nobody was thinking about the panel; shown wrong if the divergences that hurt turn out to be mismatched semantics — a control that exists and posts the wrong thing — which field enumeration passes green over — planned autonomously on the maintainer's instruction of 2026-09-10, adopting the brief's recommendations
