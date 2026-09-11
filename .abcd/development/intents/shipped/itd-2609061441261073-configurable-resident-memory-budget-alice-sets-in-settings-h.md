---
id: itd-2609061441261073
slug: configurable-resident-memory-budget-alice-sets-in-settings-h
spec_id: spc-2609061822383286
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Configurable resident memory budget: Alice sets in Settings how much memory Gropius may fill with loaded models, sees the default and what it means for which models can co-reside, and changes it without a restart

## Press Release

Alice opens Settings and sees how much of her Mac's memory Gropius may fill
with loaded models: the figure in gigabytes, what fraction of the machine that
is, and what each of her downloaded models would cost against it. She raises it
because her Mac runs nothing else, saves, and her writer and reviewer models now
sit in memory together without a restart. Bob, on a laptop he also works on,
lowers his; nothing is pulled out from under the model he is using, and the
panel simply tells him he is over his new figure until the models settle.

## Why This Matters

The budget is fixed at 60% of physical memory and is not a field anyone can
set. On the 2026-09-05 model-bench lab's 128 GB machine that is about 77 GB,
and the lab's preferred pair (a 45 GB coder and a 29 GB reviewer, charged 1.2x
each) does not fit although the machine has room. A dedicated inference Mac and
a shared laptop want different answers, and today both get the same one. The
budget is a single number the pool already holds; this intent gives it a home
in Settings next to decode concurrency and idle timeout.

iss-3 and iss-6 concern how the budget is accounted; this intent concerns who
sets it. The budget half of the docs gap iss-2609061443332414 is settled by the
same change.

Evidence: research notes 2026-09-06-model-bench-evidence and 2026-09-06-model-bench-findings (co-residency arithmetic under the 60% default: the preferred author-and-reviewer pair does not fit although the machine has room).

## Mechanism

We expect a writer-and-reviewer pair charged at about 89 GB to sit in memory
together once the operator raises the budget above that figure, because
admission is a single comparison against the budget and nothing else in the
pool constrains residency; we are wrong if a raised budget admits more than the
Mac can hold, since the charge ignores the KV cache (iss-3) and eviction
credits memory before the victim exits (iss-6).

## Scope Conditions

- Apple Silicon Macs whose physical memory the system reports. <!-- cond: cond-2609061822382119 -->
- The 1.2x charge as the only accounting rule; the claim says nothing about <!-- cond: cond-2609061822387031 -->
  concurrent long-context load, which iss-3 covers.
- Operators who know what else the Mac runs, since the high-budget warning is <!-- cond: cond-2609061822380027 -->
  advice rather than enforcement.
- One Gropius server per machine; in shared-cache mode the budget lives in the <!-- cond: cond-2609061822380142 -->
  shared settings and applies to whichever account runs the server.

## Acceptance Criteria

- Given a fresh install, when Alice opens Settings, then the budget shows the
  default in gigabytes with its share of the machine's memory beside it, and
  the stored settings hold no explicit value.
- Given two models whose charged sizes together exceed the default but fit the
  machine, when Alice raises the budget above their sum and saves, then both
  models load and stay in memory together without Gropius being restarted.
- Given a budget larger than the machine's physical memory, when Alice saves,
  then the save is refused with a message naming the machine's memory, and the
  stored settings are unchanged.
- Given two pinned models are resident, when Alice lowers the budget below the
  sum of their charged sizes, then the save is refused with a message naming
  that sum.
- Given resident models whose charged sizes exceed a lowered budget, when Alice
  saves it, then no model is unloaded on her behalf and the control panel
  reports the machine as over budget until those models unload.
- Given the machine's physical memory cannot be determined, when Settings
  renders, then the budget is shown without a percentage and an explicit value
  is accepted with no ceiling check.
- Given the search tab, when the budget changes, then models the pool would now
  refuse are hidden and models it would now admit are shown, with no restart.

## Open Questions

- Resolved: Alice types gigabytes, the value is stored in bytes, and the
  percentage of the machine's memory is shown beside the field.
- Resolved: lowering the budget below what is resident applies to future loads
  only; nothing is unloaded on save, and the panel shows the over-budget state
  until the models unload by the usual rules.
- Resolved: the machine-dependent ceiling is checked when Settings is saved,
  not when settings are read at start-up, so a settings file carried from a
  larger Mac still loads rather than resetting every other field with it.
- Resolved: the floor is the sum of the pinned models' charged sizes, the
  invariant owned by itd-2609061441241254 rather than restated here.
