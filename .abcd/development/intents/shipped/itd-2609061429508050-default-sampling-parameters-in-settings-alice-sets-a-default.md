---
id: itd-2609061429508050
slug: default-sampling-parameters-in-settings-alice-sets-a-default
spec_id: spc-2609061822378193
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
supersedes: [itd-2609061429516182]
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Default sampling parameters in Settings: Alice sets a default temperature (and the other sampling parameters mlx-lm accepts) in the control panel, and every request that omits the parameter is served with that value

## Press Release

Alice runs Gropius for a team whose tools speak the OpenAI API but never set
a temperature. She opens Settings, sets the default temperature she wants the
whole machine to serve with, and saves; the panel tells her which models must
load again before the new value counts. From then on every request that omits
the parameter is served with her value. One model wants a different figure, so
she gives that model its own override, which takes over the next time it
loads. Bob's client, which sets its own temperature, is unaffected: what a
request carries still wins. Each field shows the value that applies when Alice
leaves it blank, so she can see what she is changing.

## Why This Matters

Sampling parameters a client sends are passed through untouched and the model
server starts with none of its own set, so today the only way to change what
"unspecified" means for the machine is to change every client. A shared
server needs one place to say it. Settings already holds the other
machine-wide serving choices, so this is where it belongs.

Reproducibility does not need a seed. The pinned model server ignores the
seed a request carries (iss-2609061429558510), and a fixed temperature is what
makes generation repeatable; the intent that would have exposed a seed
(itd-2609061429516182) is superseded by this one and by that documented fact.

Evidence: research note 2026-09-06-model-bench-evidence (sampling probe: seed inert on four models, temperature effective, temperature 0 deterministic; a reasoning model needed an 8,192-token completion budget).

## Mechanism

We expect a machine-wide default to reach every request that omits a sampling
parameter and none that carries one, because the model server takes those
values once when it starts and a request's own field replaces them for that
request alone.

## Scope Conditions

- Requests served to the pinned mlx-lm 0.31.3 model server; a later server may <!-- cond: cond-2609061822371570 -->
  take a different set of parameters when it starts.
- Only the parameters that server accepts when it starts; anything it accepts <!-- cond: cond-2609061822373761 -->
  per request alone is outside this claim.
- The values Gropius accepts are no wider than the ranges the model server <!-- cond: cond-2609061822370170 -->
  itself accepts.
- Clients follow OpenAI semantics, where an omitted or null parameter means <!-- cond: cond-2609061822377006 -->
  "use the default".
- One global set of defaults with at most one override per model; a model with <!-- cond: cond-2609061822372855 -->
  no override is served with the global set.

## Acceptance Criteria

- Given a default temperature saved in Settings, when a client sends a
  completion that omits the temperature, then that request is served with the
  saved value.
- Given a default temperature saved in Settings, when a client sends its own
  temperature, then that request is served with the client's value.
- Given an override saved for one model, when that model has loaded again,
  then requests to it that omit the parameter are served with the override
  rather than the global default.
- Given a default is changed, when Alice saves it, then the panel names the
  loaded models that must load again before the new value applies to them.
- Given a value outside the range the model server accepts, when it is posted
  to Settings, then it is refused with the field named and neither the running
  nor the stored configuration changes.
- Given the documentation, when a reader looks up sampling, then it states
  which parameters can be defaulted, that a request's own value wins, what a
  blank field means, and that reproducibility comes from a fixed temperature
  because the model server ignores a seed.

## Open Questions

- Resolved: where the default is applied — to the model server when it starts,
  so a change counts from that model's next load and the panel says so.
- Resolved: precedence — a request's own value always wins; the default only
  fills what a request omits.
- Resolved: scope — a global set of defaults plus an optional per-model
  override.
- Resolved: which parameters — only those the pinned mlx-lm server accepts as
  start-up options, the exact set confirmed against the 0.31.3 server at spec
  time; a default number of completion tokens is included if that server takes
  one, since a reasoning model in the lab needed 8,192.
- Resolved: the panel shows the value that applies when a field is left blank,
  so a blank field is honest about what it means.
- Resolved: seed — the pinned server ignores it (iss-2609061429558510), so no
  seed field is offered and itd-2609061429516182 is retired; the documentation
  states the behaviour instead.
- Deferred: what a hand-edited configuration file holding an out-of-range
  sampling value does at start-up. The reviewer asked for an explicit choice
  between refusing to serve beyond loopback and dropping the value with a
  warning; the interview did not reach it, and the spec decides it alongside
  the range check.

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-e4c0369b6247 -->
Fidelity review — receipt rcp-e4c0369b6247 (verifier abcd:intent-auditor claude-opus-5[1m]).

