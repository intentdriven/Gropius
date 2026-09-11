---
id: itd-2609061441241254
slug: pinned-models-alice-marks-models-in-settings-that-stay-resid
spec_id: spc-2609061822370978
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Pinned models: Alice marks models in Settings that stay resident no matter what else is requested; a request that would need to evict a pinned model is refused with the existing busy error instead

## Press Release

Alice runs two models all day: one writes, one reviews. She ticks both in
Settings, and from then on nothing Bob or Carol requests can push them out of
memory. When Bob asks for a third model that would only fit by dropping one of
Alice's, his client is told straight away that the Mac has no room for it,
rather than Alice's workhorse vanishing mid-task and coming back minutes later.
Bob's refusal says nothing about what Alice is running. Alice can see which
models she has protected, and how much of the memory budget they leave for
everyone else, on the Settings page where she set them.

## Why This Matters

Preload brings a model up at startup but does not protect it: the
least-recently-used rule evicts any idle model the moment a different one is
requested and does not fit. Agentic clients are idle between turns, so a shared
machine evicts its most important models most often. The 2026-09-05 model-bench
lab named a keep-warm option the most valuable feature request for Gropius and
built a queueing proxy to approximate it. Pinning is the smaller change. Its
docs change also settles iss-2609061443332414 by stating the difference between
preloading a model and protecting one.

Evidence: research note 2026-09-06-model-bench-evidence (the writer-and-reviewer loop: two models alternating, swaps at 7.7 s and 38 s, the reviewer's generation as the bottleneck; the lab's stated top request was a keep-warm option).

## Mechanism

We expect a pinned model to survive every competing request because the pool
picks eviction victims only from the models its skip rule admits, and a pin is
one more clause in that rule; we are wrong if a pinned set leaves so little
room that the machine refuses more work than eviction used to cost it.

## Scope Conditions

- Shared Macs serving more than one client, where the operator knows which one <!-- cond: cond-2609061822373930 -->
  or two models matter most.
- Models that have been loaded; the pinned list is separate from the preload <!-- cond: cond-2609061822373978 -->
  list, so a pinned model nobody has loaded is protected only once something
  loads it.
- Pinned sets whose charged sizes fit within the memory budget together; a <!-- cond: cond-2609061822378129 -->
  settings save that breaks that is refused.
- The pool's single process per model: a crash of a model's own server, an <!-- cond: cond-2609061822373660 -->
  operator's own unload, and shutdown all still remove a pinned model from
  memory.

## Acceptance Criteria

- Given two pinned models are resident and the memory budget cannot also hold
  a third, when a client requests the third model, then the request is refused
  at once, both pinned models stay resident, and the refusal names no model.
- Given one pinned and one unpinned model are resident and both idle, when a
  third model is requested and freeing the unpinned one makes room, then the
  unpinned model is unloaded and the pinned one stays, even though the pinned
  one was used more recently.
- Given an idle timeout is set, when a pinned model has been idle for longer
  than that timeout, then it stays resident while an unpinned idle model is
  unloaded.
- Given a resident model that is not pinned, when Alice pins it in Settings,
  then it is protected from the next eviction without Gropius being restarted.
- Given a settings save whose pinned models' charged sizes together exceed the
  memory budget, when Alice submits it, then the save is refused with a message
  giving that sum and the budget, and the stored settings are unchanged.
- Given a pinned model written with different letter case from the one the
  registry holds, when a competing request would need its memory, then the
  model is still protected.
- Given a pinned model is resident, when the operator unloads it from the
  control panel, then it is unloaded and it remains pinned.

## Open Questions

- Resolved: pinned models are a separate list and preload is unchanged, so
  pinning adds a setting rather than altering an existing one.
- Resolved: pinned models ignore the idle timeout, and Settings says so beside
  that field.
- Resolved: the fit check runs when Settings is saved — the pinned models'
  charged sizes must sum to no more than the memory budget — so an impossible
  pin set is refused at pin time rather than discovered at request time.
  itd-2609061441261073 cites this invariant rather than stating a second one.
- Resolved: pins apply as soon as Settings is saved; no restart is needed.
- Resolved: pins are matched by the registry's canonical model id, so a
  hand-edited settings file with different letter case still protects the model
  the operator meant.
