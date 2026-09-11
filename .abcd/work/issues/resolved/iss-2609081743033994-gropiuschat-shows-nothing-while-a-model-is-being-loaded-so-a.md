---
schema_version: 1
id: "iss-2609081743033994"
slug: "gropiuschat-shows-nothing-while-a-model-is-being-loaded-so-a"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "maintainer walkthrough of the chat client on a standard account"
origin: researcher-authored
production_mode: hand-written
found_at: "client/GropiusChat/GropiusChat.swift"
resolution: "A reply waiting on a cold model now reads 'Loading' with the model's name, on the server's SSE ': loading' comments and, failing those, on the models list's residency read once a second."
impact: additive
---

GropiusChat shows nothing while a model is being loaded, so a first message to a cold model looks like a hang. Loading a model into memory takes appreciable time, and the client's only states are connected and sending; there is no signal that the delay is a load rather than a slow answer. Wanted: a visible loading state for the model, distinct from ordinary generation latency, so the user knows the wait is provisioning and roughly where it is. The gateway already distinguishes a load from a completion internally, so the question is what it should tell a client and how the client should show it.

## Grounds

- pursued: the loading state is entered only on evidence and a server that offers neither signal behaves exactly as before; shown wrong by a client that shows 'Loading' through an ordinary slow answer, or that keeps polling the models list after the answer has started.
