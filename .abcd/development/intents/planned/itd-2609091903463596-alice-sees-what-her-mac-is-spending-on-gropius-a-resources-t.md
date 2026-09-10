---
id: itd-2609091903463596
slug: alice-sees-what-her-mac-is-spending-on-gropius-a-resources-t
spec_id: spc-2609100450257929
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609061431481936, itd-2609061441261073, itd-2609061441228998]
severity: minor
impact: additive
origin: researcher-authored
production_mode: dictated-and-formatted
---

# Alice sees what her Mac is spending on Gropius. A Resources tab in the control panel shows how many models are downloaded and how many are loaded, how much disk the models take and how much the volume has left, the memory budget against what is resident and what is still exiting, and each model's declared and served context window. The figures are the ones Gropius already knows, shown in one place; nothing in it is a request or a prompt.

## Press Release

Alice's Mac is nearly full and she cannot tell what Gropius is holding. She
opens the Models tab and the first thing it shows is the roll-up: how many
models are downloaded and how many are loaded, how much disk they take and how
much the volume has left, and the memory budget against what is resident and
what is still on its way out. Each model's card says the window it declares
and the window it is set to serve.

## Why This Matters

The figures exist but are scattered: per-model sizes and windows on the cards,
the budget against resident bytes in Settings, RAM and free disk on the search
tab. Nothing adds them up, and the question a full Mac asks first has no
single answer. The posture page (itd-2609081718534201) says what is on; this
says what it costs; the measurement intent (itd-2609091712141073) says what
requests did. Placement decided at interview on 2026-09-09: a block at the
head of the Models tab, not a seventh tab.

## Mechanism

We expect no new measurement to be needed, because every figure but the
volume's free space is already on the state snapshot the panel polls, and free
space has one reader in the capability package. Shown wrong if the roll-up
needs a directory walk or a second reader.

## Scope Conditions

- Apple Silicon macOS. <!-- cond: cond-2609100450250935 -->
- The panel is loopback-only, so the figures are the operator's. <!-- cond: cond-2609100450255120 -->
- Shared-cache mode reports every local account's models. <!-- cond: cond-2609100450250643 -->
- Figures are read at the snapshot's cadence and are not live usage; that is the measurement intent. <!-- cond: cond-2609100450254408 -->

## Acceptance Criteria

- Given three ready models and one resident, when the Models tab renders, then the roll-up shows 3 downloaded and 1 loaded.
- Given models with recorded sizes, when the disk total is shown, then it equals the sum of those sizes with no directory walk on the render path.
- Given the models volume has space free, when the roll-up renders, then the figure comes from the capability package's one reader and is shown.
- Given a server still exiting, when resident memory is shown, then it includes the exiting bytes and names that part.
- Given a model with a served window below its declared one, when its card renders, then both windows are shown and the served one is resolved the fold-aware way.
- Given a model without a declared window, when its card renders, then no window is shown, never zero.
- Given a request from off loopback, when it asks for the panel, then the block is unreachable.

## Open Questions

- Resolved 2026-09-09 at interview: a block at the head of the Models tab rather than a new tab; activity stays with the usage dashboard and the measurement intent.

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._

## Grounds

- pursued: we expect the roll-up to answer the question people ask first when a Mac fills up, without a new tab; shown wrong if operators still open Finder to find out what Gropius holds.