- Resolved: a budget above the share of memory the rest of the machine needs
  draws a warning, not a refusal.
- Resolved: a saved budget takes effect immediately through a setter on the
  pool; the field is not restart-only.
- Resolved: Settings shows each downloaded model's charged size and the current
  resident total, and leaves the operator to add them up; it does not enumerate
  which combinations fit.
- Resolved: iss-6 is not a prerequisite, because lowering the budget evicts
  nothing; the docs state instead that the budget can be exceeded briefly while
  a replaced model exits.
- Resolved: iss-3 is refined rather than blocked; the docs state that the
  charge excludes the KV cache, which is why the high-budget warning exists.
- Depends on: itd-2609061441241254, the pinned models whose charged sum this
  budget's floor is measured against.

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-0b2669b3be31 -->
Fidelity review — receipt rcp-0b2669b3be31 (verifier intent-auditor claude-opus-5[1m]).

Provenance: intent-auditor@claude-opus-5[1m] · rubric_hash sha256:6a02abe3596f48cdc97f89d082e7010155cdffa99aee296df140bdcae1332607 · prompt_hash sha256:24d5df68b416d3fda48a941db798708a2da7ce4340a17841f0569fe5f415a583
Input attestations: tree:worktree at HEAD 18f1a4e286fd85f6e94bb2ae1d2ecba40e13cc77 (branch main; history rewritten by the rename, so no per-spec commit range exists — the tree as shipped was audited, with `go test ./internal/app ./internal/gateway ./internal/runtime ./internal/config ./internal/capability ./internal/ui` all passing)@sha256:unknown; request:.abcd/.work.local/reviews/rcp-0b2669b3be31.request.md@sha256:6a02abe3596f48cdc97f89d082e7010155cdffa99aee296df140bdcae1332607; intent:.abcd/development/intents/shipped/itd-2609061441261073-configurable-resident-memory-budget-alice-sets-in-settings-h.md@sha256:24d5df68b416d3fda48a941db798708a2da7ce4340a17841f0569fe5f415a583;

Acceptance rollup: MET 5 · MET_WITH_CONCERNS 2 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: A fresh install writes no max_resident_bytes key (omitempty, asserted on the written file), and the control plane hands the panel a machine object whose budget is the resolved default with budget_is_default true, which the Settings hint renders in gigabytes with the share of this Mac beside it.
  evidence: internal/config/config.go:404 — "MaxResidentBytes int64 `json:"max_resident_bytes,omitempty"`"
  evidence: internal/config/config_test.go:692 — "func TestADefaultConfigStoresNoMemoryBudget(t *testing.T)"
  evidence: internal/gateway/control.go:235 — "st.Machine = Machine{"
  evidence: internal/ui/static/app.js:654 — "? ` (${Math.round((budget * 100) / m.total_ram)}% of this Mac's ${size(m.total_ram)})`"
  evidence: internal/app/budget_test.go:43 — "func TestAFreshInstallRunsOnTheDefaultShareOfTheMachine(t *testing.T)"
- ac-2 — MET: Pool.SetMemoryBudget replaces the ceiling under p.mu on a running pool, a runtime test raises it and acquires both a writer and a reviewer that a lower budget held one of, and the settings response omits the budget from its restart list so the change needs no restart.
  evidence: internal/runtime/pool.go:343 — "func (p *Pool) SetMemoryBudget(n int64) {"
  evidence: internal/runtime/pool_test.go:1290 — "func TestRaisingTheMemoryBudgetLetsTwoModelsCoResideWithoutANewPool(t *testing.T)"
  evidence: internal/gateway/control.go:851 — "restart := incoming.Port != current.Port ||"
  evidence: internal/gateway/budget_test.go:160 — "func TestSavingTheMemoryBudgetNeedsNoRestart(t *testing.T)"
  evidence: internal/app/budget_test.go:58 — "func TestSetConfigAppliesTheBudgetToThePoolWithoutARestart(t *testing.T)"
