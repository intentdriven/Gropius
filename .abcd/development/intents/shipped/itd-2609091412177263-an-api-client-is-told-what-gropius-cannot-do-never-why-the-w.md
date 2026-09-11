---
id: itd-2609091412177263
slug: an-api-client-is-told-what-gropius-cannot-do-never-why-the-w
spec_id: spc-2609091703459471
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609081015545349]
severity: minor
impact: additive
origin: researcher-authored
production_mode: dictated-and-formatted
---

# An API client is told what Gropius cannot do, never why; the why goes to the server's own log. When Bob's client asks for a model Gropius cannot load or does not have, the answer says so plainly: 'cannot load model X', 'model X not available'. The reason stays on the server, in a log that lives in the account running Gropius and is never shared with another account. The log is sparse by default and Alice can switch it to detailed when she is diagnosing something. A client on this Mac, or one holding the API key, keeps today's informative refusal.

## Press Release

Bob's coding harness asks for a model this Mac cannot load. The answer is
plain: "cannot load model X". He asks for one that is not here: "model X not
available". Nothing in the answer says why, and nothing describes the machine.
Alice, who runs the server, opens its log and reads the reason: the model
needed more memory than the budget allows, or the download never finished. The
log is hers alone, kept in her own account, unreadable to anyone else who uses
the Mac. By default it is sparse, a line per event that mattered; when she is
chasing a problem she switches it to detailed and it says everything Gropius
knew, still never a prompt or an answer. A client on this Mac, or one holding
the API key, keeps the fuller refusal it gets today.

## Why This Matters

Today Gropius logs to standard error only, which a Finder-launched app
discards, so the reason behind a refusal a client saw is nowhere an operator
can read it; and a refusal to a client on the network can describe this Mac
(its budget in bytes, its in-flight count). The what belongs to the client, the
why to the operator, and the two must not be the same sentence. Builds on the
entitlement rule the models list and the pool refusals already share, and on
server state living in the serving account's own directory.

## Mechanism

We expect an app log file in the account's own directory, written at a sparse
level by default and switchable to detailed without a restart, to let Alice
find the why for any refusal a client saw, because every refusal already passes
through one place in the gateway. Detailed never records prompts or answers;
that stays with the separate per-model debug draft (itd-2609062346072707).
Shown wrong if the sparse level leaves out the line that explains a refusal, or
the detailed level fills the disk.

## Scope Conditions

- macOS; one log per serving account under its own directory, 0600, never under the shared root. <!-- cond: cond-2609091703454678 -->
- Sparse by default. <!-- cond: cond-2609091703454790 -->
- The model servers' own logs are unchanged. <!-- cond: cond-2609091703455271 -->
- Prompts, answers, keys and tokens are never logged at any level. <!-- cond: cond-2609091703457603 -->
- The log is size-rotated like the statistics store. <!-- cond: cond-2609091703452047 -->

## Acceptance Criteria

- Given an unentitled client, when Gropius refuses a request, then the answer says what (cannot load, not available, cannot serve now) and never why, and one sparse log line names the model and which refusal.
- Given an entitled client, when Gropius refuses, then the answer is today's informative one.
- Given the server runs, when it writes its log, then the file is in the account's own directory at 0600 and contains no prompt, answer, key or client address.
- Given the level is switched in config.json or the panel, when the next event happens, then it is logged at the new level without a restart, and a save never refuses an untouched field.
- Given detailed, when a refusal or a load happens, then the line carries the figures (in-flight, budget, wait, launch error) that sparse omits.
- Given the log grows, when it passes its size, then it rotates and old files are pruned.
- Given the docs, when a reader opens the logging page, then it says what each level writes and where the file is.

## Open Questions

- Resolved 2026-09-09 at interview: entitled clients keep the informative refusal (today's decision on iss-2609062210238684 stands); the per-model debug draft itd-2609062346072707 stays a separate intent because a shared level control would put the level within the runtime's reach and fail the statistics-switch guard.

## Audit Notes

<!-- abcd-review: OWED receipt=rcp-1f33f9504a0e -->
Fidelity review OWED (receipt rcp-1f33f9504a0e).

## Grounds

- pursued: we expect a client's plain refusal plus a server-side reason to end the guessing when a model will not load, without telling the network anything about this Mac; shown wrong if operators keep asking what a refusal meant because the sparse line did not say, or if they turn detailed on and leave it on.
