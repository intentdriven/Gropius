---
schema_version: 1
id: "iss-2609062210238684"
slug: "the-gateway-relays-a-pool-error-s-text-verbatim-to-the-clien"
severity: "minor"
category: "bug"
source: "user-observation"
found_during: "2026-09-06 residency security review"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/gateway/gateway.go"
resolution: "handleCompletions now gates the pool refusal's text on the same entitled predicate the models list uses, so an unentitled client gets a generic refusal with the same status and headers."
impact: fix
---

The gateway relays a pool error's text verbatim to the client that triggered it, and some of those texts describe the machine rather than the request. handleCompletions redacts a LaunchError, which can carry absolute local paths, and answers generically; every other pool error falls through to writeError with err.Error(). Three of them are informative to an unauthenticated LAN client on the default open install: the overload refusal names the number of requests already in flight for that model, and both the too-large-to-load refusal and the nothing-can-be-evicted refusal name the resident memory budget in bytes, which is 60 percent of physical RAM and so discloses roughly how much memory this Mac has. A client can induce all three itself by asking for a model it knows is large or by saturating one. This is pre-existing and predates the residency work, but it is the same class of fact the maintainer decided to gate behind an API key on the models list: residency, in-flight counts and the budget are reportable to entitled clients only. Worth deciding deliberately rather than by omission, since the wording of the refusals is what makes them useful to a client that is legitimately backing off.

## Grounds

- pursued: the overload, nothing-can-be-freed and too-large refusals reach a client on this machine or one a keyed install admitted with their in-flight count and budget figure intact, and every other client with 'cannot serve this model right now' under the same 503 and the same headers; shown wrong by an operator diagnosing a LAN client's refusal who cannot tell which of the three it was, since nothing is logged.