- ac-3 — MET_WITH_CONCERNS: The criterion's own case is delivered — a save carrying a budget above this Mac's memory is refused, the message names the machine's memory and config.json is byte-identical afterwards — but the guard also returns nil when `budget <= current`, so the refusal is scoped to a save that RAISES the budget above the machine; an already-in-force over-machine figure (a config.json carried from a larger Mac, or one written while sysctl could not read this one) is saved again rather than refused. Concern: the criterion as written covers every save carrying such a figure, and that class is not refused; the compensating control is enforcedBudget, which clamps what the pool may actually fill to physical memory, plus a start-up and panel warning. The narrowing is deliberate, argued in the code comment and held by tests, but it is a narrowing and the maintainer should adopt it as such.
  evidence: internal/app/app.go:445 — "if a.machineRAM <= 0 || budget <= a.machineRAM || budget <= current {"
  evidence: internal/app/app.go:449 — ""a memory budget of %s is more than this Mac has (%s) — models can only be held in the memory that exists""
  evidence: internal/app/budget_test.go:83 — "func TestSetConfigRefusesABudgetLargerThanTheMachine(t *testing.T)"
  evidence: internal/app/budget_test.go:262 — "func TestAnInheritedOverMachineBudgetDoesNotBlockAnUnrelatedSave(t *testing.T)"
  evidence: internal/app/app.go:419 — "func (a *App) enforcedBudget(stored int64) int64 {"
- ac-4 — MET_WITH_CONCERNS: For the criterion's stated setup — two pinned models that the budget in force holds — the save IS refused and the message names the sum, with config.json unchanged, because lowersUnderAFittingSet is true exactly there. The concern is that the refusal is conditional: checkPinnedFit only escalates the fit problem when the save adds a pin or lowers the budget under a set the budget in force could hold, so a pinned set that already does not fit (an inherited config.json, or a pin whose model this Mac cannot measure) gets a warning instead of a refusal when the budget is lowered further. A second, smaller caveat: the sum is computed over pinned DOWNLOADED models from the registry, not over what is resident, so the trigger is a superset of the criterion's wording rather than its literal precondition.
  evidence: internal/app/app.go:617 — "func (a *App) checkPinnedFit(incoming, current []string, budget, currentBudget int64) error {"
  evidence: internal/app/app.go:637 — "return budget < currentBudget && a.pinnedFitProblem(incoming, currentBudget) == nil"
  evidence: internal/app/app.go:653 — ""the pinned models need about %s of memory but the budget is %s — pin fewer models, or choose smaller quantizations""
  evidence: internal/app/budget_test.go:148 — "func TestSetConfigRefusesABudgetBelowThePinnedSum(t *testing.T)"
  evidence: internal/app/budget_test.go:344 — "func TestAnUnmeasurablePinDoesNotBlockALowerBudget(t *testing.T)"
- ac-5 — MET: SetMemoryBudget takes the lock, writes the figure and evicts nothing; a gateway test lowers the budget under two resident models through /api/settings and asserts both are still resident, machine.resident_bytes is the charged total and machine.over_budget is true, and the panel hint says so in the operator's words.
  evidence: internal/runtime/pool.go:343 — "func (p *Pool) SetMemoryBudget(n int64) {"
  evidence: internal/runtime/pool_test.go:1323 — "func TestLoweringTheMemoryBudgetUnloadsNothing(t *testing.T)"
  evidence: internal/gateway/budget_test.go:122 — "func TestLoweringTheBudgetLeavesTheModelsAndReportsOverBudget(t *testing.T)"
  evidence: internal/gateway/control.go:242 — "OverBudget: resident > budget,"
  evidence: internal/ui/static/app.js:658 — "The models in memory use ${size(resident)}, over the budget: a lower budget applies to the next load, and nothing is unloaded on your behalf."
- ac-6 — MET: With MachineRAM 0 the state carries total_ram 0 and warn_above 0, the panel hint takes the branch that states the budget alone with no percentage, and checkBudgetFitsTheMachine returns nil on its first clause so a 512 GB budget is accepted and reaches the pool unclamped.
  evidence: internal/app/app.go:445 — "if a.machineRAM <= 0 || budget <= a.machineRAM || budget <= current {"
  evidence: internal/app/budget_test.go:122 — "func TestAMachineOfUnknownSizeAcceptsAnyBudget(t *testing.T)"
  evidence: internal/gateway/budget_test.go:105 — "func TestStateReportsNoShareOfAMachineItCannotMeasure(t *testing.T)"
  evidence: internal/ui/static/app.js:655 — ": ', because this Mac\'s memory could not be read';"
  evidence: internal/ui/budget_test.go:77 — "func TestSettingsFormOmitsTheShareOfAMachineItCannotMeasure(t *testing.T)"
