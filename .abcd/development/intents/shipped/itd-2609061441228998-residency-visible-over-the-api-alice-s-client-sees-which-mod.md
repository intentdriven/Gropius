---
id: itd-2609061441228998
slug: residency-visible-over-the-api-alice-s-client-sees-which-mod
spec_id: spc-2609061822377049
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Residency visible over the API: Alice's client sees which models are loaded right now from the models list, so it can pick the warm one instead of triggering a swap

## Press Release

Alice's agent holds the key Bob handed out for his Mac. It lists the models and
sees, next to each one, whether that model is loaded right now, how much work
it is already handling, and when it was last used. It sends its work to the
warm model and never forces a multi-minute reload just because it did not know.
Carol's agent, holding the same key, sees the same picture and makes the same
choice, so the two of them share a resident model instead of evicting each
other's. On a Mac that Bob has left open to the network with no key, the list
is exactly what it is today, and nobody learns from it what he is running.

## Why This Matters

The models list shows every downloaded model with an id, a timestamp and an
owner, and nothing about whether it is resident. A client cannot tell a warm
model from one that will take minutes to load, so it picks blind, and a blind
pick is what triggers the swaps the 2026-09-05 model-bench lab measured as the
expensive event. This intent puts that fact where entitled clients can read it.
It is the cheapest change on the lab's list and removes the need for the lab's
status proxy. The field is meaningless without the eviction rule beside it, so
the docs defect iss-2609061443332414 is settled in the same change.

Evidence: research note 2026-09-06-model-bench-evidence (same-model concurrency against swap cost: six concurrent requests at 3.6 times the throughput of one; a cold swap between the two workhorse models at 7.7 s).

## Mechanism

We expect a keyed client that reads residency before choosing to stop
triggering load-time swaps because a request naming a resident model takes the
pool's fast path and evicts nothing, while a request naming a cold model
evicts under a full budget; we are wrong if swap counts on a two-agent Mac are
unchanged once clients can see what is warm.

## Scope Conditions

- Installs with an API key configured; where no key is set the list is <!-- cond: cond-2609061822377016 -->
  unchanged and the claim does not apply.
- Clients that list the models before each request, or on a schedule short <!-- cond: cond-2609061822376834 -->
  enough that the snapshot is still true; a client that lists once at start-up
  gains nothing after the first change.
- Machines holding more downloaded models than the memory budget can keep <!-- cond: cond-2609061822373573 -->
  resident at once, or running an idle timeout, so residency actually varies.
- The pool's process-per-model design and its least-recently-used eviction <!-- cond: cond-2609061822377503 -->
  rule as they stand today.

## Acceptance Criteria

- Given an API key is configured and a downloaded model is not loaded, when a
  client lists the models with the key, then that model's entry reports it as
  not loaded and the entry's pre-existing fields are unchanged.
- Given an API key is configured and a model's server has answered its
  readiness probe, when a client lists the models with the key, then that
  model's entry reports it as loaded.
- Given an API key is configured and a model's server has been started but has
  not yet answered its readiness probe, when a client lists the models with the
  key, then that model's entry reports it as loading.
- Given an API key is configured, when a client lists the models with the key,
  then each entry also reports how many requests are in flight for that model
  and when that model was last used.
- Given no API key is configured, when a client lists the models, then no entry
  carries residency, in-flight, last-used or pinned information.
- Given any models listing, when the response is inspected, then it carries no
  loopback port and no filesystem path.
- Given the reference page for the models list, when a reader looks it up, then
  the residency values, the keyed-install condition and the fact that the
  listing is a snapshot rather than a reservation are all stated.

## Open Questions

- Resolved: residency is reported on each entry of the models list; no separate
  status endpoint is added.
- Resolved: the residency information appears only when an API key is
  configured, so an open server discloses nothing it does not already disclose
  to anyone willing to time a request.
- Resolved: three states are reported — loaded, loading, not loaded — because a
  model mid-load is neither warm nor cold.
- Resolved: the in-flight count and the last-used time appear alongside the
  state on a keyed install, matching what the control panel shows on loopback.
