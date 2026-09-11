---
id: spc-2609111941481833
slug: every-setting-gropius-has-is-reachable-from-the-go-app-and-t
intent: itd-2609081259493890
origin: researcher-authored
production_mode: hand-written
---
# every-setting-gropius-has-is-reachable-from-the-go-app-and-t

## Summary

An architecture test that enumerates every json-tagged setting on
`config.Config` and the structs it nests, and fails unless each one is named by
the Settings pane or carries a written exemption; a companion that fails when
the pane names a key the configuration does not hold; and a save-path family
that holds the never-refused-over-an-untouched-field promise on both the server
and the panel. Beside the test, the two gaps the interview decided to close —
a control for `advertise` with the restart notice its save owes, and a
read-only `gropius config show` verb so the terminal can read what the file
holds without opening it.

This is the record that turns the AGENTS.md convention "the sync obligation is
armed the way every other cross-surface promise here is — a test, not a habit"
into the test it names.

## Depends on

- **itd-2609081259532589 / spc-2609111029315861** shipped `internal/lifecycle`
  and the verb dispatch in `cmd/gropius`. `gropius config show` is a fifth verb
  in that package and needs no new machinery; that spec deferred the parity
  test to this one and says so.
- **`internal/archtest/walk_test.go`** is on `main`, so the two file reads this
  spec does use the shared walker rather than a ninth hand-rolled tree walk
  (iss-2609081427104462).
- **iss-2609081742422989** is resolved: the bind select's third option landed
  with `TestExtraBindOptionNamesAHostTheSelectDoesNotOffer`. That was the live
  instance of the second criterion group, so the group is now written against
  the defence rather than against an open defect — and the test named there is
  the shape the new save-path tests generalise.
- **iss-2609091751184914** is closed by this work: the advertise control and
  the restart notice are two of its criteria.
- **The gateway lane** (another session, `internal/gateway/control.go`) is
  coordinated, not depended on. See "What is coordinated" below.

## Scope

**In.** One archtest file holding the enumeration, the exemption table and the
both-directions check. A save-path test family in `internal/gateway`. A panel
round-trip test in `internal/ui` with a Go-side companion that does not need
node. A control for `advertise` in `internal/ui/static/index.html` and
`app.js`, and `advertise` added to the restart list in `applySettings`. A
read-only `config show` verb in `internal/lifecycle` and its row in the verb
table. Two documentation changes. The three exemptions, written down.

**Out.** A writing terminal verb (`config get`/`set`) — deferred by the
interview, so `config.json` keeps one writer. The Swift client. The panel-only
actions "Generate a key" and "Clear records", which are not settings. The
overlapping-save race (iss-2609062045106963). Any bound on
`upstream_header_timeout_sec` in `Validate`, which is the open question its
exemption rests on. Per-field semantic round-trip tests for settings that
already have controls: the enumeration is the cheap half, and widening it into
a value-level contract for all thirty settings is the thing the mechanism claim
says to do only if the enumeration half proves to be the wrong half.

## Approach

### The enumeration is reflection, not a source scan

`reflect.TypeOf(config.Config{})` walked recursively through the structs it
nests, collecting json tag names as paths — `port`, `sampling.temperature`,
`models.*.pinned` — exactly as `per_model_settings_test.go` walks the same type
for a different question. Reflection rather than a scan of the configuration
source, for two reasons: a scan would be the ninth tree walker in a package
that already has an issue open about the eight, and a scan cannot tell a json
tag on a settings struct from one on `Notices` or `SetupStatus`. Reflection
starts from the type and can only reach settings.

The panel side is two file reads — `internal/ui/static/index.html` and
`internal/ui/static/app.js` — asserted to contain the key. That half is a
string search and is honest about being one: it proves the key is named in the
pane's markup or in the body it posts, not that the control works. The
round-trip tests are what prove the wiring; this test proves nobody forgot.

Both directions: every key in `Config` is named by the pane or exempted, and
every settings key the pane's submit body names exists in `Config`. The second
half is what catches a removed field leaving a dead control behind, and it is
also what would have caught the posted-`host:""` shape of
iss-2609081742422989 had the key been spelt differently.

### The exemption table lives in the test file

