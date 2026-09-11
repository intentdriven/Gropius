---
id: itd-2609091301112705
slug: gropius-measures-a-newly-downloaded-model-s-servable-context
spec_id: null
kind: null
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609061431481936, itd-2609061431463108]
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Gropius measures a model's real context window

## Press Release

Gropius now tells you how long a prompt each model on your Mac can actually
take. When a download finishes, Gropius measures the model rather than
believing its configuration: it sends prompts of growing length, bisects
between the last one that came back and the first that did not, and records
the window it verified beside the one the model declares.

Alice downloads a 262,144-token model in the evening. In the morning the
models list tells her that her Mac serves it to about 91,000 tokens — the
declared window was never reachable here — so her editor's long-file prompts
are sized to what works instead of failing halfway through the afternoon. The
memory budget charges the window the machine can serve rather than the one the
file advertises, and the figure comes from her own Mac rather than from someone
else's benchmark.

The measurement is not free, and the design has to carry that. Probing one
model took about forty minutes of GPU time at low load in the 2026-09-06
campaign, with an unload and a reload between steps so a retained prompt cache
cannot flatter the next reading, and it needs the machine not to be serving
anyone: a probe that runs while clients are asking for models measures the
queue rather than the model. So this runs when the Mac is idle, is
interruptible, and yields the machine to a real request the moment one arrives.

## Why This Matters

The declared window is a claim about the architecture, not about this Mac. The
2026-09-06 campaign found three of four models bounded by the gateway rather
than by the model, and one that reached only a third of its declared cap here;
that record exists because someone ran a script by hand for an evening, and it
goes stale the moment a model, a runtime or the machine changes. A window
nobody has measured is a number a client sizes its prompts from and then loses
an afternoon to.

## Mechanism

We expect an automated sweep-and-bisect to recover the same servable window a
person recovered by hand, because the campaign's method is mechanical — unique
prompts generated from a seed so no two share a prefix and no prefix is cached,
one output token at temperature 0, an unload and a reload before each long step,
the window read from the gateway's own `usage.prompt_tokens` — and every input
it needs is already in the process: the registry knows the declared cap, the
pool knows what is resident and what is idle, and the gateway already derives a
prefill deadline from prompt size. We are wrong if the figure is not stable
enough to publish — a repeat probe on the same Mac, the same models and the same
runtime returning a materially different window — or if what the probe finds is
the gateway's bound rather than the model's, in which case the number belongs to
Gropius's configuration and moves whenever `upstream_header_timeout_sec` does.

The second failure is not hypothetical: the 2026-09-06 campaign found three of
four models bounded by the gateway rather than by the model. So a reading that a
gateway bound stopped is published as a floor under the model's window and names
the bound that stopped it, never as the model's limit.

## Scope Conditions

- Apple Silicon Macs with unified memory; the probe's memory guard is arithmetic over one shared pool, not over a discrete GPU's.
- The pinned runtime, mlx-lm 0.31.3 today. The prefill rate, the retained prompt cache and the per-token cost are properties of the runtime as much as of the model, so a figure measured under one runtime is not a figure under another.
- One serving process per Mac. The singleton election means the probe competes with every account's requests, not only with its own.
- Models the registry holds as ready, with a declared context length read from their own `config.json`. A model that declares no window has nothing to bisect between.
- Text models. The campaign's recall check assumes a prompt of text filler.
- A Mac that stays awake for the duration. A probe does not survive sleep.
- The probe reaches the model the way a client does, over this Mac's own OpenAI endpoint, so what it measures is the window a client can actually get. A step that the gateway's prefill deadline or its served-window check stopped bounds Gropius's configuration, not the model, and is recorded as such.

## Acceptance Criteria

**Idle detection and yielding**

- Given the probe switch is off, when a download finishes, then nothing is measured and nothing is queued: a probe begins only from the global switch, which is off by default, or from a per-model "Measure now".
- Given a probe queued, when any model has served a request inside the idle threshold, or the pool reports a waiting caller, or anything is in flight, then the probe does not start and the panel says which of those held it back.
- Given a probe running, when a real request arrives for any model, then the probe cancels its own in-flight request within the eviction grace, asks the pool to unload the model it was driving so the abandoned prefill neither keeps the GPU nor leaves its cache resident, and records the step as yielded rather than as a failure.
- Given a probe whose unload is refused because a request is now in flight on that model, then the reading that step would have produced is discarded rather than kept, because a server that kept an abandoned cache measures the cache and not the model.
- Given a probe that has yielded, when the Mac is idle again, then it resumes from the bisection bounds already established, not from the beginning.

**Interruptibility and safety**

