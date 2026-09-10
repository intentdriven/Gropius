# Decomposition calibration (itd-84 hand-run)

One graded entry per proposal put through the routing table before filing.
The corpus gates the automated pre-pass; record whether the initial routing
survived the human's confirmation, not just the final table.

## 2026-09-06 — Gropius landing page, updated on release via Cloudflare Workers

Proposal as stated: "Build a Gropius landing page (intentdriven.sh/Gropius for
now) updated via Cloudflare workers when a new release is cut."

Initial routing proposed: FILE-AS-IS as one intent.

| Part | Type | Home |
| --- | --- | --- |
| Alice opens one link and gets the current release download plus the repository | user-facing capability | intent itd-2609061353254616 |
| The page reflects a new release without anyone editing it | user-facing capability | intent itd-2609061353258535 (builds on the first) |
| Cloudflare Worker; pull from the Releases API vs push from release.yml | plumbing | the second intent's spec |
| Page copy renders from the canonical identity block | existing invariant | IDENTITY.md; the first intent refines it by adding a surface |
| Hosting at intentdriven.sh "for now" | decision | .abcd/work/DECISIONS.md, 2026-09-06 |
| Page and Worker source location, second toolchain, dependency sign-off | scope question | open questions on both drafts, for the planning interview |

Typed links: the first intent `refines` the canonical identity invariant. No
supersedes, reverses, or duplicates. No existing `abcd site` composition.

Verdict adopted by the human: SPLIT. The initial FILE-AS-IS routing did not
survive: the human separated the static page (which already tracks the primary
download through the latest-release asset URL) from release-driven updating,
so the page can ship ahead of the Worker. Grade: routing of parts to homes
held; the verdict did not.

## 2026-09-06 — Expose model parameters (seed, temperature) via Settings

Proposal as stated: "expose model parameters (seed, temperature) via Gropius.
Make this configurable via Settings."

Initial routing proposed: FILE-AS-IS as one intent plus one issue capture.

| Part | Type | Home |
| --- | --- | --- |
| Alice sets default sampling parameters in Settings; requests that omit them get those values | user-facing capability | intent (sampling defaults) |
| Reproducible generation via a seed set in Settings | user-facing capability | intent (seed), gated on the verification issue |
| Per-request values from the client still pass through | existing behaviour | docs; stated as a scope condition on both intents |
| Whether mlx-lm 0.31.3 honours a per-request seed | fact to establish | issue capture |
| Precedence between request and Settings values; global vs per-model | open questions | planning interview |
| Injecting defaults into the buffered request body | plumbing | the spec; gateway trust boundary, security review at PR |

Typed links: none to existing records. The gateway's "rewrite only the model
field" stance is a code comment, not a recorded invariant; the spec must name
that this is the first feature merging fields into request bodies.

Verdict adopted by the human: SPLIT. The initial FILE-AS-IS routing did not
survive: the human separated temperature (and the other accepted sampling
parameters) from seed, because seed depends on an unverified upstream
behaviour and must not block the rest. Grade: parts-to-homes held; the
verdict did not. Second consecutive SPLIT where FILE-AS-IS was proposed.

## 2026-09-06 — Manage context size and tell clients the context size per model

Proposal as stated: "Carefully manage context size and let clients know what
the current context size is for each model (if possible)."

Initial routing proposed: SPLIT into two intents, measurement as an open
question on the second.

| Part | Type | Home |
| --- | --- | --- |
| Alice's client reads each model's context length from the models list | user-facing capability | intent (context window visible) |
| Gropius serves only up to the context it can hold, reports it, rejects over-long prompts clearly | user-facing capability | intent (effective context managed) |
| Architectural cap read from the model's config at rescan | plumbing | first intent's spec |
| Effective cap = min(architectural cap, KV headroom under the budget) | mechanism claim | second intent; refines iss-3 |
| Measuring the effective window per model | fact to establish | issue capture |
| Token counting in the gateway vs relying on the server's rejection | open question | planning interview |

Typed links: the second intent `refines` iss-3 (memory budget ignores KV
cache from decode concurrency). Nothing superseded or reversed.

Verdict adopted by the human: SPLIT, plus an issue capture for the
measurement. The proposed split survived; the human additionally promoted
the measurement from an open question to its own ledger record so it is
tracked. Grade: parts-to-homes held; verdict held with one part re-homed
from "open question" to "issue capture". First run where the proposed
verdict survived, after two FILE-AS-IS proposals that were split.

## 2026-09-06 — Model-bench lab review: five intents and three captures

Proposal as stated: the human adopted, verbatim, the list of lab-derived
recommendations the session had presented ("All of it"). Each item had
already been routed in that presentation, so this run records the routing
as confirmed rather than re-proposed.