A Go map from json path to a struct of `{reason, decided_by, reached_through}`,
in the archtest file, with a doc comment saying what an entry costs. Not a
comment in `internal/config`: the exemption is *about* that file, and a reader
asking "why has this no control" must find the answer where the rule is, not
where the field is. The three entries this record writes:

| Field | Reason | Decided by | Reached through |
|---|---|---|---|
| `preload` | A power-tool setting: a list of repo ids loaded at startup, best-effort, already documented as a `config.json` list. A panel control would need a model picker that duplicates the pinning list for a different verb. | itd-2609081259493890 | `config.json`, documented in `docs/getting-started.md` |
| `upstream_header_timeout_sec` | An escape hatch over a derivation, not a setting an operator chooses. `Validate` does not bound it, so a control would publish a number with no established safe range — and a figure typed into a box invites being typed. Documented instead, with the derivation it overrides. | itd-2609081259493890 | `config.json`, documented in `docs/getting-started.md` |

Two entries, and only two. `bind_mode` was a candidate and is not one: the bind
select posts `host` and `bind_mode` together through `bindSelectBody`, so the
key is named in the pane's submit body and the string search finds it. One
control may own two keys — what the test asks is that each key be named, not
that each key have a control of its own.

`advertise` is deliberately **not** in that table: the interview gives it a
control. A "most important setting" by the convention's own words — it decides
whether this Mac announces itself on the network — and it already has a
read-only line on the posture page, which is an account of what is on and not
a control.

The table carries a liveness test: every field it names must still exist on the
type, so a rename cannot silently hand one field another's exemption. That is
the rule `TestStatisticsSwitchReadersAllExist` holds its own list to.

### The never-refused promise is armed on both sides

Two of the three wedge incidents were client-side — a `step="0.1"` the browser
refused before the submit listener ran, and a select whose stored value was not
one of its options — so arming only the server leaves the majority uncaught.
The `internal/ui` tests skip when node is absent, so anything load-bearing
needs a Go-side half: the Go companion parses the Settings pane's `min`, `max`
and `step` attributes out of the markup and asserts that no control is narrower
than what `config.Validate` accepts. That runs everywhere, needs no node, and
is the general form of both client-side incidents.

### `gropius config show`

A fifth verb in `internal/lifecycle`, in the shape the other four already have:
a thin shell over a pure function from a `config.Config` to lines of text or to
a JSON document. It reads through `loadStartupConfig`, the path the other verbs
take, so a file that parses and fails validation is reported rather than
refused. `api_key` and `hf_token` are redacted with the same placeholder the
control plane serves, and the redaction is a property of the rendering function
so it cannot be forgotten per field. Nothing is written, and a test asserts
that no package under `internal/lifecycle` writes the settings file — the rule
that keeps the control plane its only writer.

## Per criterion: the test that arms it, and where it lives

