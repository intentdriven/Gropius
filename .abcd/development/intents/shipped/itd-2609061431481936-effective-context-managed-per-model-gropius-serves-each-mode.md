---
id: itd-2609061431481936
slug: effective-context-managed-per-model-gropius-serves-each-mode
spec_id: spc-2609091431038544
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609061431463108]
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Effective context managed per model: Gropius serves each model only up to the context it can actually hold on this Mac, reports that effective limit to clients, and answers an over-long prompt with a clear error instead of a crash or a thrash

## Press Release

Alice runs a dense 27B model whose configuration claims a one-million-token
window, on a Mac whose memory budget can hold a fraction of that in KV cache.
Gropius tells her client the window it will actually serve, and when a prompt
exceeds it the request is refused at once with an error that names the limit
and the prompt's size. The model server never crashes under an oversized
prompt, and other clients on the machine never lose their model to a thrash.

## Why This Matters

The architectural maximum is not what the machine can serve. The effective
window is the smaller of that cap and what the memory budget leaves for the
KV cache, and the cost per token differs sharply by architecture: the 2026-09-05
model-bench lab estimates about 0.25 GB per thousand tokens for a dense 27B
at f16 and far less for hybrid-attention MoE models. The pool's budget
currently charges a flat 1.2x of disk size per model and ignores the KV cache
(iss-3), so a long prompt can push a Mac past its budget with nothing in
Gropius having said no.

Builds on itd-2609061431463108, which reports the architectural window.
Refines iss-3. Depends on iss-2609061431537735, the measurement of effective
windows on a real machine, for the numbers that make the claim testable.

Evidence: research note 2026-09-06-model-bench-evidence (context probe: three MoE models verified past 120K; the dense 27B verified 93K with failures caused by the gateway timeout, not the window).

## Mechanism

We expect a per-model served window, enforced at the gateway from a byte
estimate and charged in the memory budget, to keep the machine off swap,
because the four measured models' peaks were linear in prompt size and the
estimate over-counts. Shown wrong if a prompt under the window still pushes the
process past its charge.

## Scope Conditions

- macOS with mlx-lm as the model server. <!-- cond: cond-2609091431038721 -->
- The window is per model and static, with the declared cap as its default. <!-- cond: cond-2609091431030549 -->
- The byte estimate is conservative, so a refusal may fire on a prompt that would have fitted. <!-- cond: cond-2609091431031658 -->
- The estimate counts the prompt plus max_tokens. <!-- cond: cond-2609091431030215 -->
- The model server's own rejection remains the backstop. <!-- cond: cond-2609091431033452 -->

Clarification (2026-09-09, from the review of the shipping change; the bullet
above stands as stamped and this says what was found true of it): there is no
backstop. mlx-lm was measured accepting an abandoned 256K prompt until the
machine swapped (iss-3's evidence, research note 2026-09-06-context-windows),
so nothing rejects an over-long prompt if the gateway's check does not. What
carries the residual instead is the estimate's direction and the charge's
margin: the byte estimate over-counts on English text and under-counts by
about a third on densely packed CJK, the request body is capped, and the
memory budget charges five to seven times the cache a configuration implies —
so a prompt a third over the window is inside what the model was charged for.

## Acceptance Criteria

- Given a model with a served window set, when a prompt plus max_tokens is estimated above it, then a 400 in the OpenAI error shape names the window and the estimated size, for streaming and non-streaming alike.
- Given no window set, when the model loads, then the declared cap is served and charged.
- Given a window, when the model is admitted, then the charge uses that window and the panel shows it.
- Given the models list, when an entry is returned, then it carries the served window beside the declared cap.
- Given config.json and the settings pane, when the window is changed, then the next load charges the new figure and a save never refuses an untouched field.
- Given the docs, when a reader opens the memory-budget page, then it explains the window and the charge in words.

## Hold

Lifted on 2026-09-09 at interview: the maintainer chose to charge the served window and enforce it at the gateway as the KV-budget rule, which is this intent; the budget rule now lives in the KV-budget change and the measurement record is resolved with its evidence.

Held on 2026-09-06 by the maintainer's decision in the planning interview: not planned until iss-2609061541313832 (the gateway's ten-minute upstream timeout, which confounds long-prompt measurement), the budget rule ADR that the configurable-budget intent's spec and iss-3 produce, and iss-2609061431537735 (effective-window measurement) have landed. The context-window reporting intent ships alone first.

