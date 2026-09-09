---
id: spc-2609091431038544
slug: effective-context-managed-per-model-gropius-serves-each-mode
intent: itd-2609061431481936
origin: researcher-authored
production_mode: hand-written
---
# Effective context managed per model

## Summary

A model is served, charged and reported at one window: `served_context`, a
per-model setting that defaults to the window the model's own configuration
declares. The memory budget charges that window; the gateway refuses a request
whose estimated size exceeds it; the models list publishes it beside the
declared cap; `config.json` and the settings pane carry it.

This replaces the ceiling the KV-cache charge shipped with, in which a model
whose honest charge exceeded the whole budget was quietly charged the budget
and loaded alone. That ceiling was a figure the machine did not support: it let
the pool believe a 262,144-token model cost what the budget happened to be. A
model whose charge at its served window does not fit is now refused, and the
refusal says what would fit.

## Scope

In scope: the served-context setting and its three surfaces (Go, `config.json`,
the panel); the charge; the gateway's size check; the models list; the docs.

Out of scope: measuring a model's real window on this Mac (that is
itd-2609091301112705, drafted); a dynamic window that moves with what else is
resident (scope condition cond-2609091431030549 fixes the window as static);
tokenising on the gateway (open question resolved at interview in favour of a
byte estimate).

## Approach

**One home for the window.** `config.Config.ServedContext(repoID, declared)`
returns the operator's figure for that model, or `declared` when they have set
none. Every surface reads it: the pool through the app's model source, the
gateway before it acquires, the models list, the panel.

**The setting.** `ModelSettings.ServedContext` (`served_context`), a token
count. Validation is the config package's own: positive, and no larger than the
ceiling the registry applies to a declared window — one constant, moved to
`config.MaxContextLength` and re-exported by the registry so both packages
bound a window by the same figure. A save that does not name the field leaves
it alone, as every other per-model field is left alone.

**The charge.** `capability.LoadCostOf` loses its `Budget` field and its
ceiling. A model's charge is its weights, a fifth of them for the working set,
and `factor x kvBytesPerToken x servedContext x sequences`. The factor is 5 for
a model that caches keys and values per head and 7 for one that caches a
compressed latent: the campaign's measured slopes are 2.0, 3.1 and 4.8 times
the configuration figure for the three grouped-query models and 6.7 times for
the latent one, so one factor for both over-charges the first three by half
again as much as their evidence supports.

**The refusal.** `startLocked` refuses a model whose charge exceeds the budget,
naming the served window and the decode concurrency, and the largest window and
the concurrency that would fit, so the operator can act without arithmetic. No
silent cap: the pool never charges a model less than it will cost.

**The gateway.** Before acquiring — a request that cannot be served must not
take a model slot or a load — the handler estimates the request's size from the
encoded body at the same four bytes per token `prefillBudget` estimates with
(one function, `estimatedTokens`, used by both), adds the request's
`max_tokens`, and refuses above the served window with 400 in the OpenAI error
shape, naming the window and the estimate. Streaming and non-streaming take the
same line, because the check runs before either path is chosen.

Clarification (2026-09-09, from the review of the shipping change): the intent's
scope conditions call the model server's own rejection the backstop for what
gets through. It is not one — mlx-lm was measured accepting an abandoned 256K
prompt until the machine swapped — so the residual is carried by the estimate's
direction and the charge's margin instead: the estimate over-counts on English
text, under-counts by about a third on densely packed CJK, the body is capped at
32 MiB, and the budget charges five to seven times the cache a configuration
implies, which covers that third. Both figures the client supplies are added
saturating, so an absurd `max_tokens` cannot make the estimate negative, and a
JSON float is read as the number it is.

**The list and the panel.** `GET /v1/models` carries `served_context` beside
`context_length`. The panel's `modelCharge` drops the ceiling and reads the
served window from the config it already holds, and the settings pane gains one
number field per downloaded model, blank meaning the declared cap.

## How each acceptance criterion is tested

| Criterion | Test |
| --- | --- |
| A prompt plus `max_tokens` estimated above the window is refused with a 400 in the OpenAI error shape naming the window and the estimate, streaming and non-streaming | `internal/gateway`: `TestAPromptOverTheServedContextIsRefused` (both `stream` values, asserts status, `error.type`, and that the message names both figures) and `TestAPromptOverTheServedContextNeverReachesThePool` (the fake pool records no acquire) |
| No window set: the declared cap is served and charged | `internal/config`: `TestServedContextFallsBackToTheDeclaredWindow`; `internal/gateway`: the same refusal test with no setting, refused at the declared cap |
| With a window set, the charge uses it and the panel shows it | `internal/runtime`: `TestTheChargeUsesTheServedWindow`; `internal/ui`: `TestSettingsFormChargesTheServedContext` |
| The models list carries the served window beside the declared cap | `internal/gateway`: `TestModelsListCarriesTheServedContext` |
| Changing it in `config.json` or the pane charges the new figure at the next load, and a save never refuses an untouched field | `internal/config`: `TestServedContextIsValidated`; `internal/app`: `TestSavingAnUnrelatedSettingKeepsTheServedContext`; `internal/ui`: `TestSettingsFormPostsTheServedContext` |
| The memory-budget page explains the window and the charge in words | `docs/memory-budget-explained.md`, held by the existing docs currency gates |

A model whose charge does not fit the budget is refused with a message naming
what would fit: `internal/runtime`,
`TestAModelWhoseServedWindowDoesNotFitIsRefusedWithWhatWouldFit`.

## Grounds

- pursued: one window, set per model and defaulting to the declared cap, is
  enough for an operator to make a long-context model fit a Mac that will not
  hold it at its declared window — and the machine stays off swap because the
  charge and the gateway agree on the same figure. Shown wrong if a prompt
  inside the served window still pushes the process past its charge, or if
  operators leave every window at the default and find the same models refused
  that the flat charge admitted.