| Criterion | Test | File |
|---|---|---|
| Every json-tagged setting is named by the pane or exempted | `TestEverySettingHasAPanelControlOrAnExemption` | `internal/archtest/settings_surface_test.go` (new) |
| An exemption for a field that no longer exists fails | `TestEveryExemptedSettingStillExists` | same file |
| The pane naming a key the configuration lacks fails | `TestTheSettingsPaneNamesNoSettingTheConfigurationDoesNotHold` | same file |
| The exemption's reason is findable beside the field | the table itself, plus `TestEveryExemptionCitesARecordThatExists` (the id resolves to a record in `.abcd/`) | same file |
| An unedited form saves, for any stored configuration the load path accepted | `TestASaveOfAnUneditedFormIsAccepted`, table-driven over stored configurations including hand-edited shapes | `internal/gateway/control_untouched_test.go` (new) |
| A setting the form does not own survives a save | `TestASettingTheFormDoesNotOwnSurvivesASave` | same file |
| No control is narrower than `Validate` | `TestNoSettingsControlIsNarrowerThanValidate` (Go, reads the markup's attributes — no node) | `internal/ui/controls_test.go` (new) |
| A control accepts a stored value the panel itself cannot write | `TestEveryControlAcceptsAStoredValue` (node-gated, skips without node) | `internal/ui/settings_test.go` (extended) |
| A cross-field refusal names a field that changed | `TestACrossFieldRefusalNamesAChangedField`, over the two live cross-field rules — the eviction-grace/API-key rule and the pin fit check | `internal/gateway/control_untouched_test.go` |
| Advertising is a control in the Settings pane | `TestTheSettingsFormPostsAdvertise` | `internal/ui/settings_test.go` |
| A save that changes advertising asks for a restart | `TestASaveThatChangesAdvertisingAsksForARestart` | `internal/gateway/control_test.go` (extended) — **coordinated** |
| `config show` redacts the secrets | `TestConfigShowRedactsTheSecrets` | `internal/lifecycle/config_test.go` (new) |
| `config show --json` is the machine contract | `TestConfigShowJSONIsTheContract` | same file |
| No lifecycle verb writes the settings file | `TestNoLifecycleVerbWritesTheSettingsFile` | `internal/archtest/lifecycle_boundary_test.go` (extended) |
| The docs name every exempted setting, and the verb table carries `config show` | `TestExemptedSettingsAreDocumented`, and the existing lifecycle docs test extended | `internal/archtest/settings_docs_test.go` (new), `internal/archtest/lifecycle_docs_test.go` |

## The inventory as it stands on `main` today

Recomputed against `internal/config/config.go`, `internal/config/sampling.go`,
`internal/config/chatrule.go` and the Settings form's submit body in
`internal/ui/static/app.js`. This is not the brief's table: two settings have
landed since it was written (`log_level`, and the per-model `served_context`),
and both arrived with a control. "Panel" means a control the Settings pane owns
and posts. "Terminal" means a verb or flag that reads or writes the value —
empty everywhere today, which this record changes only on the reading side.

| Setting (json path) | Go | Panel | Terminal | Verdict |
|---|---|---|---|---|
| `host` | yes | bind select | no | in sync |
| `bind_mode` | yes | same select, posted beside `host` | no | in sync |
| `port` | yes | number | no | in sync |
| `api_key` | yes | text + "Generate a key" | no | in sync |
| `upstream_header_timeout_sec` | yes | none | no | **gap** — exempted here, and documented |
| `advertise` | yes | none (a read-only posture line only) | no | **gap** — a control is added here |
| `idle_timeout_sec` | yes | number | no | in sync |
| `decode_concurrency` | yes | number | no | in sync |
| `hf_token` | yes | password | no | in sync |
| `preload` | yes | none | no | **gap** — exempted here |
| `max_resident_bytes` | yes | number, typed in GB | no | in sync |
| `eviction_grace` | yes | checkbox | no | in sync |
| `eviction_grace_sec` | yes | number | no | in sync |
| `eviction_max_wait_sec` | yes | number | no | in sync |
| `sampling.temperature` | yes | number | no | in sync |
| `sampling.top_p` | yes | number | no | in sync |
| `sampling.top_k` | yes | number | no | in sync |
| `sampling.min_p` | yes | number | no | in sync |
| `sampling.max_tokens` | yes | number | no | in sync |
| `statistics` | yes | checkbox | no | in sync |
| `stats_months` | yes | number | no | in sync |
| `stats_max_bytes` | yes | number, typed in MB | no | in sync |
| `log_level` | yes | select | no | in sync (landed since the brief) |
| `chat_rule.pipeline_tags` | yes | text | no | in sync |
| `chat_rule.required_tags` | yes | text | no | in sync |
| `models.*.merge_system_messages` | yes | tick list | no | in sync |
| `models.*.pinned` | yes | tick list | no | in sync |
| `models.*.served_context` | yes | per-model number | no | in sync (landed since the brief) |
| `models.*.sampling.*` | yes | override editor (five fields) | no | in sync |

**Thirty-three leaf settings. Three panel gaps — `advertise`,
`upstream_header_timeout_sec`, `preload` — the same three the brief found, in a
set that has grown by two. Zero settings reachable from the terminal.**

Two readings follow from that, and the record states both. The gap count has
not grown, which means the convention is currently being kept by habit: the
three gaps are old fields, and the last three settings to land each arrived
with a control. And the terminal column is empty by construction — the
lifecycle verbs read state but none of them reads the settings file for the
operator — which is why the intent's title reads backwards against the product
and why the note at the top of the intent says so.

### One field in flight

`self_test` is not on `main`. It is a `config.Config` field on the self-test
lane, and on that lane it arrives with a Settings-pane checkbox and a posture
line (iss-2609100515484516, resolved there). So it lands in sync, and the
enumeration test will pass green over it. That is a fact worth having in this
record: the test's value is in the field nobody thinks about, not in the ones
being added while everyone is thinking about the panel.

## What is coordinated, and what ships without it

`internal/gateway/control.go` is another session's lane. Two changes here land
inside `applySettings`:

1. `advertise` joins the restart list, so a save that changes it says a restart
   is needed (iss-2609091751184914).
2. The cross-field refusal messages are held to naming a field that changed.

Both are coordinated with that lane rather than made unilaterally: a conflicting
edit to the restart expression is the likeliest merge cost in this work. The
gateway half of the save-path tests is new files
(`control_untouched_test.go`), which conflicts with nothing.

**Ships without the gateway lane:** the archtest enumeration, the exemption
table and its liveness test; the panel's `advertise` control in `index.html`
and `app.js`; every `internal/ui` test; `gropius config show` and its tests;
both documentation changes. That is the majority of the work, and it is the
half that arms the convention.

**Needs the gateway lane:** the restart notice for `advertise`, and the
cross-field refusal criterion. If the lanes cannot be sequenced, those two
criteria land in a follow-up commit on the same branch rather than blocking the
test.

A save-path change is a trust-boundary change by AGENTS.md's own list
(`internal/gateway` takes network input), so the branch carries an adversarial
security review before it lands, as the lifecycle work did.

## Documentation

- **`docs/getting-started.md`** — the `config.json` section gains a short
  subsection naming every setting with no panel control, what it does, and how
  to set it by hand: `preload` (already mentioned in the troubleshooting
  section; it moves to the settings list and is cross-referenced) and
  `upstream_header_timeout_sec` (new — what the derivation does, why zero means
  automatic, and that a positive value overrides it). `advertise` is *removed*
  from any "hand-edit only" framing, because after this work it has a control.
- **`docs/lifecycle-reference.md`** — a row in the verb table for `gropius
  config show`, and a row in the flags table for `--json`, in the shape the
  four existing verbs use. The page is the contract, so the redaction is stated
  there: the key and the token are shown as the placeholder, never as their
  value.
- **`docs/lifecycle.md`** — one sentence pointing at the new verb where it tells
  a reader how to find out what is going on. A how-to page, so it says what to
  type, not what the output means.
- No new page. Both exemptions are properties of settings a reader meets in
  getting-started, and a page per exempted field would be a page nobody opens.

`internal/archtest/settings_docs_test.go` holds the first of those: every field
in the exemption table appears in `docs/getting-started.md`, which makes the
exemption's "reached through" column a promise the build keeps rather than a
claim in a comment.

## The CHANGELOG entry shape

Under `## [Unreleased]`, in the existing three-heading shape:

```markdown
### Added

- A control for advertising the server over Bonjour, in Settings. Changing it
  now says that a restart is needed, which it always did and never said.
- `gropius config show` — the settings in force, read from a terminal, with the
  API key and the HuggingFace token redacted. Reading only; `--json` for a
  script.

### Changed

- Every setting Gropius holds now has a control in the panel or a written
  exemption, and the build fails on a setting with neither. Two settings are
  exempted deliberately — the preload list and the upstream header timeout —
  and `docs/getting-started.md` says how to set them by hand.
```

`impact: additive`: nothing an operator relies on changes behaviour. The
`advertise` control makes a stored value editable that was already read, and
the exemptions document what already was.

## Sequencing

1. The archtest enumeration and the exemption table, watched failing on the
   three gaps before any of them is closed. That is the test this record
   exists for, and it must be seen red.
2. The `advertise` control, which turns one gap into a control and is the first
   entry the test stops reporting.
3. The two exemptions, which turn the other two gaps into table rows.
4. The save-path family, server and panel, each watched failing against a
   stored configuration the panel cannot write.
5. `gropius config show`.
6. The documentation, and the docs test that holds it.