Depends on, updated 2026-09-06 after the context-window campaign (research note 2026-09-06-context-windows): iss-2609061431537735 is satisfied as far as the timeout allows. Nominal, verified and usable windows are measured for all four local models (Nemotron 253,106 verified at its cap; Qwen3-Coder-Next 221,743; GLM-4.7-Flash 81,100; Qwen3.8-27B 91,673; 34 needle runs recalled out of 34 at sizes up to those), and the per-architecture memory cost iss-3 needs is measured (12, 115, 201 and 353 KB per token with a 1 to 3.4 GB intercept, plus cache retention across requests). Still outstanding: iss-2609061541313832, which bounds three of the four windows and must land before the windows above 222K, 81K and 92K can be verified; and the budget rule ADR. The issue records stay open for the fixing changes.

## Open Questions

All four resolved on 2026-09-09 at interview: the prompt's size is estimated from bytes with a safety margin; the served window is a static per-model setting defaulting to the declared cap; an over-long prompt gets a 400 in the OpenAI error shape naming the window and the estimated size, streaming included; max_tokens counts against the same window.

- How does the gateway know a prompt's token count without a tokenizer? Count
  in the model server and rely on its rejection, estimate from bytes with a
  safety margin, or load the model's tokenizer in Go?
- Is the effective limit a static per-model figure computed from the budget,
  or does it move with what else is resident and with decode concurrency
  (each concurrent sequence holds its own KV cache)?
- What does "clear error" look like on the wire: a 400 with the OpenAI error
  shape naming the limit and the prompt size, and the same for streaming?
- Does the limit also cap max_tokens, since generated tokens occupy the same
  cache?

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-2d5e0677298b -->
Fidelity review — receipt rcp-2d5e0677298b (verifier intent-auditor claude-opus-5[1m]).