- ac-7 — MET: handleSearch calls capability.Assess with Pool.MemoryBudget() read on every search, so the filter and the pool share one number; a gateway test hides a model under a 32 MB budget, posts a 1 GB budget to /api/settings and sees the same model shown with no new server.
  evidence: internal/gateway/control.go:636 — "machine := capability.Assess(c.App.Paths.Models, c.App.MachineRAM(), c.App.Pool.MemoryBudget())"
  evidence: internal/gateway/control.go:669 — "if state == "" && !machine.Fits(sizes[i]) {"
  evidence: internal/gateway/budget_test.go:245 — "func TestSearchFollowsTheConfiguredBudget(t *testing.T)"
  evidence: internal/gateway/budget_test.go:335 — "func TestSearchAndStateAgreeAboutTheMachine(t *testing.T)"

Gap audit:
- honoured:
  - The budget gets a home in Settings: Alice types gigabytes, the value is stored in bytes, and the panel shows the figure back.
    evidence: internal/ui/static/index.html:208 — "< input id="setBudget" type="number" step="any" min="0" placeholder="the default share of this Mac's memory">"
    evidence: internal/ui/static/app.js:614 — "function budgetBytes(text) {"
    evidence: internal/ui/budget_test.go:27 — "func TestSettingsFormPostsTheBudgetInBytes(t *testing.T)"
  - A raised budget lets the writer and the reviewer sit in memory together without a restart.
    evidence: internal/runtime/pool_test.go:1290 — "func TestRaisingTheMemoryBudgetLetsTwoModelsCoResideWithoutANewPool(t *testing.T)"
    evidence: internal/gateway/control.go:851 — "restart := incoming.Port != current.Port ||"
  - Nothing is pulled out from under the model Bob is using; the panel simply says he is over his new figure until the models settle.
    evidence: internal/runtime/pool.go:343 — "func (p *Pool) SetMemoryBudget(n int64) {"
    evidence: internal/gateway/budget_test.go:122 — "func TestLoweringTheBudgetLeavesTheModelsAndReportsOverBudget(t *testing.T)"
  - One machine reading, shared by the pool, the search filter and the app, injectable for tests.
    evidence: internal/capability/capability_darwin.go:64 — "func PhysicalMemory() int64 {"
    evidence: internal/runtime/sysmem_darwin.go:11 — "return capability.DefaultBudget(capability.PhysicalMemory())"
    evidence: internal/app/app.go:386 — "func (a *App) MachineRAM() int64 { return a.machineRAM }"
  - The search tab follows the configured budget live, with no second answer to what fits.
    evidence: internal/gateway/control.go:636 — "machine := capability.Assess(c.App.Paths.Models, c.App.MachineRAM(), c.App.Pool.MemoryBudget())"
  - A budget claiming most of the Mac draws a warning, not a refusal, and the docs say why (the charge excludes the KV cache).
    evidence: internal/app/app.go:491 — ""The memory budget (%s) is most of this Mac's memory (%s). macOS and everything else running share it, and a model is charged what it loads rather than what a long conversation adds to it, so requests can still run the machine out of memory.""
    evidence: internal/gateway/control.go:863 — "if warn := c.App.MemoryBudgetWarning(); warn != "" {"
    evidence: docs/getting-started.md:77 — "[Why there is a memory budget] (memory-budget-explained.md) for what the figure"
- diverged:
  - ac-3 as written: a budget larger than the machine's physical memory is refused on save. Delivered: only a save that RAISES the budget above this Mac is refused; a figure already in force above it is saved again, warned about, and held down at enforcement time instead. I reach this independently and agree with the intent's Audit Note that the divergence is real and deliberate — but I score it as a narrowing (MET_WITH_CONCERNS), not as a criterion left undelivered, because the operator-caused case is refused, names the machine's memory and leaves config.json byte-identical.
    evidence: internal/app/app.go:445 — "if a.machineRAM <= 0 || budget <= a.machineRAM || budget <= current {"
    evidence: internal/app/budget_test.go:262 — "func TestAnInheritedOverMachineBudgetDoesNotBlockAnUnrelatedSave(t *testing.T)"
  - ac-4 as written: a save that lowers the budget below the pinned sum is refused unconditionally. Delivered: refused only when the budget in force could hold the set (lowersUnderAFittingSet) or the save adds a pin. I agree with the intent's Audit Note that this is a narrowing — but note it lies outside the criterion's own precondition: with two pinned models the budget in force holds, the refusal fires exactly as written.
    evidence: internal/app/app.go:637 — "return budget < currentBudget && a.pinnedFitProblem(incoming, currentBudget) == nil"
    evidence: internal/app/budget_test.go:325 — "func TestARoundedBudgetDoesNotTurnAWarningIntoARefusal(t *testing.T)"
  - Beyond the record: the pool is clamped to physical memory at enforcement time (enforcedBudget), an enforcement neither the press release nor any acceptance criterion promises. It is the compensating control for the ac-3 narrowing and is defended against a planted figure under a shared install, but it means the stored figure and the enforced figure can differ, which the Settings copy does not state.
    evidence: internal/app/app.go:419 — "func (a *App) enforcedBudget(stored int64) int64 {"
    evidence: internal/app/budget_test.go:373 — "func TestAPlantedBudgetIsEnforcedNoHigherThanTheMachine(t *testing.T)"
  - config.Validate stays machine-independent but a negative budget is dropped-and-defaulted by sanitizeBudget at load as well as refused by Validate at save, which is a second disposal rule the spec did not describe.
    evidence: internal/config/config.go:645 — "func (c *Config) sanitizeBudget() []string {"
    evidence: internal/config/config.go:876 — "if c.MaxResidentBytes < 0 {"