- Given a probe running, when the operator presses Stop, or Gropius is quit, or the machine sleeps, then no partial figure is written, the model is left unloaded, and the next start reports the probe incomplete rather than silently retrying it.
- Given a step whose projected peak footprint comes within the campaign's margin of this Mac's available memory, then the step is skipped and recorded as skipped, together with the projection that skipped it.
- Given a probe between steps, when the next step begins, then the model server has been unloaded and started again and the step's prompt carries its own nonce, so no retained prompt cache flatters the reading.
- Given a pinned model, when the probe needs room, then nothing pinned is evicted for it: the probe acquires only when the room is already free, and it waits in the queue every other caller waits in and is counted by the pool's waiting count, so it cannot hide from the queue caps.

**The result, published beside the declared and the served window, and charged only when adopted**

- Given a completed probe, when a client reads `GET /v1/models`, then the entry carries the measured window beside `context_length` and `served_context`, and the models-list reference says what each of the three means.
- Given a completed probe, when the operator adopts the measured figure, then that figure becomes the model's served window, the next load is charged at it, and the panel shows the figure it is charged at; until it is adopted the measurement changes no charge and refuses no request.
- Given a probe whose largest step was stopped by the gateway's prefill deadline or by its served-window check rather than by the model, then the result names the bound that stopped it, the figure is published as a floor, and the panel and the docs say plainly that the model's own window is not known.
- Given the global switch and the per-model "Measure now", when the Go settings, `config.json` and the control panel are compared, then all three carry them and the progress and the result, and a save that names neither leaves both alone.

**Invalidation**

- Given a stored measurement, when the runtime version changes, the model is re-downloaded, or the memory budget, the decode concurrency or the served window changes, then the measurement is marked stale, is neither charged nor published as current, and the record says which of those changed.
- Given a stored measurement, when Gropius starts, then the provenance stored with it — runtime version, budget, decode concurrency, served window, and what stopped the largest step — is compared with what is in force now, so staleness is a stored fact rather than an assumption.

## Open Questions

Settled on 2026-09-10 at a planning interview delegated to the agent on the
maintainer's instruction "implement fully autonomously", adopting the
recommendations of the planning brief prepared on 2026-09-09: the probe is a
global switch defaulting off with a per-model "Measure now" (1); it runs at the
first sustained idle and never at download time (2); a result is never reused
between machines (3); it never evicts a pinned model, acquires only when the
room is already free, and is counted by the pool's waiting count like any other
caller (4); the gateway's prefill timeout is not relaxed for it, and the probe
drives the same OpenAI path a client drives, so its largest step is bounded by
the served window and by the gateway's own limits and a reading so bounded is
published as a floor (5).

Clarification (2026-09-10, from the same interview; the press release stands as
written and this says what was found true of it): the press release's opening —
"when a download finishes, Gropius measures the model" — is delivered as the
morning after rather than the moment after. A download finishing arms the
measurement; the first sustained idle window runs it, and only when the operator
has switched the probe on or asked for this model by name. Alice's story is
unchanged: she downloads in the evening and reads the figure in the morning.

What that choice cannot measure, stated plainly: the model's own window above
whatever the gateway stops first. On the 2026-09-06 evidence that is three
models of four, so for most models this intent publishes a verified floor and a
named bound rather than the architecture's limit.

- **Reuse between accounts on one Mac — deferred, not declined.** Each account keeps its own `registry.json`, so Carol pays another forty minutes of GPU for a measurement Alice already made on the same machine and the same runtime. A reading keyed on the machine and the runtime version and stored in the shared root would avoid that, and it is a new writeable file in a group-writable directory: a trust-boundary change needing its own adversarial security review, which is not on this intent's critical path.
- **How the model's own window is ever learned.** A coordinated pool-side path exempt from the gateway's bounds, a raised `upstream_header_timeout_sec` for the duration, or the manual campaign script staying the only answer. Nothing here decides it.
- **Whether the probe also samples memory**, which would keep `capability`'s safety factors of 5 and 7 under continuous evidence rather than one evening's. Out of scope here — that package belongs to another lane — and worth a follow-on.
- **The idle threshold's value**, and how many models one idle window may take: six models at forty minutes each is an evening, and the models load one at a time.
- **Arbitration with the model self-test**, which also runs at idle and is not yet on `main`. Two idle jobs on one Mac need one idea of "idle" between them, and whichever lands second inherits the question.
- **Whether the yielding seam becomes real preemption in the pool.** The pool has no preemption and no priority today; this intent reads the pool and cancels its own request instead. Any change to the pool itself is a coordinated follow-up with the lane that owns `internal/runtime`.

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._

## Grounds

- pursued: an automated sweep-and-bisect recovers the same servable window a person recovered by hand, because the campaign's method is mechanical and every input it needs — the declared cap, what is resident and idle, a prefill deadline derived from prompt size — is already in the process; shown wrong if a repeat probe on the same Mac, models and runtime returns a materially different window, or if what it finds is the gateway's bound rather than the model's, in which case the figure belongs to Gropius's configuration and moves whenever upstream_header_timeout_sec does — planned autonomously on the maintainer's instruction of 2026-09-10, adopting the brief's recommendations