Provenance: abcd:intent-auditor@claude-opus-5[1m] · rubric_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5 · prompt_hash sha256:e1e9d1573ed990850846adc444c0a8ff9dd8143db7a0bd9c8ad3580c64e2d79c
Input attestations: diff:working tree at HEAD 866e926 (history rewritten during a rename; no reliable per-spec commit range, so the tree as shipped was audited)@-; intent:.abcd/development/intents/shipped/itd-2609061429508050-default-sampling-parameters-in-settings-alice-sets-a-default.md@-; spec:.abcd/development/specs/closed/spc-2609061822378193-default-sampling-parameters-in-settings-alice-sets-a-default.md@-; research-note:.abcd/development/research/notes/2026-09-06-mlx-lm-sampling-launch-flags.md (flag spellings, server defaults and accepted ranges read from mlx-lm 0.31.3 source)@-; test-run:go test ./... — all 15 packages ok (cmd/gropius, internal/app, internal/archtest, internal/config, internal/gateway, internal/runtime, internal/ui among them), 0 failures@-;

Acceptance rollup: MET 6 · MET_WITH_CONCERNS 0 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: A saved default reaches the model server process as a launch flag and the relayed body is left untouched: the pool reads the live effective sampling at start time, the launcher appends the flag, and a gateway test asserts no sampling key is injected into the request the server receives.
  evidence: internal/runtime/launcher.go:221 — "args = append(args, samplingArgs(spec.Sampling)...)"
  evidence: internal/runtime/pool.go:806 — "var sampling config.Sampling if p.opts.SamplingFor != nil { sampling = p.opts.SamplingFor(repoID)"
  evidence: internal/app/app.go:193 — "SamplingFor: func(repoID string) config.Sampling { return a.Config().EffectiveSampling(repoID)"
  evidence: internal/gateway/sampling_wiring_test.go:128 — "func TestSavedSamplingReachesTheLaunchedProcess(t *testing.T)"
  evidence: internal/gateway/sampling_test.go:21 — "func TestConfiguredDefaultAddsNothingToTheRelayedBody(t *testing.T)"
- ac-2 — MET: The completion path is a pure relay — no sampling field is read, added or rewritten in internal/gateway/gateway.go's handleCompletions — and a gateway test asserts the fake model server's last body carries the client's own temperature and every other field it sent, unchanged.
  evidence: internal/gateway/sampling_test.go:45 — "func TestClientSamplingValuesReachTheModelServerUnchanged(t *testing.T)"
  evidence: internal/gateway/gateway.go:336 — "func (g *Gateway) handleCompletions(w http.ResponseWriter, r *http.Request) {"
  evidence: internal/config/sampling.go:10 — "They are handed to each model server as launch flags, so the server itself owns the default"
- ac-3 — MET: Config.EffectiveSampling lays a per-model override over the machine-wide set and the pool reads it at each start, so a pool test with two models asserts the override's value appears on the overridden model only, and that a saved change reaches the model on the load that follows, not before.
  evidence: internal/config/sampling.go:345 — "func (c Config) EffectiveSampling(repoID string) Sampling {"
  evidence: internal/runtime/sampling_test.go:226 — "func TestPoolLaunchesEachModelWithItsOwnSamplingDefaults(t *testing.T)"
  evidence: internal/runtime/sampling_test.go:285 — "if got := l.specFor("org/plain").Sampling.Temperature; got == nil || *got != 1.2 {"
  evidence: internal/gateway/sampling_wiring_test.go:151 — "special := l.specFor("org/special").Sampling"
- ac-4 — MET: handleSetSettings compares the effective sampling of every resident model before and after the save and returns a reload_models list, which app.js renders into the saved-settings message; a wired control test with two residents asserts the list holds exactly the model whose effective value moved, the override-shadowed one excluded.
  evidence: internal/gateway/control.go:858 — ""reload_models": samplingReloads(current, incoming, c.App.Pool.Resident()),"
  evidence: internal/gateway/control.go:904 — "func samplingReloads(before, after config.Config, resident []runtime.Resident) []string {"
  evidence: internal/ui/static/app.js:982 — "parts.push(`Load ${res.reload_models.join(', ')} again to serve with the new sampling defaults.`);"
  evidence: internal/gateway/sampling_wiring_test.go:163 — "func TestSettingsNamesExactlyTheResidentModelsThatMustLoadAgain(t *testing.T)"