Provenance: intent-auditor@claude-opus-5[1m] · rubric_hash sha256:9723df19036793dd346faee74a9f1fd0964adaa0a293b723d28fb51f34fa98ae · prompt_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5
Input attestations: diff:98a6a9d^1..98a6a9d (merge of feat/kv-cache-memory-budget; spec closed in 0e146ad)@sha256:fa1b1965e356e39a41d8e766dfa93476492b48979e142f017dbd131a2ef5bbae; tree:worktree /.claude/worktrees/posture at HEAD dd6463d (the content main becomes when PR #35 merges); every criterion re-verified here, go test green for internal/{config,gateway,runtime,ui,capability,app}@-; policy:AUDITOR-COMPUTED, not host-supplied: rubric_hash = sha256 of .abcd/.work.local/reviews/rcp-2d5e0677298b.request.md; prompt_hash = sha256 of ~/.claude/plugins/marketplaces/abcd-marketplace/agents/intent-auditor.md@-;

Acceptance rollup: MET 6 · MET_WITH_CONCERNS 0 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: overServedContext runs before the pool is acquired and returns a message naming both the estimate and the served window, written through writeError's OpenAI error shape; the test drives it for stream=false and stream=true alike and asserts status, error.type and both figures
  evidence: internal/gateway/gateway.go:600 — "if msg := g.overServedContext(cfg, model, len(raw), payload); msg != "" {"
  evidence: internal/gateway/gateway.go:1412 — ""this request is about %s tokens, more than the %s this model is served at. "+"
  evidence: internal/gateway/gateway.go:1360 — "writeError renders an OpenAI-shaped error, which is what clients parse."
  evidence: internal/gateway/servedcontext_test.go:40 — "func TestAPromptOverTheServedContextIsRefused(t *testing.T) {"
- ac-2 — MET: Config.ServedContext returns the declared window when no setting is present, the gateway then serves a 10,000-token prompt at a declared 262,144 window, and App.chargeOf feeds that same fallback into capability.LoadCostOf so the declared cap is what is charged
  evidence: internal/config/config.go:760 — "if set <= 0 || (declared > 0 && set > declared) {"
  evidence: internal/gateway/servedcontext_test.go:109 — "func TestWithNoSettingTheDeclaredWindowIsServed(t *testing.T) {"
  evidence: internal/app/app.go:981 — "Window: a.Config().ServedContext(m.RepoID, m.ContextLength),"
  evidence: internal/app/servedcontext_test.go:29 — "DiskBytes: 10 * gb, KVChargePerToken: 100, Window: 262144, Sequences: 1,"
- ac-3 — MET: the pool charges capability.LoadCostOf with ResolvedModel.ServedContext at admission (and refuses, naming what would fit, when that charge does not fit), and the panel both renders a Served context field per model and computes modelCharge from the same window, held to the Go figure by a test in internal/ui
  evidence: internal/runtime/pool.go:1047 — "Window: m.ServedContext,"
  evidence: internal/runtime/kvcharge_test.go:201 — "func TestAModelWhoseServedWindowDoesNotFitIsRefusedWithWhatWouldFit(t *testing.T) {"
  evidence: internal/ui/static/app.js:998 — "const window = servedContext(config, m.repo_id, m.context_length || 0);"
  evidence: internal/ui/static/index.html:441 — "< legend>Served context< /legend>"
  evidence: internal/ui/settings_test.go:190 — "func TestSettingsFormChargesTheServedContext(t *testing.T) {"
- ac-4 — MET: the /v1/models entry writes served_context alongside context_length/max_model_len from the same Config.ServedContext call, and a gateway test asserts the listing carries both
  evidence: internal/gateway/gateway.go:416 — "if served := cfg.ServedContext(m.RepoID, m.ContextLength); served > 0 {"
  evidence: internal/gateway/gateway.go:417 — "entry["served_context"] = served"
  evidence: internal/gateway/servedcontext_test.go:149 — "func TestModelsListCarriesTheServedContext(t *testing.T) {"
  evidence: docs/models-list.md:49 — "| `served_context` | The window this Mac will actually serve the model at, in tokens."
- ac-5 — MET: served_context is a config.json field validated in the config package and posted by the settings pane, a lowered window changes what the next charge computes (and RefreshCharges re-reads it for resident models), and a save touching only the idle timeout leaves the untouched window at 32768
  evidence: internal/config/config.go:702 — "ServedContext int64 `json:"served_context,omitempty"`"
  evidence: internal/ui/static/app.js:1243 — "applyModelNumber(out, 'served_context', listedContext, typedContext);"
  evidence: internal/app/servedcontext_test.go:15 — "func TestALoweredServedContextLowersWhatAPinnedModelCosts(t *testing.T) {"
  evidence: internal/runtime/kvcharge_test.go:170 — "func TestRefreshChargesTakesTheModelsFactsAgain(t *testing.T) {"
  evidence: internal/app/servedcontext_test.go:54 — "func TestSavingAnUnrelatedSettingKeepsTheServedContext(t *testing.T) {"
- ac-6 — MET: the memory-budget page carries a section explaining that the window in the charge is the operator's Settings figure or the model's declared window, that the budget charges the cache that window costs and the gateway refuses a larger request, and what to do when a model will not fit
  evidence: docs/memory-budget-explained.md:59 — "## The window is the operator's, and it is the same window twice"
  evidence: docs/memory-budget-explained.md:64 — "trustworthy: the memory budget charges the cache this window costs, and the"
  evidence: docs/memory-budget-explained.md:34 — "## What a model is charged, and why the cache is most of it"

Gap audit:
- honoured:
  - Gropius tells her client the window it will actually serve
    evidence: internal/gateway/gateway.go:417 — "entry["served_context"] = served"
    evidence: docs/models-list.md:148 — "`served_context` is the window Gropius serves the model at on this Mac"
  - when a prompt exceeds it the request is refused at once with an error that names the limit and the prompt's size
    evidence: internal/gateway/gateway.go:1412 — ""this request is about %s tokens, more than the %s this model is served at. "+"
    evidence: internal/gateway/servedcontext_test.go:40 — "func TestAPromptOverTheServedContextIsRefused(t *testing.T) {"
  - other clients on the machine never lose their model to a thrash: the refusal lands before the pool is asked for anything, and the charge now covers the cache the served window costs with no ceiling held down to the budget
    evidence: internal/gateway/gateway.go:600 — "if msg := g.overServedContext(cfg, model, len(raw), payload); msg != "" {"
    evidence: internal/capability/capability.go:166 — "cache := MulSaturating(MulSaturating(l.KVChargePerToken, l.Window), l.Sequences)"
    evidence: internal/capability/kvcharge_test.go:189 — "func TestNothingHoldsAChargeDownToTheBudget(t *testing.T) {"
  - max_tokens counts against the same window, and a client's own figures cannot wrap or vanish past it
    evidence: internal/gateway/gateway.go:1407 — "estimate := capability.AddSaturating(int64(estimatedTokens(bodyBytes)), requestedMaxTokens(payload))"
    evidence: internal/gateway/servedcontext_test.go:179 — "func TestAnAbsurdMaxTokensDoesNotWrapPastTheWindow(t *testing.T) {"
- diverged:
  - Gropius serves each model only up to the context it can actually hold on this Mac — delivered as an operator-set static window defaulting to the declared cap, not as a window Gropius works out from the budget. A model that does not fit at its window is refused with what would fit, rather than served at the smaller window that would hold
    evidence: internal/config/config.go:760 — "if set <= 0 || (declared > 0 && set > declared) {"
    evidence: internal/runtime/pool.go:1020 — "fits := fmt.Sprintf("lower this model's served context from %d to about %d tokens","
    evidence: docs/memory-budget-explained.md:68 — "That is what to reach for when a model will not fit."
  - the spec's test table names two tests the delivery does not carry under those names: internal/runtime TestTheChargeUsesTheServedWindow and internal/gateway TestAPromptOverTheServedContextNeverReachesThePool. The behaviour is covered — the no-acquire assertion is folded into TestAPromptOverTheServedContextIsRefused and the charge is covered by the kvcharge tests — but the spec's own map to its tests is stale
    evidence: .abcd/development/specs/closed/spc-2609091431038544-effective-context-managed-per-model-gropius-serves-each-mode.md:94 — "`internal/runtime`: `TestTheChargeUsesTheServedWindow`"
    evidence: internal/gateway/servedcontext_test.go:78 — "pool.mu.Lock()"
    evidence: internal/runtime/kvcharge_test.go:19 — "func TestTwoLongContextModelsNoLongerCoReside(t *testing.T) {"
- missing:
  - the model server never crashes under an oversized prompt — for a model that declares no window and has been given no setting there is nothing to enforce, the gateway refuses nothing, and by the code's own account no backstop exists underneath it
    evidence: internal/gateway/gateway.go:1401 — "if window <= 0 {"
    evidence: internal/gateway/servedcontext_test.go:132 — "func TestAModelWithNoWindowAtAllRefusesNothing(t *testing.T) {"
    evidence: internal/gateway/gateway.go:1387 — "implies, which is far more than a third of headroom. There is no backstop"

Scope-condition dispositions:
- cond-2609091431038721 — survived: the delivery is still built on mlx-lm as the model server on macOS: the gateway's own reasoning rests on measured mlx-lm behaviour and the gateway tests drive a fake mlx-lm server
  evidence: internal/gateway/gateway.go:1388 — "underneath this: mlx-lm was measured accepting an abandoned 256K prompt"
  evidence: internal/gateway/servedcontext_test.go:22 — "fake := mlxtest.Start(mlxtest.Options{ModelArg: modelPath, Reply: "GROPIUS OK"})"
- cond-2609091431030549 — narrowed: the window is per model and static with the declared cap as its default, as assumed — but the delivery also makes the declared cap a hard ceiling and gives a model that declares nothing no window at all, so the assumption holds over a smaller range than the condition stated
  narrowing: holds for a static per-model figure between 1 and the model's declared cap (and no larger than config.MaxContextLength): a setting above the declared window is silently ignored rather than honoured, and a model whose configuration declares no window has no served window at all and nothing enforced
  evidence: internal/config/config.go:760 — "if set <= 0 || (declared > 0 && set > declared) {"
  evidence: internal/config/config.go:850 — "if sc := c.Models[id].ServedContext; sc < 0 || sc > MaxContextLength {"
  evidence: internal/gateway/gateway.go:1401 — "if window <= 0 {"
- cond-2609091431031658 — narrowed: the estimate is conservative on the text it was sized for — the whole encoded body at four bytes per token, JSON syntax and role names included — but the delivery's own account records it under-counting by about a third on densely packed CJK, so the condition does not hold for every prompt
  narrowing: holds for text whose tokens average four bytes or more (English and similar): on densely packed CJK the estimate under-counts by about a third, so a prompt about a third over the window passes rather than being refused; what carries that overshoot is the 32 MiB body cap and the charge's five-to-sevenfold margin, not the estimate
  evidence: internal/gateway/gateway.go:1489 — "func estimatedTokens(bodyBytes int) int {"
  evidence: internal/gateway/gateway.go:1381 — "It under-counts on text whose tokens are shorter in bytes than four —"
- cond-2609091431030215 — survived: the check adds the request's requested answer length to the body estimate before comparing against the window, reading both max_tokens and max_completion_tokens as JSON numbers and adding saturating, and a test refuses a prompt that fits alone but not with its answer
  evidence: internal/gateway/gateway.go:1407 — "estimate := capability.AddSaturating(int64(estimatedTokens(bodyBytes)), requestedMaxTokens(payload))"
  evidence: internal/gateway/servedcontext_test.go:89 — "func TestMaxTokensCountsAgainstTheServedContext(t *testing.T) {"
- cond-2609091431033452 — falsified: the condition assumed the model server's own rejection remains the backstop; the delivery records that mlx-lm was measured accepting an abandoned 256K prompt until the machine swapped, so nothing rejects an over-long prompt if the gateway's check does not — a model with no window at all is served unbounded, and the residual is carried by the estimate's direction and the charge's margin instead
  evidence: internal/gateway/gateway.go:1387 — "implies, which is far more than a third of headroom. There is no backstop"
  evidence: internal/gateway/servedcontext_test.go:130 — "prompt reaches mlx-lm unbounded, as every prompt did before this"
## Grounds

- pursued: we expect long-context models to become usable beside each other once their charge reflects the window Alice chose rather than the declared maximum; shown wrong if operators leave every window at the default and the budget refuses the same models it did before.