- Resolved: pinned status follows the same rule and appears only on a keyed
  install; the pin list itself belongs to itd-2609061441241254.
- Resolved: this record owns the shape of the models-list extension — top-level
  fields under common names, no namespaced object — and itd-2609061431463108
  follows the same shape for the context length.
- Resolved: the reported state is a snapshot taken when the list is built, not
  a reservation; nothing holds a model warm on a client's behalf.
- Depends on: iss-2609061443332414, the docs defect that states the memory
  budget and the eviction rule the residency values are read against.

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-251602005fba -->
Fidelity review — receipt rcp-251602005fba (verifier abcd:intent-auditor claude-opus-5[1m]).

Provenance: abcd:intent-auditor@claude-opus-5[1m] · rubric_hash sha256:54ce95d4f60381e1e867125055b5f9234cb2c894ad9086b9422ecf252a092e7b · prompt_hash sha256:ff3be6a6cc06091ff6352a77d2a4311e9cdd40dfe02daa130cdd6059e73b99fe
Input attestations: tree:working tree at HEAD 18f1a4e286fd85f6e94bb2ae1d2ecba40e13cc77 (git tree 5f3256dd3718af34e459add3c3c30b5d631e90af); history was rewritten during the rename, so no per-spec commit range is resolvable and the tree as shipped was audited@-; request:.abcd/.work.local/reviews/rcp-251602005fba.request.md@sha256:ff3be6a6cc06091ff6352a77d2a4311e9cdd40dfe02daa130cdd6059e73b99fe; intent:.abcd/development/intents/shipped/itd-2609061441228998-residency-visible-over-the-api-alice-s-client-sees-which-mod.md@sha256:54ce95d4f60381e1e867125055b5f9234cb2c894ad9086b9422ecf252a092e7b;

Acceptance rollup: MET 6 · MET_WITH_CONCERNS 1 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: A registry model with no pool entry is passed the zero Resident, whose empty State falls through the allow-list switch to not_loaded, while the entry's id/object/created/owned_by are built before the projection and never rewritten; the test asserts both halves and `go test ./internal/gateway/` passes.
  evidence: internal/gateway/gateway.go:293 — "state := runtime.ResidencyNotLoaded"
  evidence: internal/gateway/gateway.go:243 — ""id": m.RepoID,"
  evidence: internal/gateway/gateway_test.go:1272 — "if cold["state"] != "not_loaded" {"
  evidence: internal/gateway/gateway_test.go:1284 — "// The four fields the list served before this one are unchanged."
- ac-2 — MET: Pool.Resident() computes ResidencyLoaded from the non-blocking isReady(e) under p.mu and the handler projects it verbatim through the allow-list; asserted both against a stub resident and end-to-end through a real Registry.Rescan and a real runtime.Pool.
  evidence: internal/runtime/pool.go:1287 — "state = ResidencyLoaded"
  evidence: internal/gateway/gateway.go:298 — "entry["state"] = string(state)"
  evidence: internal/gateway/gateway_test.go:1261 — "if warm["state"] != "loaded" {"
  evidence: internal/gateway/gateway_test.go:1668 — "if warm["state"] != "loaded" {"
- ac-3 — MET: An entry is present in p.entries with its ready channel still open exactly while the server is up and unprobed, and Resident() defaults that entry to ResidencyLoading without blocking on the probe; a runtime test samples the loading window with a held-open launcher and a gateway test asserts the projection.
  evidence: internal/runtime/pool.go:1285 — "state := ResidencyLoading"
  evidence: internal/runtime/pool_test.go:888 — "t.Errorf("State = %q while the model was loading, want %q", loading[0].State, ResidencyLoading)"
  evidence: internal/gateway/gateway_test.go:1306 — "if got := warm["state"]; got != "loading" {"