- ac-5 — MET: Sampling.Validate names the offending field, App.SetConfig validates before it saves or assigns, and handleSetSettings decodes into current.Clone() so a posted pointer cannot alias the live config; a control test asserts a 400 naming "temperature", App.Config() deep-equal to its prior value including pointees, and config.json bytes unchanged.
  evidence: internal/config/sampling.go:213 — "func (s Sampling) Validate() error {"
  evidence: internal/gateway/control.go:816 — "incoming := current.Clone()"
  evidence: internal/app/app.go:282 — "if err := c.Validate(); err != nil { return err"
  evidence: internal/gateway/sampling_test.go:117 — "func TestOutOfRangeSamplingIsRefusedAndChangesNothing(t *testing.T)"
  evidence: internal/config/sampling_test.go:35 — "func TestGoRangesAreNeverWiderThanThePinnedServers(t *testing.T)"
- ac-6 — MET: The documentation states all four things — the parameter table, that a request's own value wins, what a blank field means, and that reproducibility comes from a fixed temperature because the pinned server ignores a seed — and an architecture test pins the parameter table and the stated ranges to config.Sampling and config.SamplingBounds so they cannot drift.
  evidence: docs/sampling-reference.md:9 — "| Field | Applies to | Blank means |"
  evidence: docs/sampling-reference.md:51 — "## Precedence"
  evidence: docs/sampling-reference.md:17 — ""Blank means" is the model server's own default, which applies when Gropius holds no value."
  evidence: docs/sampling-explained.md:62 — "## Why there is no seed"
  evidence: docs/sampling-explained.md:70 — "What does make generation repeatable is a fixed temperature."
  evidence: internal/archtest/docs_test.go:23 — "func TestSamplingDocsMatchTheCode(t *testing.T) {"
  evidence: internal/archtest/docs_test.go:106 — "func TestSamplingReferenceStatesTheRealRanges(t *testing.T) {"