- Resolved: the message a network client receives says only that there is not
  enough memory; the protected models are named in the machine's own log.
- Resolved: pinning lives in Settings for the first cut; the control panel
  marks pinned models on their cards without offering a second place to change
  them.
- Resolved: an operator's own unload succeeds and leaves the pin in place;
  deleting a model leaves its pin behind rather than writing the settings file
  from a second code path.
- Resolved: a pinned model whose server exits is not reloaded automatically;
  the control panel shows it as pinned and not loaded until something loads it
  again.
- Depends on: itd-2609061441261073, the configurable memory budget, which makes
  pinning practical on machines where the preferred pair does not fit the
  default budget.

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-4d0d60cfdaf8 -->
Fidelity review — receipt rcp-4d0d60cfdaf8 (verifier intent-auditor claude-opus-5[1m]).

Provenance: intent-auditor@claude-opus-5[1m] · rubric_hash sha256:57a9a4dafcfb7e3a6e61d23893c42057dade6b57ac50c5f85f814e12c90ba870 · prompt_hash sha256:00d6261d9ea18e4d23de5d0dd6d56551764776d41f26f6a98ff985cac8ab5862
Input attestations: tree:HEAD 18f1a4e286fd85f6e94bb2ae1d2ecba40e13cc77 (history rewritten during a rename; no per-spec commit range exists, so the tree as shipped was audited)@sha256:5f3256dd3718af34e459add3c3c30b5d631e90af; file:internal/runtime/pool.go@sha256:8a2ce5fba471de244f5bd73d3b8d7a57920ac2aff6420e014fffbb2d4dcaa86e; file:internal/app/app.go@sha256:bc2e5aa54bdbfb5f5397a492217ddfdf5dbe4162faaa253cea4a6bdee4c9f824; request:.abcd/.work.local/reviews/rcp-4d0d60cfdaf8.request.md@sha256:00d6261d9ea18e4d23de5d0dd6d56551764776d41f26f6a98ff985cac8ab5862;

Acceptance rollup: MET 6 · MET_WITH_CONCERNS 1 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: Pinned entries are dropped from the eviction candidate set, canEverFitLocked refuses immediately rather than queueing when the pinned set plus the new load cannot fit, and the wire error names no model; the resident pinned pair is asserted intact after the refusal.
  evidence: internal/runtime/pool.go:992 — "if p.isPinnedLocked(e.repoID) {"
  evidence: internal/runtime/pool.go:1093 — "return waitNeverFits"
  evidence: internal/runtime/pool.go:1136 — "if p.isPinnedLocked(e.repoID) {"
  evidence: internal/runtime/pool.go:1408 — ""not enough memory to load another model, and no model in memory can be freed (limit %s)""
  evidence: internal/runtime/pool.go:1391 — "Protected []string"
  evidence: internal/runtime/pool_test.go:989 — "func TestPinnedModelsAreNeverEvictedAndTheRefusalNamesNoModel(t *testing.T) {"
  evidence: internal/gateway/gateway_test.go:1886 — "func TestRefusalToANetworkClientNamesNoPinnedModel(t *testing.T) {"
- ac-2 — MET: The pin removes the entry from the candidate list before the least-recently-used sort runs, so the unpinned idle model is the only victim; the shipped test exercises the strictly harder case (the pinned model is the LEAST recently used and still survives), which entails the criterion's weaker most-recently-used case.
  evidence: internal/runtime/pool.go:992 — "if p.isPinnedLocked(e.repoID) {"
  evidence: internal/runtime/pool.go:998 — "sort.Slice(candidates, func(i, j int) bool {"
  evidence: internal/runtime/pool_test.go:1025 — "func TestEvictionSkipsThePinnedModelEvenWhenItIsTheLeastRecentlyUsed(t *testing.T) {"