- missing:
  - The press release promises Alice sees "what each of her downloaded models would cost against it", and the spec restates it as "Settings shows each downloaded model's charged size and the current resident total". The resident total is shown; the per-model charged size is not. The pin list renders the model id alone, and the only charge figure is the aggregate for the ticked set.
    evidence: internal/ui/static/app.js:520 — "function renderPinSwitches() {"
    evidence: internal/ui/static/app.js:696 — "function updatePinBudget() {"
    evidence: internal/ui/static/app.js:601 — "function pinnedCharge(models, pinned) {"

Scope-condition dispositions:
- cond-2609061822382119 — survived: The reading is a Darwin-only sysctl on hw.memsize behind one exported PhysicalMemory that the pool, the search filter and the app all read, and a Mac that does not report its memory is handled by an explicit conservative default rather than contradicting the assumption.
  evidence: internal/capability/capability_darwin.go:64 — "func PhysicalMemory() int64 {"
  evidence: internal/capability/capability_darwin.go:22 — "const unmeasuredBudget = 8 << 30"
  evidence: internal/runtime/sysmem_darwin.go:11 — "return capability.DefaultBudget(capability.PhysicalMemory())"
- cond-2609061822387031 — survived: LoadCost remains the single accounting rule — 1.2x the size on disk — and every surface charges through it: the pool's admission and eviction, the control plane's resident total, the pinned sum, and the panel's own arithmetic; nothing added a second rule and the KV-cache exclusion is stated as advice rather than accounted for.
  evidence: internal/runtime/pool.go:1449 — "return diskBytes + diskBytes/5 // 1.2x"
  evidence: internal/gateway/control.go:269 — "func residentCharge(resident []runtime.Resident) int64 {"
  evidence: internal/ui/static/app.js:601 — "function pinnedCharge(models, pinned) {"
- cond-2609061822380027 — narrowed: The high-budget warning is indeed advice — an 85% threshold that returns a string and is carried alongside a successful save — but the delivery does not leave the whole range to the operator's knowledge of their Mac: above physical memory a raise is refused outright and the pool is clamped regardless of what is stored.
  narrowing: The assumption now holds only up to this Mac's physical memory: within it the operator's judgement governs and the 85% notice is advice, while above it a raising save is refused and enforcedBudget holds what the pool may fill down to what exists.
  evidence: internal/app/app.go:453 — "const warnAbovePercent = 85"
  evidence: internal/app/app.go:478 — "func (a *App) MemoryBudgetWarning() string {"
  evidence: internal/app/app.go:444 — "func (a *App) checkBudgetFitsTheMachine(budget, current int64) error {"
  evidence: internal/app/app.go:419 — "func (a *App) enforcedBudget(stored int64) int64 {"
- cond-2609061822380142 — survived: The shared-settings half is exercised: the budget lives in the single-writer config.json and applies to whichever account runs the server, and the delivery reasons explicitly about a figure another local account could plant there, with a test holding the clamp that answers it. The 'one server per machine' half is consistent with the delivery but nothing exercises a second server, so that clause rests on the assumption rather than on evidence.
  evidence: internal/app/app.go:411 — "(the shared install) would have the pool admitting every model a client names"
  evidence: internal/app/budget_test.go:373 — "func TestAPlantedBudgetIsEnforcedNoHigherThanTheMachine(t *testing.T)"
  evidence: internal/gateway/control.go:283 — "operator's, and the machine is the boundary (adr-2609061503319212). The"
## Grounds

- pursued: we expect a shared Mac to serve several agents without their models evicting each other once the operator can pin, budget and grace, and once keyed clients can see what is warm; we are wrong if model swaps stay as frequent with those controls set as they were without them, measured by the statistics store