| Part | Type | Home |
| --- | --- | --- |
| Client sees which models are loaded, picks the warm one | user-facing capability | intent (residency visible) |
| Models Alice pins stay resident; evicting request refused | user-facing capability | intent (pinned models) |
| Alice sets the resident memory budget in Settings | user-facing capability | intent (configurable budget) |
| Recently used models get a grace interval; requests wait bounded time | user-facing capability | intent (eviction grace) |
| Opt-in per-model merging of system messages | user-facing capability | intent (system-message merging); first gateway feature reading prompt content, named in its open questions |
| Docs omit budget default, eviction rule, preload-does-not-pin | defect | issue capture, docs category |
| KV-cache cost per architecture | evidence | appended to existing iss-3 |
| Lab measurements and co-residency arithmetic | evidence | dated research note 2026-09-06-model-bench-findings |

Typed links: the eviction-grace intent `refines` the pool's LRU rule, which is
a code comment and README sentence rather than a recorded invariant; the
configurable-budget intent `refines` iss-3 and iss-6 (budget accounting);
the docs capture `refines` the README's "LRU memory budget" claim. Nothing
superseded or reversed.

Verdict adopted by the human: FILE-AS-IS for all eight parts. Grade: routing
survived unchanged. Note for calibration: this run's routing was proposed in
prose a turn earlier and adopted as a batch, so it is weaker evidence than a
table confirmed part by part.

## 2026-09-06 — Local telemetry: statistics, on-disk store, telemetry pack, dashboard

Proposal as stated: the local-telemetry research recommendation "plus we also
need it to be stored on disk so that we can later build a dashboard with
insights into token use, latency etc. -- we want to use Gropius to learn
about the use of local models; we also want to enable clients to request a
'telemetry pack' for their session(s)".

Initial routing proposed: SPLIT into three intents, dashboard drafted or held.

| Part | Type | Home |
| --- | --- | --- |
| Opt-in statistics shown per model in the control panel | user-facing capability | intent (local request statistics) |
| Records kept on disk with stated retention and format | user-facing capability | intent (durable store), builds on the first |
| A client fetches a telemetry pack for its own sessions | user-facing capability | intent (telemetry pack), builds on both |
| A dashboard over the store | user-facing capability, later | intent (usage dashboard), drafted |
| "Learn about the use of local models" | conjecture | grounds at each gate, not a record |
| On-disk format and retention (JSON Lines vs SQLite, a new dependency) | decision | ADR when the store's spec decides it |
| What a session is on an API with none; who may fetch whose pack | trust-boundary rule | open question on the pack intent; likely its own ADR |
| No prompt text, completions, keys or client addresses in statistics | existing invariant | adr-2609061503319212; all four refine it |

Typed links: all four `refine` adr-2609061503319212. The statistics intent
`refines` itd-2609061441228998 (residency visible). Context-fill figures
depend on itd-2609061431463108. The research note's advice against SQLite
was scoped to a bounded ring; a durable dataset reopens it, no reversal.

Verdict adopted by the human: SPLIT, with the dashboard drafted as a fourth
intent. Grade: parts-to-homes held; the verdict held, with the human choosing
the "draft it" branch of the one open choice offered.

## 2026-09-06 — Planning interview across the store: re-routing after review

The interactive planning interview (fifteen drafts, two adversarial
reviewers per theme) changed four earlier routings. Recorded here because a
decomposition that survives filing and then falls at planning is the signal
the calibration corpus exists to catch.

| Record | Earlier routing | Outcome at planning | Why |
| --- | --- | --- | --- |
| itd-2609061429516182 (seed in Settings) | intent, gated on a verification issue | superseded by itd-2609061429508050 | The verification found the server ignores seed entirely; the capability cannot exist. A gated intent was the wrong home for an unverified premise; a capture plus a documented fact would have sufficed. |
| itd-2609061521134968 (telemetry pack) | intent, with an access-rule ADR to follow | dropped, superseded by adr-2609061503319212 | Both reviewers found it contradicted the ADR's "for the operator of that Mac". The decomposition should have flagged the reversal at filing rather than routing the conflict to a future ADR. |
| itd-2609061431481936 (effective context) | intent, refines iss-3 | held | Needs an ADR on the budget rule and a measurement now confounded by a newly found gateway timeout; three prerequisites, none in the original table. |
| itd-2609061602043757 (summary before deletion) | not present | new intent, builds on the store | Surfaced by the maintainer while answering the retention question; the store's decomposition missed that deleting detailed records loses the history the dashboard exists for. |

