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

<!-- abcd-review: INGESTED receipt=rcp-58fdecfa6463 -->
Fidelity review — receipt rcp-58fdecfa6463 (verifier intent-auditor claude-sonnet-5).

Provenance: intent-auditor@claude-sonnet-5 · rubric_hash sha256:0bee18e00a36397442ddeefa9bdb8e926f920d10154fd07a78e0b330a40d07c4 · prompt_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5
Input attestations: diff:auditor-computed only: rubric_hash is the sha256 of .abcd/.work.local/reviews/rcp-58fdecfa6463.request.md and prompt_hash is the sha256 of /Users/dev/ABCDevelopment/abcd/agents/intent-auditor.md, both computed locally by this auditor, not supplied by the host. Delivered range: main..4f8d475 (merge commit 4f8d4759f9ed4d3834ef9bf29c3460434541e367, PR #46 'feat/three-surfaces-sync', parent1 acc46d163f5dfffb65327f0199b7f1b0c43ba6e2, parent2 22b871b6d42062d37548bdc7c15da320343ae249), diffed as 4f8d475^1..4f8d475 against the worktree at HEAD.@-;

Acceptance rollup: MET 14 · MET_WITH_CONCERNS 0 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: the enumeration test walks config.Config by reflection and fails on any json-tagged path the Settings pane script does not name and that carries no exemption entry with a reason, deciding record and reached-through surface
  evidence: internal/archtest/settings_surface_test.go:99 — "func TestEverySettingHasAPanelControlOrAnExemption(t *testing.T) {"
  evidence: internal/archtest/settings_surface_test.go:78 — "var settingsPaneExemptions = map[string]settingExemption{"
- ac-2 — MET: a separate liveness test fails if an exemption names a path settingPaths() no longer finds, so a renamed field cannot inherit another field's exemption
  evidence: internal/archtest/settings_surface_test.go:137 — "func TestEveryExemptedSettingStillExists(t *testing.T) {"
- ac-3 — MET: the reverse-direction test fails when the pane's submit body names a key config.Config does not hold, catching a field removed in Go that leaves a dead control behind
  evidence: internal/archtest/settings_surface_test.go:184 — "func TestTheSettingsPaneNamesNoSettingTheConfigurationDoesNotHold(t *testing.T) {"
- ac-4 — MET: the exemption table with its reason/decided-by/reached-through columns lives in the archtest file, not as a comment in internal/config; config.go's field comments for preload and upstream_header_timeout_sec document only behaviour, never why the panel omits a control, and the app.js comment about fields 'preserved server-side' is on the panel script, not the configuration package
  evidence: internal/archtest/settings_surface_test.go:78 — "settingsPaneExemptions is the whole of the exemption table"
  evidence: internal/config/config.go:542 — "UpstreamHeaderTimeoutSec int `json:"upstream_header_timeout_sec"`"
- ac-5 — MET: a table-driven test posts an unedited form built from stored configurations only a hand edit could produce (a bind host the select never offers, an IPv6 loopback, both exempted settings set, sampling values pinned at their bounds, a decode concurrency above the panel's former ceiling) and asserts every row is accepted and the bind does not move
  evidence: internal/gateway/control_untouched_test.go:145 — "func TestASaveOfAnUneditedFormIsAccepted(t *testing.T) {"
- ac-6 — MET: a dedicated test stores the two exempted settings (preload, upstream_header_timeout_sec), posts an unrelated save, and asserts both survive unchanged; applySettings decodes into a clone of the current config rather than a zero value so an omitted field keeps its stored value
  evidence: internal/gateway/control_untouched_test.go:286 — "func TestASettingTheFormDoesNotOwnSurvivesASave(t *testing.T) {"
  evidence: internal/gateway/control.go:1360 — "func (c *Control) applySettings(raw []byte) (map[string]any, error) {"
- ac-7 — MET: a Go-only test reads every numeric control's min/max/step out of the markup and asserts config.Validate refuses the value just outside that bound (so no control is narrower than the server), and a node-backed round-trip test writes stored sampling values including zero into the controls and reads them back unchanged; both ran (node present at /opt/homebrew/bin/node) and passed
  evidence: internal/ui/controls_test.go:39 — "func TestNoSettingsControlIsNarrowerThanValidate(t *testing.T) {"
  evidence: internal/ui/settings_test.go:391 — "func TestEveryControlAcceptsAStoredValue(t *testing.T) {"
- ac-8 — MET: refusalNamingWhatChanged computes changedSettings structurally from the two encoded configurations rather than from a per-rule list, so a refusal always names the field(s) this save actually moved; a table test over the bind-widening/grace/idle-timeout cross-field rules asserts the refusal names the changed field and not the unchanged ones the rule's own wording is about
  evidence: internal/gateway/control.go:1504 — "func changedSettings(before, after config.Config, posted []byte) []string {"
  evidence: internal/gateway/control_untouched_test.go:31 — "func TestACrossFieldRefusalNamesAChangedField(t *testing.T) {"
- ac-9 — MET: the Settings pane gains a checkbox control bound to advertise, asserted to live inside the tab-settings section rather than on the posture page
  evidence: internal/ui/static/index.html:229 — "< input id="setAdvertise" type="checkbox">"
  evidence: internal/ui/settings_test.go:316 — "func TestTheSettingsFormPostsAdvertise(t *testing.T) {"
- ac-10 — MET: advertise joins the restart expression in applySettings, and a table test asserts a save that flips advertise reports restart=true while a save that leaves it unchanged reports restart=false; this closes iss-2609091751184914, whose resolved-by commit fe723bd is in this range
  evidence: internal/gateway/control.go:1433 — "incoming.Advertise != current.Advertise ||"
  evidence: internal/gateway/control_test.go:1187 — "func TestASaveThatChangesAdvertisingAsksForARestart(t *testing.T) {"
- ac-11 — MET: RunConfig's show subcommand reads through the ordinary config load path, redacts api_key and hf_token via ConfigInForce, and writes nothing; a test asserts the redaction in both the text and JSON forms, and a separate test asserts nothing is written
  evidence: internal/lifecycle/config.go:172 — "func RunConfig(env Env, args []string) int {"
  evidence: internal/lifecycle/config_test.go:56 — "func TestConfigShowRedactsTheSecrets(t *testing.T) {"
- ac-12 — MET: the --json flag serialises the same SettingsInForce map as ConfigDocument with the same redaction applied upstream by ConfigInForce, and a contract test locks the shape
  evidence: internal/lifecycle/config.go:216 — "type ConfigDocument struct {"
  evidence: internal/lifecycle/config_test.go:151 — "func TestConfigShowJSONIsTheContract(t *testing.T) {"
- ac-13 — MET: an archtest scans every non-test source file in internal/lifecycle for config.Save( or a direct WriteFile onto env.Paths.Config and fails if either appears; RunConfig itself calls neither
  evidence: internal/archtest/lifecycle_boundary_test.go:171 — "func TestNoLifecycleVerbWritesTheSettingsFile(t *testing.T) {"
- ac-14 — MET: getting-started.md gains a '10. Settings that live only in config.json' section naming preload and upstream_header_timeout_sec with how to set each by hand, and lifecycle-reference.md's verb table carries gropius config show beside status and doctor exactly as the press release promised
  evidence: docs/getting-started.md:242 — "## 10. Settings that live only in `config.json` (optional)"
  evidence: docs/lifecycle-reference.md:25 — "| `gropius config show` | The settings in force, read from the settings file the way the server reads it."

Gap audit:
- honoured:
  - a build-time test enumerates every json-tagged setting and fails on a gap in either direction, replacing the habit the intent's own note said was currently holding by luck
    evidence: internal/archtest/settings_surface_test.go:99 — "func TestEverySettingHasAPanelControlOrAnExemption(t *testing.T) {"
  - advertise closes iss-2609091751184914's silence: it is now a Settings control, and a save that changes it is answered with restart=true
    evidence: internal/gateway/control.go:1433 — "incoming.Advertise != current.Advertise ||"
  - gropius config show reads the settings in force with secrets redacted and writes nothing, sitting beside status and doctor as the press release said
    evidence: docs/lifecycle-reference.md:25 — "| `gropius config show` | The settings in force, read from the settings file the way the server reads it."
  - the security review the spec required for this trust-boundary change (AGENTS.md's rule on internal/gateway) actually ran and found a real secret-equality oracle in the refusal path, which was closed before the branch shipped: an early version of refusalNamingWhatChanged compared secret values between the stored and posted configuration, which a loopback caller (any other account on this Mac) could have used to test a guessed api_key or hf_token by posting it beside a value guaranteed to be refused and reading whether the secret was named among the changes; the shipped changedSettings instead reports a secret as changed only when the posted body carries it and it is not the redacted placeholder or empty, never by comparing values
    evidence: internal/gateway/control.go:1568 — "func postedASecret(body map[string]json.RawMessage, field string) bool {"
    evidence: .abcd/work/DECISIONS.md (line beginning "2026-09-11 — itd-2609081259493890, after adversarial review: the refusal that names what a save changed must NOT decide the two secrets by comparing them") — "The review returned BLOCK on it, and the finding reproduces: ... a per-candidate equality oracle on two secrets"
- diverged: (none)
- missing: (none)

Scope-condition dispositions:
- cond-2609111941481159 — survived: settingPaths() walks only reflect.TypeOf(config.Config{}) and the structs it nests; the registry, statistics store, PID ledger and Paths are separate types the walk never reaches, so they cannot appear in the enumeration by construction
  evidence: internal/archtest/settings_surface_test.go:204 — "func settingPaths(t *testing.T) []string {"
- cond-2609111941480600 — survived: the markup and script scans are checked against the tab-settings section specifically for the advertise test, and the posture page's own read-only advertise line is left untouched rather than counted as a control
  evidence: internal/ui/settings_test.go:329 — "the advertising box is not in the Settings pane; a read-only line elsewhere in the panel is an account of what is on, not a control"
- cond-2609111941484746 — survived: the only new lifecycle verb is config show, RunConfig writes nothing, and no config set/get verb was added to cmd/gropius's dispatch table
  evidence: cmd/gropius/verbs.go:19 — ""config": lifecycle.RunConfig,"
- cond-2609111941489668 — survived: the delivered diff (4f8d475^1..4f8d475) touches no file under client/; the Swift client is untouched
  evidence: git diff 4f8d475^1 4f8d475 --stat — "33 files changed, 3286 insertions(+), 76 deletions(-) (none under client/)"
- cond-2609111941481733 — untested: internal/config's Save/Load path is not in the diff at all and no test in this delivery exercises an older build reading a config.json a newer build wrote; the assumption is neither exercised nor contradicted by this work
- cond-2609111941486975 — survived: the new save-path work is added inside the existing settingsMu serialization rather than any multi-operator scheme, and the comment beside the lock states the handler remains the only caller of SetConfig, so the single-writer, one-operator assumption is unchanged
  evidence: internal/gateway/control.go:86 — "This handler is the only caller of SetConfig there is, so serialising it here serialises every settings write."
- cond-2609111941489272 — survived: the enumeration and its companions are _test.go files run by go test; no runtime check was added to the server's own startup or request path
  evidence: internal/archtest/settings_surface_test.go:1 — "package archtest_test"
- cond-2609111941484015 — survived: the exemption table names only preload and upstream_header_timeout_sec; neither 'Generate a key' nor 'Clear records' is a json-tagged field of config.Config, so they are outside the enumeration's reach by construction and never appear in the table
  evidence: internal/archtest/settings_surface_test.go:78 — "var settingsPaneExemptions = map[string]settingExemption{"
## Grounds

- pursued: we expect a reflection-driven sync test over config.Config to keep the Go configuration and the control panel level, because every divergence so far entered field by field in a diff where nobody was thinking about the panel; shown wrong if the divergences that hurt turn out to be mismatched semantics — a control that exists and posts the wrong thing — which field enumeration passes green over — planned autonomously on the maintainer's instruction of 2026-09-10, adopting the brief's recommendations