- ac-4 — MET_WITH_CONCERNS: in_flight is projected unconditionally and last_used as Unix seconds, both asserted; the named concern is that last_used is omitted rather than emitted for a model the pool is not holding, so not literally every entry carries a last-used time.
  evidence: internal/gateway/gateway.go:300 — "entry["in_flight"] = res.InFlight"
  evidence: internal/gateway/gateway.go:306 — "if !res.LastUsed.IsZero() {"
  evidence: internal/gateway/gateway_test.go:1264 — "if n, ok := warm["in_flight"].(float64); !ok || int(n) != 3 {"
  evidence: internal/gateway/gateway_test.go:1267 — "want %d (Unix seconds)"
  evidence: docs/models-list.md:157 — "goes when the model does"
- ac-5 — MET: The whole projection sits behind the residency map being non-nil, which is populated only when withAuth recorded a keyed admission, and the unkeyed test asserts the entry's key set exactly plus a byte-level absence of all four field names.
  evidence: internal/gateway/gateway.go:216 — "if g.admittedKeyed(r) {"
  evidence: internal/gateway/gateway.go:262 — "if residency != nil {"
  evidence: internal/gateway/gateway_test.go:1331 — "want := map[string]bool{"id": true, "object": true, "created": true, "owned_by": true}"
  evidence: internal/gateway/gateway_test.go:1348 — "for _, name := range []string{"state", "in_flight", "last_used", "pinned"} {"
- ac-6 — MET: addResidency writes four named fields rather than marshalling runtime.Resident, so Port, Bytes and LoadedAt are structurally unreachable and no model path is ever placed on an entry; the standing regression guard asserts the response bytes contain neither the loopback address, the port, nor the fixture's model path.
  evidence: internal/gateway/gateway.go:288 — "func addResidency(entry map[string]any, res runtime.Resident, pinned bool) {"
  evidence: internal/gateway/gateway_test.go:1375 — "for _, leak := range []string{"127.0.0.1", "51234", "/models/org/warm", "bytes", "loaded_at", "port"} {"
- ac-7 — MET: docs/models-list.md states all three residency values in its own table, states the keyed-install condition as its own headed paragraph, and states the snapshot-not-reservation rule outright; a test holds the page's field table to the union of the keyed and unkeyed listings in both directions and asserts the phrases.
  evidence: docs/models-list.md:122 — "| `loaded` | The model's server has answered its readiness probe."
  evidence: docs/models-list.md:97 — "**They appear only when an API key is configured.**"
  evidence: docs/models-list.md:170 — "**A snapshot, not a reservation.**"
  evidence: internal/gateway/gateway_test.go:1100 — "t.Errorf("the reference page describes %q, which the models list does not serve", field)"