- ac-3 — MET: The idle reaper's unload condition carries an explicit not-pinned clause, so a pinned model past the timeout is skipped while the unpinned idle one is reaped; the test advances a fake clock an hour and asserts exactly that split.
  evidence: internal/runtime/pool.go:1329 — "if e.inFlight == 0 && isReady(e) && !p.isPinnedLocked(e.repoID) &&"
  evidence: internal/runtime/pool.go:1331 — "p.stopEntryLocked(e, StopIdle)"
  evidence: internal/runtime/pool_test.go:1060 — "func TestPinnedModelIgnoresTheIdleTimeout(t *testing.T) {"
- ac-4 — MET: Settings renders a checkbox per model and posts the whole pinned list; SetConfig hands it to the live pool via SetPinned under the same lock both eviction paths read, with no restart involved, and tests assert the pool holds the new pin immediately and drops it again when cleared.
  evidence: internal/app/app.go:320 — "a.Pool.SetPinned(c.Pinned)"
  evidence: internal/runtime/pool.go:319 — "func (p *Pool) SetPinned(ids []string) {"
  evidence: internal/ui/static/index.html:363 — "< legend>Pinned models< /legend>"
  evidence: internal/ui/static/app.js:967 — "pinned: pinnedModels(state.pinned, listedPinModels(), checkedPinModels()),"
  evidence: internal/app/app_test.go:974 — "func TestSetConfigAppliesPinsToThePoolWithoutARestart(t *testing.T) {"
  evidence: internal/gateway/control_test.go:447 — "func TestSavingPinnedModelsNeedsNoRestart(t *testing.T) {"
  evidence: internal/runtime/pool_test.go:1099 — "func TestSetPinnedProtectsAModelWithoutANewPool(t *testing.T) {"
- ac-5 — MET_WITH_CONCERNS: A save that pins an over-budget set is refused before config.Save runs, with a message carrying both the pinned sum and the budget, and the test asserts config.json, the live config and the pool are all untouched — but the refusal is gated on the save making the fit worse, so the criterion holds narrower than written.
  evidence: internal/app/app.go:299 — "if err := a.checkPinnedFit(c.Pinned, a.Config().Pinned, budget, a.Pool.MemoryBudget()); err != nil {"
  evidence: internal/app/app.go:302 — "if err := config.Save(a.Paths.Config, c); err != nil {"
  evidence: internal/app/app.go:653 — ""the pinned models need about %s of memory but the budget is %s — pin fewer models, or choose smaller quantizations""
  evidence: internal/app/app.go:622 — "if addsAPin(incoming, current) || a.lowersUnderAFittingSet(incoming, budget, currentBudget) {"
  evidence: internal/app/app.go:625 — "a.Log.Warn("the pinned models cannot all be kept in memory as configured", "err", problem)"
  evidence: internal/app/app_test.go:888 — "func TestSetConfigRefusesPinsThatDoNotFitTheMemoryBudget(t *testing.T) {"
  evidence: internal/app/app_test.go:1151 — "func TestAnInheritedOverBudgetPinnedSetDoesNotBlockAnUnrelatedSave(t *testing.T) {"
  evidence: internal/app/app.go:762 — "func (a *App) PinnedFitWarning() string {"
- ac-6 — MET: The pool keys its protected set on config.FoldRepoID, so a pin written in another letter case still matches at the eviction check; the live test pins "ORG/Keep" against a resident "org/keep" and a competing request for a third model evicts the unpinned spare instead.
  evidence: internal/runtime/pool.go:482 — "func pinnedSet(ids []string) map[string]string {"
  evidence: internal/runtime/pool.go:492 — "_, ok := p.pinned[config.FoldRepoID(repoID)]"
  evidence: internal/runtime/pool_test.go:1099 — "p.SetPinned([]string{"ORG/Keep"})"
  evidence: internal/app/app.go:173 — "a.cfg.Pinned = a.adoptPinned(a.cfg.Pinned)"
  evidence: internal/app/app_test.go:954 — "func TestSetConfigCanonicalizesPinnedModelIDs(t *testing.T) {"