Gap audit:
- honoured:
  - Alice sets the default temperature the whole machine serves with, in Settings, and saves
    evidence: internal/ui/static/index.html:254 — "< legend>Sampling defaults< /legend>"
    evidence: internal/config/sampling.go:24 — "type Sampling struct {"
  - the panel tells her which models must load again before the new value counts
    evidence: internal/gateway/control.go:904 — "func samplingReloads(before, after config.Config, resident []runtime.Resident) []string {"
    evidence: internal/ui/static/app.js:982 — "Load ${res.reload_models.join(', ')} again to serve with the new sampling defaults."
  - every request that omits the parameter is served with her value
    evidence: internal/runtime/launcher.go:221 — "args = append(args, samplingArgs(spec.Sampling)...)"
    evidence: internal/gateway/sampling_wiring_test.go:128 — "func TestSavedSamplingReachesTheLaunchedProcess(t *testing.T)"
  - one model gets its own override, which takes over the next time it loads
    evidence: internal/config/sampling.go:345 — "func (c Config) EffectiveSampling(repoID string) Sampling {"
    evidence: internal/runtime/sampling_test.go:226 — "func TestPoolLaunchesEachModelWithItsOwnSamplingDefaults(t *testing.T)"
  - Bob's client, which sets its own temperature, is unaffected — what a request carries still wins
    evidence: internal/gateway/sampling_test.go:45 — "func TestClientSamplingValuesReachTheModelServerUnchanged(t *testing.T)"
    evidence: internal/gateway/sampling_test.go:21 — "func TestConfiguredDefaultAddsNothingToTheRelayedBody(t *testing.T)"
  - each field shows the value that applies when Alice leaves it blank
    evidence: internal/ui/static/index.html:260 — "< input id="setTemp" type="number" step="any" min="0" placeholder="0 — greedy, and repeatable">"
    evidence: internal/ui/sampling_test.go:79 — "func TestBlankSamplingFieldsShowTheModelServerDefaults(t *testing.T)"
  - a hand-edited out-of-range value does not lock the server down — it is dropped with a warning (the intent's deferred question)
    evidence: internal/config/sampling.go:413 — "func (c *Config) sanitizeSampling() []string {"
    evidence: cmd/gropius/main.go:113 — "func warnDroppedSettings(log *slog.Logger, dropped []string) {"
    evidence: internal/config/sampling_test.go:137 — "func TestLoadDropsOutOfRangeSamplingRatherThanRefusingTheFile(t *testing.T)"
- diverged:
  - "the other sampling parameters mlx-lm accepts" (intent title) — delivered as exactly the five parameters the pinned server takes as LAUNCH flags; repetition/presence penalties, xtc_*, logit_bias, logprobs and seed accept no default at all, which is narrower than the title reads
    evidence: internal/runtime/launcher.go:48 — "var samplingFlags = map[string]string{ "temperature": "--temp","
    evidence: docs/sampling-reference.md:21 — "Anything else a request can carry — repetition and presence penalties, `logit_bias`, `logprobs`, `seed` — is a per-request field only, and cannot be given a default."
  - "every request that omits the parameter is served with that value" — delivered only for a request that literally omits the key; a request carrying an explicit null is NOT served with the default, it is refused by the model server and reaches the client as 502
    evidence: docs/sampling-explained.md:30 — "## Why null is not the same as omitted"
    evidence: docs/sampling-reference.md:57 — "| carries `null` | nothing — the request fails"
  - the spec promised "a new how-to page under docs/" — three pages shipped instead (how-to, reference, explanation), a larger docs surface than the spec's plan, held together by a Diataxis test
    evidence: docs/sampling-defaults.md:1 — "# Set default sampling parameters"
    evidence: docs/sampling-reference.md:1 — "# Reference: sampling parameters"
    evidence: internal/archtest/docs_test.go:139 — "func TestSamplingPagesKeepTheirDiataxisType(t *testing.T) {"
- missing: (none)

Scope-condition dispositions:
- cond-2609061822371570 — survived: The delivery is bound to the pinned mlx-lm 0.31.3: the flag spellings and ranges are recorded from that release's source, and a test fails the build if mlx-requirements.txt moves off it without the table being re-read.
  evidence: internal/runtime/launcher.go:43 — "const samplingFlagsVerifiedAgainst = "0.31.3""
  evidence: internal/runtime/sampling_test.go:206 — "func TestSamplingFlagsWereVerifiedAgainstThePinnedServer(t *testing.T)"
- cond-2609061822373761 — survived: Only the five parameters the server takes as start-up options are defaultable; everything it reads per request alone is excluded in the type's own comment, in the flag table, and on the reference page.
  evidence: internal/config/sampling.go:21 — "The set is exactly the parameters mlx-lm 0.31.3 exposes as launch flags; anything it reads per request only ... cannot be defaulted this way."
  evidence: internal/runtime/launcher.go:48 — "var samplingFlags = map[string]string{"
  evidence: internal/runtime/sampling_test.go:183 — "func TestEverySamplingParameterHasALaunchFlag(t *testing.T)"
- cond-2609061822370170 — survived: A table test pins every Go range against the ranges recorded from the pinned server, and the two ceilings Gropius adds (top-k 1024, max_tokens 1048576) make it narrower than the server, never wider — which is what the condition asks.
  evidence: internal/config/sampling_test.go:35 — "func TestGoRangesAreNeverWiderThanThePinnedServers(t *testing.T)"
  evidence: internal/config/sampling.go:92 — "const MaxTopK = 1024"
  evidence: internal/config/sampling.go:103 — "const MaxCompletionTokens = 1 << 20"
- cond-2609061822377006 — falsified: The condition assumed an omitted OR null parameter means "use the default"; delivered reality honours only the omitted half — an explicit null reaches the model server, which refuses it and closes the connection, so the client gets 502 rather than the saved default, and the documentation names this as a client-side workaround rather than a behaviour Gropius provides.
  evidence: docs/sampling-explained.md:32 — "To be served with a default, a request must leave the parameter *out*. An explicit `null` is not the same thing"
  evidence: docs/sampling-explained.md:37 — "Some OpenAI client libraries send `null` for an option that was never set. If requests fail that way, configure the client to omit the field instead."
  evidence: docs/sampling-reference.md:57 — "| carries `null` | nothing — the request fails"
- cond-2609061822372855 — survived: One global Sampling block plus a ModelSampling map that refuses two spellings of the same folded repo id, and EffectiveSampling returns a clone of the global set for any model with no override — exactly the shape the condition assumed.
  evidence: internal/config/sampling.go:359 — "return c.Sampling.Clone()"
  evidence: internal/config/sampling.go:393 — "return fmt.Errorf("sampling overrides %q and %q name the same model", first, id)"
  evidence: internal/gateway/sampling_test.go:265 — "func TestCaseVariantOverrideKeysAreRefused(t *testing.T)"
## Grounds

- pursued: we expect a machine-wide temperature default and a visible context length to remove the two commonest client misconfigurations on a shared Mac, wrong temperature and oversized prompts; we are wrong if clients keep sending their own values regardless, or if no client reads the context field within two releases of it shipping