Two ADRs were minted at planning rather than filed as parts at capture time
(store format; the gateway's one permitted prompt rewrite); both had been
named as "ADR when the spec decides" in earlier tables, which held.

Grade for the corpus: of fifteen filed intents, eleven survived the
interview as routed, one was re-homed as a new intent's parent, and three
changed bucket. The two that fell were both cases where a trust-boundary or
upstream-behaviour question had been routed forward instead of resolved.

## 2026-09-08 — Tailscale: does a mesh VPN benefit Gropius, and should features key on it?

Proposal as stated: "I have tailscale installed on my machines. Does that
benefit Gropius? And could we activate certain features when Gropius detects
Tailscale? The other way around: if not, should certain features be unavailable
automatically?"

Initial routing proposed: SPLIT — one intent now, one intent later, one ADR,
docs deferred.

| Part | Type | Home |
| --- | --- | --- |
| The endpoint list names a tailnet address as private and encrypted, under the name the tailnet resolves | user-facing capability | intent (itd-2609081015545349), filed |
| A "tailnet only" bind the local network cannot reach | user-facing capability, later | intent, deferred to a separate filing by the human |
| Detection may inform what the app reports, never what it enforces | trust-boundary rule | adr-2609081118587999 |
| How-to for serving over a tailnet, and the standing warning against public funnelling | docs | follows whichever intent ships; not a record of its own |
| mDNS does not traverse a tailnet | verified fact | stated inside the intent, no record of its own |

Typed links: both intents `refine` adr-2609081118587999; the later bind intent
`builds_on` the filed one. Nothing superseded. No reversal — the filed intent
changes no enforcement, which is the ADR's rule applied to itself.

The reversal that was *not* filed is the interesting part: the obvious feature
("a tailnet is present, so waive the API key") reverses the exposure rule in
`config.validate` and the warning in the control panel's status. It was routed
to the ADR as a rejected alternative rather than to an intent. That is the
routing the earlier telemetry-pack run got wrong in the opposite direction —
there a trust-boundary conflict was routed forward to a future ADR and only
caught at planning.

Verdict adopted by the human: SPLIT, with the later bind intent explicitly
deferred to its own filing and the ADR requested by name. Grade: routing
survived. Calibration caveat, the same one the 2026-09-06 shipped-divergences
run carries — the options were put in prose a turn before the table existed and
the human adopted them as a batch, so this is weaker evidence than a table
confirmed part by part. The acceptance criteria on the filed intent are
agent-seeded and marked as such in its Open Questions; the planning interview
has not run.

## 2026-09-09 — model category from the Hub (itd-2609091129451578)

Proposal: the server lists a model's type (chat, coding, other) when browsing,
records it at download, advertises it via the API; clients pick by category.

| Part | Type | Home |
| --- | --- | --- |
| Search results show each model's category | user-facing capability | this intent |
| The category is recorded in the registry at download | mechanism | this intent (spec) |
| The models list advertises the category | user-facing API contract | this intent, plus the models-list reference page |
| GropiusChat's picker offers only chat models | client half | this intent (client criterion) |
| How a category is derived | mechanism claim | spec's Mechanism section, no ADR (no trust boundary) |
| A coding harness picks coding for implementation, chat plus coding for reviews | illustration of a third-party client | press release only |

Typed links: `refines` iss-2609081743175520 (`promoted_from`); `builds_on`
itd-2609061431463108 (the models-list field convention). Flagged reversal: it
widened the same day's decision to publish a boolean chat capability from the
chat template; the human confirmed the widening ("category supersedes the
boolean").

Verdict adopted: FILE-AS-IS, one intent. Grade: routing survived the human;
the VOCABULARY did not survive the design review — a 500-repository sample
showed "coding" is not derivable from Hub metadata, and the human then chose
"rely on the HuggingFace tag, don't invent your own", with a server-side chat
rule (default text-generation or image-text-to-text plus conversational) an
operator can change and the same default in GropiusChat. Lesson for the
protocol: run the feasibility review BEFORE the routing question when a
proposal names a taxonomy, because the taxonomy was the decision.

## 2026-09-09 — server-side logging (itd-2609091412177263)

Proposal: API users get clear messages on what is not possible but never the
why; the why is logged server-side (sparse default, detailed option), within
each account, never shared.

| Part | Type | Home |
| --- | --- | --- |
| API answers say what, never why | user-facing API contract | this intent |
| The why goes to a server log; sparse by default, detailed on request | user-facing capability (setting on three surfaces) | this intent |
| What each level contains, and what is never logged | mechanism | spec, plus the existing prompt-content invariant |
| Logs live within each account, never shared | trust-boundary rule | already shipped the same day (iss-2609091131311102); linked, not restated |

Typed links: `builds_on` the entitlement rule (iss-2609062210238684) and
per-account state (iss-2609091131311102); NOT a duplicate of the per-model
debug draft itd-2609062346072707 (a shared level control would fail the
statistics-switch guard). Flagged reversal: "never the why" for entitled
clients would reverse the same day's error-text decision; the human kept the
why for entitled clients, so nothing reversed.

Verdict adopted: FILE-AS-IS, one intent. Grade: routing survived. The design
review found the log the intent writes to does not exist (stderr only), which
became the first sentence of the mechanism rather than a routing change.

## 2026-09-09 — recording mode (itd-2609091707499248)

Proposal: a server-side mode recording every prompt and answer, explicitly
activated, announced to every client session as a first message, with no way
to circumvent it.

| Part | Type | Home |
| --- | --- | --- |
| A server-wide recording mode | user-facing capability | this intent |
| Explicit activation on three surfaces | capability | this intent |
| Every session told first, no way round | user-facing API contract | this intent |
| A second reader (and, it turned out, a writer) of prompt content | trust-boundary rule | a superseding ADR for adr-2609061610102325 |
| The store lives per-account | mechanism | the spec, on the per-account rule |

Typed links: `supersedes` itd-2609062346072707 (proposed); `refines` the
logging intent's no-prompts scope condition; `reverses` the never-retain
clause of the prompt-content ADR (flagged; the human confirmed the ADR route).

Verdict adopted: FILE-AS-IS with the ADR as companion. Grade: routing
survived; the PROMISE did not survive the design review — "told first, no way
round" is not a thing a stateless server can do — and the human HELD the
intent rather than adopt the every-answer rule. Lesson, same as the category
run: when a proposal's headline is a guarantee, run the feasibility review
before the press-release question, because the guarantee is the decision.

## 2026-09-09 — usage measurement, and held adaptive settings (itd-2609091712141073, itd-2609091712142715)

Proposal: fully dynamic, customised per-model and context-window settings on
the server; until then, collect the data that says what to configure.

| Part | Type | Home |
| --- | --- | --- |
| Per-model settings an operator can change | exists or in flight | no record; `builds_on` the unified models map and the served-window intent |
| Settings that adapt without a restart | capability, unproven | a held draft (itd-2609091712142715) the data lifts |
| Per-request and per-model usage facts on the dashboard, exportable | capability, buildable now | this intent (itd-2609091712141073) |
| No prompt content in it | invariant | already holds; linked |
| Which record carries it | mechanism | the spec: fields on the existing request line, no new kind |

Typed links: `builds_on` itd-2609061521082551 and its store ADRs; `builds_on`
itd-2609061431481936; `refines` itd-2609091301112705.

Verdict adopted: SPLIT. Grade: routing survived as proposed.

## 2026-09-09 — transcript exceptions per model (stub)

Proposal: an exception list so named models keep no transcripts under the
recording mode; recorded as a stub only, by the maintainer's instruction.

| Part | Type | Home |
| --- | --- | --- |
| Named models keep no transcript while recording is on | user-facing capability | this draft, a stub |
| Where the exception lives | mechanism | the per-model settings map; settled at planning |
| What a client is told for an excepted model | open question | inherits the parent's held notice question |

Typed links: `builds_on` itd-2609091707499248 (held); inherits its hold.
Verdict adopted: FILE-AS-IS as a stub, no interview, at the human's request.
Grade: routing not tested (the human asked for a stub); recorded for the count.

## 2026-09-09 — resources view (itd-2609091903463596)

Proposal: a dashboard for server activity and resource utilisation (number of
models, disk space, context window).

| Part | Type | Home |
| --- | --- | --- |
| Server activity | shipped and planned elsewhere | `duplicates` itd-2609061521082551 and itd-2609091712141073 |
| Resource utilisation: models, disk, budget vs resident, windows | capability | this intent |
| What is on and off | planned elsewhere | `builds_on` itd-2609081718534201 |
| Where it lives | mechanism | the spec; decided at interview: a block on the Models tab |

Verdict adopted: SPLIT. Grade: routing survived; the design review moved the
placement from "a tab" to "a block on the Models tab" and the human took it.

## 2026-09-10 — an intuitive control panel for the server

Proposal: an intuitive, state-of-the-art website to configure the Gropius
server — read as the control panel, not the landing page, which the human
confirmed.

| Part | Type | Home |
| --- | --- | --- |
| A redesigned control panel: settings found by task, state shown first, live validation in the server's words | user-facing capability | this draft (itd-2609100519003748) |
| Whether the panel may take a build step, a framework or an asset fetched from outside the Mac | trust-boundary rule and dependency sign-off | a decision before planning (DECISIONS.md, 2026-09-10); an ADR if adopted |
| "Intuitive" as a standing stance | standing stance | the draft's press release; no principle |
| Plumbing | none | — |

Typed links: `refines` itd-2609081259493890, itd-2609081718534201,
itd-2609091903463596. Reversal flagged against adr-2609061503319212 for an
asset fetched from a CDN; the human did not confirm a reversal, so none is
recorded and the question is held at the decision.

Verdict adopted: SPLIT. Grade: routing survived as proposed.