Gap audit:
- honoured:
  - A keyed client sees, next to each model, whether it is loaded right now, how much work it is handling, and when it was last used
    evidence: internal/gateway/gateway.go:298 — "entry["state"] = string(state)"
    evidence: internal/gateway/gateway.go:300 — "entry["in_flight"] = res.InFlight"
    evidence: internal/gateway/gateway.go:307 — "entry["last_used"] = res.LastUsed.Unix()"
  - On a Mac left open to the network with no key, the list is exactly what it is today and nobody learns from it what is running
    evidence: internal/gateway/gateway.go:262 — "if residency != nil {"
    evidence: internal/gateway/gateway_test.go:1324 — "func TestListModelsCarriesNoResidencyWithoutAnAPIKey(t *testing.T) {"
  - The extension is an allow-list projection, so a field added to runtime.Resident later cannot leak by default
    evidence: internal/gateway/gateway.go:281 — "It is deliberately an allow-list of named fields rather than a marshalling of"
    evidence: internal/gateway/gateway_test.go:1364 — "func TestListModelsPublishesNoPortOrPath(t *testing.T) {"
  - The residency field is meaningless without the eviction rule beside it, so docs defect iss-2609061443332414 is settled in the same change
    evidence: docs/models-list.md:186 — "## The memory budget and eviction"
    evidence: docs/models-list.md:191 — "The budget defaults to 60% of this Mac's physical RAM"
  - Extensions are top-level fields under common names with no namespaced object, recorded as this repository's rule
    evidence: internal/gateway/gateway.go:249 — "Gropius's extensions to the OpenAI shape are top-level fields with"
    evidence: .abcd/work/DECISIONS.md:26 — "extensions to the models list are top-level fields with the common names"
  - Pinned status follows the same key-gated rule (the spec's As-built note said it had not yet shipped; at HEAD it has)
    evidence: internal/gateway/gateway.go:312 — "entry["pinned"] = pinned"
    evidence: internal/gateway/gateway_test.go:1844 — "func TestModelsListReportsPinnedOnlyOnAKeyedInstall(t *testing.T) {"
- diverged:
  - The spec's Scope and trust-boundary notes promise withAuth is untouched and out of scope; withAuth changed to take one configuration reading and record a keyed-admission bit on the request
    evidence: internal/gateway/gateway.go:140 — "r = withAdmittedKeyed(r, apiKey != "")"
    evidence: internal/gateway/ctxutil.go:31 — "func withAdmittedKeyed(r *http.Request, keyed bool) *http.Request {"
    evidence: .abcd/development/specs/closed/spc-2609061822377049-residency-visible-over-the-api-alice-s-client-sees-which-mod.md:0 — "**`withAuth` changed.**"
  - The spec left open whether last_used is omitted or null for a model never used in this process; omission shipped, and every entry the criterion covers therefore does not carry the field
    evidence: internal/gateway/gateway.go:306 — "if !res.LastUsed.IsZero() {"
    evidence: docs/models-list.md:159 — "The field is absent rather than zero"
  - Settled beyond the plan: the residency join and the pool are both case-folded so one model is one entry whatever the spelling (iss-2609062150484966)
    evidence: internal/gateway/gateway.go:264 — "addResidency(entry, residency[config.FoldRepoID(m.RepoID)], pinned[config.FoldRepoID(m.RepoID)])"
    evidence: internal/runtime/pool_test.go:942 — "func TestAcquireHoldsOneEntryPerModelWhateverTheSpelling(t *testing.T) {"
  - The spec's docs plan put the network client's pointer to the reference page in getting-started section 5; the pointers landed in sections 4 and 6 instead, section 5 carrying none
    evidence: docs/getting-started.md:65 — "The [models list reference] (models-list.md)"
    evidence: docs/getting-started.md:145 — "triggering a load — see the [models list reference] (models-list.md)."
- missing: (none)

Scope-condition dispositions:
- cond-2609061822377016 — survived: The keyed-install gate is exactly the condition the delivery implements: the projection runs only on withAuth's keyed-admission bit, and the unkeyed listing is asserted to be today's key set to the byte.
  evidence: internal/gateway/gateway.go:216 — "if g.admittedKeyed(r) {"
  evidence: internal/gateway/gateway_test.go:1331 — "want := map[string]bool{"id": true, "object": true, "created": true, "owned_by": true}"
- cond-2609061822376834 — untested: Nothing in the delivery exercises or contradicts how often a real client lists; the snapshot rule is written down for readers but no shipped artefact observes a client's listing cadence.
- cond-2609061822373573 — untested: The delivery says nothing about how many models a deployed Mac holds against its budget; the eviction and idle-reaper machinery that would make residency vary exists and is tested, but whether real installs sit above the budget is neither exercised nor contradicted here.
- cond-2609061822377503 — narrowed: Process-per-model still holds and Resident() reads one entry per model, but the eviction rule the residency values are read against is no longer the plain least-recently-used rule the condition assumed as standing today.
  narrowing: Holds for the process-per-model design and for a least-recently-used base rule only; victim selection now additionally skips pinned models and models still loading, and eviction grace can make a request wait rather than evict, so 'as they stand today' no longer describes the eviction rule exactly.
  evidence: internal/runtime/pool.go:1281 — "for _, e := range p.entries {"
  evidence: internal/runtime/pool.go:1206 — "if e.inFlight > 0 || !isReady(e) || p.isPinnedLocked(e.repoID) {"
  evidence: docs/models-list.md:213 — "**Eviction grace** (**Settings**, off by default) changes when that unload"
## Grounds

- pursued: we expect a shared Mac to serve several agents without their models evicting each other once the operator can pin, budget and grace, and once keyed clients can see what is warm; we are wrong if model swaps stay as frequent with those controls set as they were without them, measured by the statistics store