- ac-7 — MET: Pool.Unload carries no pin guard and stops the entry outright, and the pin lives in the settings rather than on the entry, so it survives the unload; the control-panel POST /api/models/unload path is tested end to end and asserts the model is gone while Config().Pinned still names it.
  evidence: internal/runtime/pool.go:1257 — "func (p *Pool) Unload(repoID string) error {"
  evidence: internal/runtime/pool.go:1269 — "p.stopEntryLocked(e, StopUnloaded)"
  evidence: internal/gateway/control.go:62 — "mux.HandleFunc("POST /api/models/unload", c.handleUnload)"
  evidence: internal/runtime/pool_test.go:1132 — "func TestUnloadSucceedsOnAPinnedModel(t *testing.T) {"
  evidence: internal/gateway/sampling_wiring_test.go:223 — "func TestUnloadingAPinnedModelLeavesThePinInPlace(t *testing.T) {"

Gap audit:
- honoured:
  - Nothing another client requests can push a pinned model out of memory; the third model's request is refused instead.
    evidence: internal/runtime/pool.go:992 — "if p.isPinnedLocked(e.repoID) {"
    evidence: internal/runtime/pool_test.go:989 — "func TestPinnedModelsAreNeverEvictedAndTheRefusalNamesNoModel(t *testing.T) {"
  - The refusing client is told straight away rather than held: a load the pinned set can never leave room for skips the eviction-grace queue entirely.
    evidence: internal/runtime/pool.go:1093 — "return waitNeverFits"
    evidence: internal/runtime/grace_test.go:382 — "func TestPinningTheOnlyCandidateReleasesAWaitingRequestAtOnce(t *testing.T) {"
  - Bob's refusal says nothing about what Alice is running: the protected names go to the machine's own log, not onto the wire.
    evidence: internal/runtime/pool.go:1391 — "Protected []string"
    evidence: internal/runtime/pool.go:1406 — "func (e *NoRoomError) Error() string {"
    evidence: internal/gateway/gateway_test.go:1886 — "func TestRefusalToANetworkClientNamesNoPinnedModel(t *testing.T) {"
  - The pin is one more clause in the pool's skip rule, covering both eviction paths — the victim search and the idle reaper.
    evidence: internal/runtime/pool.go:992 — "if p.isPinnedLocked(e.repoID) {"
    evidence: internal/runtime/pool.go:1329 — "!p.isPinnedLocked(e.repoID) &&"
  - Alice can see which models she has protected, and how much of the memory budget they leave for everyone else, on the Settings page where she set them.
    evidence: internal/ui/static/index.html:363 — "< legend>Pinned models< /legend>"
    evidence: internal/ui/static/index.html:370 — "< p id="pinBudget" class="hint">< /p>"
    evidence: internal/ui/settings_test.go:153 — "func TestSettingsFormChargesPinnedModelsWhatThePoolCharges(t *testing.T) {"
    evidence: internal/ui/panel_test.go:226 — "func TestCardPillSaysWhetherAPinnedModelIsLoaded(t *testing.T) {"
  - The docs change states the difference between preloading a model and protecting one.
    evidence: docs/pinning-models.md:34 — "**Preload** — the card reads `pinned, not loaded`."
    evidence: internal/config/config.go:384 — "// Separate from Preload, and the two do different things."
- diverged:
  - "A settings save whose pinned models' charged sizes exceed the memory budget is refused" — as shipped, only a save that makes the fit worse is refused; a save that leaves an inherited over-budget pinned set untouched is accepted and merely warned about, on the control panel and in the log.
    evidence: internal/app/app.go:622 — "if addsAPin(incoming, current) || a.lowersUnderAFittingSet(incoming, budget, currentBudget) {"
    evidence: internal/app/app.go:625 — "a.Log.Warn("the pinned models cannot all be kept in memory as configured", "err", problem)"
    evidence: internal/app/app.go:762 — "func (a *App) PinnedFitWarning() string {"
    evidence: internal/app/app_test.go:1151 — "func TestAnInheritedOverBudgetPinnedSetDoesNotBlockAnUnrelatedSave(t *testing.T) {"
  - Acceptance criterion 2 is weaker than what shipped: it asks only that a MORE recently used pinned model survive, which plain least-recently-used eviction would grant with no pin at all. The delivery holds the stronger case (pinned model is the LEAST recently used and still survives). A divergence in the delivery's favour, not a shortfall.
    evidence: internal/runtime/pool.go:992 — "if p.isPinnedLocked(e.repoID) {"
    evidence: internal/runtime/pool_test.go:1025 — "func TestEvictionSkipsThePinnedModelEvenWhenItIsTheLeastRecentlyUsed(t *testing.T) {"
- missing: (none)

Scope-condition dispositions:
- cond-2609061822373930 — survived: The delivery is built for the shared-Mac, several-clients case the condition assumes: pinning is an operator-only Settings control bounded to a small set, and the refusal other clients receive discloses nothing about the operator's choice.
  evidence: internal/config/config.go:572 — "const MaxPinned = MaxPerModel"
  evidence: internal/runtime/pool.go:1391 — "Protected []string"
  evidence: internal/gateway/gateway_test.go:1886 — "func TestRefusalToANetworkClientNamesNoPinnedModel(t *testing.T) {"
  evidence: internal/ui/static/index.html:363 — "< legend>Pinned models< /legend>"
- cond-2609061822373978 — survived: Pinned is a field of its own beside Preload and neither loads anything; the pool's pinned set is independent of what is resident, and the panel says so on the card of a model that is pinned but not loaded.
  evidence: internal/config/config.go:389 — "Pinned []string `json:"pinned,omitempty"`"
  evidence: internal/config/config.go:384 — "// Separate from Preload, and the two do different things."
  evidence: internal/runtime/pool.go:469 — "func (p *Pool) Pinned() []string {"
  evidence: internal/ui/static/app.js:31 — "return loaded ? 'pinned' : 'pinned, not loaded';"
  evidence: internal/ui/panel_test.go:226 — "func TestCardPillSaysWhetherAPinnedModelIsLoaded(t *testing.T) {"
- cond-2609061822378129 — narrowed: The refusal the condition assumes exists, but only for the save that causes the misfit; a pinned set that already did not fit when it was read is applied, warned about, and left in force.
  narrowing: The refusal holds only for a save that adds a pin, or that lowers the memory budget under a set the budget in force could hold. A stored set that no longer fits — a config.json carried to a smaller Mac, or a RAM detection that fell back to its default — is applied with a warning on the control panel and in the log so that an unrelated setting can still be saved, and a set that fits neither the incoming nor the current budget is treated as inherited rather than caused.
  evidence: internal/app/app.go:622 — "if addsAPin(incoming, current) || a.lowersUnderAFittingSet(incoming, budget, currentBudget) {"
  evidence: internal/app/app.go:637 — "return budget < currentBudget && a.pinnedFitProblem(incoming, currentBudget) == nil"
  evidence: internal/app/app.go:218 — "_ = a.checkPinnedFit(a.cfg.Pinned, a.cfg.Pinned, budget, budget)"
  evidence: internal/app/app_test.go:1151 — "func TestAnInheritedOverBudgetPinnedSetDoesNotBlockAnUnrelatedSave(t *testing.T) {"
  evidence: internal/app/app_test.go:1340 — "func TestAPinnedSetThatStopsFittingIsReportedToTheOperator(t *testing.T) {"
- cond-2609061822373660 — survived: All three exits the condition names still remove a pinned model: watchExit deletes an entry whose server process died with no pin check, Unload stops it outright, and Close stops every model server; only competing clients are held off.
  evidence: internal/runtime/pool.go:878 — "func (p *Pool) watchExit(e *entry) {"
  evidence: internal/runtime/pool.go:883 — "p.notify(func(o PoolObserver) { o.EntryStopped(e.repoID, StopCrashed) })"
  evidence: internal/runtime/pool.go:1269 — "p.stopEntryLocked(e, StopUnloaded)"
  evidence: internal/runtime/pool.go:1350 — "p.notify(func(o PoolObserver) { o.EntryStopped(e.repoID, StopShutdown) })"
  evidence: internal/runtime/pool_test.go:1132 — "func TestUnloadSucceedsOnAPinnedModel(t *testing.T) {"
## Grounds

- pursued: we expect a shared Mac to serve several agents without their models evicting each other once the operator can pin, budget and grace, and once keyed clients can see what is warm; we are wrong if model swaps stay as frequent with those controls set as they were without them, measured by the statistics store
